package xymsrv

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 天梯（LADDER_PTO）：按积分匹配对手打一场，赢加分、输扣分，按等级组（30 / 40 / 50 级组）排榜，每周一个赛季。
//
// 依据：
//   - 抓包（session-01）：C_OPEN_LADDER → S_OPEN_LADDER{score, rank 0 = 未上榜, win, lose, draw, maxwin,
//     state 0 没有对手 / 1 已匹配, is_double} + S_GET_PAIR_TIME{times}；客户端 state 0 时自己发 C_OPEN_RANK{type 50}
//     → S_OPEN_RANK{type, list[name, uid, icon, grade, score, pos, fightpoint, win, lose, draw, dongfu…]}（约 100 人）。
//     C_START_PAIR → S_OPEN_FIGHT（对手：servername、积分相近、icon1–5 是阵容前 5 张、fightpoint）→ S_GET_PAIR_TIME
//     （匹配后第一次是 0，之后每换一次对手 +1）→ S_OPEN_LADDER{state 1}。
//     C_START_FIGHT → S_FIGHT_RECORD{rid「tianti:N」} → S_ATTR_UPDATE（活力 −1、元宝 +5）→ S_SEND_SHARE_INFO{maxwin,
//     score, servername, rank30/40/50, gotsharereward 1, icon} → S_OPEN_LADDER{state 0} → S_REWARD_MSG{type 15, gold 5,
//     record, no_fight_again 1}；赢一场积分 +20（980 → 1000 → 1020）。活力不够时 S_RET_MSG「活力不足，天梯每场战斗
//     消耗一点活力。」
//   - view/TianTiView.lua：「主动连胜」显示 maxwin；榜单「历史」C_APPLY_LAST_RANK、「全服」C_APPLY_ALL_RANK；
//     S_GET_PAIR_TIME.times ≤ 2 时按钮是「更换对手」。
//
// 整合2026-11-01天梯交付包：只匹配真人，防守阵容冻结；每日最多25次匹配。
// 积分按分差每10分一档、胜负各限1至15，超时平局按分差调整。
// 累进等级榜每周日22:02结算，排名前10和积分档位奖励可叠加。

var (
	ttDayLimit = 25 // 2026-11-01每日次数补丁：20→25。
	ttWinGold  = 5
	ttLoseGold = 2
)

const (
	ttInitScore         = 1000
	ttStep              = 20
	ttHPCost            = 1
	ttRankSize          = 100
	ttServerName        = "再续前缘"
	ttPickNearest       = 5
	ttFreeChanges       = 3
	ttPaidChanges       = 5
	ttChangeGold  int32 = 50

	// 赛季结算公告延迟：22:02:00 发奖，22:02:30 推送公告（避开结算卡顿）。
	ttAnnounceDelay = 30 * time.Second
)

// ttUser 是一位真人这个赛季的天梯数据。
type ttUser struct {
	Name   string `json:"name"`
	DFName string `json:"df_name,omitempty"`
	Grade  int    `json:"grade"`
	FP     int    `json:"fp"`
	Score  int    `json:"score"`
	Win    int    `json:"win"`
	Lose   int    `json:"lose"`
	Draw   int    `json:"draw"`
	Streak int    `json:"streak"`          // 当前主动连胜
	MaxWin int    `json:"max_win"`         // 本赛季最高主动连胜
	Pairs  int    `json:"pairs,omitempty"` // 这次匹配后换了几次对手
	Icon   int32  `json:"icon"`
	DFIcon int32  `json:"df_icon,omitempty"`
	Foe    int32  `json:"foe,omitempty"`

	Day        string `json:"day,omitempty"` // 匹配计数所属日期（本地时间 2006-01-02）
	ChangeN    int    `json:"change_n,omitempty"`
	ChangePaid int    `json:"change_paid,omitempty"`
	DayN       int    `json:"day_n,omitempty"` // 当天已匹配次数
}

// ttRow 是榜上一行。
type ttRow struct {
	Name   string `json:"name"`
	DFName string `json:"df_name,omitempty"`
	Grade  int    `json:"grade"`
	Score  int    `json:"score"`
	FP     int    `json:"fp"`
	Win    int    `json:"win"`
	Lose   int    `json:"lose"`
	Draw   int    `json:"draw"`
	UID    int32  `json:"uid"`
	Icon   int32  `json:"icon"`
	DFIcon int32  `json:"df_icon,omitempty"`
}

// Tianti 是全服天梯（全服数据 globalTianti）。
type Tianti struct {
	mu sync.Mutex
	st store.Store

	Week  string            `json:"week"`
	Users map[int32]*ttUser `json:"users,omitempty"`
	Last  []ttRow           `json:"last,omitempty"` // 上赛季全服前 100

	// 防守阵容快照：每打完一场天梯就把当时的阵容冻结在这里。之后别人匹配到该玩家，
	// 对战的都是这份快照，与玩家当前实际阵容无关；发起战斗的一方始终用自己当前的出战阵容。
	// 不存档，服务重启后按需重建。
	snaps map[int32]*ldFoe

	srv *Server // 赛季结算发邮件用（NewServer 里回填）
}

// defSnap 取 uid 的防守阵容快照（没有返回 nil）。调用方持锁。
func (t *Tianti) defSnap(uid int32) *ldFoe {
	return t.snaps[uid]
}

// setDefSnap 冻结 uid 的防守阵容。调用方持锁。
func (t *Tianti) setDefSnap(uid int32, f *ldFoe) {
	if t.snaps == nil {
		t.snaps = map[int32]*ldFoe{}
	}
	t.snaps[uid] = f
}

// ttNow 是当前时间（测试可改）。
var ttNow = time.Now

// 三个「累进榜单」定义：50 级组=全部参赛玩家；40 级组=等级 ≤40；30 级组=等级 ≤30。
var ttRankDefs = []struct {
	name string
	max  int // 允许的最大等级；0 = 不限
}{
	{"50级组", 0},
	{"40级组", 40},
	{"30级组", 30},
}

// 各组第 1~10 名的奖励：元宝 / 银子 / 魂值（用户 2026-10-31 定）。
var ttRankTiers = []struct{ gold, cash, soul int32 }{
	{100000, 10000000, 80000},
	{90000, 9500000, 70000},
	{80000, 9000000, 60000},
	{70000, 8500000, 50000},
	{60000, 8000000, 40000},
	{50000, 7000000, 30000},
	{40000, 7000000, 20000},
	{30000, 7000000, 20000},
	{20000, 7000000, 20000},
	{10000, 7000000, 20000},
}

// ttChatTop 是公告/世界频道公布的名单长度（只发前三名）。
const ttChatTop = 3

// ttScoreDelta 天梯积分（10 分一档，同分基准 胜 +10 / 负 -10，胜负各限 1~15）。
// 分差 = 我的积分 - 对手积分：
//
//	相差 <10 : 胜 +10 / 负 -10
//	相差 10  : 胜 +11 / 负 -9   （我低）；对称：胜 +9  / 负 -11（我高）
//	相差 20  : 胜 +12 / 负 -8           胜 +8  / 负 -12
//	相差 30  : 胜 +13 / 负 -7           胜 +7  / 负 -13
//	相差 40  : 胜 +14 / 负 -6           胜 +6  / 负 -14
//	相差 50  : 胜 +15 / 负 -5           胜 +5  / 负 -15
//	相差 60+ : 胜 +15 封顶              负 -15 封顶；我低到 90 分差时负分封底 -1
func ttScoreDelta(my, foe int, win bool) int {
	steps := (my - foe) / 10
	add, sub := 10-steps, 10+steps
	if add < 1 {
		add = 1
	}
	if add > 15 {
		add = 15
	}
	if sub < 1 {
		sub = 1
	}
	if sub > 15 {
		sub = 15
	}
	if win {
		return add
	}
	return -sub
}

// ttDrawDelta 平局积分：对方分高出 8 以上加 1，自己分高出 8 以上扣 1，分差 8 以内不变。
func ttDrawDelta(my, foe int) int {
	switch diff := my - foe; {
	case diff > 8:
		return -1
	case diff < -8:
		return 1
	}
	return 0
}

// ttRanks 逐组过滤等级后按积分降序取前 10（同分按 uid 升序，结果确定）。调用方持锁。
func ttRanks(t *Tianti) map[string][]int32 {
	top := map[string][]int32{}
	for _, d := range ttRankDefs {
		var ids []int32
		for uid, u := range t.Users {
			if d.max == 0 || u.Grade <= d.max {
				ids = append(ids, uid)
			}
		}
		slices.SortFunc(ids, func(a, b int32) int {
			if c := cmp.Compare(t.Users[b].Score, t.Users[a].Score); c != 0 {
				return c
			}
			return cmp.Compare(a, b)
		})
		if len(ids) > len(ttRankTiers) {
			ids = ids[:len(ttRankTiers)]
		}
		top[d.name] = ids
	}
	return top
}

// ttSettleChats 生成赛季结算的世界频道播报：每组前 3 名的玩家、积分与名次奖励。调用方持锁。
func ttSettleChats(t *Tianti) []string {
	top := ttRanks(t)
	var out []string
	for _, d := range ttRankDefs {
		for i, uid := range top[d.name] {
			if i >= ttChatTop { // 公告只公布各组前三名
				break
			}
			u := t.Users[uid]
			if u == nil {
				continue
			}
			out = append(out, fmt.Sprintf("【天梯赛季结算】%s第%d名 %s 积分%d 奖励：元宝%d 银子%d 魂值%d",
				d.name, i+1, u.Name, u.Score, ttRankTiers[i].gold, ttRankTiers[i].cash, ttRankTiers[i].soul))
		}
	}
	return out
}

// ttSettleSeason 结算上赛季奖励，返回要发的邮件。调用方持锁（只读 t.Users）。
//
// 每组前10名按ttRankTiers发奖；同人多榜与达到的积分档位可叠加。
func ttSettleSeason(t *Tianti) []swMail {
	tiers := ttRankTiers
	top := ttRanks(t)

	// 2) 汇总奖励：排名可多榜叠加，积分档位可累加
	type acc struct {
		gold, cash, soul int32
		notes            []string
	}
	got := map[int32]*acc{}
	add := func(uid int32) *acc {
		a := got[uid]
		if a == nil {
			a = &acc{}
			got[uid] = a
		}
		return a
	}
	for _, d := range ttRankDefs {
		for i, uid := range top[d.name] {
			a := add(uid)
			a.gold, a.cash, a.soul = a.gold+tiers[i].gold, a.cash+tiers[i].cash, a.soul+tiers[i].soul
			a.notes = append(a.notes, fmt.Sprintf("%s第%d名", d.name, i+1))
		}
	}
	for uid, u := range t.Users {
		for _, tr := range []struct {
			min              int
			gold, cash, soul int32
			name             string
		}{
			{1301, 1000, 200000, 888, "积分1301-1500"},
			{1501, 1800, 300000, 1288, "积分1501-1700"},
			{1701, 2600, 450000, 1999, "积分1701-1800"},
			{1801, 3888, 980000, 3888, "积分1801以上"},
		} {
			if u.Score >= tr.min {
				a := add(uid)
				a.gold, a.cash, a.soul = a.gold+tr.gold, a.cash+tr.cash, a.soul+tr.soul
				a.notes = append(a.notes, tr.name)
			}
		}
	}

	var out []swMail
	for uid, a := range got {
		out = append(out, swMail{to: uid, spec: MailSpec{
			Title: "天梯赛季奖励",
			Text: fmt.Sprintf("上赛季天梯（%s 赛季）结算：%s。元宝 %d、银子 %d、魂值 %d。",
				t.Week, strings.Join(a.notes, "、"), a.gold, a.cash, a.soul),
			Gold: a.gold, Cash: a.cash, Soul: a.soul,
		}})
	}
	return out
}

// ttWeek 返回赛季标识。赛季边界 = 每周日 22:02（服务器本地时间）：
// 周日 22:02 起进入新赛季，之前的战斗属于上一赛季。返回值为赛季起点日期。
func ttWeek(t time.Time) string {
	sun := t.AddDate(0, 0, -int(t.Weekday())) // 本周日（Weekday: 0=周日）
	start := time.Date(sun.Year(), sun.Month(), sun.Day(), 22, 2, 0, 0, t.Location())
	if t.Before(start) { // 还没到本周日 22:00，属于上周日 22:00 起的赛季
		start = start.AddDate(0, 0, -7)
	}
	return start.Format("2006-01-02")
}

func openTianti(st store.Store) *Tianti {
	t := &Tianti{st: st}
	loadGlobal(st, globalTianti, t)
	return t
}

// do 持锁执行 fn（先滚动赛季），fn 返回 true 时存档。
func (t *Tianti) do(fn func() bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	dirty := false
	// 旧版用ISO周号；首次合并只迁移标识，不清空旧赛季或触发历史发奖。
	if len(t.Week) == 7 && t.Week[4] == '-' {
		t.Week = ttWeek(ttNow())
		dirty = true
	}
	if wk := ttWeek(ttNow()); t.Week != wk {
		if t.Week != "" {
			t.Last = t.rows(0)
			if t.srv != nil { // 上赛季结束：锁内算好，锁外异步发邮件 + 公告
				mails, chats := ttSettleSeason(t), ttSettleChats(t)
				go func() {
					t.srv.sendSWMails(mails)
					if len(chats) > 0 {
						time.Sleep(ttAnnounceDelay)                       // 避开结算卡顿，按既定时间推送公告
						t.srv.gmBroadcast("天梯赛季已结算，详情见世界聊天", true, false) // 仅弹窗
						for _, c := range chats {
							t.srv.gmBroadcast(c, false, true) // 仅世界频道
						}
					}
				}()
			}
		}
		t.Week, t.Users = wk, map[int32]*ttUser{}
		dirty = true
	}
	if t.Users == nil {
		t.Users = map[int32]*ttUser{}
	}
	if fn() || dirty {
		saveGlobal(t.st, globalTianti, t)
	}
}

func ttGroup(grade int) int {
	switch {
	case grade <= 30:
		return 30
	case grade <= 40:
		return 40
	}
	return 50
}

func (u *ttUser) row(uid int32) ttRow {
	return ttRow{UID: uid, Name: u.Name, Icon: u.Icon, Grade: u.Grade, Score: u.Score, FP: u.FP, Win: u.Win,
		Lose: u.Lose, Draw: u.Draw, DFName: u.DFName, DFIcon: u.DFIcon}
}

// rows 是等级组 group（0 = 全服）的榜，积分从高到低，只含真实玩家（用户要求天梯全真人对战）。调用方持锁。
func (t *Tianti) rows(group int) []ttRow {
	var out []ttRow
	for uid, u := range t.Users {
		if group == 0 || group == 50 || u.Grade <= group {
			out = append(out, u.row(uid))
		}
	}
	slices.SortFunc(out, func(a, b ttRow) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.UID, b.UID)
	})
	return out
}

// rankOf 是 uid 在自己等级组的名次（不在前 100 为 0）。调用方持锁。
func (t *Tianti) rankOf(uid int32, group int) int {
	for i, r := range t.rows(group) {
		if i >= ttRankSize {
			break
		}
		if r.UID == uid {
			return i + 1
		}
	}
	return 0
}

func ttRowsMsg(rows []ttRow, server bool) []xym.Msg {
	out := []xym.Msg{}
	for i, r := range rows {
		if i >= ttRankSize {
			break
		}
		m := xym.Msg{"name": r.Name, "uid": r.UID, "icon": r.Icon, "vip_icon_ts": 0, "grade": r.Grade, "score": r.Score,
			"pos": i + 1, "fightpoint": r.FP, "win": r.Win, "lose": r.Lose, "draw": r.Draw, "dongfu_name": r.DFName,
			"dongfu_icon": r.DFIcon, "xgrade": 0}
		if server {
			m["servername"] = ttServerName
		}
		out = append(out, m)
	}
	return out
}

func init() {
	Handle("LADDER_PTO.C_OPEN_LADDER", func(s *Session, p *Player, m xym.Msg) { s.ttOpen(p, -1) })
	Handle("LADDER_PTO.C_OPEN_RANK", func(s *Session, p *Player, m xym.Msg) {
		typ := int(m["type"].(int64))
		var list []xym.Msg
		s.srv.tianti.do(func() bool { list = ttRowsMsg(s.srv.tianti.rows(ttGroup(typ)), false); return false })
		s.SendExt("LADDER_PTO.S_OPEN_RANK", xym.Msg{"type": typ, "list": list})
	})
	Handle("LADDER_PTO.C_APPLY_ALL_RANK", func(s *Session, p *Player, m xym.Msg) {
		var list []xym.Msg
		s.srv.tianti.do(func() bool { list = ttRowsMsg(s.srv.tianti.rows(0), true); return false })
		s.SendExt("LADDER_PTO.S_OPEN_ALL_RANK", xym.Msg{"list": list})
	})
	Handle("LADDER_PTO.C_APPLY_LAST_RANK", func(s *Session, p *Player, m xym.Msg) {
		var list []xym.Msg
		s.srv.tianti.do(func() bool { list = ttRowsMsg(s.srv.tianti.Last, true); return false })
		s.SendExt("LADDER_PTO.S_OPEN_LAST_RANK", xym.Msg{"list": list})
	})
	// 当前客户端使用ETC_PTO.C_OPEN_RULE_TIPS{text_id:1008}，不注册交付包独有的协议。
	Handle("LADDER_PTO.C_START_PAIR", onTTPair)
	Handle("LADDER_PTO.C_START_FIGHT", onTTFight)
}

// ttPeek 读自己的天梯数据；从未参赛（不在 Users 里）时返回临时默认值且不写回榜单——
// 打开界面不等于参赛，只有点匹配才算进入本赛季榜单。调用方持锁。
func (s *Session) ttPeek(p *Player, fp int, dfName string, dfIcon int32) *ttUser {
	u := s.srv.tianti.Users[s.role.UID]
	if u == nil {
		u = &ttUser{Score: ttInitScore}
	}
	u.Name, u.Grade, u.Icon = s.role.Name, s.role.Grade, int32(s.role.Icon)
	if fp > 0 {
		u.FP = fp
	}
	u.DFName, u.DFIcon = dfName, dfIcon
	return u
}

// ttMe 刷新自己的展示信息（名字、等级、战力、洞府）并返回自己的天梯数据，不存在则创建（参与匹配即上榜）。调用方持锁。
func (s *Session) ttMe(p *Player, fp int, dfName string, dfIcon int32) *ttUser {
	t := s.srv.tianti
	u := t.Users[s.role.UID]
	if u == nil {
		u = &ttUser{Score: ttInitScore}
		t.Users[s.role.UID] = u
	}
	u.Name, u.Grade, u.Icon = s.role.Name, s.role.Grade, int32(s.role.Icon)
	if fp > 0 {
		u.FP = fp
	}
	u.DFName, u.DFIcon = dfName, dfIcon
	return u
}

// ttInfo 是自己当前的战力与洞府（锁外算）。
func (s *Session) ttInfo(p *Player) (int, string, int32) {
	_, pets := p.dyTeam(nil)
	var name string
	var icon int32
	if df, _ := s.srv.dongfuOf(s.role.UID); df != nil {
		name, icon = df.Name, df.Icon
	}
	return p.teamCap(pets), name, icon
}

// ttOpen 发 S_OPEN_LADDER（state < 0 时按有没有对手）+ S_GET_PAIR_TIME。
func (s *Session) ttOpen(p *Player, state int) {
	fp, dfn, dfi := s.ttInfo(p)
	var msg xym.Msg
	var pairs int
	s.srv.tianti.do(func() bool {
		u := s.ttPeek(p, fp, dfn, dfi)
		st := state
		if st < 0 {
			st = b2i(u.Foe != 0)
		}
		msg = xym.Msg{"score": u.Score, "rank": s.srv.tianti.rankOf(s.role.UID, ttGroup(u.Grade)), "win": u.Win,
			"lose": u.Lose, "draw": u.Draw, "maxwin": u.MaxWin, "state": st, "is_double": 0}
		pairs = u.Pairs
		return true
	})
	s.SendExt("LADDER_PTO.S_OPEN_LADDER", msg)
	if state < 0 {
		s.SendExt("LADDER_PTO.S_GET_PAIR_TIME", xym.Msg{"times": pairs})
	}
}

// onTTPair：匹配 / 更换对手——优先真实玩家，候选里积分最接近的 5 人随机一个（尽量不重复上一个）。
// onTTRule 响应天梯规则弹窗（客户端 C_OPEN_RULE_TIPS → S_OPEN_RULE_TIPS{text}）。
func onTTRule(s *Session, p *Player, m xym.Msg) {
	s.SendExt("ETC_PTO.S_OPEN_RULE_TIPS", xym.Msg{"text": ruleTips[1008]})
}

// ttPairStamp binds a confirmation to the day, opponent and change quota seen when it opened.
type ttPairStamp struct {
	day     string
	foe     int32
	changes int
	paid    bool
}

func (u *ttUser) resetPairDay() bool {
	today := ttNow().Format("2006-01-02")
	if u.Day == today {
		return false
	}
	u.Day, u.DayN, u.ChangeN, u.ChangePaid = today, 0, 0, 0
	return true
}

func onTTPair(s *Session, p *Player, m xym.Msg) {
	fp, dfn, dfi := s.ttInfo(p)
	t := s.srv.tianti
	var stamp ttPairStamp
	var dayN, paid int
	t.do(func() bool {
		u := s.ttMe(p, fp, dfn, dfi)
		dirty := u.resetPairDay()
		stamp = ttPairStamp{day: u.Day, foe: u.Foe, changes: u.ChangeN, paid: u.ChangeN >= ttFreeChanges}
		dayN, paid = u.DayN, u.ChangePaid
		return dirty
	})
	if stamp.foe == 0 && dayN >= ttDayLimit {
		s.SendExt("ETC_PTO.S_SEND_NO_CACHE_MSG", xym.Msg{"msg": "次数已经用完，请明天再战。"})
		return
	}
	if stamp.foe != 0 {
		if stamp.changes >= ttFreeChanges+ttPaidChanges || paid >= ttPaidChanges {
			s.SendExt("ETC_PTO.S_SEND_NO_CACHE_MSG", xym.Msg{"msg": "今日更换对手次数已用完，请明天再战。"})
			return
		}
		if stamp.paid {
			text := fmt.Sprintf("每次更换对手消耗 %d元宝 每日可免费更换对手%d次 付费更换对手%d次 今日你已更换%d次，确定要消耗%d元宝进行更换吗？", ttChangeGold, ttFreeChanges, ttPaidChanges, stamp.changes, ttChangeGold)
			s.askBox(text, []string{"确定", "取消"}, func(opt int) {
				if opt == 0 {
					s.ttPairNow(p, fp, dfn, dfi, stamp)
				}
			})
			return
		}
	}
	s.ttPairNow(p, fp, dfn, dfi, stamp)
}

func (s *Session) ttPairNow(p *Player, fp int, dfn string, dfi int32, stamp ttPairStamp) {
	t := s.srv.tianti
	rng := s.actRng()
	var foe int32
	var pairs int
	var foeRow ttRow
	valid := func(u *ttUser) bool {
		return u != nil && stamp.day == ttNow().Format("2006-01-02") && u.Day == stamp.day && u.Foe == stamp.foe && u.ChangeN == stamp.changes &&
			((stamp.foe == 0 && u.DayN < ttDayLimit) || (stamp.foe != 0 && u.ChangeN < ttFreeChanges+ttPaidChanges && u.ChangePaid < ttPaidChanges))
	}
	t.do(func() bool {
		u := s.ttMe(p, fp, dfn, dfi)
		if !valid(u) {
			return false
		}
		// 只在真实玩家中匹配（用户要求天梯全真人）：同组（不重复上一个）→ 全服（不重复上一个）→ 全服（可以是上一个）。
		pick := func(group int, allowLast bool) []ttRow {
			var out []ttRow
			for _, uid := range s.srv.roles.UIDs() {
				if uid == s.role.UID || (!allowLast && uid == u.Foe) {
					continue
				}
				ro := s.srv.roles.Get(uid)
				if ro == nil || (group != 0 && ttGroup(ro.Grade) != group) {
					continue
				}
				if u2 := t.Users[uid]; u2 != nil {
					row := u2.row(uid)
					row.Name, row.Grade, row.Icon = ro.Name, ro.Grade, int32(ro.Icon)
					out = append(out, row)
				} else {
					// 未参赛的真实角色按初始分参与配对，但不进榜单
					out = append(out, ttRow{UID: uid, Name: ro.Name, Grade: ro.Grade, Icon: int32(ro.Icon), Score: ttInitScore})
				}
			}
			return out
		}
		var cands []ttRow
		for _, c := range []struct {
			group     int
			allowLast bool
		}{{ttGroup(u.Grade), false}, {0, false}, {0, true}} {
			if cands = pick(c.group, c.allowLast); len(cands) > 0 {
				break
			}
		}
		slices.SortStableFunc(cands, func(a, b ttRow) int { return cmp.Compare(abs(a.Score-u.Score), abs(b.Score-u.Score)) })
		if len(cands) == 0 {
			return false
		}
		foeRow = cands[rng.IntN(min(ttPickNearest, len(cands)))]
		foe = foeRow.UID
		return false
	})
	if foe == 0 {
		s.sendMsg("暂时没有可以匹配的对手，或匹配状态已变化，请重新匹配。")
		return
	}
	var icons [5]int32
	var f *ldFoe
	t.do(func() bool { f = t.defSnap(foe); return false })
	if f == nil || len(f.Team) == 0 { // 从未打过天梯或旧快照为空：重新生成。
		if f = s.srv.ldFoe(foe, p); f != nil && len(f.Team) > 0 {
			t.do(func() bool { t.setDefSnap(foe, f); return false })
		}
	}
	if f != nil && len(f.Team) > 0 {
		for k, x := range f.Team[:min(5, len(f.Team))] {
			icons[k] = x.Icon
		}
		foeRow.FP = max(foeRow.FP, f.FP)
		foeRow.DFName, foeRow.DFIcon = f.DFName, f.DFIcon
	} else {
		s.sendMsg("对方阵容暂不可用，请重新匹配。")
		s.ttOpen(p, -1)
		return
	}
	committed := false
	failure := "匹配状态已变化，请重新操作。"
	t.do(func() bool {
		u := t.Users[p.UID]
		if !valid(u) {
			return false
		}
		// Only charge after validating the candidate's usable lineup and current confirmation.
		if stamp.foe != 0 && stamp.paid {
			if !s.roleUpdate(func(r *Role) bool {
				if r.Gold < ttChangeGold {
					return false
				}
				r.Gold -= ttChangeGold
				return true
			}, "gold") {
				failure = "元宝不足或保存失败，无法更换对手。"
				return false
			}
			u.ChangePaid++
		}
		if stamp.foe != 0 {
			u.ChangeN++
			u.Pairs++
		} else {
			u.DayN++
			u.Pairs = 0
		}
		u.Foe, pairs = foe, u.Pairs
		committed = true
		return true
	})
	if !committed {
		s.sendMsg(failure)
		return
	}

	var rank int
	t.do(func() bool { rank = t.rankOf(foe, ttGroup(foeRow.Grade)); return false })
	s.SendExt("LADDER_PTO.S_OPEN_FIGHT", xym.Msg{"servername": ttServerName, "name": foeRow.Name, "uid": foe,
		"icon": foeRow.Icon, "vip_icon_ts": 0, "grade": foeRow.Grade, "score": foeRow.Score, "rank": rank,
		"win": foeRow.Win, "lose": foeRow.Lose, "draw": foeRow.Draw, "icon1": icons[0], "icon2": icons[1],
		"icon3": icons[2], "icon4": icons[3], "icon5": icons[4], "fightpoint": foeRow.FP,
		"dongfu_name": foeRow.DFName, "dongfu_icon": foeRow.DFIcon})
	s.SendExt("LADDER_PTO.S_GET_PAIR_TIME", xym.Msg{"times": pairs})
	s.ttOpen(p, 1)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// onTTFight：和匹配到的对手打一场（活力 −1）。
func onTTFight(s *Session, p *Player, m xym.Msg) {
	t := s.srv.tianti
	var foeUID int32
	t.do(func() bool {
		if u := t.Users[s.role.UID]; u != nil {
			foeUID = u.Foe
		}
		return false
	})
	if foeUID == 0 {
		s.retMsg("请先匹配对手。")
		s.ttOpen(p, -1)
		return
	}
	team1, _ := p.dyTeam(nil) // 发起方：自己当前的出战阵容
	var foe *ldFoe
	t.do(func() bool { foe = t.defSnap(foeUID); return false }) // 对手：防守阵容快照
	if foe == nil || len(foe.Team) == 0 {
		foe = s.srv.ldFoe(foeUID, p)
	}
	if len(team1) == 0 {
		s.retMsg("阵容为空，请先上阵。")
		return
	}
	if foe == nil || len(foe.Team) == 0 {
		s.retMsg("对方正忙，请稍后再试。")
		return
	}
	if !s.roleUpdate(func(ro *Role) bool {
		if ro.HPoint < ttHPCost {
			return false
		}
		ro.HPoint -= ttHPCost
		return true
	}) {
		s.retMsg("活力不足，天梯每场战斗消耗一点活力。")
		return
	}
	team2 := battle.CloneTeam(foe.Team)
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulateWith(team1, team2, battle.Options{DrawOnTimeout: true,
		Leaders: [2]int32{p.leaderSkill(), foe.Leader}, Formations: [2]battle.Formation{p.formation(), foe.Zf},
		Prestige: [2]int{s.prestige(), foe.W}}, s.actRng())
	rid := "tianti:" + strconv.FormatInt(recordSeq.Add(1), 10)
	s.fightRecordW(rid, foe.Name, foe.Grade, foe.W, r1, r2, res.Record)
	p.track(evTianti, 1)
	p.saveLog(s)
	gold := int32(ttLoseGold)
	if res.Win {
		gold = int32(ttWinGold)
	}
	s.roleUpdate(func(ro *Role) bool { ro.Gold += gold; return true }, "hpoint", "tpoint", "cash", "gold", "soulexp", "grade", "exp", "next_exp")
	var share xym.Msg
	var championAnnounce string // 新晋榜一的全服公告（锁外发送）
	var scoreDelta int          // 本场积分变化（正=胜，负=负，0=平局不变）
	t.do(func() bool {
		u := t.Users[s.role.UID]
		if u == nil {
			return false
		}
		u.Foe, u.Pairs = 0, 0
		g := ttGroup(u.Grade)
		beforeRank := t.rankOf(s.role.UID, g) // 结算前名次，用来判断是否「新晋」榜一
		foeScore := ttInitScore               // 对手积分；从未参赛过的按初始分算
		if f := t.Users[foeUID]; f != nil {
			foeScore = f.Score
		}
		if res.Draw {
			scoreDelta = ttDrawDelta(u.Score, foeScore)
			u.Draw++
		} else {
			scoreDelta = ttScoreDelta(u.Score, foeScore, res.Win)
			if res.Win {
				u.Win++
				u.Streak++
				u.MaxWin = max(u.MaxWin, u.Streak)
			} else {
				u.Lose++
				u.Streak = 0
			}
		}
		d := scoreDelta
		u.Score = max(0, u.Score+d)
		// 被挑战者不扣分、不记负场（用户要求：只有发起战斗的一方记账，别人积分不掉）。
		if res.Win {
			ranks := map[int]int{30: 0, 40: 0, 50: 0}
			ranks[g] = t.rankOf(s.role.UID, g)
			if ranks[g] == 1 && beforeRank != 1 {
				championAnnounce = fmt.Sprintf("[公告]恭喜 %s 战胜 %s 成为天梯榜一 当前积分：%d", s.role.Name, foe.Name, u.Score)
			}
			share = xym.Msg{"maxwin": u.Streak, "score": u.Score, "servername": ttServerName, "rank30": ranks[30],
				"rank40": ranks[40], "rank50": ranks[50], "gotsharereward": 1, "icon": s.role.Icon, "vip_icon_ts": 0}
		}
		return true
	})
	if share != nil {
		s.SendExt("LADDER_PTO.S_SEND_SHARE_INFO", share)
	}
	if championAnnounce != "" {
		s.srv.gmBroadcast(championAnnounce, true, true)
	}
	// 冻结自己的防守阵容：这场用的阵容，之后别人匹配到你就按这套打（你换阵容也不影响）。
	if f := s.srv.realFoe(s.role.UID, p); f != nil {
		t.do(func() bool { t.setDefSnap(s.role.UID, f); return false })
	}
	// 发起者不管胜负都弹窗告知本场积分变化：战报已在上方发出，客户端此时开始进入战斗画面，
	// 延时 1 秒再弹，避免盖在战斗动画之前（平局单独文案）。
	go func() {
		time.Sleep(3 * time.Second)
		var text string
		switch {
		case res.Draw:
			text = fmt.Sprintf("战斗平局 %+d分", scoreDelta)
		case res.Win:
			text = fmt.Sprintf("战斗胜利 +%d分", scoreDelta)
		default:
			text = fmt.Sprintf("战斗失败 %d分", scoreDelta)
		}
		s.SendExt("ETC_PTO.S_SEND_NO_CACHE_MSG", xym.Msg{"msg": text})
	}()
	s.ttOpen(p, 0)
	typ := 15 // rewardview_mgr BOX_TIANTIWIN（天梯胜利界面）
	if !res.Win {
		typ = boxFightLose // 输了是战斗失败界面（以前输赢都发 15，输了也显示胜利）
	}
	r := rewardMsg(typ, nil)
	r["gold"], r["record"], r["no_fight_again"] = gold, rid, 1
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
}

// ttLoop 每 5 秒检查一次赛季切换：周日 22:02 到点即结算并发奖，不依赖玩家在线。
func (srv *Server) ttLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-srv.opsStop:
			return
		case <-t.C:
			srv.tianti.do(func() bool { return false })
		}
	}
}
