package xymsrv

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
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
// 本服：对手是全服真实玩家（出战阵容，同论道）加论道的 100 个假玩家（积分 3700 往下按强弱排开，不变）。
// 估计：初始积分 1000；输一场 −20（不低于 0）、被挑战的真人反向加减；输了奖励 2 元宝；更换对手不收费；
// 赛季按自然周（周一）重置，上赛季全服前 100 名存进「历史」。

const (
	ttInitScore   = 1000
	ttStep        = 20
	ttWinGold     = 5
	ttLoseGold    = 2
	ttHPCost      = 1
	ttRankSize    = 100
	ttServerName  = "细水长流"
	ttPickNearest = 5
)

// ttUser 是一位真人这个赛季的天梯数据。
type ttUser struct {
	Name   string `json:"name"`
	Grade  int    `json:"grade"`
	Icon   int32  `json:"icon"`
	FP     int    `json:"fp"`
	DFName string `json:"df_name,omitempty"`
	DFIcon int32  `json:"df_icon,omitempty"`
	Score  int    `json:"score"`
	Win    int    `json:"win"`
	Lose   int    `json:"lose"`
	Draw   int    `json:"draw"`
	Streak int    `json:"streak"`  // 当前主动连胜
	MaxWin int    `json:"max_win"` // 本赛季最高主动连胜
	Foe    int32  `json:"foe,omitempty"`
	Pairs  int    `json:"pairs,omitempty"` // 这次匹配后换了几次对手
}

// ttRow 是榜上一行。
type ttRow struct {
	UID    int32  `json:"uid"`
	Name   string `json:"name"`
	Icon   int32  `json:"icon"`
	Grade  int    `json:"grade"`
	Score  int    `json:"score"`
	FP     int    `json:"fp"`
	Win    int    `json:"win"`
	Lose   int    `json:"lose"`
	Draw   int    `json:"draw"`
	DFName string `json:"df_name,omitempty"`
	DFIcon int32  `json:"df_icon,omitempty"`
}

// Tianti 是全服天梯（存档位 tiantiUID）。
type Tianti struct {
	mu sync.Mutex
	st store.Store

	Week  string            `json:"week"`
	Users map[int32]*ttUser `json:"users,omitempty"`
	Last  []ttRow           `json:"last,omitempty"` // 上赛季全服前 100
}

// ttNow 是当前时间（测试可改）。
var ttNow = time.Now

func ttWeek(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-%02d", y, w)
}

func openTianti(st store.Store) *Tianti {
	t := &Tianti{st: st}
	loadGlobal(st, tiantiUID, t, "tianti")
	return t
}

// do 持锁执行 fn（先滚动赛季），fn 返回 true 时存档。
func (t *Tianti) do(fn func() bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	dirty := false
	if wk := ttWeek(ttNow()); t.Week != wk {
		if t.Week != "" {
			t.Last = t.rows(0)
		}
		t.Week, t.Users = wk, map[int32]*ttUser{}
		dirty = true
	}
	if t.Users == nil {
		t.Users = map[int32]*ttUser{}
	}
	if fn() || dirty {
		saveGlobal(t.st, tiantiUID, t, "tianti")
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

// ttFakeRow 是第 i 个假玩家（论道的 fakeFoe）在天梯上的数据：积分按强弱从 3700 往下排。
func ttFakeRow(uid int32) ttRow {
	f := fakeFoeCached(uid)
	i := int(uid - ldFakeUIDBase)
	return ttRow{UID: uid, Name: f.Name, Icon: f.Icon, Grade: f.Grade, Score: 3700 - i*27, FP: f.FP,
		Win: 20 + (ldFakeCount-i)/3, Lose: 5 + i%9, Draw: i % 3}
}

func (u *ttUser) row(uid int32) ttRow {
	return ttRow{UID: uid, Name: u.Name, Icon: u.Icon, Grade: u.Grade, Score: u.Score, FP: u.FP, Win: u.Win,
		Lose: u.Lose, Draw: u.Draw, DFName: u.DFName, DFIcon: u.DFIcon}
}

// rows 是等级组 group（0 = 全服）的榜，积分从高到低。调用方持锁。
func (t *Tianti) rows(group int) []ttRow {
	var out []ttRow
	for i := range ldFakeCount {
		if r := ttFakeRow(int32(ldFakeUIDBase + i)); group == 0 || ttGroup(r.Grade) == group {
			out = append(out, r)
		}
	}
	for uid, u := range t.Users {
		if group == 0 || ttGroup(u.Grade) == group {
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
	Handle("LADDER_PTO.C_START_PAIR", onTTPair)
	Handle("LADDER_PTO.C_START_FIGHT", onTTFight)
}

// ttMe 刷新自己的展示信息（名字、等级、战力、洞府）并返回自己的天梯数据。调用方持锁。
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
		u := s.ttMe(p, fp, dfn, dfi)
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
func onTTPair(s *Session, p *Player, m xym.Msg) {
	fp, dfn, dfi := s.ttInfo(p)
	var foe int32
	var pairs int
	var foeRow ttRow
	t := s.srv.tianti
	rng := s.actRng()
	t.do(func() bool {
		u := s.ttMe(p, fp, dfn, dfi)
		// 尽量匹配真实玩家（用户要求）：同组真人（不重复上一个）→ 全服真人（不重复上一个）→ 全服真人（可以是上一个）
		// → 同组任何人 → 全服任何人。
		pick := func(group int, realOnly, allowLast bool) []ttRow {
			var out []ttRow
			for _, r := range t.rows(group) {
				if r.UID != s.role.UID && (allowLast || r.UID != u.Foe) && (!realOnly || !isFakeUID(r.UID)) {
					out = append(out, r)
				}
			}
			return out
		}
		var cands []ttRow
		for _, c := range []struct {
			group     int
			realOnly  bool
			allowLast bool
		}{{ttGroup(u.Grade), true, false}, {0, true, false}, {0, true, true}, {ttGroup(u.Grade), false, false}, {0, false, true}} {
			if cands = pick(c.group, c.realOnly, c.allowLast); len(cands) > 0 {
				break
			}
		}
		slices.SortStableFunc(cands, func(a, b ttRow) int { return cmp.Compare(abs(a.Score-u.Score), abs(b.Score-u.Score)) })
		if len(cands) == 0 {
			return false
		}
		foeRow = cands[rng.IntN(min(ttPickNearest, len(cands)))]
		if u.Foe != 0 {
			u.Pairs++
		} else {
			u.Pairs = 0
		}
		u.Foe, foe, pairs = foeRow.UID, foeRow.UID, u.Pairs
		return true
	})
	if foe == 0 {
		s.sendMsg("暂时没有可以匹配的对手。")
		return
	}
	var icons [5]int32
	if f := s.srv.ldFoe(foe, p); f != nil {
		for k, x := range f.Team[:min(5, len(f.Team))] {
			icons[k] = x.Icon
		}
		foeRow.FP = max(foeRow.FP, f.FP)
		foeRow.DFName, foeRow.DFIcon = f.DFName, f.DFIcon
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
	team1, _ := p.dyTeam(nil)
	foe := s.srv.ldFoe(foeUID, p)
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
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), foe.Leader}, foe.Zf, s.actRng())
	rid := "tianti:" + strconv.FormatInt(recordSeq.Add(1), 10)
	s.fightRecord(rid, foe.Name, foe.Grade, r1, r2, res.Record)
	p.track(evTianti, 1)
	p.saveLog(s)
	gold := int32(ttLoseGold)
	if res.Win {
		gold = ttWinGold
	}
	s.roleUpdate(func(ro *Role) bool { ro.Gold += gold; return true }, "hpoint", "tpoint", "cash", "gold", "soulexp", "grade", "exp", "next_exp")
	var share xym.Msg
	t.do(func() bool {
		u := t.Users[s.role.UID]
		if u == nil {
			return false
		}
		u.Foe, u.Pairs = 0, 0
		d := ttStep
		if res.Win {
			u.Win++
			u.Streak++
			u.MaxWin = max(u.MaxWin, u.Streak)
		} else {
			d = -ttStep
			u.Lose++
			u.Streak = 0
		}
		u.Score = max(0, u.Score+d)
		if o := t.Users[foeUID]; o != nil { // 被挑战的真人反向加减
			o.Score = max(0, o.Score-d)
			if res.Win {
				o.Lose++
			} else {
				o.Win++
			}
		}
		if res.Win {
			g := ttGroup(u.Grade)
			ranks := map[int]int{30: 0, 40: 0, 50: 0}
			ranks[g] = t.rankOf(s.role.UID, g)
			share = xym.Msg{"maxwin": u.Streak, "score": u.Score, "servername": ttServerName, "rank30": ranks[30],
				"rank40": ranks[40], "rank50": ranks[50], "gotsharereward": 1, "icon": s.role.Icon, "vip_icon_ts": 0}
		}
		return true
	})
	if share != nil {
		s.SendExt("LADDER_PTO.S_SEND_SHARE_INFO", share)
	}
	s.ttOpen(p, 0)
	typ := 15 // rewardview_mgr BOX_TIANTIWIN（天梯胜利界面）
	if !res.Win {
		typ = boxFightLose // 输了是战斗失败界面（以前输赢都发 15，输了也显示胜利）
	}
	r := rewardMsg(typ, nil)
	r["gold"], r["record"], r["no_fight_again"] = gold, rid, 1
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
}
