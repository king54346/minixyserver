package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"turnrpg/internal/model"
)

type MySQLImportReport struct{ Accounts, Roles, Players, Globals, Records int64 }

// OpenSQLiteReadOnly never creates or upgrades the source. Migration is an
// explicit offline operation against a backup, not part of normal startup.
func OpenSQLiteReadOnly(path string) (*SQLite, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=query_only(ON)&_pragma=busy_timeout(5000)"),
		&gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, err
	}
	s := &SQLite{SQLStore: &SQLStore{db: db, readDB: db}}
	if version, err := s.schemaVersion(); err != nil || version != schemaVersion {
		s.Close()
		return nil, fmt.Errorf("store: source must be SQLite schema version %d (got %d): %v", schemaVersion, version, err)
	}
	return s, nil
}

func CheckSQLiteSource(path string) (MySQLImportReport, error) {
	src, err := OpenSQLiteReadOnly(path)
	if err != nil {
		return MySQLImportReport{}, err
	}
	defer src.Close()
	var report MySQLImportReport
	err = src.db.Transaction(func(tx *gorm.DB) error {
		var err error
		report, err = sourceCounts(tx)
		if err != nil {
			return err
		}
		var rows []model.Player
		return tx.FindInBatches(&rows, 50, func(_ *gorm.DB, _ int) error {
			for _, row := range rows {
				if _, err := splitPlayer(row.UID, []byte(row.Save)); err != nil {
					return fmt.Errorf("player %d: %w", row.UID, err)
				}
			}
			return nil
		}).Error
	})
	return report, err
}

func sourceCounts(db *gorm.DB) (r MySQLImportReport, err error) {
	for _, pair := range []struct {
		model any
		count *int64
	}{
		{&model.Account{}, &r.Accounts}, {&model.Role{}, &r.Roles}, {&model.Player{}, &r.Players},
		{&model.Global{}, &r.Globals}, {&model.Record{}, &r.Records},
	} {
		if err := db.Model(pair.model).Count(pair.count).Error; err != nil {
			return r, err
		}
	}
	return r, nil
}

func (s *MySQL) ImportSQLite(path string) (MySQLImportReport, error) {
	src, err := OpenSQLiteReadOnly(path)
	if err != nil {
		return MySQLImportReport{}, err
	}
	defer src.Close()
	var report MySQLImportReport
	err = src.db.Transaction(func(source *gorm.DB) error {
		return s.db.Transaction(func(target *gorm.DB) error {
			var err error
			report, err = importSQLiteRows(source, target)
			return err
		})
	})
	if err != nil {
		return MySQLImportReport{}, err
	}
	return report, nil
}

func importSQLiteRows(src, dst *gorm.DB) (report MySQLImportReport, err error) {
	lock := model.Meta{Key: "sqlite_import", Value: "pending"}
	if err := dst.Clauses(clause.OnConflict{DoNothing: true}).Create(&lock).Error; err != nil {
		return report, err
	}
	if err := dst.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&lock, "name = ?", lock.Key).Error; err != nil {
		return report, err
	}
	tables := []string{"accounts", "roles", "player_states", "player_components", "globals", "records"}
	for _, spec := range playerTables {
		tables = append(tables, spec.Table)
	}
	for _, table := range tables {
		var count int64
		if err := dst.Table(table).Count(&count).Error; err != nil {
			return report, err
		}
		if count != 0 {
			return report, errors.New("store: MySQL destination is not empty; refusing to overwrite saves")
		}
	}
	copyRows := func(rows any, count *int64) error {
		return src.FindInBatches(rows, 100, func(batch *gorm.DB, _ int) error {
			if err := dst.Create(rows).Error; err != nil {
				return err
			}
			*count += batch.RowsAffected
			return nil
		}).Error
	}
	if err := copyRows(&[]model.Account{}, &report.Accounts); err != nil {
		return report, err
	}
	if err := copyRows(&[]model.Role{}, &report.Roles); err != nil {
		return report, err
	}
	if err := copyRows(&[]model.Global{}, &report.Globals); err != nil {
		return report, err
	}
	// Records have a composite primary key; page explicitly to avoid keyset
	// pagination choosing only one part of (kind, rec_key).
	for offset := 0; ; offset += 100 {
		var rows []model.Record
		if err := src.Order("kind, rec_key").Limit(100).Offset(offset).Find(&rows).Error; err != nil {
			return report, err
		}
		if len(rows) == 0 {
			break
		}
		if err := dst.Create(&rows).Error; err != nil {
			return report, err
		}
		report.Records += int64(len(rows))
	}
	var players []model.Player
	err = src.FindInBatches(&players, 50, func(batch *gorm.DB, _ int) error {
		for _, p := range players {
			snapshot, err := splitPlayer(p.UID, []byte(p.Save))
			if err != nil {
				return fmt.Errorf("player %d: %w", p.UID, err)
			}
			if err := savePlayerSnapshot(dst, p.UID, snapshot); err != nil {
				return err
			}
			got, err := loadPlayerSnapshot(dst, p.UID)
			if err != nil {
				return err
			}
			if !equalJSON([]byte(p.Save), got) {
				return fmt.Errorf("store: player %d migration verification failed", p.UID)
			}
			report.Players++
		}
		return nil
	}).Error
	if err != nil {
		return report, err
	}
	if err := upsert(dst, &model.Meta{Key: lock.Key, Value: "complete"}); err != nil {
		return report, err
	}
	return report, nil
}

// UseNumber preserves large integer IDs when verifying a JSON round trip.
func equalJSON(a, b []byte) bool {
	decode := func(data []byte) (any, error) {
		var value any
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		err := d.Decode(&value)
		return value, err
	}
	x, e1 := decode(a)
	y, e2 := decode(b)
	return e1 == nil && e2 == nil && reflect.DeepEqual(x, y)
}
