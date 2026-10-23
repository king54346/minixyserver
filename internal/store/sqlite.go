package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite" // GORM 的纯 Go SQLite 方言（底层 modernc，无 cgo）
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// 表结构即下面三个模型。启动时 ensureSchema：表不存在就建，已有的表只补缺的列。
// 不用 AutoMigrate：它在 SQLite 上发现已有列的定义与模型有出入时会重建整张表（建临时表再拷数据），
// 对已有存档风险太大。
// 哈希 / 盐沿用 JSON 存档时的十六进制文本（迁移不做转换）；时间为 Unix 纳秒。
// 玩家玩法存档整体存 JSON（结构在开发期变动频繁，拆关系表留待以后）。

type accountRow struct {
	UID       int32  `gorm:"column:uid;primaryKey;autoIncrement:false"`
	URSKey    string `gorm:"column:urs_key;not null;uniqueIndex"` // 小写账号名
	URS       string `gorm:"column:urs;not null"`                 // 注册时的写法
	PwdSalt   string `gorm:"column:pwd_salt;not null"`
	PwdHash   string `gorm:"column:pwd_hash;not null"`
	CodeHash  string `gorm:"column:code_hash;not null;default:''"` // 安全码哈希，空 = 未设
	Device    string `gorm:"column:device;not null;default:''"`
	SID       string `gorm:"column:sid;not null;default:''"`
	CreatedAt int64  `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (accountRow) TableName() string { return "accounts" }

type roleRow struct {
	UID         int32  `gorm:"column:uid;primaryKey;autoIncrement:false"` // 与账号 uid 相同（一账号一角色）
	Name        string `gorm:"column:name;not null"`
	Icon        int    `gorm:"column:icon;not null"`
	Grade       int    `gorm:"column:grade;not null"`
	Exp         int32  `gorm:"column:exp;not null"`
	Cash        int32  `gorm:"column:cash;not null"`
	Gold        int32  `gorm:"column:gold;not null"`
	FriendPoint int32  `gorm:"column:friend_point;not null;default:0"`
	SoulExp     int32  `gorm:"column:soulexp;not null;default:0"`
	VIP         int    `gorm:"column:vip;not null;default:0"`
	TPoint      int    `gorm:"column:tpoint;not null"`
	HPoint      int    `gorm:"column:hpoint;not null"`
	CreatedAt   int64  `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (roleRow) TableName() string { return "roles" }

type playerRow struct {
	UID       int32  `gorm:"column:uid;primaryKey;autoIncrement:false"`
	Save      string `gorm:"column:save;not null"` // JSON
	UpdatedAt int64  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (playerRow) TableName() string { return "players" }

// SQLite 存档。只开一个连接：写入天然串行，不会撞 SQLITE_BUSY；本服的量远用不到并发写。
type SQLite struct{ db *gorm.DB }

func OpenSQLite(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	if err := ensureSchema(db, &accountRow{}, &roleRow{}, &playerRow{}); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("store: init schema %s: %w", path, err)
	}
	return &SQLite{db: db}, nil
}

// ensureSchema 建缺的表、给已有的表补缺的列（新加的列要有默认值或可空）；从不修改 / 删除已有的列。
func ensureSchema(db *gorm.DB, models ...any) error {
	m := db.Migrator()
	for _, model := range models {
		if !m.HasTable(model) {
			if err := m.CreateTable(model); err != nil {
				return err
			}
			continue
		}
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(model); err != nil {
			return err
		}
		for _, f := range stmt.Schema.Fields {
			if f.DBName != "" && !m.HasColumn(model, f.DBName) {
				if err := m.AddColumn(model, f.Name); err != nil {
					return fmt.Errorf("add column %s.%s: %w", stmt.Schema.Table, f.DBName, err)
				}
			}
		}
	}
	return nil
}

func (s *SQLite) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// upsert 按主键插入或整行覆盖。
func upsert(db *gorm.DB, row any) error {
	return db.Clauses(clause.OnConflict{UpdateAll: true}).Create(row).Error
}

func (s *SQLite) LoadAccounts() ([]Account, error) {
	var rows []accountRow
	if err := s.db.Order("uid").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, Account{UID: r.UID, URS: r.URS, PwdSalt: r.PwdSalt, PwdHash: r.PwdHash,
			CodeHash: r.CodeHash, Device: r.Device, Created: time.Unix(0, r.CreatedAt), SID: r.SID})
	}
	return out, nil
}

func (s *SQLite) SaveAccount(a Account) error { return saveAccount(s.db, a) }

func saveAccount(db *gorm.DB, a Account) error {
	return upsert(db, &accountRow{UID: a.UID, URSKey: AccountKey(a.URS), URS: a.URS, PwdSalt: a.PwdSalt,
		PwdHash: a.PwdHash, CodeHash: a.CodeHash, Device: a.Device, SID: a.SID, CreatedAt: a.Created.UnixNano()})
}

func (s *SQLite) LoadRoles() ([]Role, error) {
	var rows []roleRow
	if err := s.db.Order("uid").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Role, 0, len(rows))
	for _, r := range rows {
		out = append(out, Role{UID: r.UID, Name: r.Name, Icon: r.Icon, Grade: r.Grade, Exp: r.Exp, Cash: r.Cash,
			Gold: r.Gold, FriendPoint: r.FriendPoint, SoulExp: r.SoulExp, VIP: r.VIP, TPoint: r.TPoint,
			HPoint: r.HPoint, Created: time.Unix(0, r.CreatedAt)})
	}
	return out, nil
}

func (s *SQLite) SaveRole(r Role) error { return saveRole(s.db, r) }

func saveRole(db *gorm.DB, r Role) error {
	return upsert(db, &roleRow{UID: r.UID, Name: r.Name, Icon: r.Icon, Grade: r.Grade, Exp: r.Exp, Cash: r.Cash,
		Gold: r.Gold, FriendPoint: r.FriendPoint, SoulExp: r.SoulExp, VIP: r.VIP, TPoint: r.TPoint,
		HPoint: r.HPoint, CreatedAt: r.Created.UnixNano()})
}

func (s *SQLite) LoadPlayer(uid int32) ([]byte, error) {
	var row playerRow
	err := s.db.Take(&row, "uid = ?", uid).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return []byte(row.Save), nil
}

func (s *SQLite) SavePlayer(uid int32, save []byte) error { return savePlayer(s.db, uid, save) }

func savePlayer(db *gorm.DB, uid int32, save []byte) error {
	return upsert(db, &playerRow{UID: uid, Save: string(save), UpdatedAt: time.Now().UnixNano()})
}

// empty 报告库里是否还没有任何存档（迁移只对空库做）。
func (s *SQLite) empty() (bool, error) {
	for _, m := range []any{&accountRow{}, &roleRow{}, &playerRow{}} {
		var n int64
		if err := s.db.Model(m).Count(&n).Error; err != nil || n > 0 {
			return false, err
		}
	}
	return true, nil
}
