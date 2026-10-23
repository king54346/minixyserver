// Package store 是用户存档的持久化层：账号、角色、玩家玩法存档。
//
// 两种实现：SQLite（默认，data/xym/xym.db）与 File（原来的 JSON 文件：accounts.json、roles.json、
// players/<uid>.json，兜底 / 回退用）。业务侧（xymsrv）把全部数据缓存在内存里，修改后立即调
// Save* 落盘；Load* 只在启动或首次访问时调用。
//
// Account / Role 的字段与 xymsrv 里的同名类型逐一对应（xymsrv 直接做结构体类型转换，两边不一致会
// 编译失败）；玩家存档结构在开发期变动频繁，这里只当作不透明的 JSON 存取。
package store

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound：LoadPlayer 时该玩家还没有存档。
var ErrNotFound = errors.New("store: not found")

type Account struct {
	UID      int32     `json:"uid"`
	URS      string    `json:"urs"`
	PwdSalt  string    `json:"pwd_salt"`
	PwdHash  string    `json:"pwd_hash"`
	CodeHash string    `json:"code_hash,omitempty"` // 安全码（转小写后）的哈希
	Device   string    `json:"device,omitempty"`    // 最近一次登录的设备号
	Created  time.Time `json:"created"`
	SID      string    `json:"sid,omitempty"` // 最近一次发给客户端的 sid（断线重连用）
}

type Role struct {
	UID         int32     `json:"uid"`
	Name        string    `json:"name"`
	Icon        int       `json:"icon"`
	Grade       int       `json:"grade"`
	Exp         int32     `json:"exp"`
	Cash        int32     `json:"cash"`
	Gold        int32     `json:"gold"`
	FriendPoint int32     `json:"friend_point,omitempty"`
	SoulExp     int32     `json:"soulexp,omitempty"`
	VIP         int       `json:"vip"`
	TPoint      int       `json:"tpoint"`
	HPoint      int       `json:"hpoint"`
	Created     time.Time `json:"created"`
}

type Store interface {
	LoadAccounts() ([]Account, error)
	SaveAccount(Account) error
	LoadRoles() ([]Role, error)
	SaveRole(Role) error
	// LoadPlayer 返回玩家存档（JSON）；还没有存档时返回 ErrNotFound。
	LoadPlayer(uid int32) ([]byte, error)
	SavePlayer(uid int32, save []byte) error
	Close() error
}

// AccountKey 是账号名的唯一键：去首尾空白、转小写（登录不分大小写）。
func AccountKey(urs string) string { return strings.ToLower(strings.TrimSpace(urs)) }

// Open 按 kind（sqlite / file）打开存档。sqlite 首次打开空库时，若 dir 下有旧的 JSON 存档会自动导入。
func Open(kind, dir, dbPath string) (Store, error) {
	switch kind {
	case "sqlite":
		db, err := OpenSQLite(dbPath)
		if err != nil {
			return nil, err
		}
		if _, err := Migrate(db, dir); err != nil {
			db.Close()
			return nil, err
		}
		return db, nil
	case "file":
		return OpenFile(dir), nil
	}
	return nil, fmt.Errorf("store: unknown kind %q (sqlite|file)", kind)
}
