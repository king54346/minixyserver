package store

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"
)

// The release script prepares a disposable database with a copy of the old
// roles schema, no production rows, and no stop/peak columns.
func TestStopUpgradeMySQLLegacyMigration(t *testing.T) {
	dsn := os.Getenv("XYM_STOP_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set XYM_STOP_TEST_MYSQL_DSN to a disposable legacy-schema database")
	}
	cfg, _, err := mysqlConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.DBName, "xym_stop_verify_") {
		t.Fatal("requires an explicitly named disposable database")
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var columns int
	if err = db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='roles' AND column_name='stop_upgrade'").Scan(&columns); err != nil || columns != 0 {
		t.Fatal("test database must start with legacy roles schema")
	}
	if _, err = db.Exec("INSERT INTO roles(uid,name,icon,grade,vip,tpoint,hpoint,exp,cash,gold,friend_point,soulexp,created_at) VALUES(1,'migration-probe',1,30,0,120,40,123,456,789,0,0,0)"); err != nil {
		t.Fatal(err)
	}
	st, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.LoadRoles()
	if err != nil || len(rows) != 1 {
		st.Close()
		t.Fatal("migration lost the legacy row", err)
	}
	r := rows[0]
	if r.StopUpgrade != 0 || r.Xiuwei != 0 || r.XGrade != 0 || r.Exp != 123 || r.Gold != 789 {
		st.Close()
		t.Fatal("legacy defaults/data changed")
	}
	r.StopUpgrade, r.XGrade, r.Xiuwei = 1, 10, 12345
	r.Created = time.Unix(0, 0)
	if err = st.SaveRole(r); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE roles SET stop_upgrade=2 WHERE uid=1"); err == nil {
		st.Close()
		t.Fatal("nonbinary stop_upgrade accepted")
	}
	st.Close()
	st, err = OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err = st.LoadRoles()
	if err != nil || len(rows) != 1 || rows[0].StopUpgrade != 1 || rows[0].XGrade != 10 || rows[0].Xiuwei != 12345 {
		t.Fatal("reopen lost stop/peak fields", err)
	}
}
