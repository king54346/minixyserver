package store

import (
	"strings"
	"testing"
	"time"
)

func clearMySQLEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"DATABASE_URL", "XYM_MYSQL_DSN", "MYSQL_MAX_OPEN_CONNS", "MYSQL_MAX_IDLE_CONNS", "MYSQL_CONN_MAX_LIFETIME", "MYSQL_CONN_MAX_IDLE_TIME", "MYSQL_CONNECT_TIMEOUT", "MYSQL_READ_TIMEOUT", "MYSQL_WRITE_TIMEOUT", "MYSQL_LOCK_WAIT_TIMEOUT", "MYSQL_INTERPOLATE_PARAMS"} {
		t.Setenv(key, "")
	}
}

func TestMySQLConfigURLAndPool(t *testing.T) {
	clearMySQLEnv(t)
	t.Setenv("XYM_MYSQL_DSN", "legacy:legacy@tcp(localhost:3306)/old")
	t.Setenv("DATABASE_URL", "mysql://game:p%40ss%3Aword@127.0.0.1:3307/live?parseTime=true")
	t.Setenv("MYSQL_MAX_OPEN_CONNS", "12")
	t.Setenv("MYSQL_MAX_IDLE_CONNS", "3")
	t.Setenv("MYSQL_CONN_MAX_LIFETIME", "10m")
	t.Setenv("MYSQL_CONN_MAX_IDLE_TIME", "30s")
	t.Setenv("MYSQL_CONNECT_TIMEOUT", "3s")
	t.Setenv("MYSQL_READ_TIMEOUT", "7s")
	t.Setenv("MYSQL_WRITE_TIMEOUT", "8s")
	t.Setenv("MYSQL_LOCK_WAIT_TIMEOUT", "4s")
	cfg, opt, err := mysqlConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != "game" || cfg.Passwd != "p@ss:word" || cfg.DBName != "live" || cfg.Addr != "127.0.0.1:3307" {
		t.Fatal("URL credentials or endpoint parsed incorrectly")
	}
	if opt.maxOpen != 12 || opt.maxIdle != 3 || opt.lifetime != 10*time.Minute || opt.idleTime != 30*time.Second {
		t.Fatalf("pool %+v", opt)
	}
	if cfg.Timeout != 3*time.Second || cfg.ReadTimeout != 7*time.Second || cfg.WriteTimeout != 8*time.Second || cfg.Params["innodb_lock_wait_timeout"] != "4" {
		t.Fatal("timeouts not applied")
	}
	cfg, _, err = mysqlConfig("explicit:pw@tcp(localhost:3306)/explicit")
	if err != nil || cfg.DBName != "explicit" {
		t.Fatal("explicit DSN did not take precedence")
	}
}

func TestMySQLConfigLegacyDefaultsAndValidation(t *testing.T) {
	clearMySQLEnv(t)
	t.Setenv("XYM_MYSQL_DSN", "legacy:pw@tcp(localhost:3306)/legacy")
	cfg, opt, err := mysqlConfig("")
	if err != nil || cfg.DBName != "legacy" || opt.maxOpen != 32 || opt.maxIdle != 8 {
		t.Fatal("legacy configuration failed")
	}
	for _, item := range []struct{ key, value string }{
		{"MYSQL_MAX_OPEN_CONNS", "0"}, {"MYSQL_MAX_IDLE_CONNS", "33"}, {"MYSQL_READ_TIMEOUT", "oops"}, {"MYSQL_CONNECT_TIMEOUT", "0s"}, {"MYSQL_LOCK_WAIT_TIMEOUT", "500ms"},
	} {
		t.Run(item.key, func(t *testing.T) {
			t.Setenv(item.key, item.value)
			if _, _, err := mysqlConfig(""); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	t.Setenv("DATABASE_URL", "mysql://game:test-secret%zz@127.0.0.1/db")
	if _, _, err := mysqlConfig(""); err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatal("invalid URL leaked credentials or was accepted")
	}
}
