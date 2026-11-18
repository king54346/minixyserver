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

	"turnrpg/internal/model"
)

// 表结构见 internal/model。启动时 ensureSchema：表不存在就建，已有的表只补缺的列和索引；
// 之后 upgradeSchema 做结构迁移（schema.go）。
// 不用 AutoMigrate：它在 SQLite 上发现已有列的定义与模型有出入时会重建整张表（建临时表再拷数据），
// 对已有存档风险太大。哈希 / 盐沿用 JSON 存档时的十六进制文本；时间为 Unix 纳秒。

// SQLite 存档。写连接保持串行；独立读连接池利用 WAL 并发读取，避免读档排在大量写档后面。
type SQLStore struct {
	db     *gorm.DB
	readDB *gorm.DB
}

type SQLite struct{ *SQLStore }

func OpenSQLite(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// WAL + synchronous NORMAL：写入只追加日志，读写互不阻塞；cache_size 负数 = KB（16MB 页缓存）；
	// temp_store 内存：排序 / 临时表不落盘。
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)" +
		"&_pragma=cache_size(-16000)&_pragma=temp_store(MEMORY)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard, PrepareStmt: true,
		SkipDefaultTransaction: true})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	s := &SQLite{SQLStore: &SQLStore{db: db}}
	if err := ensureSchema(db, model.All()...); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("store: init schema %s: %w", path, err)
	}
	if err := s.upgradeSchema(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("store: upgrade schema %s: %w", path, err)
	}
	if err := ensureRoleConstraints(db); err != nil {
		sqlDB.Close()
		return nil, err
	}
	// 所有结构变更完成后再打开读池。WAL 下读者可读取已提交的快照，
	// 不占用唯一的写连接；写入仍由单连接串行，避免 SQLITE_BUSY。
	readDB, err := gorm.Open(sqlite.Open(dsn+"&_pragma=query_only(ON)"), &gorm.Config{Logger: logger.Discard, PrepareStmt: true,
		SkipDefaultTransaction: true})
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("store: open read pool %s: %w", path, err)
	}
	readSQL, err := readDB.DB()
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	readSQL.SetMaxOpenConns(4)
	readSQL.SetMaxIdleConns(4)
	s.readDB = readDB
	return s, nil
}

// ensureSchema 建缺的表、给已有的表补缺的列（新加的列要有默认值或可空）和索引；从不修改 / 删除已有的列。
func ensureSchema(db *gorm.DB, models ...any) error {
	m := db.Migrator()
	for _, mdl := range models {
		if !m.HasTable(mdl) {
			if err := m.CreateTable(mdl); err != nil {
				return err
			}
			continue
		}
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(mdl); err != nil {
			return err
		}
		for _, f := range stmt.Schema.Fields {
			if f.DBName != "" && !m.HasColumn(mdl, f.DBName) {
				if err := m.AddColumn(mdl, f.Name); err != nil {
					return fmt.Errorf("add column %s.%s: %w", stmt.Schema.Table, f.DBName, err)
				}
			}
		}
		for _, idx := range stmt.Schema.ParseIndexes() {
			if !m.HasIndex(mdl, idx.Name) {
				if err := m.CreateIndex(mdl, idx.Name); err != nil {
					return fmt.Errorf("add index %s.%s: %w", stmt.Schema.Table, idx.Name, err)
				}
			}
		}
	}
	return nil
}

func (s *SQLStore) Close() error {
	var readErr error
	if s.readDB != nil && s.readDB != s.db {
		readDB, err := s.readDB.DB()
		if err != nil {
			readErr = err
		} else {
			readErr = readDB.Close()
		}
	}
	sqlDB, err := s.db.DB()
	if err != nil {
		return errors.Join(readErr, err)
	}
	return errors.Join(readErr, sqlDB.Close())
}

// upsert 按主键插入或整行覆盖。
func upsert(db *gorm.DB, row any) error {
	return db.Clauses(clause.OnConflict{UpdateAll: true}).Create(row).Error
}

func now() int64 { return time.Now().UnixNano() }

// ---- 账号 / 角色 ----

func (s *SQLStore) LoadAccounts() ([]Account, error) {
	var rows []model.Account
	if err := s.readDB.Order("uid").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, Account{UID: r.UID, URS: r.URS, PwdSalt: r.PwdSalt, PwdHash: r.PwdHash,
			CodeHash: r.CodeHash, Device: r.Device, Created: time.Unix(0, r.CreatedAt), SID: r.SID})
	}
	return out, nil
}

func (s *SQLStore) SaveAccount(a Account) error { return saveAccount(s.db, a) }

func saveAccount(db *gorm.DB, a Account) error {
	return upsert(db, &model.Account{UID: a.UID, URSKey: AccountKey(a.URS), URS: a.URS, PwdSalt: a.PwdSalt,
		PwdHash: a.PwdHash, CodeHash: a.CodeHash, Device: a.Device, SID: a.SID, CreatedAt: a.Created.UnixNano()})
}

func (s *SQLStore) LoadRoles() ([]Role, error) {
	var rows []model.Role
	if err := s.readDB.Order("uid").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Role, 0, len(rows))
	for _, r := range rows {
		out = append(out, Role{UID: r.UID, Name: r.Name, Icon: r.Icon, Grade: r.Grade, Exp: r.Exp, Cash: r.Cash,
			Gold: r.Gold, FriendPoint: r.FriendPoint, SoulExp: r.SoulExp, VIP: r.VIP, TPoint: r.TPoint,
			HPoint: r.HPoint, StopUpgrade: r.StopUpgrade, Xiuwei: r.Xiuwei, XGrade: r.XGrade, Created: time.Unix(0, r.CreatedAt)})
	}
	return out, nil
}

func (s *SQLStore) SaveRole(r Role) error { return saveRole(s.db, r) }

func saveRole(db *gorm.DB, r Role) error {
	return upsert(db, &model.Role{UID: r.UID, Name: r.Name, Icon: r.Icon, Grade: r.Grade, Exp: r.Exp, Cash: r.Cash,
		Gold: r.Gold, FriendPoint: r.FriendPoint, SoulExp: r.SoulExp, VIP: r.VIP, TPoint: r.TPoint,
		HPoint: r.HPoint, StopUpgrade: r.StopUpgrade, Xiuwei: r.Xiuwei, XGrade: r.XGrade, CreatedAt: r.Created.UnixNano()})
}

// ---- 玩家存档 ----

func (s *SQLStore) LoadPlayer(uid int32) ([]byte, error) {
	var row model.Player
	err := s.readDB.Take(&row, "uid = ?", uid).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return []byte(row.Save), nil
}

func (s *SQLStore) SavePlayer(uid int32, save []byte) error { return savePlayer(s.db, uid, save) }

func savePlayer(db *gorm.DB, uid int32, save []byte) error {
	return upsert(db, &model.Player{UID: uid, Save: string(save), UpdatedAt: now()})
}

// ---- 全服数据 ----

func (s *SQLStore) LoadGlobal(name string) ([]byte, error) {
	var row model.Global
	err := s.readDB.Take(&row, "name = ?", name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return []byte(row.Data), nil
}

func (s *SQLStore) SaveGlobal(name string, data []byte) error { return saveGlobal(s.db, name, data) }

func saveGlobal(db *gorm.DB, name string, data []byte) error {
	return upsert(db, &model.Global{Name: name, Data: string(data), UpdatedAt: now()})
}

// ---- 录像 ----

func (s *SQLStore) LoadRecord(kind string, key int64) ([]byte, error) {
	var row model.Record
	err := s.readDB.Take(&row, "kind = ? AND rec_key = ?", kind, key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return []byte(row.Data), nil
}

func (s *SQLStore) SaveRecord(kind string, key int64, owner int32, data []byte) error {
	return saveRecord(s.db, kind, key, owner, data)
}

func saveRecord(db *gorm.DB, kind string, key int64, owner int32, data []byte) error {
	return upsert(db, &model.Record{Kind: kind, Key: key, Owner: owner, Data: string(data), UpdatedAt: now()})
}

func (s *SQLStore) ListRecords(kind string) ([]Record, error) {
	var rows []model.Record
	if err := s.readDB.Where("kind = ?", kind).Order("rec_key").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, Record{Kind: r.Kind, Key: r.Key, Owner: r.Owner, Data: []byte(r.Data)})
	}
	return out, nil
}

// empty 报告库里是否还没有任何存档（JSON 导入只对空库做）。
func (s *SQLStore) empty() (bool, error) {
	for _, m := range []any{&model.Account{}, &model.Role{}, &model.Player{}, &model.Global{}} {
		var n int64
		if err := s.db.Model(m).Count(&n).Error; err != nil || n > 0 {
			return false, err
		}
	}
	return true, nil
}

// SaveRoleAndGlobal commits a purchase and its delivered global state together.
func (s *SQLStore) SaveRoleAndGlobal(role Role, name string, data []byte) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := saveRole(tx, role); err != nil {
			return err
		}
		return saveGlobal(tx, name, data)
	})
}
