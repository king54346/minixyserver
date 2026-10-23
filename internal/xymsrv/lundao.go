package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 论道（LUNDAO_PTO，排名挑战）：全服一张名次榜，挑战比自己靠前的名次，赢了互换名次；按名次每小时累积积分，
// 积分在「兑换」里换道具。
//
// 依据：
//   - 抓包（session-01）：C_OPEN_LUNDAO → S_OPEN_LUNDAO{score, times 10, list} + S_SEND_TIMES；list 是前 10 名
//     加上自己上 5 名、下 4 名（自己 182 时列 177–186）。C_FIGHT_LUNDAO{rank} 回 S_FIGHT_RECORD（rid「lundao:N」）
//     → 赢了才有 S_SEND_SHARE_INFO（rank = 自己的新名次，targetrank = 对方的新名次）→ S_OPEN_LUNDAO → S_SEND_TIMES
//     → S_SEND_SCORE → 赢了才有 S_REWARD_MSG{type 14, record}（输了没有奖励弹窗）。挑战胜负都扣 1 次，积分不变。
//   - logic/dao_mgr.lua：每小时积分 getRankScore(人物等级段 ≤30/≤40/≤50, 名次)（RankScoreCfg）；
//     兑换表 AwardList（cid = 奖励号，price 积分，grade 需要的人物等级）；C_OPEN_TOP{type 30/40/50} 是各等级组榜；
//     C_REFRESH_SCORE / C_CHANGE_REWARD 等 S_SEND_SCORE。
//   - view/RankAwardView.lua：S_SEND_ITEMLIST{score, list[pos, cid, times 剩余次数]}，C_BUY_ITEM{cid, pos, count}，
//     「这个物品你购买够多了，刷新后才可以购买」。
//
// 本服：对手是全服真实玩家（用其当前出战阵容；对方正被别的请求占用时用上次看到的快照）加 100 个假玩家
// （按名次由强到弱随机生成的阵容，种子固定，重启后不变）。新玩家第一次打开时排在榜尾。
// 每天 15 次（用户要求；抓包是 10），用完后挑战时弹框花 ldBuyGold 元宝买 1 次并直接开打（官方确认框文字）；
// 估计：只能挑战比自己靠前且相差不超过 5 名的；兑换次数每天刷新
// （宝石、丹药 10 次，高级宝石 5 次，魂魄 1 次）；挑战动态（C_APPLY_STATE）保留最近 30 条。

const (
	ldDailyTimes   = 15
	ldBuyGold      = 10 // 用完后购买 1 次挑战的元宝
	ldFakeCount    = 100
	ldFakeUIDBase  = 900001
	ldChallengeGap = 5
	ldKeepStates   = 30
)

// ldScoreCfg：名次上限 -> 各等级段每小时积分（dao_mgr.RankScoreCfg）。
var ldScoreCfg = []struct {
	rank  int
	score [3]int32
}{
	{1, [3]int32{90, 114, 138}}, {5, [3]int32{72, 90, 114}}, {10, [3]int32{60, 75, 96}}, {25, [3]int32{56, 68, 83}},
	{50, [3]int32{54, 66, 81}}, {100, [3]int32{53, 65, 80}}, {300, [3]int32{44, 56, 71}}, {600, [3]int32{42, 54, 69}},
	{1000, [3]int32{41, 53, 68}}, {1500, [3]int32{39, 51, 66}}, {2100, [3]int32{30, 42, 57}}, {2800, [3]int32{29, 41, 56}},
	{3600, [3]int32{27, 39, 54}}, {4500, [3]int32{18, 30, 45}}, {5500, [3]int32{17, 29, 44}}, {6500, [3]int32{15, 27, 42}},
	{7600, [3]int32{6, 18, 33}}, {8800, [3]int32{5, 17, 32}}, {10000, [3]int32{3, 15, 30}},
}

func ldHourlyScore(grade, rank int) int32 {
	g := 2
	switch {
	case grade <= 30:
		g = 0
	case grade <= 40:
		g = 1
	}
	for _, c := range ldScoreCfg {
		if rank <= c.rank {
			return c.score[g]
		}
	}
	return 0
}

// ldShop：兑换表（dao_mgr.AwardList；奖励内容照 auto/reward.lua 同号奖励）。
var ldShop = []struct {
	cid   int32
	code  string
	n     int32
	price int32
	grade int
	times int
}{
	{11118, "WP10095", 5, 700, 0, 10}, {11119, "WP10096", 5, 1350, 0, 10},
	{10343, "WP10127", 5, 1000, 25, 10}, {10344, "WP10126", 5, 1000, 25, 10},
	{11120, "WP10140", 1, 1000, 20, 10}, {11121, "WP10141", 1, 1000, 20, 10}, {11122, "WP10151", 1, 1000, 20, 10},
	{11123, "WP10161", 1, 1000, 20, 10}, {11124, "WP10171", 1, 1000, 20, 10}, {11125, "WP10181", 1, 1000, 20, 10},
	{11126, "WP10191", 1, 1000, 20, 10},
	{11127, "WP10142", 1, 3000, 20, 5}, {11128, "WP10152", 1, 3000, 20, 5}, {11129, "WP10162", 1, 3000, 20, 5},
	{11130, "WP10172", 1, 3000, 20, 5}, {11131, "WP10182", 1, 3000, 20, 5}, {11132, "WP10192", 1, 3000, 20, 5},
	{24320, "WP10436", 1, 1800, 30, 5}, {24321, "WP10437", 1, 1800, 30, 5},
	{11117, "WP10130", 1, 750, 31, 10}, {11133, "WP10130", 10, 7500, 31, 5},
	{44006, "HP15005", 1, 5000, 1, 1}, {24110, "HP25006", 1, 5000, 1, 1}, {24111, "HP35008", 1, 5000, 1, 1},
}

// LundaoState 是玩家自己的论道数据（Player.Lundao）。
type LundaoState struct {
	Day     string        `json:"day"`
	Times   int           `json:"times"`    // 今日剩余挑战次数
	Score   int32         `json:"score"`    // 论道积分
	ScoreAt int64         `json:"score_at"` // 积分结算到的时间（Unix 秒）
	Bought  map[int32]int `json:"bought,omitempty"`
	Base    int           `json:"base,omitempty"` // 当天按哪个每日次数发的（改每日次数后当天补差）
}

func (p *Player) lundao() *LundaoState {
	if p.Lundao == nil {
		p.Lundao = &LundaoState{ScoreAt: nowUnix()}
	}
	l := p.Lundao
	if d := today(); l.Day != d {
		l.Day, l.Times, l.Bought, l.Base = d, ldDailyTimes, map[int32]int{}, ldDailyTimes
	}
	if l.Base == 0 {
		l.Base = 10 // 旧存档：当天按 10 次发的
	}
	if l.Base < ldDailyTimes {
		l.Times += ldDailyTimes - l.Base
		l.Base = ldDailyTimes
	}
	if l.Bought == nil {
		l.Bought = map[int32]int{}
	}
	return l
}

// Ladder 是全服论道榜（Ranks[i] 是第 i+1 名的 uid）。
type Ladder struct {
	mu sync.Mutex
	st store.Store

	Ranks  []int32   `json:"ranks"`
	States []LDState `json:"states,omitempty"`

	snaps map[int32]*ldFoe // 真实玩家阵容快照（不存档）
}

// LDState 是一条挑战动态（S_SEND_STATE.list）。
type LDState struct {
	UID      int32  `json:"uid"`
	Time     int64  `json:"time"`
	Attacker string `json:"attacker"`
	Target   string `json:"target"`
	Pos      int    `json:"pos"` // 挑战者的新名次
}

// ldFoe 是榜上一位的展示信息与阵容。
type ldFoe struct {
	UID    int32
	Name   string
	Grade  int
	Icon   int32
	Faces  [2]int32
	FP     int
	Team   []*battle.Fighter
	Leader int32
	DFName string
	DFIcon int32
	Zf     battle.Formation // 阵法（zhenfa.go）
}

func openLadder(st store.Store) *Ladder {
	l := &Ladder{st: st, snaps: map[int32]*ldFoe{}}
	loadGlobal(st, lundaoUID, l, "lundao")
	if len(l.Ranks) == 0 {
		for i := range ldFakeCount {
			l.Ranks = append(l.Ranks, int32(ldFakeUIDBase+i))
		}
	}
	return l
}

func (l *Ladder) do(fn func() bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if fn() {
		saveGlobal(l.st, lundaoUID, l, "lundao")
	}
}

// rankOf 是 uid 的名次（1 起；不在榜上为 0）。调用方持锁。
func (l *Ladder) rankOf(uid int32) int {
	if i := slices.Index(l.Ranks, uid); i >= 0 {
		return i + 1
	}
	return 0
}

// join 让 uid 上榜（排在榜尾），返回名次。
func (l *Ladder) join(uid int32) int {
	var r int
	l.do(func() bool {
		if r = l.rankOf(uid); r > 0 {
			return false
		}
		l.Ranks = append(l.Ranks, uid)
		r = len(l.Ranks)
		return true
	})
	return r
}

func isFakeUID(uid int32) bool { return uid >= ldFakeUIDBase && uid < ldFakeUIDBase+ldFakeCount }

var (
	ldNamesA = []rune("云风雪月星辰霜雨烟霞青紫玄苍碧墨白金清幽逸凌")
	ldNamesB = []string{"散人", "道长", "真人", "剑客", "居士", "仙子", "公子", "书生", "侠客", "老祖", "上人", "行者",
		"无极", "长生", "问道", "逍遥", "听澜", "望舒", "知秋", "寻梅"}
)

// fakeFoe 生成第 i 个（0 起，越小越强）假玩家：名字、等级、阵容都由 i 决定（种子固定）。
func fakeFoe(i int) *ldFoe {
	rng := rand.New(rand.NewPCG(uint64(i)+7919, 104729))
	f := &ldFoe{UID: int32(ldFakeUIDBase + i)}
	f.Name = string(ldNamesA[i%len(ldNamesA)]) + string(ldNamesA[(i*7+3)%len(ldNamesA)]) + ldNamesB[(i/2)%len(ldNamesB)]
	switch {
	case i < 50:
		f.Grade = 50
	case i < 80:
		f.Grade = 45
	default:
		f.Grade = 40
	}
	petGrade := max(20, min(100, f.Grade*2)*(110-i)/110)
	tal := max(0, 9-i/10) // 神通等级下标：榜首满级，往后逐级递减
	w := [5]int{0, 0, 10, 40, 50}
	switch {
	case i < 20:
		w = [5]int{0, 0, 0, 10, 90}
	case i >= 60:
		w = [5]int{0, 5, 35, 45, 15}
	}
	n := 8
	if i >= 50 {
		n = 6
	}
	used := map[int32]bool{}
	for slot := 1; slot <= n; slot++ {
		var t int32
		for range 10 {
			if t = slPick(randStar(w, rng), rng); !used[t] && len(gamedata.Pets[t].Talents[0]) > 0 {
				break
			}
		}
		used[t] = true
		if x := petFighter(t, petGrade, slot, tal); x != nil {
			x.ID = int32(800000 + i*10 + slot)
			f.Team = append(f.Team, x)
		}
	}
	slices.SortStableFunc(f.Team, func(a, b *battle.Fighter) int { return int(b.Att)*4 + int(b.HP) - int(a.Att)*4 - int(a.HP) })
	for k, x := range f.Team {
		x.Pos = 101 + k
		f.FP += int(x.Att)*4 + int(x.HP)
	}
	if len(f.Team) > 0 {
		f.Icon = f.Team[0].Icon
		f.Faces[0] = f.Team[0].Icon
		f.Leader = gamedata.Pets[f.Team[0].Icon].LeaderSkill
	}
	if len(f.Team) > 1 {
		f.Faces[1] = f.Team[1].Icon
	}
	return f
}

var (
	ldFakesOnce sync.Once
	ldFakes     []*ldFoe
)

func fakeFoeCached(uid int32) *ldFoe {
	ldFakesOnce.Do(func() {
		for i := range ldFakeCount {
			ldFakes = append(ldFakes, fakeFoe(i))
		}
	})
	return ldFakes[uid-ldFakeUIDBase]
}

// realFoe 是真实玩家 uid 的当前出战阵容（敌方站位 101 起）。me 是本请求自己的存档（已持锁）。
func (srv *Server) realFoe(uid int32, me *Player) *ldFoe {
	ro := srv.roles.Get(uid)
	if ro == nil {
		return nil
	}
	f := &ldFoe{UID: uid, Name: ro.Name, Grade: ro.Grade, Icon: int32(ro.Icon)}
	var p *Player
	if me != nil && me.UID == uid {
		p = me
	} else if q, err := srv.players.Get(uid); err == nil && q.mu.TryLock() {
		defer q.mu.Unlock()
		p = q
		if p.role == nil {
			p.role = ro
		}
	}
	if p == nil { // 正被占用：用快照
		srv.ladder.mu.Lock()
		defer srv.ladder.mu.Unlock()
		return srv.ladder.snaps[uid]
	}
	team, pets := p.dyTeam(nil)
	for _, x := range team {
		x.Pos += 100
	}
	f.Team, f.Leader, f.FP, f.Zf = team, p.leaderSkill(), p.teamCap(pets), p.formation()
	for k, x := range team[:min(2, len(team))] {
		f.Faces[k] = x.Icon
	}
	if df, _ := srv.dongfuOf(uid); df != nil {
		f.DFName, f.DFIcon = df.Name, df.Icon
	}
	srv.ladder.mu.Lock()
	srv.ladder.snaps[uid] = f
	srv.ladder.mu.Unlock()
	return f
}

// onOpenPlayerInfo：排行榜（RankView，论道排行等）点玩家看阵容。C_OPEN_PLAYER_INFO{uid} →
// S_OPEN_PLAYER_INFO{list[pos 出战位, type 护法编号, skillfull]}（客户端 view/UtilsTeamView），阵容同论道对手。
func onOpenPlayerInfo(s *Session, p *Player, m xym.Msg) {
	list := []xym.Msg{}
	if f := s.srv.ldFoe(int32(m["uid"].(int64)), p); f != nil {
		for _, x := range f.Team {
			list = append(list, xym.Msg{"pos": x.Pos % 100, "type": x.Icon, "skillfull": b2i(x.SkillFull)})
		}
	}
	s.SendExt("ETC_PTO.S_OPEN_PLAYER_INFO", xym.Msg{"list": list})
}

func (srv *Server) ldFoe(uid int32, me *Player) *ldFoe {
	if isFakeUID(uid) {
		return fakeFoeCached(uid)
	}
	return srv.realFoe(uid, me)
}

func (f *ldFoe) msg(rank int) xym.Msg {
	return xym.Msg{"uid": f.UID, "rank": rank, "name": f.Name, "grade": f.Grade, "icon": f.Icon, "vip_icon_ts": 0,
		"petface_min": f.Faces[0], "petface_max": f.Faces[1], "fight_point": f.FP, "dongfu_name": f.DFName,
		"dongfu_icon": f.DFIcon, "xgrade": 0}
}

// dongfuOf 是 uid 所在洞府（不在返回 nil）。
func (srv *Server) dongfuOf(uid int32) (*Dongfu, *DFMember) {
	var df *Dongfu
	var me *DFMember
	srv.dongfu.do(func() bool {
		df, me = srv.dongfu.of(uid)
		return false
	})
	return df, me
}

// settleScore 按当前名次结算到现在为止的整点积分。
func (s *Session) ldSettle(p *Player, rank int) {
	l := p.lundao()
	now := nowUnix()
	if l.ScoreAt == 0 || l.ScoreAt > now {
		l.ScoreAt = now
	}
	if h := (now - l.ScoreAt) / 3600; h > 0 && rank > 0 {
		l.Score += int32(h) * ldHourlyScore(s.role.Grade, rank)
		l.ScoreAt += h * 3600
	}
}

func init() {
	Handle("LUNDAO_PTO.C_OPEN_LUNDAO", func(s *Session, p *Player, m xym.Msg) { s.ldOpen(p) })
	Handle("LUNDAO_PTO.C_FIGHT_LUNDAO", onFightLundao)
	Handle("LUNDAO_PTO.C_REFRESH_SCORE", func(s *Session, p *Player, m xym.Msg) { s.ldScore(p) })
	Handle("LUNDAO_PTO.C_CHANGE_REWARD", func(s *Session, p *Player, m xym.Msg) { s.ldScore(p) })
	Handle("LUNDAO_PTO.C_OPEN_TOP", onLundaoTop)
	Handle("ETC_PTO.C_OPEN_PLAYER_INFO", onOpenPlayerInfo)
	Handle("LUNDAO_PTO.C_APPLY_STATE", onLundaoState)
	Handle("LUNDAO_PTO.C_OPEN_ITEMLIST", func(s *Session, p *Player, m xym.Msg) { s.ldItemList(p) })
	Handle("LUNDAO_PTO.C_BUY_ITEM", onLundaoBuy)
	Handle("LUNDAO_PTO.C_OPEN_XIUXING", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("LUNDAO_PTO.S_OPEN_XIUXING", xym.Msg{"list": []xym.Msg{}})
	})
}

func (s *Session) ldScore(p *Player) {
	var rank int
	s.srv.ladder.do(func() bool { rank = s.srv.ladder.rankOf(s.role.UID); return false })
	s.ldSettle(p, rank)
	p.saveLog(s)
	s.SendExt("LUNDAO_PTO.S_SEND_SCORE", xym.Msg{"score": p.lundao().Score})
}

// ldOpenMsg 编 S_OPEN_LUNDAO：前 10 名 + 自己上 5 名到下 4 名。
func (s *Session) ldOpenMsg(p *Player) xym.Msg {
	lad := s.srv.ladder
	var ranks []int32
	var my int
	lad.do(func() bool {
		ranks, my = slices.Clone(lad.Ranks), lad.rankOf(s.role.UID)
		return false
	})
	var show []int
	for r := 1; r <= min(10, len(ranks)); r++ {
		show = append(show, r)
	}
	for r := max(11, my-5); r <= min(len(ranks), my+4); r++ {
		show = append(show, r)
	}
	list := []xym.Msg{}
	for _, r := range show {
		if f := s.srv.ldFoe(ranks[r-1], p); f != nil {
			list = append(list, f.msg(r))
		}
	}
	l := p.lundao()
	return xym.Msg{"score": l.Score, "times": l.Times, "is_double": 0, "is_bonus": 0, "list": list}
}

func (s *Session) ldOpen(p *Player) {
	rank := s.srv.ladder.join(s.role.UID)
	s.ldSettle(p, rank)
	p.saveLog(s)
	s.SendExt("LUNDAO_PTO.S_OPEN_LUNDAO", s.ldOpenMsg(p))
	s.SendExt("LUNDAO_PTO.S_SEND_TIMES", xym.Msg{"times": p.lundao().Times})
}

func onFightLundao(s *Session, p *Player, m xym.Msg) {
	target := int(m["rank"].(int64))
	lad := s.srv.ladder
	my := lad.join(s.role.UID)
	s.ldSettle(p, my)
	l := p.lundao()
	var foeUID int32
	lad.do(func() bool {
		if target >= 1 && target <= len(lad.Ranks) {
			foeUID = lad.Ranks[target-1]
		}
		return false
	})
	fail := func(msg string) {
		s.sendMsg(msg)
		s.SendExt("LUNDAO_PTO.S_OPEN_LUNDAO", s.ldOpenMsg(p))
		s.SendExt("LUNDAO_PTO.S_SEND_TIMES", xym.Msg{"times": l.Times})
	}
	switch {
	case l.Times <= 0:
		text := fmt.Sprintf("今日的论道挑战次数已经用完了。#r花费#G%d元宝#n购买1次挑战次数并开始挑战？", ldBuyGold)
		s.askBox(text, []string{"购买", "关闭"}, func(opt int) {
			if opt != 0 || p.lundao().Times > 0 || !s.spend(0, ldBuyGold) {
				return
			}
			p.lundao().Times++
			onFightLundao(s, p, m)
		})
		return
	case foeUID == 0 || foeUID == s.role.UID || target >= my:
		fail("只能挑战排名比你靠前的对手。")
		return
	case my-target > ldChallengeGap:
		fail("只能挑战排名在你之前 5 名以内的对手。")
		return
	}
	foe := s.srv.ldFoe(foeUID, p)
	team1, _ := p.dyTeam(nil)
	if foe == nil || len(foe.Team) == 0 || len(team1) == 0 {
		fail("对方正忙，请稍后再试。")
		return
	}
	team2 := battle.CloneTeam(foe.Team)
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), foe.Leader}, foe.Zf, s.actRng())
	rid := "lundao:" + dfNum(int32(recordSeq.Add(1)))
	s.fightRecord(rid, foe.Name, foe.Grade, r1, r2, res.Record)
	l.Times--
	p.track(evLundao, 1)
	if res.Win {
		lad.do(func() bool {
			a, b := lad.rankOf(s.role.UID), lad.rankOf(foeUID)
			if a == 0 || b == 0 || b >= a {
				return false
			}
			lad.Ranks[a-1], lad.Ranks[b-1] = foeUID, s.role.UID
			lad.States = append(lad.States, LDState{UID: s.role.UID, Time: nowUnix(), Attacker: s.role.Name, Target: foe.Name, Pos: b})
			if n := len(lad.States); n > ldKeepStates {
				lad.States = lad.States[n-ldKeepStates:]
			}
			my, target = b, a
			return true
		})
	}
	p.saveLog(s)
	if res.Win {
		var faces [2]int32
		for k, x := range r1[:min(2, len(r1))] {
			faces[k] = x.Icon
		}
		s.SendExt("LUNDAO_PTO.S_SEND_SHARE_INFO", xym.Msg{"rank": my, "grade": s.role.Grade, "name": s.role.Name,
			"icon": s.role.Icon, "vip_icon_ts": 0, "pet1": faces[0], "pet2": faces[1], "targetuid": foeUID,
			"target_vip_icon_ts": 0, "targetrank": target, "targetgrade": foe.Grade, "targetname": foe.Name,
			"targeticon": foe.Icon, "targetpet1": foe.Faces[0], "targetpet2": foe.Faces[1], "gotsharereward": 1})
	}
	s.SendExt("LUNDAO_PTO.S_OPEN_LUNDAO", s.ldOpenMsg(p))
	s.SendExt("LUNDAO_PTO.S_SEND_TIMES", xym.Msg{"times": l.Times})
	s.SendExt("LUNDAO_PTO.S_SEND_SCORE", xym.Msg{"score": l.Score})
	typ := 14 // rewardview_mgr BOX_LUNDAOWIN
	if !res.Win {
		typ = boxFightLose // 输了也要结果弹窗（跳过战斗后原来什么都不显示）
	}
	r := rewardMsg(typ, nil)
	r["record"], r["no_fight_again"] = rid, 1
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
}

// onLundaoTop：等级组榜（type 30 / 40 / 50 = 人物 ≤30、31–40、41–50），列该组名次最前的 10 人。
func onLundaoTop(s *Session, p *Player, m xym.Msg) {
	typ := int(m["type"].(int64))
	lad := s.srv.ladder
	var ranks []int32
	lad.do(func() bool { ranks = slices.Clone(lad.Ranks); return false })
	list := []xym.Msg{}
	for i, uid := range ranks {
		if len(list) >= 10 {
			break
		}
		f := s.srv.ldFoe(uid, p)
		if f == nil || f.Grade > typ || f.Grade <= typ-10 && typ > 30 {
			continue
		}
		list = append(list, f.msg(i+1))
	}
	s.SendExt("LUNDAO_PTO.S_OPEN_TOP", xym.Msg{"type": typ, "list": list})
}

func onLundaoState(s *Session, p *Player, m xym.Msg) {
	group := int(m["group"].(int64))
	list := []xym.Msg{}
	s.srv.ladder.do(func() bool {
		for i := len(s.srv.ladder.States) - 1; i >= 0; i-- {
			x := s.srv.ladder.States[i]
			list = append(list, xym.Msg{"uid": x.UID, "time": x.Time, "attackername": x.Attacker, "targetname": x.Target, "pos": x.Pos})
		}
		return false
	})
	s.SendExt("LUNDAO_PTO.S_SEND_STATE", xym.Msg{"group": group, "list": list})
}

func (s *Session) ldItemList(p *Player) {
	l := p.lundao()
	list := []xym.Msg{}
	for i, it := range ldShop {
		if s.role.Grade < it.grade {
			continue
		}
		list = append(list, xym.Msg{"pos": i + 1, "cid": it.cid, "times": max(0, it.times-l.Bought[it.cid])})
	}
	s.SendExt("LUNDAO_PTO.S_SEND_ITEMLIST", xym.Msg{"score": l.Score, "list": list})
}

func onLundaoBuy(s *Session, p *Player, m xym.Msg) {
	cid, pos, n := int32(m["cid"].(int64)), int(m["pos"].(int64)), int(m["count"].(int64))
	l := p.lundao()
	if pos < 1 || pos > len(ldShop) || ldShop[pos-1].cid != cid || n < 1 {
		s.ldItemList(p)
		return
	}
	it := ldShop[pos-1]
	switch {
	case s.role.Grade < it.grade:
		s.sendMsg("等级不足。")
	case l.Bought[cid]+n > it.times:
		s.sendMsg("少侠，这个物品你购买够多了，刷新后才可以购买~")
	case l.Score < it.price*int32(n):
		s.sendMsg("积分不足，无法兑换")
	default:
		l.Score -= it.price * int32(n)
		l.Bought[cid] += n
		d := &bagDelta{}
		p.giveCode(it.code, it.n*int32(n), s.actRng(), d)
		p.saveLog(s)
		s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, s.sendDelta(p, d)))
	}
	s.SendExt("LUNDAO_PTO.S_SEND_SCORE", xym.Msg{"score": l.Score})
	s.ldItemList(p)
}
