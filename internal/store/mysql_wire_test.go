package store

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestMySQLParameterModeConfig(t *testing.T) {
	const dsn = "user:pass@tcp(127.0.0.1:3306)/wiretest"
	t.Setenv("MYSQL_INTERPOLATE_PARAMS", "")
	cfg, _, err := mysqlConfig(dsn)
	if err != nil || !cfg.InterpolateParams {
		t.Fatal("default parameter encoding disabled", err)
	}
	cfg, _, err = mysqlConfig(dsn + "?interpolateParams=false")
	if err != nil || cfg.InterpolateParams {
		t.Fatal("explicit DSN setting ignored", err)
	}
	t.Setenv("MYSQL_INTERPOLATE_PARAMS", "true")
	cfg, _, err = mysqlConfig(dsn + "?interpolateParams=false")
	if err != nil || !cfg.InterpolateParams {
		t.Fatal("environment override ignored", err)
	}
	t.Setenv("MYSQL_INTERPOLATE_PARAMS", "invalid")
	if _, _, err = mysqlConfig(dsn); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

// Opt in with a fresh disposable database. Runs both wire modes against real
// MySQL, checking stored bytes, partial updates, and actual COM_STMT_PREPARE use.
func TestMySQLWireModes(t *testing.T) {
	dsn := os.Getenv("XYM_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set XYM_TEST_MYSQL_DSN to an empty disposable schema")
	}
	for i, mode := range []bool{false, true} {
		t.Run(strconv.FormatBool(mode), func(t *testing.T) {
			t.Setenv("MYSQL_INTERPOLATE_PARAMS", strconv.FormatBool(mode))
			t.Setenv("MYSQL_MAX_OPEN_CONNS", "1")
			t.Setenv("MYSQL_MAX_IDLE_CONNS", "1")
			s, err := OpenMySQL(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			uid := int32(30001 + i)
			var count int64
			if err := s.db.Table("player_states").Where("uid = ?", uid).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("wire test UID must be unused", err)
			}
			counter := func() int64 {
				var name, value string
				if err := s.db.Raw("SHOW SESSION STATUS LIKE 'Com_stmt_prepare'").Row().Scan(&name, &value); err != nil {
					t.Fatal(err)
				}
				n, _ := strconv.ParseInt(value, 10, 64)
				return n
			}
			pets := make([]map[string]any, 300)
			for n := range pets {
				pets[n] = map[string]any{"npcnum": n + 10001, "grade": 100, "note": "中文😀'\"\\\n\x00; DROP TABLE roles; --"}
			}
			value := map[string]any{"uid": uid, "pets": pets, "items": []any{}, "next_npc": 10400, "counter": 0}
			before := counter()
			start := time.Now()
			for n := range 40 {
				value["counter"] = n
				data, _ := json.Marshal(value)
				if err := s.SavePlayer(uid, data); err != nil {
					t.Fatal(err)
				}
				got, err := s.LoadPlayer(uid)
				if err != nil || !equalJSON(data, got) {
					t.Fatal("escaped JSON or updated value corrupted", err)
				}
			}
			prepares := counter() - before
			if mode && prepares != 0 {
				t.Fatalf("interpolated mode still prepares %d statements", prepares)
			}
			t.Log(fmt.Sprintf("40 atomic saves + reloads: %v, prepares=%d", time.Since(start), prepares))
		})
	}
}
