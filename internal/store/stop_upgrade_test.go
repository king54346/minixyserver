package store

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestStopUpgradeMigrationPreservesLegacyRole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ddl := `CREATE TABLE roles (uid INTEGER PRIMARY KEY,name TEXT NOT NULL,icon INTEGER NOT NULL,grade INTEGER NOT NULL,vip INTEGER NOT NULL DEFAULT 0,tpoint INTEGER NOT NULL,hpoint INTEGER NOT NULL,exp INTEGER NOT NULL,cash INTEGER NOT NULL,gold INTEGER NOT NULL,friend_point INTEGER NOT NULL DEFAULT 0,soulexp INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL)`
	if err = db.Exec(ddl).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(`INSERT INTO roles(uid,name,icon,grade,tpoint,hpoint,exp,cash,gold,created_at) VALUES(1,'legacy',1,30,120,40,123,456,789,0)`).Error; err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	conn.Close()
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rows, err := st.LoadRoles()
	if err != nil || len(rows) != 1 {
		t.Fatalf("load: %v %v", rows, err)
	}
	r := rows[0]
	if r.Exp != 123 || r.Gold != 789 || r.StopUpgrade != 0 || r.Xiuwei != 0 || r.XGrade != 0 {
		t.Fatal("migration changed existing role or defaults")
	}
	for _, column := range []string{"stop_upgrade", "xiuwei", "xgrade"} {
		if !st.db.Migrator().HasColumn("roles", column) {
			t.Fatalf("missing %s", column)
		}
	}
	r.StopUpgrade, r.Xiuwei, r.XGrade = 1, 12345, 10
	if err = st.SaveRole(r); err != nil {
		t.Fatal(err)
	}
	if err = st.db.Exec("UPDATE roles SET stop_upgrade=2 WHERE uid=1").Error; err == nil {
		t.Fatal("database accepted non-binary stop_upgrade")
	}
	st.Close()
	st, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err = st.LoadRoles()
	if err != nil || len(rows) != 1 || rows[0].StopUpgrade != 1 || rows[0].Xiuwei != 12345 || rows[0].XGrade != 10 {
		t.Fatal("peak fields did not survive reopen")
	}
}
