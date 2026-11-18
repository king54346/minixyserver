package store

import (
	"errors"
	"os"
	"path/filepath"

	"gorm.io/gorm"
)

// AccountDeleter is optional so custom stores need not implement GM deletion.
type AccountDeleter interface {
	DeleteAccountData(uid int32) error
}

func (s *SQLStore) DeleteAccountData(uid int32) error {
	if uid <= 0 {
		return errors.New("invalid uid")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		// Fixed table names only. MySQL entity tables and legacy SQLite snapshots coexist.
		for _, table := range []string{"player_pets", "player_items", "player_suits", "player_books", "player_lineups", "player_components", "player_states", "players", "roles", "accounts"} {
			if tx.Migrator().HasTable(table) {
				if err := tx.Exec("DELETE FROM `"+table+"` WHERE uid = ?", uid).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (f *File) DeleteAccountData(uid int32) error {
	if uid <= 0 {
		return errors.New("invalid uid")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.loadAccountsLocked(); err != nil {
		return err
	}
	if err := f.loadRolesLocked(); err != nil {
		return err
	}
	accounts := make(map[string]Account, len(f.accounts))
	roles := make(map[int32]Role, len(f.roles))
	for k, a := range f.accounts {
		if a.UID != uid {
			accounts[k] = a
		}
	}
	for k, r := range f.roles {
		if k != uid {
			roles[k] = r
		}
	}
	// File mode retains per-file atomic writes; SQL mode above uses one transaction.
	if err := os.Remove(f.playerPath(uid)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeJSON(filepath.Join(f.dir, "roles.json"), rolesFile{ByUID: roles}); err != nil {
		return err
	}
	f.roles = roles
	if err := writeJSON(filepath.Join(f.dir, "accounts.json"), accountsFile{NextUID: uid + 1, ByURS: accounts}); err != nil {
		return err
	}
	f.accounts = accounts
	return nil
}
