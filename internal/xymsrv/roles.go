package xymsrv

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// Role 是一个账号的角色（一账号一角色，UID 与账号相同）。字段对应 S_LOGIN_SUC；
// 与 store.Role 字段逐一对应（存取时直接做类型转换）。
type Role struct {
	UID         int32     `json:"uid"`
	Name        string    `json:"name"`
	Icon        int       `json:"icon"`
	Grade       int       `json:"grade"`
	Exp         int32     `json:"exp"`
	Cash        int32     `json:"cash"`                   // 铜钱
	Gold        int32     `json:"gold"`                   // 元宝
	FriendPoint int32     `json:"friend_point,omitempty"` // 友情点（招募）
	SoulExp     int32     `json:"soulexp,omitempty"`      // 魂值（开宝箱得）
	VIP         int       `json:"vip"`
	TPoint      int       `json:"tpoint"` // 体力
	HPoint      int       `json:"hpoint"` // 耐力
	Created     time.Time `json:"created"`
}

// 新角色的初值。经验表在服务端（客户端只拿 next_exp），本地没有原表，P2 补玩法时再换。
const (
	maxTPoint   = 120 // 用户要求（官方 60）
	maxHPoint   = 40  // 用户要求（官方 20）
	maxRoleName = 12  // 字符数
	defaultVIP  = 15  // 本服默认所有角色 VIP15（没有充值系统）
)

var (
	ErrRoleExists = errors.New("account already has a role")
	ErrNameUsed   = errors.New("role name in use")
	ErrBadName    = errors.New("invalid role name")
)

// Roles 是全部角色（内存缓存，修改后立即写入 store）。
type Roles struct {
	mu    sync.Mutex
	st    store.Store
	ByUID map[int32]*Role
}

func OpenRoles(st store.Store) (*Roles, error) {
	list, err := st.LoadRoles()
	if err != nil {
		return nil, err
	}
	r := &Roles{st: st, ByUID: map[int32]*Role{}}
	for _, x := range list {
		ro := Role(x)
		if ro.VIP < defaultVIP { // 旧角色补到默认 VIP
			ro.VIP = defaultVIP
			if err := st.SaveRole(store.Role(ro)); err != nil {
				return nil, err
			}
		}
		r.ByUID[ro.UID] = &ro
	}
	return r, nil
}

func validRoleName(name string) bool {
	n := utf8.RuneCountInString(name)
	if n < 1 || n > maxRoleName || !utf8.ValidString(name) {
		return false
	}
	for _, c := range name {
		if unicode.IsSpace(c) || unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// Get 返回角色的副本；没有角色返回 nil。
// UIDs 是全部角色号（升序）。
func (r *Roles) UIDs() []int32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int32, 0, len(r.ByUID))
	for uid := range r.ByUID {
		out = append(out, uid)
	}
	slices.Sort(out)
	return out
}

func (r *Roles) Get(uid int32) *Role {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ro := r.ByUID[uid]; ro != nil {
		c := *ro
		return &c
	}
	return nil
}

// Create 建角（帧 3 C_CREATE）。名字全服唯一，不分大小写。
func (r *Roles) Create(uid int32, icon int, name string) (*Role, error) {
	name = strings.TrimSpace(name)
	if !validRoleName(name) {
		return nil, ErrBadName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ByUID[uid] != nil {
		return nil, ErrRoleExists
	}
	for _, ro := range r.ByUID {
		if strings.EqualFold(ro.Name, name) {
			return nil, ErrNameUsed
		}
	}
	if icon != 1 && icon != 2 {
		icon = 1
	}
	ro := &Role{UID: uid, Name: name, Icon: icon, Grade: 1, VIP: defaultVIP, TPoint: maxTPoint, HPoint: maxHPoint, Created: time.Now()}
	r.ByUID[uid] = ro
	if err := r.st.SaveRole(store.Role(*ro)); err != nil {
		delete(r.ByUID, uid)
		return nil, err
	}
	c := *ro
	return &c, nil
}

// Update 在锁内修改角色并落盘，返回修改后的副本。fn 返回 false 表示不改（不落盘）。
func (r *Roles) Update(uid int32, fn func(*Role) bool) (*Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ro := r.ByUID[uid]
	if ro == nil {
		return nil, errors.New("no role")
	}
	old := *ro
	if fn(ro) {
		if err := r.st.SaveRole(store.Role(*ro)); err != nil {
			*ro = old
			return nil, err
		}
	}
	c := *ro
	return &c, nil
}

// nextExp 是 S_LOGIN_SUC / S_ATTR_UPDATE 的 next_exp：满级时官方发 999999。
func nextExp(grade int) int32 {
	if n := gamedata.RoleNeed(grade); n > 0 {
		return n
	}
	return 999999
}

// AddExp 加人物经验并升级（满级后经验照加，不再升级），返回升了几级。
func (ro *Role) AddExp(n int32) int {
	ro.Exp += n
	up := 0
	for need := gamedata.RoleNeed(ro.Grade); need > 0 && ro.Exp >= need; need = gamedata.RoleNeed(ro.Grade) {
		ro.Exp -= need
		ro.Grade++
		up++
	}
	return up
}

// PetCap 是卡牌等级上限：人物等级×2，且不超过 100。
func (ro *Role) PetCap() int { return min(ro.Grade*2, gamedata.MaxPetGrade) }

// LoginSuc 编出 S_LOGIN_SUC 的字段。
func (ro *Role) LoginSuc(urs string, now time.Time) xym.Msg {
	return xym.Msg{
		"uid": ro.UID, "icon": ro.Icon, "origin_icon": ro.Icon, "name": ro.Name,
		"tpoint": ro.TPoint, "max_tpoint": maxTPoint, "hpoint": ro.HPoint, "max_hpoint": maxHPoint,
		"grade": ro.Grade, "exp": ro.Exp, "next_exp": nextExp(ro.Grade),
		"cash": ro.Cash, "gold": ro.Gold, "vip": ro.VIP, "soulexp": ro.SoulExp, "leader": 1, "friend_point": ro.FriendPoint,
		"srv_time": now.Unix(), "urs": urs, "serverid": 1, // 单区「细水长流」，hostid 1（offline_probe.zone_list）
	}
}
