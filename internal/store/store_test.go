package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var (
	created = time.Date(2026, 10, 12, 20, 6, 2, 760454900, time.Local)
	acc     = Account{UID: 20001, URS: "Tester01", PwdSalt: "5a3f", PwdHash: "512a", CodeHash: "2da0",
		Device: "and:380e", Created: created, SID: "6164"}
	role = Role{UID: 20001, Name: "毛乐志", Icon: 1, Grade: 7, Exp: 123, Cash: 16535, Gold: 3, FriendPoint: 10,
		SoulExp: 218, VIP: 0, TPoint: 50, HPoint: 20, Created: created}
	save = []byte(`{"uid":20001,"next_npc":10007,"items":[{"pos":1,"type":10006,"amount":2}]}`)
)

func sameAccount(a, b Account) bool { a.Created, b.Created = time.Time{}, time.Time{}; return a == b }
func sameRole(a, b Role) bool       { a.Created, b.Created = time.Time{}, time.Time{}; return a == b }

// roundTrip：写入一个账号、角色、玩家存档，改一次再读回。
func roundTrip(t *testing.T, st Store) {
	t.Helper()
	if _, err := st.LoadPlayer(20001); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty LoadPlayer err = %v", err)
	}
	if err := st.SaveAccount(acc); err != nil {
		t.Fatal(err)
	}
	a2 := acc
	a2.SID, a2.Device = "ffff", "and:new"
	if err := st.SaveAccount(a2); err != nil { // 同一 uid 覆盖
		t.Fatal(err)
	}
	if err := st.SaveRole(role); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePlayer(20001, save); err != nil {
		t.Fatal(err)
	}
	accs, err := st.LoadAccounts()
	if err != nil || len(accs) != 1 || !sameAccount(accs[0], a2) || !accs[0].Created.Equal(created) {
		t.Fatalf("accounts %v %+v", err, accs)
	}
	roles, err := st.LoadRoles()
	if err != nil || len(roles) != 1 || !sameRole(roles[0], role) || !roles[0].Created.Equal(created) {
		t.Fatalf("roles %v %+v", err, roles)
	}
	if got, err := st.LoadPlayer(20001); err != nil || string(got) != string(save) {
		t.Fatalf("player %v %s", err, got)
	}
}

func TestSQLiteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "xym.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, db)
	// 账号名唯一（不分大小写）
	dup := acc
	dup.UID, dup.URS = 20002, "TESTER01"
	if err := db.SaveAccount(dup); err == nil {
		t.Fatal("duplicate account name accepted")
	}
	db.Close()
	db, err = OpenSQLite(path) // 重开仍在
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if accs, _ := db.LoadAccounts(); len(accs) != 1 {
		t.Fatalf("reopen: %d accounts", len(accs))
	}
}

func TestFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	roundTrip(t, OpenFile(dir))
	roundTripReopen := OpenFile(dir) // 新实例从文件读
	if accs, _ := roundTripReopen.LoadAccounts(); len(accs) != 1 || accs[0].SID != "ffff" {
		t.Fatalf("file reopen %+v", accs)
	}
}

// TestMigrate：旧 JSON 存档（与 xymsrv 原来写出的格式一致）首次打开 sqlite 时整体导入，
// 数值一致、原文件保留；库里已有数据时不再导入。
func TestMigrate(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("accounts.json", `{"next_uid": 20003, "accounts": {
 "tester01": {"uid": 20001, "urs": "tester01", "pwd_salt": "5a3f", "pwd_hash": "512a", "code_hash": "2da0",
  "device": "and:380e", "created": "2026-10-12T20:06:02.7604549+08:00", "sid": "6164"},
 "bob": {"uid": 20002, "urs": "Bob", "pwd_salt": "aa", "pwd_hash": "bb", "created": "2026-10-13T08:00:00+08:00"}}}`)
	write("roles.json", `{"roles": {"20001": {"uid": 20001, "name": "毛乐志", "icon": 1, "grade": 7, "exp": 123,
 "cash": 16535, "gold": 3, "friend_point": 10, "soulexp": 218, "vip": 0, "tpoint": 50, "hpoint": 20,
 "created": "2026-10-12T20:07:00+08:00"}}}`)
	write("players/20001.json", string(save))
	write("players/20002.json.tmp", "{garbage") // 残留临时文件不导入

	db, err := OpenSQLite(filepath.Join(dir, "xym.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := Migrate(db, dir)
	if err != nil || res != (MigrateResult{2, 1, 1}) {
		t.Fatalf("migrate %+v %v", res, err)
	}
	accs, _ := db.LoadAccounts()
	if len(accs) != 2 || accs[0].URS != "tester01" || accs[0].CodeHash != "2da0" || accs[0].SID != "6164" ||
		accs[1].URS != "Bob" || accs[1].CodeHash != "" ||
		!accs[0].Created.Equal(time.Date(2026, 10, 12, 20, 6, 2, 760454900, time.FixedZone("", 8*3600))) {
		t.Fatalf("accounts %+v", accs)
	}
	roles, _ := db.LoadRoles()
	if len(roles) != 1 || roles[0].Name != "毛乐志" || roles[0].Cash != 16535 || roles[0].SoulExp != 218 {
		t.Fatalf("roles %+v", roles)
	}
	if got, err := db.LoadPlayer(20001); err != nil || string(got) != string(save) {
		t.Fatalf("player %v %s", err, got)
	}
	if _, err := os.Stat(filepath.Join(dir, "accounts.json")); err != nil {
		t.Fatal("original accounts.json removed")
	}
	// 再开一次：库非空，不重复导入
	if res, err := Migrate(db, dir); err != nil || res != (MigrateResult{}) {
		t.Fatalf("second migrate %+v %v", res, err)
	}
}

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open("mysql", dir, ""); err == nil {
		t.Fatal("unknown kind accepted")
	}
	st, err := Open("sqlite", dir, filepath.Join(dir, "sub", "xym.db")) // 空目录、库所在目录不存在
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
}

// TestOpenLegacySchema：用旧版（手写建表语句）建的库、且少一列，新代码打开后数据不丢、缺的列补上、可正常读写。
func TestOpenLegacySchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xym.db")
	raw, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(path)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE accounts (uid INTEGER PRIMARY KEY, urs_key TEXT NOT NULL UNIQUE, urs TEXT NOT NULL,
			pwd_salt TEXT NOT NULL, pwd_hash TEXT NOT NULL, code_hash TEXT NOT NULL DEFAULT '',
			device TEXT NOT NULL DEFAULT '', sid TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL)`,
		`CREATE TABLE roles (uid INTEGER PRIMARY KEY, name TEXT NOT NULL, icon INTEGER NOT NULL, grade INTEGER NOT NULL,
			exp INTEGER NOT NULL, cash INTEGER NOT NULL, gold INTEGER NOT NULL, friend_point INTEGER NOT NULL DEFAULT 0,
			vip INTEGER NOT NULL DEFAULT 0, tpoint INTEGER NOT NULL, hpoint INTEGER NOT NULL,
			created_at INTEGER NOT NULL)`, // 少 soulexp 列
		`CREATE TABLE players (uid INTEGER PRIMARY KEY, save TEXT NOT NULL, updated_at INTEGER NOT NULL)`,
		`INSERT INTO accounts VALUES (20001, 'tester01', 'tester01', '5a3f', '512a', '', '', '', 1)`,
		`INSERT INTO roles VALUES (20001, '毛乐志', 1, 7, 0, 135, 0, 0, 0, 50, 20, 1)`,
		`INSERT INTO players VALUES (20001, '{"uid":20001}', 1)`,
	} {
		if err := raw.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	if d, _ := raw.DB(); d != nil {
		d.Close()
	}

	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	roles, err := db.LoadRoles()
	if err != nil || len(roles) != 1 || roles[0].Cash != 135 || roles[0].SoulExp != 0 {
		t.Fatalf("roles %v %+v", err, roles)
	}
	roles[0].SoulExp = 218
	if err := db.SaveRole(roles[0]); err != nil {
		t.Fatal(err)
	}
	if roles, _ = db.LoadRoles(); roles[0].SoulExp != 218 {
		t.Fatalf("soulexp not saved: %+v", roles[0])
	}
	if accs, _ := db.LoadAccounts(); len(accs) != 1 || accs[0].URS != "tester01" {
		t.Fatalf("accounts %+v", accs)
	}
	if p, err := db.LoadPlayer(20001); err != nil || string(p) != `{"uid":20001}` {
		t.Fatalf("player %v %s", err, p)
	}
}
