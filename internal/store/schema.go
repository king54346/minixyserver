package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"gorm.io/gorm"

	"turnrpg/internal/model"
)

// 结构迁移：按 meta.schema_version 一步一步做，每步一个事务，做完记下版本。
//
//	v1  accounts / roles / players 三张表（全服数据、录像都塞在 players 里，用保留 uid 当键）
//	v2  全服数据搬到 globals（按名字），录像搬到 records（按 kind + key，带 owner 索引）
const schemaVersion = 2

// 旧版占用的保留 uid（xymsrv/globals.go、gate_star.go、qujing.go 原来的约定）。
var legacyGlobals = map[int32]string{0: "social", 1: "dongfu", 2: "lundao", 3: "starwar", 4: "tianti", 5: "jiuyou",
	6: "tree", 7: "liexi", 8: "qujing"}

const (
	legacyGateRecBase  = 1_000_000_000 // 关卡录像：gateRecBase + gid×8 + 类型×4 + 档位
	legacyTrialRecBase = 1_100_000_000 // 取经试炼录像：qjTrialRecBase + 劫×4 + 槽位
)

// 录像的 kind（与 xymsrv 一致）。
const (
	RecordGate        = "gate"
	RecordQujingTrial = "qujing_trial"
)

// LegacyGlobalUID 是全服数据 name 在旧版存档里的保留 uid（File 存档还按这个存）；没有返回 -1。
func LegacyGlobalUID(name string) int32 {
	for uid, n := range legacyGlobals {
		if n == name {
			return uid
		}
	}
	return -1
}

func (s *SQLite) schemaVersion() (int, error) {
	var m model.Meta
	err := s.db.Where("name = ?", "schema_version").Limit(1).Find(&m).Error
	if err != nil || m.Value == "" {
		return 1, err
	}
	return strconv.Atoi(m.Value)
}

// upgradeSchema 把库升到 schemaVersion。空库直接记成最新版本。
func (s *SQLite) upgradeSchema() error {
	v, err := s.schemaVersion()
	if err != nil {
		return err
	}
	for v < schemaVersion {
		v++
		err := s.db.Transaction(func(tx *gorm.DB) error {
			if err := upgradeTo(tx, v); err != nil {
				return err
			}
			return upsert(tx, &model.Meta{Key: "schema_version", Value: strconv.Itoa(v)})
		})
		if err != nil {
			return fmt.Errorf("to v%d: %w", v, err)
		}
	}
	return nil
}

func upgradeTo(tx *gorm.DB, v int) error {
	switch v {
	case 2:
		return moveLegacyRows(tx)
	}
	return nil
}

// moveLegacyRows：players 里保留 uid 的行搬到 globals / records（JSON 导入之后也要再跑一次）。
func moveLegacyRows(tx *gorm.DB) error {
	var rows []model.Player
	if err := tx.Where("uid < ? OR uid >= ?", 20001, legacyGateRecBase).Find(&rows).Error; err != nil {
		return err
	}
	moved := map[string]int{}
	for _, r := range rows {
		switch {
		case legacyGlobals[r.UID] != "" && r.UID < 20001:
			if err := saveGlobal(tx, legacyGlobals[r.UID], []byte(r.Save)); err != nil {
				return err
			}
			moved["globals"]++
		case r.UID >= legacyTrialRecBase:
			if err := saveRecord(tx, RecordQujingTrial, int64(r.UID-legacyTrialRecBase), ownerOf(r.Save), []byte(r.Save)); err != nil {
				return err
			}
			moved[RecordQujingTrial]++
		case r.UID >= legacyGateRecBase:
			if err := saveRecord(tx, RecordGate, int64(r.UID-legacyGateRecBase), ownerOf(r.Save), []byte(r.Save)); err != nil {
				return err
			}
			moved[RecordGate]++
		default:
			slog.Warn("store: unknown reserved uid left in players", "uid", r.UID)
			continue
		}
		if err := tx.Delete(&model.Player{}, "uid = ?", r.UID).Error; err != nil {
			return err
		}
	}
	if len(moved) > 0 {
		slog.Info("store: moved legacy rows out of players", "moved", moved)
	}
	return nil
}

// ownerOf 取录像 JSON 里的 uid（录像主人）。
func ownerOf(data string) int32 {
	var v struct {
		UID int32 `json:"uid"`
	}
	_ = json.Unmarshal([]byte(data), &v)
	return v.UID
}
