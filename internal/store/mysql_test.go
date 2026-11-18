package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

// Exercise the normalized storage algorithm on a real transactional SQL engine
// even when a local MySQL service is unavailable. MySQL-specific integration is
// separately enabled only against an explicitly configured empty test database.
func normalizedTestStore(t *testing.T) *MySQL {
	t.Helper()
	base, err := OpenSQLite(filepath.Join(t.TempDir(), "normalized.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := &MySQL{SQLStore: &SQLStore{db: base.db, readDB: base.db}}
	if err := s.ensureSchema(); err != nil {
		base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Close() })
	return s
}

func TestNormalizedPlayerRoundTrip(t *testing.T) {
	s := normalizedTestStore(t)
	if _, err := s.LoadPlayer(20001); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	a := []byte(`{"uid":20001,"next_npc":10003,"pets":[{"npcnum":10001,"grade":1},{"npcnum":10002,"grade":2}],"items":[{"pos":1,"amount":3}],"suits":[],"books":null,"lineups":[null,{"no":1,"slots":[]}],"progress":{"key":9007199254740993},"future_field":{"x":true}}`)
	if err := s.SavePlayer(20001, a); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LoadPlayer(20001); err != nil || !equalJSON(a, got) {
		t.Fatalf("round trip %s: %v", got, err)
	}
	var petWrites atomic.Int32
	if err := s.db.Callback().Create().After("gorm:create").Register("test:count_pets", func(tx *gorm.DB) {
		if tx.Statement.Table == "player_pets" {
			petWrites.Add(int32(tx.RowsAffected))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlayer(20001, a); err != nil {
		t.Fatal(err)
	}
	if petWrites.Load() != 0 {
		t.Fatal("unchanged pets were rewritten")
	}
	b := []byte(`{"uid":20001,"pets":[{"npcnum":10002,"grade":3}],"items":[],"suits":null,"books":[],"lineups":[]}`)
	if err := s.SavePlayer(20001, b); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LoadPlayer(20001); err != nil || !equalJSON(b, got) {
		t.Fatalf("update/delete %s: %v", got, err)
	}
	if petWrites.Load() != 1 {
		t.Fatalf("changed entity writes: %d", petWrites.Load())
	}
	for _, bad := range []string{`null`, `[]`, `{"uid":2}`, `{"uid":20001,"pets":[{"npcnum":1},{"npcnum":1}]}`} {
		if err := s.SavePlayer(20001, []byte(bad)); err == nil {
			t.Fatalf("invalid save accepted: %s", bad)
		}
	}
}

func TestNormalizedPlayerRollback(t *testing.T) {
	s := normalizedTestStore(t)
	before := []byte(`{"uid":20001,"pets":[{"npcnum":1,"grade":1}],"items":[{"pos":1,"amount":1}]}`)
	if err := s.SavePlayer(20001, before); err != nil {
		t.Fatal(err)
	}
	fail := errors.New("simulated write failure")
	if err := s.db.Callback().Create().Before("gorm:create").Register("test:fail_items", func(tx *gorm.DB) {
		if tx.Statement.Table == "player_items" {
			tx.AddError(fail)
		}
	}); err != nil {
		t.Fatal(err)
	}
	after := []byte(`{"uid":20001,"pets":[{"npcnum":1,"grade":2}],"items":[{"pos":1,"amount":2}]}`)
	if err := s.SavePlayer(20001, after); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if got, err := s.LoadPlayer(20001); err != nil || !equalJSON(before, got) {
		t.Fatalf("partial transaction persisted: %s, %v", got, err)
	}
}

func TestSQLiteToNormalizedMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	src, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, src)
	if err := src.SaveGlobal("social", []byte(`{"friends":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := src.SaveRecord("gate", 123, 20001, []byte(`{"record":"abc"}`)); err != nil {
		t.Fatal(err)
	}
	src.Close()
	dst := normalizedTestStore(t)
	report, err := dst.ImportSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if report != (MySQLImportReport{Accounts: 1, Roles: 1, Players: 1, Globals: 1, Records: 1}) {
		t.Fatalf("report %+v", report)
	}
	if got, err := dst.LoadPlayer(20001); err != nil || !equalJSON(save, got) {
		t.Fatalf("player %s, %v", got, err)
	}
	if _, err := dst.ImportSQLite(path); err == nil {
		t.Fatal("nonempty target overwritten")
	}
	ro, err := OpenSQLiteReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if err := ro.SavePlayer(20001, []byte(`{}`)); err == nil {
		t.Fatal("source was writable")
	}
}

func TestMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("XYM_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set XYM_TEST_MYSQL_DSN to an empty MySQL 8 test database")
	}
	db, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"accounts", "roles", "player_states", "globals", "records"} {
		var n int64
		if err := db.db.Table(table).Count(&n).Error; err != nil || n != 0 {
			t.Fatalf("test database must be empty (%s): %d, %v", table, n, err)
		}
	}
	tx := db.db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	testDB := &MySQL{SQLStore: &SQLStore{db: tx, readDB: tx}}
	roundTrip(t, testDB)
	if got, err := testDB.LoadPlayer(20001); err != nil || !equalJSON(got, save) {
		t.Fatalf("MySQL round trip: %s %v", got, err)
	}
}
