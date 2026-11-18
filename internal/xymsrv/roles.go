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
	Created     time.Time `json:"created"`
	Name        string    `json:"name"`
	Icon        int       `json:"icon"`
	Grade       int       `json:"grade"`
	VIP         int       `json:"vip"`
	TPoint      int       `json:"tpoint"` // 体力
	HPoint      int       `json:"hpoint"` // 耐力
	UID         int32     `json:"uid"`
	Exp         int32     `json:"exp"`
	Cash        int32     `json:"cash"`                   // 铜钱
	Gold        int32     `json:"gold"`                   // 元宝
	FriendPoint int32     `json:"friend_point,omitempty"` // 友情点（招募）
	SoulExp     int32     `json:"soulexp,omitempty"`      // 魂值（开宝箱得）
	StopUpgrade int8      `json:"stop_upgrade,omitempty"` // 0正常升级，1停级（30/40级生效）
	Xiuwei      int64     `json:"xiuwei,omitempty"`       // 当前巅峰等级剩余经验
	XGrade      int       `json:"xgrade,omitempty"`       // 巅峰等级0~100
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
	mu          sync.RWMutex
	updates     sync.Map   // uid -> *sync.Mutex，同一角色的修改按顺序落盘
	createMu    sync.Mutex // 建角名字检查与提交串行，查询不等待存档 I/O
	st          store.Store
	ByUID       map[int32]*Role
	newRoleGold int32 // Set once by NewServer before accepting connections.
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]int32, 0, len(r.ByUID))
	for uid := range r.ByUID {
		out = append(out, uid)
	}
	slices.Sort(out)
	return out
}

func (r *Roles) Get(uid int32) *Role {
	r.mu.RLock()
	defer r.mu.RUnlock()
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
	r.createMu.Lock()
	defer r.createMu.Unlock()
	mu := r.updateLock(uid)
	mu.Lock()
	defer mu.Unlock()
	r.mu.RLock()
	if r.ByUID[uid] != nil {
		r.mu.RUnlock()
		return nil, ErrRoleExists
	}
	for _, ro := range r.ByUID {
		if strings.EqualFold(ro.Name, name) {
			r.mu.RUnlock()
			return nil, ErrNameUsed
		}
	}
	r.mu.RUnlock()
	if icon != 1 && icon != 2 {
		icon = 1
	}
	ro := &Role{UID: uid, Name: name, Icon: icon, Grade: 1, VIP: defaultVIP, Gold: r.newRoleGold, TPoint: maxTPoint, HPoint: maxHPoint, Created: time.Now()}
	if err := r.st.SaveRole(store.Role(*ro)); err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.ByUID[uid] = ro
	r.mu.Unlock()
	c := *ro
	return &c, nil
}

func (r *Roles) updateLock(uid int32) *sync.Mutex {
	value, _ := r.updates.LoadOrStore(uid, &sync.Mutex{})
	return value.(*sync.Mutex)
}

// Update 按角色串行修改并落盘；磁盘 I/O 不持有全服索引锁。
// 成功落盘后才发布新快照，fn 返回 false 或存档失败不改变内存数据。
func (r *Roles) Update(uid int32, fn func(*Role) bool) (*Role, error) {
	mu := r.updateLock(uid)
	mu.Lock()
	defer mu.Unlock()
	ro := r.Get(uid)
	if ro == nil {
		return nil, errors.New("no role")
	}
	old := *ro
	if fn(ro) && *ro != old {
		if err := r.st.SaveRole(store.Role(*ro)); err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.ByUID[uid] = ro
		r.mu.Unlock()
	} else {
		*ro = old
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

// AddExp保留已有调用入口，统一经过停级/巅峰结算。
func (ro *Role) AddExp(n int32) int {
	return ro.GainExp(n)
}

func (ro *Role) GainExp(n int32) int {
	if n <= 0 {
		return 0
	}
	if ro.Grade >= gamedata.MaxRoleGrade || ro.StopUpgrade == 1 && (ro.Grade == 30 || ro.Grade == 40) {
		ro.gainPeakExp(int64(n))
		return 0
	}
	exp := int64(ro.Exp) + int64(n)
	up := 0
	for need := gamedata.RoleNeed(ro.Grade); need > 0 && exp >= int64(need); need = gamedata.RoleNeed(ro.Grade) {
		exp -= int64(need)
		ro.Grade++
		up++
		if ro.Grade >= gamedata.MaxRoleGrade || ro.StopUpgrade == 1 && (ro.Grade == 30 || ro.Grade == 40) {
			ro.Exp = 0
			ro.gainPeakExp(exp)
			return up
		}
	}
	ro.Exp = int32(exp)
	return up
}

func (ro *Role) gainPeakExp(n int64) {
	if ro.XGrade >= gamedata.MaxXiuweiGrade {
		ro.Xiuwei = 0
		return
	}
	ro.Xiuwei += n
	for ro.XGrade < gamedata.MaxXiuweiGrade && ro.Xiuwei >= gamedata.XiuweiNeed(ro.XGrade) {
		ro.Xiuwei -= gamedata.XiuweiNeed(ro.XGrade)
		ro.XGrade++
	}
	if ro.XGrade >= gamedata.MaxXiuweiGrade {
		ro.Xiuwei = 0
	}
}

// PetCap 是卡牌等级上限：人物等级×2，且不超过 100。
func (ro *Role) PetCap() int { return min(ro.Grade*2, gamedata.MaxPetGrade) }

// LoginSuc 编出 S_LOGIN_SUC 的字段。
func (ro *Role) LoginSuc(urs string, now time.Time) xym.Msg {
	return xym.Msg{
		"uid": ro.UID, "icon": ro.Icon, "origin_icon": ro.Icon, "name": ro.Name,
		"tpoint": ro.TPoint, "max_tpoint": maxTPoint, "hpoint": ro.HPoint, "max_hpoint": maxHPoint,
		"grade": ro.Grade, "exp": ro.Exp, "next_exp": nextExp(ro.Grade),
		"stop_upgrade": int64(ro.StopUpgrade), "xgrade": int64(ro.XGrade), "xiuwei": ro.Xiuwei, "tot_xiuwei": ro.Xiuwei,
		"cash": ro.Cash, "gold": ro.Gold, "vip": ro.VIP, "soulexp": ro.SoulExp, "leader": 1, "friend_point": ro.FriendPoint,
		"srv_time": now.Unix(), "urs": urs, "serverid": 1, // 单区「再续前缘」，hostid 1（offline_probe.zone_list）
	}
}
