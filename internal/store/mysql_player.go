package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type playerSnapshot struct {
	components []mysqlComponent
	entities   map[string][]mysqlEntity
}

// Only decode entity identity. Decoding every stat into a RawMessage map for
// every save allocated thousands of short-lived objects on populated accounts.
type entityIdentity struct {
	NpcNum  json.RawMessage `json:"npcnum"`
	Pos     json.RawMessage `json:"pos"`
	ItemNum json.RawMessage `json:"itemnum"`
	No      json.RawMessage `json:"no"`
}

func (k entityIdentity) key(name string) json.RawMessage {
	switch name {
	case "npcnum":
		return k.NpcNum
	case "pos":
		return k.Pos
	case "itemnum":
		return k.ItemNum
	case "no":
		return k.No
	}
	return nil
}

// The containing object/list was already JSON-validated by Unmarshal. Normal
// game saves come from json.Marshal and need no further compaction pass.
func compactValidatedJSON(raw []byte) (string, error) {
	if bytes.IndexAny(raw, " \t\r\n") < 0 {
		return string(raw), nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", err
	}
	return compact.String(), nil
}

func digestJSON(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func splitPlayer(uid int32, data []byte) (playerSnapshot, error) {
	out := playerSnapshot{entities: make(map[string][]mysqlEntity)}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return out, errors.New("store: player save must be a JSON object")
	}
	if raw, ok := fields["uid"]; ok {
		var savedUID int32
		if json.Unmarshal(raw, &savedUID) != nil || savedUID != uid {
			return out, errors.New("store: player UID mismatch")
		}
	}
	for _, spec := range playerTables {
		raw, ok := fields[spec.Field]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return out, fmt.Errorf("store: invalid %s array", spec.Field)
		}
		seen := make(map[string]bool)
		for pos, item := range list {
			entityKey := "slot:" + strconv.Itoa(pos) // also preserves legacy null slots
			if !bytes.Equal(bytes.TrimSpace(item), []byte("null")) {
				var identity entityIdentity
				var key json.RawMessage
				// Struct decoding folds key case. Preserve exact map semantics for
				// legacy/unknown uppercase or escaped keys; normal saves are lowercase.
				if bytes.IndexAny(item, "ABCDEFGHIJKLMNOPQRSTUVWXYZ\\") >= 0 {
					var obj map[string]json.RawMessage
					if json.Unmarshal(item, &obj) != nil || obj == nil {
						return out, fmt.Errorf("store: invalid %s entity", spec.Field)
					}
					key = obj[spec.Key]
				} else if json.Unmarshal(item, &identity) != nil {
					return out, fmt.Errorf("store: invalid %s entity", spec.Field)
				} else {
					key = identity.key(spec.Key)
				}
				if key != nil {
					entityKey = spec.Key + ":" + string(key)
				}
			}
			if seen[entityKey] {
				return out, fmt.Errorf("store: duplicate %s entity key", spec.Field)
			}
			if len(entityKey) > 64 {
				return out, fmt.Errorf("store: %s entity key too long", spec.Field)
			}
			seen[entityKey] = true
			compact, err := compactValidatedJSON(item)
			if err != nil {
				return out, err
			}
			out.entities[spec.Table] = append(out.entities[spec.Table], mysqlEntity{UID: uid, EntityKey: entityKey,
				Position: pos, Data: compact, Digest: digestJSON([]byte(compact))})
		}
		fields[spec.Field] = json.RawMessage("[]") // shape marker, keeps [] different from null
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if len(name) > 64 {
			return out, errors.New("store: player component name too long")
		}
		compact, err := compactValidatedJSON(fields[name])
		if err != nil {
			return out, err
		}
		out.components = append(out.components, mysqlComponent{UID: uid, Name: name, Data: compact, Digest: digestJSON([]byte(compact))})
	}
	return out, nil
}

func (s *MySQL) SavePlayer(uid int32, data []byte) error {
	snapshot, err := splitPlayer(uid, data)
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error { return savePlayerSnapshot(tx, uid, snapshot) })
}

func savePlayerSnapshot(tx *gorm.DB, uid int32, snapshot playerSnapshot) error {
	// A UID row is the transaction lock. Different players can commit concurrently.
	state := mysqlPlayerState{UID: uid}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, "uid = ?", uid).Error; err != nil {
		return err
	}
	var previous []mysqlComponent
	if err := tx.Select("uid", "name", "digest").Where("uid = ?", uid).Find(&previous).Error; err != nil {
		return err
	}
	old := make(map[string]string)
	for _, row := range previous {
		old[row.Name] = row.Digest
	}
	var changed []mysqlComponent
	for _, row := range snapshot.components {
		if old[row.Name] != row.Digest {
			changed = append(changed, row)
		}
		delete(old, row.Name)
	}
	if len(changed) > 0 {
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(changed, 100).Error; err != nil {
			return err
		}
	}
	if len(old) > 0 {
		var names []string
		for name := range old {
			names = append(names, name)
		}
		if err := tx.Where("uid = ? AND name IN ?", uid, names).Delete(&mysqlComponent{}).Error; err != nil {
			return err
		}
	}
	for _, spec := range playerTables {
		var previous []mysqlEntity
		if err := tx.Table(spec.Table).Select("uid", "entity_key", "position", "digest").Where("uid = ?", uid).Find(&previous).Error; err != nil {
			return err
		}
		old := make(map[string]mysqlEntity)
		for _, row := range previous {
			old[row.EntityKey] = row
		}
		var changed []mysqlEntity
		for _, row := range snapshot.entities[spec.Table] {
			prev, exists := old[row.EntityKey]
			if !exists || prev.Digest != row.Digest || prev.Position != row.Position {
				changed = append(changed, row)
			}
			delete(old, row.EntityKey)
		}
		if len(changed) > 0 {
			if err := tx.Table(spec.Table).Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(changed, 100).Error; err != nil {
				return err
			}
		}
		if len(old) > 0 {
			var keys []string
			for key := range old {
				keys = append(keys, key)
			}
			if err := tx.Table(spec.Table).Where("uid = ? AND entity_key IN ?", uid, keys).Delete(&mysqlEntity{}).Error; err != nil {
				return err
			}
		}
	}
	return tx.Model(&state).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": now()}).Error
}

func (s *MySQL) LoadPlayer(uid int32) ([]byte, error) {
	var data []byte
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var err error
		data, err = loadPlayerSnapshot(tx, uid)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return data, err
}

func loadPlayerSnapshot(tx *gorm.DB, uid int32) ([]byte, error) {
	var state mysqlPlayerState
	if err := tx.Take(&state, "uid = ?", uid).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var components []mysqlComponent
	if err := tx.Where("uid = ?", uid).Find(&components).Error; err != nil {
		return nil, err
	}
	fields := make(map[string]json.RawMessage)
	for _, row := range components {
		fields[row.Name] = json.RawMessage(row.Data)
	}
	for _, spec := range playerTables {
		if raw, ok := fields[spec.Field]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var rows []mysqlEntity
		if err := tx.Table(spec.Table).Where("uid = ?", uid).Order("position").Find(&rows).Error; err != nil {
			return nil, err
		}
		list := make([]json.RawMessage, 0, len(rows))
		for _, row := range rows {
			list = append(list, json.RawMessage(row.Data))
		}
		data, err := json.Marshal(list)
		if err != nil {
			return nil, err
		}
		fields[spec.Field] = data
	}
	return json.Marshal(fields)
}
