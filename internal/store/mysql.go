package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"log/slog"

	"turnrpg/internal/model"
)

// MySQL shares scalar-table CRUD with SQLite. Player snapshots are split into
// per-feature and per-entity rows; each save commits atomically for one UID.
type MySQL struct{ *SQLStore }

func OpenMySQL(dsn string) (*MySQL, error) {
	cfg, options, err := mysqlConfig(dsn)
	if err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, errors.New("store: cannot configure MySQL connection")
	}
	sqlDB.SetMaxOpenConns(options.maxOpen)
	sqlDB.SetMaxIdleConns(options.maxIdle)
	sqlDB.SetConnMaxLifetime(options.lifetime)
	sqlDB.SetConnMaxIdleTime(options.idleTime)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	err = sqlDB.PingContext(ctx)
	cancel()
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("store: connect MySQL: %w", err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB}), &gorm.Config{
		Logger: logger.Discard, PrepareStmt: !cfg.InterpolateParams, SkipDefaultTransaction: true,
	})
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("store: open MySQL: %w", err)
	}
	s := &MySQL{SQLStore: &SQLStore{db: db, readDB: db}}
	if err := s.ensureSchema(); err != nil {
		s.Close()
		return nil, err
	}
	slog.Info("MySQL connection pool configured", "max_open", options.maxOpen, "max_idle", options.maxIdle,
		"max_lifetime", options.lifetime, "max_idle_time", options.idleTime, "connect_timeout", cfg.Timeout,
		"read_timeout", cfg.ReadTimeout, "write_timeout", cfg.WriteTimeout, "lock_wait_seconds", options.lockSeconds,
		"interpolate_params", cfg.InterpolateParams)
	return s, nil
}

type mysqlPlayerState struct {
	UID       int32  `gorm:"primaryKey;autoIncrement:false"`
	Revision  uint64 `gorm:"not null;default:0"`
	UpdatedAt int64  `gorm:"not null;autoUpdateTime:false"`
}

func (mysqlPlayerState) TableName() string { return "player_states" }

type mysqlComponent struct {
	UID    int32  `gorm:"primaryKey;autoIncrement:false"`
	Name   string `gorm:"size:64;primaryKey"`
	Data   string `gorm:"type:json;not null"`
	Digest string `gorm:"size:64;not null"`
}

func (mysqlComponent) TableName() string { return "player_components" }

type mysqlEntity struct {
	UID       int32  `gorm:"primaryKey;autoIncrement:false"`
	EntityKey string `gorm:"size:64;primaryKey"`
	Position  int    `gorm:"not null"`
	Data      string `gorm:"type:json;not null"`
	Digest    string `gorm:"size:64;not null"`
}

var playerTables = []struct{ Field, Table, Key string }{
	{"pets", "player_pets", "npcnum"},
	{"items", "player_items", "pos"},
	{"suits", "player_suits", "itemnum"},
	{"books", "player_books", "pos"},
	{"lineups", "player_lineups", "no"},
}

func (s *MySQL) ensureSchema() error {
	db := s.db
	if db.Dialector.Name() == "mysql" {
		db = db.Set("gorm:table_options", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin")
	}
	if err := ensureSchema(db, &model.Account{}, &model.Role{}, &model.Global{}, &model.Record{}, &model.Meta{},
		&mysqlPlayerState{}, &mysqlComponent{}); err != nil {
		return fmt.Errorf("store: MySQL schema: %w", err)
	}
	for _, spec := range playerTables {
		if err := ensureSchema(db.Table(spec.Table), &mysqlEntity{}); err != nil {
			return fmt.Errorf("store: MySQL table %s: %w", spec.Table, err)
		}
	}
	if err := ensureRoleConstraints(s.db); err != nil {
		return err
	}
	return upsert(s.db, &model.Meta{Key: "mysql_schema_version", Value: "1"})
}
