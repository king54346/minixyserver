package store

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
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
	if got, err := st.LoadPlayer(20001); err != nil || !equalJSON(got, save) {
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

// WAL 读连接不能排在写连接后面，否则多人登录读档会被频繁写档阻塞。
func TestSQLiteReadDuringWrite(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "xym.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SavePlayer(20001, []byte(`{"version":1}`)); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- db.db.Transaction(func(tx *gorm.DB) error {
			if err := savePlayer(tx, 20001, []byte(`{"version":2}`)); err != nil {
				return err
			}
			close(started)
			<-release
			return nil
		})
	}()
	select {
	case <-started:
	case err := <-finished:
		t.Fatalf("write failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("write did not start")
	}
	read := make(chan string, 1)
	go func() {
		data, err := db.LoadPlayer(20001)
		if err != nil {
			read <- err.Error()
			return
		}
		read <- string(data)
	}()
	select {
	case got := <-read:
		if got != `{"version":1}` {
			t.Errorf("uncommitted or invalid read: %s", got)
		}
	case <-time.After(time.Second):
		t.Error("read blocked behind write")
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if got, err := db.LoadPlayer(20001); err != nil || string(got) != `{"version":2}` {
		t.Fatalf("committed read: %s, %v", got, err)
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
	if got, err := db.LoadPlayer(20001); err != nil || !equalJSON(got, save) {
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
	if _, err := Open("unsupported", dir, ""); err == nil {
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

// globalsAndRecords：全服数据、录像的存取与列出。
func globalsAndRecords(t *testing.T, st Store) {
	t.Helper()
	if _, err := st.LoadGlobal("lundao"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty global err = %v", err)
	}
	if err := st.SaveGlobal("lundao", []byte(`{"ranks":[1]}`)); err != nil {
		t.Fatal(err)
	}
	if got, err := st.LoadGlobal("lundao"); err != nil || string(got) != `{"ranks":[1]}` {
		t.Fatalf("global %v %s", err, got)
	}
	for k, owner := range map[int64]int32{808: 20001, 809: 20002} {
		if err := st.SaveRecord(RecordGate, k, owner, []byte(`{"uid":`+strconv.Itoa(int(owner))+`}`)); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := st.LoadRecord(RecordGate, 809); err != nil || string(got) != `{"uid":20002}` {
		t.Fatalf("record %v %s", err, got)
	}
	l, err := st.ListRecords(RecordGate)
	if err != nil || len(l) != 2 || l[0].Key != 808 || l[0].Owner != 20001 || l[1].Owner != 20002 {
		t.Fatalf("list %v %+v", err, l)
	}
	if l, _ := st.ListRecords(RecordQujingTrial); len(l) != 0 {
		t.Fatalf("other kind %+v", l)
	}
}

func TestGlobalsAndRecords(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "xym.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	globalsAndRecords(t, db)
	globalsAndRecords(t, OpenFile(t.TempDir()))
}

// TestUpgradeV2：旧库里用保留 uid 存的全服数据和录像，打开后搬进 globals / records，players 里只剩玩家。
func TestUpgradeV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xym.db")
	raw, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(path)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE players (uid INTEGER PRIMARY KEY, save TEXT NOT NULL, updated_at INTEGER NOT NULL)`,
		`INSERT INTO players VALUES (20001, '{"uid":20001}', 1)`,
		`INSERT INTO players VALUES (2, '{"ranks":[20001]}', 1)`,
		`INSERT INTO players VALUES (1000080809, '{"uid":20001,"name":"甲"}', 1)`,
		`INSERT INTO players VALUES (1100000013, '{"uid":20002}', 1)`,
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
	if got, err := db.LoadGlobal("lundao"); err != nil || string(got) != `{"ranks":[20001]}` {
		t.Fatalf("lundao %v %s", err, got)
	}
	if l, _ := db.ListRecords(RecordGate); len(l) != 1 || l[0].Key != 80809 || l[0].Owner != 20001 {
		t.Fatalf("gate %+v", l)
	}
	if l, _ := db.ListRecords(RecordQujingTrial); len(l) != 1 || l[0].Key != 13 || l[0].Owner != 20002 {
		t.Fatalf("trial %+v", l)
	}
	for _, uid := range []int32{2, 1000080809, 1100000013} {
		if _, err := db.LoadPlayer(uid); !errors.Is(err, ErrNotFound) {
			t.Fatalf("uid %d still in players", uid)
		}
	}
	if _, err := db.LoadPlayer(20001); err != nil {
		t.Fatal(err)
	}
	if v, _ := db.schemaVersion(); v != schemaVersion {
		t.Fatalf("version %d", v)
	}
}
