package xymsrv

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 洞府（DONGFU_PTO，玩家帮会）：创建、浏览 / 搜索 / 申请加入、审批、成员与职位、退出 / 逐出、口号 / 改名 / 徽章、
// 每日捐献，以及「组织」「修为」「差事」三页的基础数据。
//
// 依据：
//   - logic/party_mgr.lua：S_SEND_HAS_DONGFU.yes 0 = 打开「创建 / 加入」入口（只在点主界面「洞府」按钮
//     C_GET_DONGFU_INFO 时这样回），1 = 有洞府，2 = 没有但不弹（抓包：个人信息页 C_QUERY_DONGFU 回 yes 2）；
//     职位 owner 府主 / master 洞主 / elder 长老 / member；isAdministrator = owner / master；
//     徽章 1–5 免费，6–25 按洞府等级（PartyIconInfo）。
//   - view/PartyEntranceView.lua、view/PartyView.lua：创建 C_CREATE_DONGFU{name ≤12 字, slogan ≤60, icon 1–5}，
//     界面写 1000 元宝；列表 C_GET_DONGFU_LIST{page} → S_SEND_DONGFU_LIST{cnt 总页数, page, list}；
//     申请 C_APPLY_JOIN_DONGFU{dongfu_id, reason ≤18} / 取消 C_CANCEL_APPLY_JOIN_DONGFU → S_REFRESH_APPLY_JOIN_DONGFU_STATUS；
//     捐献 C_DONGFU_JOB_DONATE{gold}（1 – VIP×50，每天一次）；差事捐献 C_DONGFU_JOB_COLLECTION{type 1/2/5}：
//     30000 银子 / 20 元宝 / 50 元宝 → 洞府元宝 +10/20/50、贡献 +2/5/10（CollectionData）。
//   - view/PartyMemberListView.lua：成员 C_GET_DONGFU_MEMBER_LIST、申请 C_GET_DONGFU_APPLY_JOIN_LIST（带锁），
//     审批 C_HANDLE_APPLY_JOIN_DONGFU{other, pass} 后等 S_SEND_DONGFU_APPLY_JOIN_LIST；
//     C_GRANT_OWNER / C_GRANT_MASTER / C_REMOVE_MASTER / C_KICK_DONGFU_MEMBER{other, reason}。
//   - 抓包（session-01，一级洞府）：size 25、lingqi_pool_max 1200、lingmai 3；登录推 S_SEND_HAS_DONGFU{1}、
//     S_SEND_MEMBER_INFO{position}、S_SEND_MEMBER_XIUWEI_LIST（六种修为，0 级 exp_next 10、avail_count 60）。
//
// 估计 / 未做：容纳 20 + 5×等级、灵气池 600×(等级+1)、灵脉 3×等级；捐献的贡献按 4 元宝 1 点（与差事捐献同比例）；
// 洞主最多 2 个；府主有其他成员时不能退出（先转让），只剩自己时退出即解散。建设、修为、商店、魔潮、争夺、灵穴、
// 守卫见 dongfu_*.go。全服存档见 globals.go。

const (
	dfCreateGold     = 1000
	dfPageSize       = 10
	dfMaxNameRunes   = 12
	dfMaxSloganRunes = 60
	dfMaxReasonRunes = 18
	dfMaxMasters     = 2
	dfMaxHistory     = 100
	dfRenameItem     = 11152 // 洞府改名卡（PartyOrganizeView：有它才显示改名按钮）

	dfOwner  = "owner"
	dfMaster = "master"
	dfElder  = "elder"
	dfMember = "member"
)

var dfPositionRank = map[string]int{dfOwner: 1, dfMaster: 2, dfElder: 3, dfMember: 4}

// dfCollections：差事捐献 type -> 消耗银两 / 元宝、洞府元宝、贡献（PartyView CollectionData）。
var dfCollections = map[int]struct{ cash, gold, dfGold, contri int32 }{
	1: {30000, 0, 10, 2},
	2: {0, 20, 20, 5},
	5: {0, 50, 50, 10},
}

var dfXiuweiTypes = []string{"human", "god", "beast", "ghost", "attack", "hp"}

// Dongfus 是全服洞府。
type Dongfus struct {
	mu sync.Mutex
	st store.Store

	NextID int32             `json:"next_id"`
	List   map[int32]*Dongfu `json:"list"`
	// Applies：玩家 -> 申请中的洞府号
	Applies map[int32][]int32 `json:"applies"`
	// Caves：洞天号 -> 灵穴（dongfu_cave.go）
	Caves map[int32]*DFCave `json:"caves,omitempty"`
}

// Dongfu 是一个洞府。
type Dongfu struct {
	ID      int32       `json:"id"`
	Name    string      `json:"name"`
	Slogan  string      `json:"slogan"`
	Icon    int32       `json:"icon"`
	Lv      int32       `json:"lv"`
	Gold    int32       `json:"gold"` // 洞府元宝
	Yuanqi  int32       `json:"yuanqi"`
	Lingqi  int32       `json:"lingqi"`
	Members []*DFMember `json:"members"`
	Applies []*DFApply  `json:"applies,omitempty"`
	History []DFLog     `json:"history,omitempty"`
	Created int64       `json:"created"`

	Builds     map[string]*DFBuild   `json:"builds,omitempty"` // 六座修为建筑（dongfu_build.go）
	CurXiuwei  string                `json:"cur_xiuwei,omitempty"`
	HouseLv    int32                 `json:"house_lv,omitempty"`
	PoolLv     int32                 `json:"pool_lv,omitempty"`
	LingqiAt   int64                 `json:"lingqi_at,omitempty"`
	Extra      int                   `json:"extra,omitempty"` // 今天额外发布的建设任务数
	ExtraDay   string                `json:"extra_day,omitempty"`
	Guards     [3]int32              `json:"guards,omitempty"`  // 人界 / 地界 / 天界守卫（dongfu_guard.go）
	Shop       *DFShop               `json:"shop,omitempty"`    // 仙灵铺（dongfu_shop.go）
	Invade     *DFInvade             `json:"invade,omitempty"`  // 魔潮入侵（dongfu_invade.go）
	War        *DFWar                `json:"war,omitempty"`     // 争夺（dongfu_gank.go）
	Station    int32                 `json:"station,omitempty"` // 所在洞天（灵穴，dongfu_cave.go；0 = 桃花源）
	CaveOn     bool                  `json:"cave_on,omitempty"`
	CaveUsers  map[int32]*DFCaveUser `json:"cave_users,omitempty"`
	CaveLingqi int32                 `json:"cave_lingqi,omitempty"` // 灵穴累计得的灵气
}

type DFMember struct {
	UID      int32  `json:"uid"`
	Position string `json:"position"`
	Contri   int32  `json:"contri"`         // 洞府贡献（累计）
	Week     int32  `json:"week,omitempty"` // 本周贡献
	WeekKey  string `json:"week_key,omitempty"`
	Donate   string `json:"donate,omitempty"`  // 最近一次元宝捐献的日期
	Collect  string `json:"collect,omitempty"` // 最近一次差事捐献的日期
	Joined   int64  `json:"joined"`
}

type DFApply struct {
	UID    int32  `json:"uid"`
	Reason string `json:"reason"`
	Time   int64  `json:"time"`
}

type DFLog struct {
	Time int64  `json:"time"`
	Msg  string `json:"msg"`
}

func openDongfus(st store.Store) *Dongfus {
	d := &Dongfus{st: st}
	loadGlobal(st, dongfuUID, d, "dongfu")
	if d.List == nil {
		d.List = map[int32]*Dongfu{}
	}
	if d.Applies == nil {
		d.Applies = map[int32][]int32{}
	}
	if d.NextID == 0 {
		d.NextID = 1
	}
	return d
}

// do 在锁内执行 fn，返回 true 时写回存档。
func (d *Dongfus) do(fn func() bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if fn() {
		saveGlobal(d.st, dongfuUID, d, "dongfu")
	}
}

// of 是 uid 所在的洞府和成员记录（调用方持锁）。
func (d *Dongfus) of(uid int32) (*Dongfu, *DFMember) {
	for _, df := range d.List {
		for _, m := range df.Members {
			if m.UID == uid {
				return df, m
			}
		}
	}
	return nil, nil
}

func (d *Dongfus) nameTaken(name string, except int32) bool {
	for _, df := range d.List {
		if df.ID != except && strings.EqualFold(df.Name, name) {
			return true
		}
	}
	return false
}

// 屋舍 1 级容纳 25 人、每级 +5；灵池 1 级 1200、每级 +600（抓包 1 级洞府 25 人、1200）；
// 灵脉按洞府等级（抓包 1 级 3 点/小时，截图 5 级 14 点）。
func (df *Dongfu) size() int32      { return 20 + 5*df.houseLv() }
func (df *Dongfu) lingqiMax() int32 { return 600 * (df.poolLv() + 1) }
func (df *Dongfu) lingmai() int32   { return (11*df.Lv + 1) / 4 }
func (df *Dongfu) full() bool       { return int32(len(df.Members)) >= df.size() }
func (df *Dongfu) owner() *DFMember {
	return df.find(func(m *DFMember) bool { return m.Position == dfOwner })
}
func (df *Dongfu) member(uid int32) *DFMember {
	return df.find(func(m *DFMember) bool { return m.UID == uid })
}

func (df *Dongfu) find(f func(*DFMember) bool) *DFMember {
	for _, m := range df.Members {
		if f(m) {
			return m
		}
	}
	return nil
}

func (df *Dongfu) log(msg string) {
	df.History = append(df.History, DFLog{Time: nowUnix(), Msg: msg})
	if n := len(df.History); n > dfMaxHistory {
		df.History = df.History[n-dfMaxHistory:]
	}
}

func (df *Dongfu) removeApply(uid int32) {
	df.Applies = slices.DeleteFunc(df.Applies, func(a *DFApply) bool { return a.UID == uid })
}

// week 把成员的本周贡献按周清零后返回。
func (m *DFMember) week() int32 {
	if k := weekKey(time.Now()); m.WeekKey != k {
		m.WeekKey, m.Week = k, 0
	}
	return m.Week
}

func (m *DFMember) contribute(n int32) {
	m.week()
	m.Week += n
	m.Contri += n
}

func isAdmin(pos string) bool { return pos == dfOwner || pos == dfMaster }

// ---- 消息编码 ----

func (srv *Server) roleBrief(uid int32) (name string, icon int32, grade int) {
	if ro := srv.roles.Get(uid); ro != nil {
		return ro.Name, int32(ro.Icon), ro.Grade
	}
	return "", 0, 0
}

func (srv *Server) dfInfoMsg(df *Dongfu, me *DFMember) xym.Msg {
	ownerName := ""
	if o := df.owner(); o != nil {
		ownerName, _, _ = srv.roleBrief(o.UID)
	}
	m := xym.Msg{"name": df.Name, "owner_name": ownerName, "station_id": df.Station, "slogan": df.Slogan, "lv": df.Lv,
		"icon": df.Icon, "member_num": len(df.Members), "size": df.size(), "gold": df.Gold, "lingmai": df.lingmai() + srv.dongfu.caveHeld(df),
		"lingqi": df.Lingqi, "lingqi_pool_max": df.lingqiMax(), "yuanqi": df.Yuanqi, "xiuwei_lv_sum": df.xiuweiSum(),
		"guard": srv.dfGuardList(df), "position": "", "title": 0, "contribute": 0}
	if me != nil {
		m["position"], m["contribute"], m["title"] = me.Position, me.Contri, dfTitle(me.Contri)
	}
	return m
}

func (srv *Server) dfListItem(df *Dongfu, applied bool) xym.Msg {
	ownerName := ""
	if o := df.owner(); o != nil {
		ownerName, _, _ = srv.roleBrief(o.UID)
	}
	has := 0
	if applied {
		has = 1
	}
	return xym.Msg{"id": df.ID, "name": df.Name, "owner_name": ownerName, "station": "桃花源", "slogan": df.Slogan,
		"lv": df.Lv, "rank": 0, "icon": df.Icon, "member_num": len(df.Members), "size": df.size(), "has_apply": has}
}

func (srv *Server) dfMemberList(df *Dongfu) xym.Msg {
	list := []xym.Msg{}
	for _, m := range df.Members {
		name, icon, grade := srv.roleBrief(m.UID)
		online := 0
		if srv.sessionOf(m.UID) != nil {
			online = 1
		}
		list = append(list, xym.Msg{"usernum": m.UID, "name": name, "icon": icon, "vip_icon_ts": 0, "grade": grade,
			"xgrade": 0, "week_contribute": m.week(), "dongfu_contribute": m.Contri, "position": m.Position, "title": dfTitle(m.Contri),
			"pvp_rank": 0, "is_online": online, "active_point": 0})
	}
	return xym.Msg{"list": list}
}

func (srv *Server) dfApplyList(df *Dongfu) xym.Msg {
	list := []xym.Msg{}
	for _, a := range df.Applies {
		name, icon, grade := srv.roleBrief(a.UID)
		list = append(list, xym.Msg{"usernum": a.UID, "name": name, "icon": icon, "vip_icon_ts": 0, "grade": grade,
			"xgrade": 0, "pvp_rank": 0, "reason": a.Reason, "active_point": 0})
	}
	return xym.Msg{"list": list}
}

func dfJobInfo(me *DFMember) xym.Msg {
	collect := 1
	if me.Collect == today() {
		collect = 0
	}
	return xym.Msg{"week_contribute": me.week(), "collection_left_times": collect, "xiuwei_task_type": 0,
		"xiuwei_task_left_times": 0, "xiuwei_task_need_type": 0, "xiuwei_task_vs_usernum": 0, "xiuwei_task_vs_name": "",
		"xiuwei_task_vs_icon": 0, "xiuwei_task_vs_fight_point": 0, "xiuwei_task_fight_cd": 0, "xiuwei_task_need_kind": "",
		"invade_left_time": -1, "invade_monster_left": 0, "invade_monster_leader_left": 0, "invade_monster_governor_left": 0,
		"invade_monster_boss_hp": 0, "invade_status": 3, "invade_finish_time": 0, "invade_open_left_time": 0,
		"invade_open_hour": 0, "gank_dongfu_icon": 0, "gank_dongfu_name": "", "gank_server_name": "", "gank_status": 0,
		"gank_side": 0, "gank_type": 0, "gank_left_time": 0, "gank_finish_time": 0, "gank_revenge_add": 0,
		"push_msg_xiuwei_task": 1, "push_msg_dongfu_upgrade": 1, "push_msg_monster_invade": 1, "push_msg_gank_attack": 1,
		"push_msg_gank_defense": 1, "push_msg_battle": 1, "push_msg_cave": 1, "cave_game_times": -1,
		"battle": []xym.Msg{}, "territory": []xym.Msg{}}
}

// pushDongfuLogin：在洞府里的玩家登录时推职位与修为（抓包顺序）。
func (s *Session) pushDongfuLogin() {
	var pos string
	p := s.player
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s.srv.dongfu.do(func() bool {
		df, me := s.srv.dongfu.of(s.role.UID)
		if me != nil {
			pos = me.Position
		}
		p.syncDF(df, me) // 刷新修为生效上限（被逐出 / 洞府解散后清掉）
		return df != nil
	})
	p.saveLog(s)
	if pos == "" {
		return
	}
	s.SendExt("DONGFU_PTO.S_SEND_HAS_DONGFU", xym.Msg{"yes": 1})
	s.SendExt("DONGFU_PTO.S_SEND_MEMBER_INFO", xym.Msg{"position": pos})
	s.SendExt("DONGFU_PTO.S_SEND_MEMBER_XIUWEI_LIST", p.dfXiuweiList())
}

// notify 给在线的 uid 发消息。
func (srv *Server) notify(uid int32, f func(t *Session)) {
	if t := srv.sessionOf(uid); t != nil {
		f(t)
	}
}

// ---- 处理 ----

func init() {
	Handle("DONGFU_PTO.C_GET_DONGFU_INFO", onDongfuInfo)
	Handle("DONGFU_PTO.C_QUERY_DONGFU", onQueryDongfu)
	Handle("DONGFU_PTO.C_CREATE_DONGFU", onCreateDongfu)
	Handle("DONGFU_PTO.C_GET_DONGFU_LIST", onDongfuList)
	Handle("DONGFU_PTO.C_FIND_DONGFU", onFindDongfu)
	Handle("DONGFU_PTO.C_APPLY_JOIN_DONGFU", onApplyDongfu)
	Handle("DONGFU_PTO.C_CANCEL_APPLY_JOIN_DONGFU", onCancelApplyDongfu)
	Handle("DONGFU_PTO.C_GET_DONGFU_APPLY_JOIN_LIST", onDongfuApplyList)
	Handle("DONGFU_PTO.C_HANDLE_APPLY_JOIN_DONGFU", onHandleApplyDongfu)
	Handle("DONGFU_PTO.C_GET_DONGFU_MEMBER_LIST", onDongfuMembers)
	Handle("DONGFU_PTO.C_QUIT_DONGFU", onQuitDongfu)
	Handle("DONGFU_PTO.C_KICK_DONGFU_MEMBER", onKickDongfu)
	Handle("DONGFU_PTO.C_GRANT_OWNER", func(s *Session, p *Player, m xym.Msg) { s.dfGrant(m, dfOwner) })
	Handle("DONGFU_PTO.C_GRANT_MASTER", func(s *Session, p *Player, m xym.Msg) { s.dfGrant(m, dfMaster) })
	Handle("DONGFU_PTO.C_REMOVE_MASTER", func(s *Session, p *Player, m xym.Msg) { s.dfGrant(m, dfMember) })
	Handle("DONGFU_PTO.C_MODIFY_DONGFU_SLOGAN", onDongfuSlogan)
	Handle("DONGFU_PTO.C_MODIFY_DONGFU_NAME", onDongfuRename)
	Handle("DONGFU_PTO.C_GET_DONGFU_ICON_LIST", func(s *Session, p *Player, m xym.Msg) {
		icons := []xym.Msg{}
		for i := 1; i <= 5; i++ {
			icons = append(icons, xym.Msg{"icon": i})
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_ICON_LIST", xym.Msg{"icon_list": icons, "avail_icon_list": []xym.Msg{}})
	})
	Handle("DONGFU_PTO.C_CHANGE_DONGFU_ICON", onDongfuIcon)
	Handle("DONGFU_PTO.C_DONGFU_JOB_DONATE", onDongfuDonate)
	Handle("DONGFU_PTO.C_DONGFU_JOB_COLLECTION", onDongfuCollect)
	Handle("DONGFU_PTO.C_DONGFU_HISTORY", onDongfuHistory)
	Handle("DONGFU_PTO.C_GET_DONGFU_MAIL_INVITE_LIST", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_MAIL_INVITE_USER_LIST", xym.Msg{"list": []xym.Msg{}})
	})
	Handle("DONGFU_PTO.C_GET_DONGFU_INFO_PREVIEW", onDongfuPreview)
}

// dfMine 在锁内对自己的洞府执行 fn（不在洞府时提示）；fn 返回 true 写回存档。
func (s *Session) dfMine(fn func(df *Dongfu, me *DFMember) bool) {
	s.srv.dongfu.do(func() bool {
		df, me := s.srv.dongfu.of(s.role.UID)
		if df == nil {
			s.retMsg("您还没有加入洞府。")
			return false
		}
		return fn(df, me)
	})
}

func onDongfuInfo(s *Session, p *Player, m xym.Msg) {
	s.srv.dongfu.do(func() bool {
		df, me := s.srv.dongfu.of(s.role.UID)
		if df == nil {
			s.SendExt("DONGFU_PTO.S_SEND_HAS_DONGFU", xym.Msg{"yes": 0})
			return false
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
		return false
	})
}

func onQueryDongfu(s *Session, p *Player, m xym.Msg) {
	s.srv.dongfu.do(func() bool {
		df, me := s.srv.dongfu.of(s.role.UID)
		if df == nil {
			s.SendExt("DONGFU_PTO.S_SEND_HAS_DONGFU", xym.Msg{"yes": 2})
			return false
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
		return false
	})
}

func validDFName(name string) bool {
	n := utf8.RuneCountInString(name)
	return n >= 1 && n <= dfMaxNameRunes && len(name) >= 2 && !strings.ContainsAny(name, "\r\n\t")
}

func onCreateDongfu(s *Session, p *Player, m xym.Msg) {
	name, slogan := strings.TrimSpace(m["name"].(string)), strings.TrimSpace(m["slogan"].(string))
	icon := int32(m["icon"].(int64))
	d := s.srv.dongfu
	d.do(func() bool {
		switch {
		case func() bool { df, _ := d.of(s.role.UID); return df != nil }():
			s.sendMsg("您已经在洞府里了。")
			return false
		case !validDFName(name):
			s.sendMsg("洞府名称为1-12个字。")
			return false
		case slogan == "" || utf8.RuneCountInString(slogan) > dfMaxSloganRunes:
			s.sendMsg("洞府口号为1-60个字。")
			return false
		case icon < 1 || icon > 5:
			s.sendMsg("请选择洞府徽章！")
			return false
		case d.nameTaken(name, 0):
			s.sendMsg("该洞府名称已被使用。")
			return false
		}
		if !s.spend(0, dfCreateGold) {
			return false
		}
		now := nowUnix()
		df := &Dongfu{ID: d.NextID, Name: name, Slogan: slogan, Icon: icon, Lv: 1, Created: now,
			Members: []*DFMember{{UID: s.role.UID, Position: dfOwner, Joined: now}}}
		d.NextID++
		d.List[df.ID] = df
		for _, id := range d.Applies[s.role.UID] {
			if x := d.List[id]; x != nil {
				x.removeApply(s.role.UID)
			}
		}
		delete(d.Applies, s.role.UID)
		df.log(s.role.Name + " 创建了洞府")
		s.sendMsg("洞府创建成功！")
		s.SendExt("DONGFU_PTO.S_SEND_HAS_DONGFU", xym.Msg{"yes": 1})
		s.SendExt("DONGFU_PTO.S_SEND_MEMBER_INFO", xym.Msg{"position": dfOwner})
		p.syncDF(df, df.Members[0])
		p.saveLog(s)
		s.SendExt("DONGFU_PTO.S_SEND_MEMBER_XIUWEI_LIST", p.dfXiuweiList())
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, df.Members[0]))
		return true
	})
}

// sortedDongfus：等级、人数从高到低，再按创建先后。
func (d *Dongfus) sorted(match func(*Dongfu) bool) []*Dongfu {
	var l []*Dongfu
	for _, df := range d.List {
		if match == nil || match(df) {
			l = append(l, df)
		}
	}
	sort.Slice(l, func(i, j int) bool {
		a, b := l[i], l[j]
		if a.Lv != b.Lv {
			return a.Lv > b.Lv
		}
		if len(a.Members) != len(b.Members) {
			return len(a.Members) > len(b.Members)
		}
		return a.ID < b.ID
	})
	return l
}

func (s *Session) sendDongfuPage(l []*Dongfu, page int) {
	d := s.srv.dongfu
	pages := max(1, (len(l)+dfPageSize-1)/dfPageSize)
	page = min(max(page, 1), pages)
	list := []xym.Msg{}
	for _, df := range l[min((page-1)*dfPageSize, len(l)):min(page*dfPageSize, len(l))] {
		list = append(list, s.srv.dfListItem(df, slices.Contains(d.Applies[s.role.UID], df.ID)))
	}
	s.SendExt("DONGFU_PTO.S_SEND_DONGFU_LIST", xym.Msg{"cnt": pages, "page": page, "list": list})
}

func onDongfuList(s *Session, p *Player, m xym.Msg) {
	d := s.srv.dongfu
	d.do(func() bool {
		s.sendDongfuPage(d.sorted(nil), int(m["page"].(int64)))
		return false
	})
}

func onFindDongfu(s *Session, p *Player, m xym.Msg) {
	key := strings.ToLower(strings.TrimSpace(m["name"].(string)))
	d := s.srv.dongfu
	d.do(func() bool {
		l := d.sorted(func(df *Dongfu) bool { return key != "" && strings.Contains(strings.ToLower(df.Name), key) })
		if len(l) == 0 {
			s.sendMsg("没有找到该洞府。")
			return false
		}
		s.sendDongfuPage(l, 1)
		return false
	})
}

func onApplyDongfu(s *Session, p *Player, m xym.Msg) {
	id, reason := int32(m["dongfu_id"].(int64)), strings.TrimSpace(m["reason"].(string))
	if utf8.RuneCountInString(reason) > dfMaxReasonRunes {
		reason = string([]rune(reason)[:dfMaxReasonRunes])
	}
	d := s.srv.dongfu
	var admins []int32
	d.do(func() bool {
		df := d.List[id]
		cur, _ := d.of(s.role.UID)
		switch {
		case cur != nil:
			s.sendMsg("您已经在洞府里了。")
			return false
		case df == nil:
			s.sendMsg("该洞府不存在。")
			return false
		case df.full():
			s.sendMsg("该洞府人数已满。")
			return false
		case slices.Contains(d.Applies[s.role.UID], id):
			s.SendExt("DONGFU_PTO.S_REFRESH_APPLY_JOIN_DONGFU_STATUS", xym.Msg{"dongfu_id": id, "has_apply": 1})
			return false
		}
		df.removeApply(s.role.UID)
		df.Applies = append(df.Applies, &DFApply{UID: s.role.UID, Reason: reason, Time: nowUnix()})
		d.Applies[s.role.UID] = append(d.Applies[s.role.UID], id)
		for _, x := range df.Members {
			if isAdmin(x.Position) {
				admins = append(admins, x.UID)
			}
		}
		s.SendExt("DONGFU_PTO.S_REFRESH_APPLY_JOIN_DONGFU_STATUS", xym.Msg{"dongfu_id": id, "has_apply": 1})
		s.sendMsg("申请已发送，请等待洞府管理者审核。")
		return true
	})
	for _, uid := range admins {
		name := s.role.Name
		s.srv.notify(uid, func(t *Session) { t.sendMsg(name + " 申请加入洞府，请在「成员 → 申请」中处理。") })
	}
}

func onCancelApplyDongfu(s *Session, p *Player, m xym.Msg) {
	id := int32(m["dongfu_id"].(int64))
	d := s.srv.dongfu
	d.do(func() bool {
		if df := d.List[id]; df != nil {
			df.removeApply(s.role.UID)
		}
		d.Applies[s.role.UID] = remove(d.Applies[s.role.UID], id)
		if len(d.Applies[s.role.UID]) == 0 {
			delete(d.Applies, s.role.UID)
		}
		s.SendExt("DONGFU_PTO.S_REFRESH_APPLY_JOIN_DONGFU_STATUS", xym.Msg{"dongfu_id": id, "has_apply": 0})
		return true
	})
}

func onDongfuApplyList(s *Session, p *Player, m xym.Msg) {
	d := s.srv.dongfu
	d.do(func() bool {
		df, me := d.of(s.role.UID)
		if df == nil || !isAdmin(me.Position) && me.Position != dfElder {
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_APPLY_JOIN_LIST", xym.Msg{"list": []xym.Msg{}})
			return false
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_APPLY_JOIN_LIST", s.srv.dfApplyList(df))
		return false
	})
}

func onHandleApplyDongfu(s *Session, p *Player, m xym.Msg) {
	other, pass := int32(m["other"].(int64)), m["pass"].(int64) != 0
	d := s.srv.dongfu
	var joined *Dongfu
	var refused string
	d.do(func() bool {
		df, me := d.of(s.role.UID)
		if df == nil || !isAdmin(me.Position) {
			s.sendMsg("只有府主和洞主可以审批。")
			if df != nil {
				s.SendExt("DONGFU_PTO.S_SEND_DONGFU_APPLY_JOIN_LIST", s.srv.dfApplyList(df))
			} else {
				s.SendExt("DONGFU_PTO.S_SEND_DONGFU_APPLY_JOIN_LIST", xym.Msg{"list": []xym.Msg{}})
			}
			return false
		}
		defer func() { s.SendExt("DONGFU_PTO.S_SEND_DONGFU_APPLY_JOIN_LIST", s.srv.dfApplyList(df)) }()
		if !slices.ContainsFunc(df.Applies, func(a *DFApply) bool { return a.UID == other }) {
			return false
		}
		df.removeApply(other)
		d.Applies[other] = remove(d.Applies[other], df.ID)
		if !pass {
			refused = df.Name
			return true
		}
		if cur, _ := d.of(other); cur != nil {
			s.sendMsg("对方已经加入了其他洞府。")
			return true
		}
		if df.full() {
			s.sendMsg("洞府人数已满。")
			return true
		}
		df.Members = append(df.Members, &DFMember{UID: other, Position: dfMember, Joined: nowUnix()})
		for _, id := range d.Applies[other] {
			if x := d.List[id]; x != nil {
				x.removeApply(other)
			}
		}
		delete(d.Applies, other)
		name, _, _ := s.srv.roleBrief(other)
		df.log(name + " 加入了洞府")
		joined = df
		return true
	})
	switch {
	case joined != nil:
		dfName := joined.Name
		s.srv.notify(other, func(t *Session) {
			t.SendExt("DONGFU_PTO.S_SEND_HAS_DONGFU", xym.Msg{"yes": 1})
			t.SendExt("DONGFU_PTO.S_SEND_MEMBER_INFO", xym.Msg{"position": dfMember})
			t.sendMsg("您已加入洞府「" + dfName + "」。")
		})
	case refused != "":
		s.srv.notify(other, func(t *Session) { t.sendMsg("洞府「" + refused + "」拒绝了您的申请。") })
	}
}

func onDongfuMembers(s *Session, p *Player, m xym.Msg) {
	d := s.srv.dongfu
	d.do(func() bool {
		df, _ := d.of(s.role.UID)
		if df == nil {
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_MEMBER_LIST", xym.Msg{"list": []xym.Msg{}})
			return false
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_MEMBER_LIST", s.srv.dfMemberList(df))
		return true // 本周贡献可能刚清零
	})
}

func onQuitDongfu(s *Session, p *Player, m xym.Msg) {
	d := s.srv.dongfu
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		if me.Position == dfOwner && len(df.Members) > 1 {
			s.sendMsg("府主需要先把府主之位转让给其他成员才能离开。")
			return false
		}
		df.Members = slices.DeleteFunc(df.Members, func(x *DFMember) bool { return x == me })
		if len(df.Members) == 0 {
			for _, a := range df.Applies {
				d.Applies[a.UID] = remove(d.Applies[a.UID], df.ID)
			}
			delete(d.List, df.ID)
			s.sendMsg("洞府已解散。")
		} else {
			df.log(s.role.Name + " 离开了洞府")
			s.sendMsg("您已离开洞府。")
		}
		d := p.df() // 「离开将扣除一半灵气」；修为生效上限清掉
		d.Lingqi /= 2
		p.syncDF(nil, nil)
		p.saveLog(s)
		s.SendExt("DONGFU_PTO.S_SEND_HAS_DONGFU", xym.Msg{"yes": 2})
		return true
	})
}

func onKickDongfu(s *Session, p *Player, m xym.Msg) {
	other := int32(m["other"].(int64))
	var dfName string
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		t := df.member(other)
		switch {
		case t == nil || t == me:
			return false
		case !isAdmin(me.Position) || dfPositionRank[t.Position] <= dfPositionRank[me.Position]:
			s.sendMsg("您没有权限逐出该成员。")
			return false
		}
		df.Members = slices.DeleteFunc(df.Members, func(x *DFMember) bool { return x == t })
		name, _, _ := s.srv.roleBrief(other)
		df.log(name + " 被逐出洞府")
		dfName = df.Name
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_MEMBER_LIST", s.srv.dfMemberList(df))
		return true
	})
	if dfName != "" {
		s.srv.notify(other, func(t *Session) {
			t.SendExt("DONGFU_PTO.S_SEND_HAS_DONGFU", xym.Msg{"yes": 2})
			t.sendMsg("您已被逐出洞府「" + dfName + "」。")
		})
	}
}

// dfGrant：府主转让府主（to = owner）、任命 / 撤销洞主（master / member）。
func (s *Session) dfGrant(m xym.Msg, to string) {
	other := int32(m["other"].(int64))
	var notifyPos string
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		t := df.member(other)
		switch {
		case t == nil || t == me:
			return false
		case me.Position != dfOwner:
			s.sendMsg("只有府主可以任免。")
			return false
		case to == dfMaster && t.Position == dfMaster:
			return false
		case to == dfMaster && len(slices.DeleteFunc(slices.Clone(df.Members), func(x *DFMember) bool { return x.Position != dfMaster })) >= dfMaxMasters:
			s.sendMsg("洞主人数已满。")
			return false
		}
		name, _, _ := s.srv.roleBrief(other)
		switch to {
		case dfOwner:
			me.Position, t.Position = dfMember, dfOwner
			df.log(s.role.Name + " 把府主之位转让给了 " + name)
			s.SendExt("DONGFU_PTO.S_SEND_MEMBER_INFO", xym.Msg{"position": dfMember})
		case dfMaster:
			t.Position = dfMaster
			df.log(name + " 被任命为洞主")
		default:
			t.Position = dfMember
			df.log(name + " 被撤销了洞主")
		}
		notifyPos = t.Position
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_MEMBER_LIST", s.srv.dfMemberList(df))
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
		return true
	})
	if notifyPos != "" {
		s.srv.notify(other, func(t *Session) { t.SendExt("DONGFU_PTO.S_SEND_MEMBER_INFO", xym.Msg{"position": notifyPos}) })
	}
}

func onDongfuSlogan(s *Session, p *Player, m xym.Msg) {
	slogan := strings.TrimSpace(m["slogan"].(string))
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		switch {
		case !isAdmin(me.Position):
			s.sendMsg("只有府主和洞主可以修改口号。")
			return false
		case slogan == "" || utf8.RuneCountInString(slogan) > dfMaxSloganRunes:
			s.sendMsg("洞府口号为1-60个字。")
			return false
		}
		df.Slogan = slogan
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
		return true
	})
}

func onDongfuRename(s *Session, p *Player, m xym.Msg) {
	name := strings.TrimSpace(m["name"].(string))
	d := s.srv.dongfu
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		switch {
		case !isAdmin(me.Position):
			s.sendMsg("只有府主和洞主可以改名。")
			return false
		case !validDFName(name):
			s.sendMsg("洞府名称为1-12个字。")
			return false
		case d.nameTaken(name, df.ID):
			s.sendMsg("该洞府名称已被使用。")
			return false
		case p.itemCount(dfRenameItem) < 1:
			s.sendMsg("缺少洞府改名卡。")
			return false
		}
		dd := &bagDelta{}
		dd.items, dd.removed = p.takeItems(dfRenameItem, 1)
		p.saveLog(s)
		s.sendDelta(p, dd)
		df.log(s.role.Name + " 把洞府改名为「" + name + "」")
		df.Name = name
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
		return true
	})
}

func onDongfuIcon(s *Session, p *Player, m xym.Msg) {
	icon := int32(m["icon"].(int64))
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		if !isAdmin(me.Position) || icon < 1 || icon > 5 {
			s.sendMsg("暂时不能更换这个徽章。")
			return false
		}
		df.Icon = icon
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
		return true
	})
}

func onDongfuDonate(s *Session, p *Player, m xym.Msg) {
	gold := int32(m["gold"].(int64))
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		switch {
		case me.Donate == today():
			s.sendMsg("今天已经捐献过了。")
			return false
		case gold < 1 || gold > int32(max(s.role.VIP, 1)*50):
			s.sendMsg("捐献数量不正确。")
			return false
		}
		if !s.spend(0, gold) {
			return false
		}
		me.Donate = today()
		p.track(evDongfu, 1)
		df.Gold += gold
		c := max(1, gold/4)
		me.contribute(c)
		df.log(s.role.Name + " 捐献了 " + dfNum(gold) + " 元宝")
		s.sendMsg("捐献成功，洞府元宝 +" + dfNum(gold) + "，贡献 +" + dfNum(c) + "。")
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
		return true
	})
}

func onDongfuCollect(s *Session, p *Player, m xym.Msg) {
	c, ok := dfCollections[int(m["type"].(int64))]
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		switch {
		case !ok:
			return false
		case me.Collect == today():
			s.sendMsg("今天已经捐献过了。")
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO", s.dfJobInfo(p, df, me))
			return false
		}
		if !s.spend(c.cash, c.gold) {
			return false
		}
		me.Collect = today()
		p.track(evDongfu, 1)
		df.Gold += c.dfGold
		me.contribute(c.contri)
		df.log(s.role.Name + " 参与了洞府建设捐献")
		s.sendMsg("捐献成功，洞府元宝 +" + dfNum(c.dfGold) + "，贡献 +" + dfNum(c.contri) + "。")
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO", s.dfJobInfo(p, df, me))
		return true
	})
}

func onDongfuHistory(s *Session, p *Player, m xym.Msg) {
	page := int(m["page"].(int64))
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		h := df.History
		pages := max(1, (len(h)+dfPageSize-1)/dfPageSize)
		page = min(max(page, 1), pages)
		list := []xym.Msg{}
		for i := len(h) - 1 - (page-1)*dfPageSize; i >= 0 && i > len(h)-1-page*dfPageSize; i-- {
			list = append(list, xym.Msg{"datetime": time.Unix(h[i].Time, 0).Format("01-02 15:04"), "msg": h[i].Msg})
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_HISTORY", xym.Msg{"cnt": pages, "page": page, "list": list})
		return false
	})
}

func onDongfuPreview(s *Session, p *Player, m xym.Msg) {
	d := s.srv.dongfu
	d.do(func() bool {
		df := d.List[int32(m["dongfu_id"].(int64))]
		if df == nil {
			s.sendMsg("该洞府不存在。")
			return false
		}
		info := s.srv.dfInfoMsg(df, nil)
		xw := []xym.Msg{}
		for _, t := range dfXiuweiTypes {
			xw = append(xw, xym.Msg{"type": t, "lv": 0})
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO_PREVIEW", xym.Msg{"name": info["name"], "owner_name": info["owner_name"],
			"station_id": 0, "slogan": df.Slogan, "lv": df.Lv, "icon": df.Icon, "member_num": len(df.Members),
			"size": df.size(), "gold": df.Gold, "lingmai": df.lingmai(), "lingqi": df.Lingqi,
			"lingqi_pool_max": df.lingqiMax(), "yuanqi": df.Yuanqi, "house_lv": 1, "lingqi_pool_lv": 1, "xiuwei_list": xw})
		return false
	})
}

// dongfuMates 是 uid 所在洞府的全部成员（不在洞府返回 nil、0）。
func (srv *Server) dongfuMates(uid int32) ([]int32, int32) {
	var out []int32
	var id int32
	srv.dongfu.do(func() bool {
		if df, _ := srv.dongfu.of(uid); df != nil {
			id = df.ID
			for _, m := range df.Members {
				out = append(out, m.UID)
			}
		}
		return false
	})
	return out, id
}

func dfNum(n int32) string { return strconv.Itoa(int(n)) }
