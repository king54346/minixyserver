package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"strconv"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 时空裂隙（LIEXI_PTO，玩法栏「时空」→ view/TemporalRiftMainView.lua；协议见 internal/xym/proto.json，
// 客户端代码的解码版在 analysis/data/时空裂隙/client/）。
//
// 规则（客户端帮助 TemporalRift_Help）：全天开放，每周二 9:00 刷新；裂隙关卡打怪得奖励与「自选神通」，关卡里只能用
// 该关卡掉落的自选神通；时空竞技场用本周得到的全部自选神通与玩家对战。裂隙里护法等级不足 人物等级×2 的按 人物等级×2，
// 套装、宝石、精修无效，缘全部点亮，拥有的缘装备 / 缘法术自动装备（天匠有效）。每个关卡每周随机战地因素
// （battle/liexi.go）。
//
// 官方截图（analysis/data/时空裂隙）：固定 4 个副本——火焰山（普通）、小雷音寺（普通）、高老庄（困难）、狮驼岭（噩梦），
// 关卡数很多（「当前关卡:81」「可扫荡至：80关」），每个副本每周 2–4 个战地因素，竞技场 3–4 个，敌人显示 84 级。
// 本服估计（见 T9）：每个副本 100 关（5 的倍数精英、10 的倍数首领），敌人用对应精英关卡的阵容逐关加强（困难 ×1.3、
// 噩梦 ×1.7）；胜利 3 选 1 自选神通（普通 5 级、困难 8 级、噩梦 10 级）；每关碎片 20 / 40 / 60（首领翻倍，「奖」
// 再翻倍）；每天副本 4 次、竞技场 4 次；称谓按累计碎片 + 徽记。

const (
	lxGrade      = 30 // 开放等级（logic/activity.lua ACT_TEMPORAL_RIFT）
	lxGates      = 100
	lxFbDaily    = 4
	lxJjcDaily   = 4
	lxTalSlots   = 3 // 每个护法可装备的自选神通数（PetEasyDetailTemporalRiftMainView：pos 1–3）
	lxRewardOpts = 3

	lxStateClosed  = 0
	lxStateTeam    = 1
	lxStateChoose  = 2
	lxStateCleared = 3
)

// lxFbDef 是一个裂隙副本：名字、难度、敌人阵容取自哪一章的精英关卡、图标。
type lxFbDef struct {
	Name  string
	Level int // 0 普通 1 困难 2 噩梦
	Task  int // 关卡任务号（gates.json task）：敌人阵容取该任务各关的精英（jy_）阵型
	Icon  string
}

var lxLevelNames = []string{"普通", "困难", "噩梦"}

// 图标借用地图图标（map/icon/NN.png；小雷音寺是第 8 张图，其余没有对应的地图，取样子相近的）。
var lxFbDefs = []lxFbDef{
	// 图标是大地图上的建筑（官方截图对过）：火焰山 6-001、小雷音寺 8-015、高老庄 3-003、狮驼岭 10-010
	{"火焰山", 0, 1101, "map/06/001.png"}, {"小雷音寺", 0, 1111, "map/08/015.png"},
	{"高老庄", 1, 1201, "map/03/003.png"}, {"狮驼岭", 2, 1205, "map/10/010.png"},
}

// 难度系数：敌人属性倍率、胜利给的自选神通等级、每关碎片。
var lxLevelScale = []float64{1, 1.3, 1.7}
var lxLevelTal = []int{5, 8, 10}
var lxLevelPieces = []int{20, 40, 60}

// lxNow / lxWeekOf：一周从「周二 00:00」开始换周。
//
// 官方口径是「下一个周一24:00点时截止统计」（客户端 ui/HeFu_Help.lua），即周一 24:00 =
// 周二 0 点开新的一周，本服也是周二上午维护。早先按 lxEpoch（2026-10-20 09:00，周二上午）
// 滑动 7 天算周，等于每周要等到**周二 9 点**才换周，取经/裂隙的每周重置比官方晚 9 小时——
// 周一整晚和周二早上看着都像「没重置」。
var lxNow = time.Now

// lxWeekAnchor 是某个周二 00:00，只作为周序号的基准点（必须是周二，保证差值正好是 7 天的整数倍）。
var lxWeekAnchor = time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)

// lxWeekOf 返回 t 所在的周序号：按日历找最近的「周二 00:00」，再按 7 天步长计数。
func lxWeekOf(t time.Time) int {
	y, m, d := t.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	back := (int(t.Weekday()) - int(time.Tuesday) + 7) % 7 // 距本周二几天（周二当天为 0）
	start := day.AddDate(0, 0, -back)
	return int(start.Sub(lxWeekAnchor).Hours()/24/7) + 1000
}

// LiexiState 是玩家的时空裂隙存档。
type LiexiState struct {
	Week    int               `json:"week"`
	Day     string            `json:"day"`
	FbUsed  int               `json:"fb_used"`
	JjcUsed int               `json:"jjc_used"`
	Fbs     map[int]*lxFbRun  `json:"fbs"`
	Shop    []int32           `json:"shop,omitempty"` // 本周增益兑换来的自选神通（关卡、竞技场都能用）
	TJPlus  int               `json:"tj_plus,omitempty"`
	Bought  map[string]int    `json:"bought,omitempty"` // 本周兑换次数：「shop-idx」
	Jjc     *lxJjc            `json:"jjc,omitempty"`
	JTeam   map[int]int32     `json:"jteam,omitempty"`
	JArmed  map[int32][]int32 `json:"jarmed,omitempty"` // 竞技场：护法 -> 槽位 1–3 装的自选神通（0 = 空）
	Pieces  int               `json:"pieces,omitempty"` // 时空碎片（不随周清空）
	Crest   int               `json:"crest,omitempty"`  // 时空徽记
	Exp     int               `json:"exp,omitempty"`    // 称谓经验：累计得到的碎片 + 徽记
}

// lxFbRun 是本周某个副本的进度。
type lxFbRun struct {
	State int               `json:"state"`
	Next  int               `json:"next"`
	Best  int               `json:"best"`            // 本周打通过的最高关
	Bag   []int32           `json:"bag,omitempty"`   // 本副本掉落的自选神通
	Offer []int32           `json:"offer,omitempty"` // 胜利后待选的神通
	Team  map[int]int32     `json:"team,omitempty"`  // 站位 -> 护法类型
	Armed map[int32][]int32 `json:"armed,omitempty"` // 护法 -> 槽位 1–3 装的神通（0 = 空）
}

type lxJjc struct {
	State  int   `json:"state"`
	Foe    int32 `json:"foe,omitempty"`
	Streak int   `json:"streak,omitempty"`
	Got    []int `json:"got,omitempty"` // 本周已领的连胜奖励档（5 / 10 / 15）
}

func (p *Player) liexi() *LiexiState {
	if p.Liexi == nil {
		p.Liexi = &LiexiState{}
	}
	x := p.Liexi
	now := lxNow()
	if wk := lxWeekOf(now); x.Week != wk {
		x.Week, x.Fbs, x.Shop, x.TJPlus, x.Bought, x.Jjc, x.JArmed = wk, nil, nil, 0, nil, nil, nil
	}
	if d := now.Format("2006-01-02"); x.Day != d {
		x.Day, x.FbUsed, x.JjcUsed = d, 0, 0
	}
	if x.Fbs == nil {
		x.Fbs = map[int]*lxFbRun{}
	}
	if x.Bought == nil {
		x.Bought = map[string]int{}
	}
	if x.Jjc == nil {
		x.Jjc = &lxJjc{}
	}
	if x.JArmed == nil {
		x.JArmed = map[int32][]int32{}
	}
	return x
}

func (x *LiexiState) run(fb int) *lxFbRun {
	r := x.Fbs[fb]
	if r == nil {
		r = &lxFbRun{Next: 1}
		x.Fbs[fb] = r
	}
	if r.Armed == nil {
		r.Armed = map[int32][]int32{}
	}
	return r
}

// lxWeekRng 是本周固定的随机源（副本选择、战地因素、商店），全服一致。
func lxWeekRng(week, salt int) *rand.Rand {
	return rand.New(rand.NewPCG(uint64(week), uint64(salt)+0x51e7))
}

// lxWeekFbs 是开放的副本号（lxFbDefs 下标 + 1）：四个副本每周都开（官方截图）。
func lxWeekFbs(week int) []int {
	out := make([]int, len(lxFbDefs))
	for i := range out {
		out[i] = i + 1
	}
	return out
}

// lxFactors 是本周 fb（0 = 竞技场）的战地因素：副本 2–4 个，竞技场 3–4 个（竞技场不出「奖」）。
func lxFactors(week, fb int) []int {
	rng := lxWeekRng(week, 100+fb)
	pool := make([]int, 0, 20)
	for f := 1; f <= 20; f++ {
		if fb == 0 && f == battle.LxReward {
			continue
		}
		pool = append(pool, f)
	}
	n := 2 + rng.IntN(3)
	if fb == 0 {
		n = 3 + rng.IntN(2)
	}
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	out := slices.Clone(pool[:n])
	sort.Ints(out)
	return out
}

func idList(ids []int) []xym.Msg {
	out := []xym.Msg{}
	for _, id := range ids {
		out = append(out, xym.Msg{"id": id})
	}
	return out
}

func init() {
	Handle("LIEXI_PTO.C_OPEN_MAIN", func(s *Session, p *Player, m xym.Msg) { s.lxMain(p) })
	Handle("LIEXI_PTO.C_FB_BUY_TICKET", onLxFbBuy)
	Handle("LIEXI_PTO.C_FB_GET_LINEUP", func(s *Session, p *Player, m xym.Msg) { s.lxFbLineup(p, lxFbID(m)) })
	Handle("LIEXI_PTO.C_FB_UPDATE_TEAM", func(s *Session, p *Player, m xym.Msg) { s.lxUpdateTeam(p, lxFbID(m), m) })
	Handle("LIEXI_PTO.C_FB_QUERY_PET", func(s *Session, p *Player, m xym.Msg) { s.lxQueryPet(p, lxFbID(m), m) })
	Handle("LIEXI_PTO.C_FB_GET_TAL_LIST", func(s *Session, p *Player, m xym.Msg) { s.lxTalList(p, lxFbID(m), m) })
	Handle("LIEXI_PTO.C_FB_GET_TAL_BAG", func(s *Session, p *Player, m xym.Msg) { s.lxTalBag(p, lxFbID(m)) })
	Handle("LIEXI_PTO.C_FB_ARM_TAL", func(s *Session, p *Player, m xym.Msg) { s.lxArm(p, lxFbID(m), m, true) })
	Handle("LIEXI_PTO.C_FB_UNARM_TAL", func(s *Session, p *Player, m xym.Msg) { s.lxArm(p, lxFbID(m), m, false) })
	Handle("LIEXI_PTO.C_FB_FIGHT", onLxFbFight)
	Handle("LIEXI_PTO.C_FB_GET_TAL_REWARDS", func(s *Session, p *Player, m xym.Msg) { s.lxOffer(p, lxFbID(m)) })
	Handle("LIEXI_PTO.C_FB_CHOOSE_TAL_REWARD", onLxChoose)
	Handle("LIEXI_PTO.C_FB_SKIP_GATE", onLxSkip)
	Handle("LIEXI_PTO.C_FB_GET_REPLAY_LIST", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("LIEXI_PTO.S_FB_SEND_REPLAY_LIST", xym.Msg{"fb_id": m["fb_id"], "gate_id": m["gate_id"], "list": []xym.Msg{}})
	})
	Handle("LIEXI_PTO.C_FB_GET_RECORD", func(s *Session, p *Player, m xym.Msg) {}) // 录像榜没做（列表为空，点不到）
}

func lxFbID(m xym.Msg) int {
	v, _ := m["fb_id"].(int64)
	return int(v)
}

// lxMain：S_OPEN_MAIN。
func (s *Session) lxMain(p *Player) {
	x := p.liexi()
	week := x.Week
	fbs := []xym.Msg{}
	for _, fb := range lxWeekFbs(week) {
		d := lxFbDefs[fb-1]
		r := x.Fbs[fb]
		state, next := lxStateClosed, 1
		if r != nil {
			state, next = r.State, r.Next
		}
		name := d.Name + "(" + lxLevelNames[d.Level] + ")"
		fbs = append(fbs, xym.Msg{"fb_id": fb, "name": name, "boss_icon": d.Icon, "next_gate": next, "state": state,
			"factor_list": idList(lxFactors(week, fb))})
	}
	s.SendExt("LIEXI_PTO.S_OPEN_MAIN", xym.Msg{"jjc_state": x.Jjc.State, "jjc_left_times": max(lxJjcDaily-x.JjcUsed, 0),
		"jjc_boss_icon": "ui/activity/shujia_2023/icon_sjhd_xianzu.png", "jjc_factor_list": idList(lxFactors(week, 0)),
		"fb_left_times": max(lxFbDaily-x.FbUsed, 0), "has_second_view": 0, "fb_list": fbs})
}

func lxOpen(fb, week int) bool { return slices.Contains(lxWeekFbs(week), fb) }

// onLxFbBuy：开一次副本（扣当天次数），之后客户端拉布阵。
func onLxFbBuy(s *Session, p *Player, m xym.Msg) {
	x, fb := p.liexi(), lxFbID(m)
	switch {
	case s.role.Grade < lxGrade:
		s.retMsg(fmt.Sprintf("时空裂隙需要人物等级达到 %d 级。", lxGrade))
		return
	case !lxOpen(fb, x.Week):
		s.retMsg("这个裂隙本周没有开放。")
		return
	case x.FbUsed >= lxFbDaily:
		s.retMsg("今天的裂隙挑战次数已用完。")
		return
	}
	r := x.run(fb)
	if r.State == lxStateCleared {
		s.retMsg("本周已经通关这个裂隙了。")
		return
	}
	x.FbUsed++
	r.State, r.Next, r.Offer = lxStateTeam, 1, nil
	if r.Team == nil {
		r.Team = p.lxDefaultTeam()
	}
	p.saveLog(s)
	s.SendExt("LIEXI_PTO.S_FB_BUY_TICKET_SUC", xym.Msg{"fb_id": fb})
}

// lxDefaultTeam 是第一次进裂隙时的默认布阵：出战阵容的站位。
func (p *Player) lxDefaultTeam() map[int]int32 {
	t := map[int]int32{}
	cands := p.lxCandidates()
	for fp := 1; fp <= lineupSlots; fp++ {
		if pet := p.petAtFightPos(fp); pet != nil && cands[pet.Type] != nil {
			t[fp] = pet.Type
		}
	}
	return t
}

// lxCandidates 是可上阵的护法类型（同类型多张取等级最高的那张）：只有金卡（五星护法）能出战（用户确认）。
func (p *Player) lxCandidates() map[int32]*Pet {
	out := map[int32]*Pet{}
	for _, pet := range p.Pets {
		if pi := gamedata.Pets[pet.Type]; pi == nil || pi.Star != 5 {
			continue
		}
		if o := out[pet.Type]; o == nil || pet.Grade > o.Grade {
			out[pet.Type] = pet
		}
	}
	return out
}

func teamMsg(team map[int]int32) []xym.Msg {
	out := []xym.Msg{}
	for pos := 1; pos <= lineupSlots; pos++ {
		if t := team[pos]; t != 0 {
			out = append(out, xym.Msg{"type": t, "pos": pos})
		}
	}
	return out
}

func (p *Player) candidateMsg() []xym.Msg {
	cands := p.lxCandidates()
	types := make([]int32, 0, len(cands))
	for t := range cands {
		types = append(types, t)
	}
	slices.Sort(types)
	out := make([]xym.Msg, 0, len(types))
	for _, t := range types {
		out = append(out, xym.Msg{"type": t})
	}
	return out
}

// lxFbLineup：S_FB_SEND_LINEUP（当前关的敌人、我方布阵、候选护法）。
func (s *Session) lxFbLineup(p *Player, fb int) {
	x := p.liexi()
	r := x.run(fb)
	if r.Team == nil {
		r.Team = p.lxDefaultTeam()
	}
	d := lxFbDefs[max(fb-1, 0)%len(lxFbDefs)]
	gate := min(r.Next, lxGates)
	var target []xym.Msg
	for _, f := range lxEnemy(fb, gate, s.role.Grade) {
		tals := []xym.Msg{}
		for _, t := range f.Talents {
			tals = append(tals, xym.Msg{"tid": t})
		}
		mg := []xym.Msg{}
		if f.Magic != 0 {
			mg = append(mg, xym.Msg{"mid": f.Magic, "lev": f.MagicLv})
		}
		target = append(target, xym.Msg{"name": f.Name, "skillfull": 0, "hp": f.HP, "att": f.Att, "type": f.Icon,
			"grade": min(f.Grade, 127), "speed": f.Speed, "pos": f.Pos - 100, "talent": tals, "magic": mg})
	}
	if target == nil {
		target = []xym.Msg{}
	}
	s.SendExt("LIEXI_PTO.S_FB_SEND_LINEUP", xym.Msg{"pieces_count": x.Pieces, "factor_list": idList(lxFactors(x.Week, fb)),
		"fb_id": fb, "fb_name": d.Name, "next_gate": gate, "target_team": target, "my_team": teamMsg(r.Team),
		"candidate_pets": p.candidateMsg(), "can_skip_to": lxSkipTo(r)})
}

// lxSkipTo：本周打通过的关可以扫荡（跳到最高通关的下一关）。
func lxSkipTo(r *lxFbRun) int {
	return min(r.Best+1, lxGates)
}

// lxEnemy 是副本 fb 第 gate 关的敌人：取该章精英关卡的阵型逐关加强（按难度再乘系数），等级照人物等级。
func lxEnemy(fb, gate, grade int) []*battle.Fighter {
	if fb < 1 || fb > len(lxFbDefs) {
		return nil
	}
	d := lxFbDefs[fb-1]
	ids := gamedata.TaskGates[d.Task]
	if len(ids) == 0 {
		return nil
	}
	g := gamedata.Gates[ids[(gate-1)%len(ids)]]
	if g == nil {
		return nil
	}
	mode := g.Mode[1]
	scale := (1 + 0.03*float64(gate-1)) * lxLevelScale[d.Level]
	mode.Param = int(float64(mode.Param) * scale)
	team := battle.EnemyTeam(mode)
	for _, f := range team {
		f.Grade = max(f.Grade, grade*2)
	}
	return team
}

func onLxFbFight(s *Session, p *Player, m xym.Msg) {
	x, fb := p.liexi(), lxFbID(m)
	r := x.run(fb)
	gate, _ := m["gate_id"].(int64)
	if r.State != lxStateTeam || int(gate) != r.Next {
		s.retMsg("请先开启裂隙挑战。")
		s.lxMain(p)
		return
	}
	team1 := p.lxTeam(r.Team, r.Armed, x.Shop, r.Bag, x.TJPlus, lxFactors(x.Week, fb))
	team2 := lxEnemy(fb, r.Next, s.role.Grade)
	if len(team1) == 0 || len(team2) == 0 {
		s.retMsg("阵容为空，请先上阵护法。")
		return
	}
	lxApplyStatFactors(team2, lxFactors(x.Week, fb))
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulateWith(team1, team2, battle.Options{Factors: lxFactors(x.Week, fb)}, s.actRng())
	rid := "liexi:" + strconv.FormatInt(recordSeq.Add(1), 10)
	d := lxFbDefs[fb-1]
	s.fightRecord(rid, fmt.Sprintf("%s第%d关", d.Name, r.Next), s.role.Grade, r1, r2, res.Record)
	if !res.Win {
		r.State = lxStateClosed // 输了这次挑战结束，下次重新开（本周掉落的神通保留）
		p.saveLog(s)
		rw := rewardMsg(boxFightLose, nil)
		rw["record"] = rid
		s.SendExt("ETC_PTO.S_REWARD_MSG", rw)
		s.SendExt("LIEXI_PTO.S_FB_FIGHT_END", xym.Msg{"fb_id": fb, "state": r.State, "next_gate": r.Next, "fb_left_times": max(lxFbDaily-x.FbUsed, 0)})
		return
	}
	pieces := lxLevelPieces[d.Level]
	if r.Next%10 == 0 {
		pieces *= 2
	}
	if slices.Contains(lxFactors(x.Week, fb), battle.LxReward) {
		pieces *= 2
	}
	x.Pieces += pieces
	x.Exp += pieces
	r.Best = max(r.Best, r.Next)
	r.State, r.Offer = lxStateChoose, lxRollOffer(s.actRng(), lxLevelTal[d.Level])
	p.saveLog(s)
	rw := rewardMsg(2, nil)
	rw["record"] = rid
	s.SendExt("ETC_PTO.S_REWARD_MSG", rw)
	s.sendMsg(fmt.Sprintf("获得时空碎片 ×%d。", pieces))
	s.SendExt("LIEXI_PTO.S_FB_FIGHT_END", xym.Msg{"fb_id": fb, "state": r.State, "next_gate": r.Next, "fb_left_times": max(lxFbDaily-x.FbUsed, 0)})
}

// lxRollOffer：胜利后的 3 个自选神通（不重复的族），等级按难度。
func lxRollOffer(rng *rand.Rand, lv int) []int32 {
	groups := slices.Clone(gamedata.LiexiTals)
	rng.Shuffle(len(groups), func(i, j int) { groups[i], groups[j] = groups[j], groups[i] })
	var out []int32
	for _, g := range groups[:min(lxRewardOpts, len(groups))] {
		out = append(out, g.Members[min(lv, len(g.Members))-1])
	}
	return out
}

func (s *Session) lxOffer(p *Player, fb int) {
	x := p.liexi()
	r := x.run(fb)
	tals := []xym.Msg{}
	for _, t := range r.Offer {
		tals = append(tals, xym.Msg{"tal": t})
	}
	s.SendExt("LIEXI_PTO.S_FB_SEND_TAL_REWARDS", xym.Msg{"fb_id": fb, "cur_gate": r.Next, "factor_list": idList(lxFactors(x.Week, fb)), "tal_rewards": tals})
}

func onLxChoose(s *Session, p *Player, m xym.Msg) {
	x, fb := p.liexi(), lxFbID(m)
	r := x.run(fb)
	idx, _ := m["choosed"].(int64)
	if r.State == lxStateChoose {
		if idx >= 1 && int(idx) <= len(r.Offer) {
			r.Bag = append(r.Bag, r.Offer[idx-1])
		}
		r.Offer = nil
		if r.Next >= lxGates {
			r.State = lxStateCleared
		} else {
			r.State, r.Next = lxStateTeam, r.Next+1
		}
		p.saveLog(s)
	}
	s.SendExt("LIEXI_PTO.S_FB_CHOOSE_TAL_REWARD_END", xym.Msg{"fb_id": fb, "state": r.State, "next_gate": r.Next, "fb_left_times": max(lxFbDaily-x.FbUsed, 0)})
}

// onLxSkip：扫荡到本周最高通关的下一关，跳过的关照发碎片（不给神通）。
func onLxSkip(s *Session, p *Player, m xym.Msg) {
	x, fb := p.liexi(), lxFbID(m)
	r := x.run(fb)
	if r.State == lxStateTeam && lxSkipTo(r) > r.Next {
		d := lxFbDefs[fb-1]
		got := 0
		for g := r.Next; g < lxSkipTo(r); g++ {
			n := lxLevelPieces[d.Level]
			if g%10 == 0 {
				n *= 2
			}
			got += n
		}
		r.Next = lxSkipTo(r)
		x.Pieces += got
		x.Exp += got
		p.saveLog(s)
		s.sendMsg(fmt.Sprintf("扫荡完成，获得时空碎片 ×%d。", got))
	}
	s.lxFbLineup(p, fb)
}
