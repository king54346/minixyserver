package store

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGMDeleteMySQLIsolated(t *testing.T) {
	dsn := os.Getenv("XYM_GM_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires a disposable MySQL database")
	}
	cfg, _, err := mysqlConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.DBName, "xym_gm_verify_") {
		t.Fatal("requires a disposable xym_gm_verify_ schema")
	}
	s, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, uid := range []int32{1, 2} {
		if err := s.SaveAccount(Account{UID: uid, URS: fmt.Sprintf("gm-probe-%d", uid), Created: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveRole(Role{UID: uid, Name: fmt.Sprintf("probe-%d", uid), Grade: 30, Gold: 777, Created: time.Now()}); err != nil {
			t.Fatal(err)
		}
		data := []byte(fmt.Sprintf(`{"uid":%d,"fno":1,"pets":[{"npcnum":10001,"type":13001}],"items":[{"pos":1,"type":11487,"amount":3}],"suits":[{"itemnum":1,"pos":1,"type":1001}],"books":[{"pos":1,"type":12101}],"lineups":[{"no":1,"slots":[]}]}`, uid))
		if err := s.SavePlayer(uid, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteAccountData(1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadPlayer(1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted snapshot remained: %v", err)
	}
	accounts, err := s.LoadAccounts()
	if err != nil || len(accounts) != 1 || accounts[0].UID != 2 {
		t.Fatal("account isolation", err)
	}
	roles, err := s.LoadRoles()
	if err != nil || len(roles) != 1 || roles[0].UID != 2 || roles[0].Gold != 777 {
		t.Fatal("role isolation", err)
	}
	if _, err := s.LoadPlayer(2); err != nil {
		t.Fatal("surviving snapshot", err)
	}
	for _, table := range []string{"player_pets", "player_items", "player_suits", "player_books", "player_lineups", "player_components", "player_states"} {
		var count int64
		if err := s.db.Table(table).Where("uid = ?", 1).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s retained deleted rows", table)
		}
		if err := s.db.Table(table).Where("uid = ?", 2).Count(&count).Error; err != nil || count == 0 {
			t.Fatalf("%s lost survivor rows", table)
		}
	}
}
