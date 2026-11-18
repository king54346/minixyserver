// Package model 是存档库（SQLite，GORM）的表结构：一张表一个类型，列的含义写在字段注释里。
//
//	accounts  账号（登录用）                         主键 uid，唯一索引 urs_key
//	roles     角色（主界面的等级、货币等常用字段）       主键 uid，索引 name
//	players   玩家玩法存档（整份 JSON）                主键 uid
//	globals   全服数据（好友 / 邮件、洞府、论道榜…，整份 JSON） 主键 name
//	records   战报录像（关卡录像、取经试炼录像…）        主键 (kind, rec_key)，索引 (kind, owner)
//	meta      库的元数据（schema_version）           主键 key
//
// 时间列都是 Unix 纳秒。玩法存档、全服数据的结构在开发期变动频繁，按 JSON 整份存；需要按条件查的
// （录像按玩家、按类型列出）单独成表并建索引，不用扫全表、不用拿保留 uid 凑键。
//
// 改表的规矩：只加表 / 加列（新列要有默认值或可空），不改、不删已有的列（store.ensureSchema 只补缺）。
// 结构上的迁移（把数据从一张表搬到另一张）写在 store/schema.go，按 schema_version 只跑一次。
package model

// Account 是一个登录账号。
type Account struct {
	UID       int32  `gorm:"column:uid;primaryKey;autoIncrement:false"`
	URSKey    string `gorm:"column:urs_key;size:128;not null;uniqueIndex"` // 小写账号名（登录不分大小写）
	URS       string `gorm:"column:urs;size:128;not null"`                 // 注册时的写法
	PwdSalt   string `gorm:"column:pwd_salt;size:32;not null"`             // 十六进制
	PwdHash   string `gorm:"column:pwd_hash;size:64;not null"`             // 十六进制
	CodeHash  string `gorm:"column:code_hash;size:64;not null;default:''"` // 安全码哈希，空 = 未设
	Device    string `gorm:"column:device;size:255;not null;default:''"`   // 最近一次登录的设备号
	SID       string `gorm:"column:sid;size:32;not null;default:''"`       // 最近一次发给客户端的 sid（断线重连用）
	CreatedAt int64  `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Account) TableName() string { return "accounts" }

// Role 是账号的角色（一账号一角色，uid 相同）。
type Role struct {
	UID         int32  `gorm:"column:uid;primaryKey;autoIncrement:false"`
	Name        string `gorm:"column:name;size:64;not null;index"`
	Icon        int    `gorm:"column:icon;not null"`
	Grade       int    `gorm:"column:grade;not null"` // 人物等级
	VIP         int    `gorm:"column:vip;not null;default:0"`
	TPoint      int    `gorm:"column:tpoint;not null"` // 体力
	HPoint      int    `gorm:"column:hpoint;not null"` // 活力
	Exp         int32  `gorm:"column:exp;not null"`
	Cash        int32  `gorm:"column:cash;not null"` // 银两
	Gold        int32  `gorm:"column:gold;not null"` // 元宝
	FriendPoint int32  `gorm:"column:friend_point;not null;default:0"`
	SoulExp     int32  `gorm:"column:soulexp;not null;default:0"` // 魂值
	StopUpgrade int8   `gorm:"column:stop_upgrade;type:tinyint;not null;default:0;check:chk_roles_stop_upgrade,stop_upgrade IN (0,1)"`
	Xiuwei      int64  `gorm:"column:xiuwei;not null;default:0"`
	XGrade      int    `gorm:"column:xgrade;not null;default:0"`
	CreatedAt   int64  `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Role) TableName() string { return "roles" }

// Player 是一名玩家的玩法存档（护法、背包、各玩法进度……，xymsrv.Player 的 JSON）。
type Player struct {
	UID       int32  `gorm:"column:uid;primaryKey;autoIncrement:false"`
	Save      string `gorm:"column:save;type:json;not null"` // JSON
	UpdatedAt int64  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Player) TableName() string { return "players" }

// Global 是一份全服数据（name 见 xymsrv/globals.go：social、dongfu、lundao、starwar、tianti、jiuyou、tree、liexi、qujing）。
type Global struct {
	Name      string `gorm:"column:name;size:64;primaryKey"`
	Data      string `gorm:"column:data;type:json;not null"` // JSON
	UpdatedAt int64  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Global) TableName() string { return "globals" }

// Record 是一条录像（重建 S_FIGHT_RECORD 用的双方阵容 + 战报字节，JSON）。
// kind 区分用途（gate 关卡录像：key = 关卡×8 + 普通/精英×4 + 档位；qujing_trial 取经试炼录像：key = 劫×4 + 槽位），
// owner 是录像主人的 uid（按玩家列出录像，关卡录像排行用）。
type Record struct {
	Kind      string `gorm:"column:kind;size:32;primaryKey;index:idx_records_owner,priority:1"`
	Key       int64  `gorm:"column:rec_key;primaryKey;autoIncrement:false"`
	Owner     int32  `gorm:"column:owner;not null;default:0;index:idx_records_owner,priority:2"`
	Data      string `gorm:"column:data;type:json;not null"` // JSON
	UpdatedAt int64  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Record) TableName() string { return "records" }

// Meta 是库的元数据（schema_version：结构迁移做到第几步）。
type Meta struct {
	Key   string `gorm:"column:name;size:64;primaryKey"`
	Value string `gorm:"column:value;type:text;not null"`
}

func (Meta) TableName() string { return "meta" }

// All 是全部表（建表 / 补列的顺序）。
func All() []any {
	return []any{&Account{}, &Role{}, &Player{}, &Global{}, &Record{}, &Meta{}}
}
