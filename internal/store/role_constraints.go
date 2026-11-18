package store

import (
	"gorm.io/gorm"
	"turnrpg/internal/model"
)

// Existing tables only gain columns/constraints; SQLite constraints use triggers
// because adding a CHECK through GORM would rebuild the entire roles table.
func ensureRoleConstraints(db *gorm.DB) error {
	if db.Dialector.Name() == "sqlite" {
		for _, event := range []string{"INSERT", "UPDATE OF stop_upgrade"} {
			name := "roles_stop_upgrade_insert"
			if event != "INSERT" {
				name = "roles_stop_upgrade_update"
			}
			q := "CREATE TRIGGER IF NOT EXISTS " + name + " BEFORE " + event + " ON roles WHEN NEW.stop_upgrade NOT IN (0,1) BEGIN SELECT RAISE(ABORT, 'stop_upgrade must be 0 or 1'); END"
			if err := db.Exec(q).Error; err != nil {
				return err
			}
		}
		return nil
	}
	if !db.Migrator().HasConstraint(&model.Role{}, "chk_roles_stop_upgrade") {
		return db.Migrator().CreateConstraint(&model.Role{}, "chk_roles_stop_upgrade")
	}
	return nil
}
