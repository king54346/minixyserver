package store

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"gorm.io/gorm"
)

// MigrateResult 是一次导入的条数。
type MigrateResult struct{ Accounts, Roles, Players int }

// Migrate 把 dir 下旧的 JSON 存档（accounts.json、roles.json、players/*.json）导入 db。
// 只在 db 完全为空时做，一个事务内完成；原文件保留不删。没有旧存档时什么也不做。
func Migrate(db *SQLite, dir string) (MigrateResult, error) {
	var res MigrateResult
	empty, err := db.empty()
	if err != nil || !empty {
		return res, err
	}
	src := OpenFile(dir)
	accounts, err := src.LoadAccounts()
	if err != nil {
		return res, err
	}
	roles, err := src.LoadRoles()
	if err != nil {
		return res, err
	}
	uids, err := src.playerUIDs()
	if err != nil {
		return res, err
	}
	if len(accounts)+len(roles)+len(uids) == 0 {
		return res, nil
	}
	err = db.db.Transaction(func(tx *gorm.DB) error {
		for _, a := range accounts {
			if err := saveAccount(tx, a); err != nil {
				return fmt.Errorf("store: migrate account %d: %w", a.UID, err)
			}
		}
		for _, r := range roles {
			if err := saveRole(tx, r); err != nil {
				return fmt.Errorf("store: migrate role %d: %w", r.UID, err)
			}
		}
		for _, uid := range uids {
			data, err := src.LoadPlayer(uid)
			if err != nil {
				return err
			}
			if !json.Valid(data) {
				return fmt.Errorf("store: migrate player %d: invalid JSON", uid)
			}
			if err := savePlayer(tx, uid, data); err != nil {
				return fmt.Errorf("store: migrate player %d: %w", uid, err)
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	res = MigrateResult{len(accounts), len(roles), len(uids)}
	slog.Info("store: imported JSON saves into sqlite", "dir", dir, "accounts", res.Accounts,
		"roles", res.Roles, "players", res.Players)
	return res, nil
}
