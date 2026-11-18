package store

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	driver "github.com/go-sql-driver/mysql"
)

type mysqlOptions struct {
	maxOpen, maxIdle     int
	lifetime, idleTime   time.Duration
	connect, read, write time.Duration
	lockSeconds          int64
}

func mysqlConfig(dsn string) (*driver.Config, mysqlOptions, error) {
	o := mysqlOptions{maxOpen: 32, maxIdle: 8, lifetime: 5 * time.Minute, idleTime: time.Minute,
		connect: 5 * time.Second, read: 10 * time.Second, write: 10 * time.Second, lockSeconds: 5}
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = os.Getenv("XYM_MYSQL_DSN")
	}
	if dsn == "" {
		return nil, o, errors.New("store: set DATABASE_URL (or XYM_MYSQL_DSN) for MySQL")
	}
	if strings.HasPrefix(dsn, "mysql://") {
		u, err := url.Parse(dsn)
		if err != nil || u.User == nil || u.Hostname() == "" || u.Fragment != "" || u.Path == "" || u.Path == "/" {
			return nil, o, errors.New("store: invalid MySQL DATABASE_URL")
		}
		c := driver.NewConfig()
		c.User = u.User.Username()
		c.Passwd, _ = u.User.Password()
		port := u.Port()
		if port == "" {
			port = "3306"
		}
		c.Net = "tcp"
		c.Addr = net.JoinHostPort(u.Hostname(), port)
		c.DBName = strings.TrimPrefix(u.Path, "/")
		c.Params = make(map[string]string)
		for key, values := range u.Query() {
			if len(values) != 1 {
				return nil, o, errors.New("store: duplicate MySQL URL parameter")
			}
			c.Params[key] = values[0]
		}
		dsn = c.FormatDSN()
	}
	cfg, err := driver.ParseDSN(dsn)
	if err != nil || cfg.DBName == "" {
		return nil, o, errors.New("store: invalid MySQL connection configuration")
	}
	// Transaction-bound prepared statements are repeatedly prepared/closed on
	// commit. The driver's utf8mb4-aware parameter encoder avoids these extra
	// round trips while retaining parameter escaping and atomic transactions.
	if !strings.Contains(dsn, "interpolateParams=") {
		cfg.InterpolateParams = true
	}
	if raw := os.Getenv("MYSQL_INTERPOLATE_PARAMS"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, o, errors.New("store: invalid MYSQL_INTERPOLATE_PARAMS boolean")
		}
		cfg.InterpolateParams = value
	}
	for _, item := range []struct {
		name  string
		value *int
		min   int
	}{
		{"MYSQL_MAX_OPEN_CONNS", &o.maxOpen, 1}, {"MYSQL_MAX_IDLE_CONNS", &o.maxIdle, 0},
	} {
		if raw := os.Getenv(item.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < item.min {
				return nil, o, fmt.Errorf("store: invalid %s", item.name)
			}
			*item.value = value
		}
	}
	if o.maxIdle > o.maxOpen {
		return nil, o, errors.New("store: MYSQL_MAX_IDLE_CONNS exceeds MYSQL_MAX_OPEN_CONNS")
	}
	for _, item := range []struct {
		name  string
		value *time.Duration
		zero  bool
	}{
		{"MYSQL_CONN_MAX_LIFETIME", &o.lifetime, true}, {"MYSQL_CONN_MAX_IDLE_TIME", &o.idleTime, true},
		{"MYSQL_CONNECT_TIMEOUT", &o.connect, false}, {"MYSQL_READ_TIMEOUT", &o.read, false}, {"MYSQL_WRITE_TIMEOUT", &o.write, false},
	} {
		if raw := os.Getenv(item.name); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil || value < 0 || value == 0 && !item.zero {
				return nil, o, fmt.Errorf("store: invalid %s duration", item.name)
			}
			*item.value = value
		}
	}
	if raw := os.Getenv("MYSQL_LOCK_WAIT_TIMEOUT"); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value < time.Second || value%time.Second != 0 || value/time.Second > 1073741824 {
			return nil, o, errors.New("store: MYSQL_LOCK_WAIT_TIMEOUT must be a positive whole number of seconds")
		}
		o.lockSeconds = int64(value / time.Second)
	}
	cfg.ParseTime = true
	if cfg.Timeout == 0 || os.Getenv("MYSQL_CONNECT_TIMEOUT") != "" {
		cfg.Timeout = o.connect
	}
	if cfg.ReadTimeout == 0 || os.Getenv("MYSQL_READ_TIMEOUT") != "" {
		cfg.ReadTimeout = o.read
	}
	if cfg.WriteTimeout == 0 || os.Getenv("MYSQL_WRITE_TIMEOUT") != "" {
		cfg.WriteTimeout = o.write
	}
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	cfg.Params["charset"] = "utf8mb4"
	cfg.Params["innodb_lock_wait_timeout"] = strconv.FormatInt(o.lockSeconds, 10)
	return cfg, o, nil
}
