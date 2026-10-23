package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 秘境（QUEST_PTO C_OPEN_MIJING_MAIN / C_OPEN_MIJING_DETAIL / C_START_QUEST_FIGHT）。
//
// 依据：ui/Mijing_Help 规则文字——六个副本按星期开放：金沙湾（周一、六）、乱魂冢（周二、日）、万刃山（周三）、
// 斗法坛（周四）、灵药谷（周五）、天启台（周六、日）；每种副本每日最多挑战 3 次；每种副本有多个难度，难度越高单次奖励越高；
// 每次挑战有几率触发特殊奖励。view/Mijing_DetailView.lua（难度号 num % 100 显示为「难度 N」、open_level 等级开启、
// energy 消耗体力、count_cur / count_max 次数、items_main / items_random 展示的物品、C_START_QUEST_FIGHT{qid = num}）。
// 抓包 S_OPEN_MIJING_MAIN：6 个副本的 title / icon / open 文案、今天开放的 status = 1、stars 3、展示奖励
// （万刃山 2001/2002 强化碎晶 / 晶体、斗法坛 11489/11490 悟法随想 / 心得、灵药谷 10060/10061 药材、
// 天启台 11488/11487 三星装备 / 法术）；count 在当天次数用完时是 0，按「剩余次数」发。
// 装备表 2001–2004 强化碎晶 / 晶体 / 精晶 / 晶魄的来源是「秘境」。
//
// 估计（没有详情和战斗的抓包）：每个副本 3 个难度，13 / 30 / 50 级开启，体力 6 / 10 / 14；
// 敌人取主线第 2 / 6 / 10 章首关的精英阵容；各难度奖励见 mijingReward（按档位 1 / 3 / 5 计）；特殊奖励 10% 概率、奖励翻倍。

const (
	mijingTimes   = 3
	mijingDiffs   = 3
	mijingSpecial = 10 // 特殊奖励概率 %
)

type mijingInfo struct {
	Title, Icon, Open, Desc string
	Days                    []time.Weekday
	Awards                  []int // S_OPEN_MIJING_MAIN.awards（物品 / 装备号）
}

var mijings = []mijingInfo{
	{"金沙湾", "img_mijing_lihui1.png", "(周一、六)", "富饶的金色沙滩，据说这里的沙子都是黄金的，这里的怪物收集了无数的黄金。",
		[]time.Weekday{time.Monday, time.Saturday}, nil},
	{"乱魂冢", "img_mijing_lihui2.png", "(周二、日)", "游魂聚集之地，击败守墓的妖魔可以获得大量魂值与护法魂魄。",
		[]time.Weekday{time.Tuesday, time.Sunday}, nil},
	{"万刃山", "img_mijing_lihui3.png", "(周三)", "神匠遗留的铸剑之地，遍地都是强化装备的晶石。",
		[]time.Weekday{time.Wednesday}, []int{2001, 2002}},
	{"斗法坛", "img_mijing_lihui4.png", "(周四)", "各路仙家斗法论道之所，在此可以领悟法术的心得。",
		[]time.Weekday{time.Thursday}, []int{11489, 11490}},
	{"灵药谷", "img_mijing_lihui5.png", "(周五)", "仙草遍地的山谷，可以采集炼丹用的药材。",
		[]time.Weekday{time.Friday}, []int{10060, 10061}},
	{"天启台", "img_mijing_lihui6.png", "(周六、日)", "上古神祇降下天启之地，有机会获得三星装备与法术。",
		[]time.Weekday{time.Saturday, time.Sunday}, []int{11488, 11487}},
}

var (
	mijingLevel  = [mijingDiffs]int{13, 30, 50}
	mijingEnergy = [mijingDiffs]int{6, 10, 14}
	mijingGate   = [mijingDiffs]int{20101, 60101, 100101} // 敌人用这一关（或其后第一关）的精英阵容
	mijingTier   = [mijingDiffs]int{1, 3, 5}              // 奖励档位（mijingReward 的 d）
)

// MijingState 是秘境存档（Player.Mijing）。
type MijingState struct {
	Day    string      `json:"day"`
	Used   map[int]int `json:"used,omitempty"`   // 副本 -> 今日已挑战次数
	Select map[int]int `json:"select,omitempty"` // 副本 -> 上次打的难度号
}

func (p *Player) mijing() *MijingState {
	if p.Mijing == nil {
		p.Mijing = &MijingState{}
	}
	if d := today(); p.Mijing.Day != d {
		p.Mijing.Day, p.Mijing.Used = d, map[int]int{}
	}
	if p.Mijing.Used == nil {
		p.Mijing.Used = map[int]int{}
	}
	if p.Mijing.Select == nil {
		p.Mijing.Select = map[int]int{}
	}
	return p.Mijing
}

// mijingOpen：副本 id（1 起）今天是否开放。
func mijingOpen(id int, now time.Time) bool {
	if id < 1 || id > len(mijings) {
		return false
	}
	for _, d := range mijings[id-1].Days {
		if d == now.Weekday() {
			return true
		}
	}
	return false
}

// mijingItem 是一种奖励（展示用的类型号 + 发放）。
type mijingItem struct {
	Type  int    // 物品 / 装备号（展示用；银两 20010、魂值 20012）
	Desc  string // 数量说明
	Cash  int32
	Soul  int32
	Code  string // 掉落编码（giveCode）
	Count int32
	Rate  int // 掉落概率 %（100 = 必得）
}

// mijingReward 是副本 id 难度 diff（1 起）一次胜利的奖励：main 必得，random 按概率。
func mijingReward(id, diff int) (main, random []mijingItem) {
	d := mijingTier[diff-1]
	switch id {
	case 1: // 金沙湾：银两
		main = []mijingItem{{Type: 20010, Desc: fmt.Sprintf("银两×%d", 30000*d), Cash: int32(30000 * d), Rate: 100}}
		random = []mijingItem{{Type: 20010, Desc: fmt.Sprintf("银两×%d", 20000*d), Cash: int32(20000 * d), Rate: 30}}
	case 2: // 乱魂冢：魂值 + 魂魄
		main = []mijingItem{{Type: 20012, Desc: fmt.Sprintf("魂值×%d", 100*d), Soul: int32(100 * d), Rate: 100}}
		random = []mijingItem{{Type: 20012, Desc: fmt.Sprintf("%d星护法魂魄", min(d, 5)), Code: "GPHP" + strconv.Itoa(min(d, 5)), Count: int32(1 + d/2), Rate: 50}}
	case 3: // 万刃山：强化晶
		star := 1 + (d-1)*3/4
		main = []mijingItem{{Type: 2000 + star, Desc: fmt.Sprintf("×%d", 1+d/2), Code: "ZB" + strconv.Itoa(2000+star), Count: int32(1 + d/2), Rate: 100}}
		if star < 4 {
			random = []mijingItem{{Type: 2001 + star, Desc: "×1", Code: "ZB" + strconv.Itoa(2001+star), Count: 1, Rate: 30}}
		}
	case 4: // 斗法坛：悟法
		main = []mijingItem{{Type: 11489, Desc: fmt.Sprintf("×%d", d), Code: "WP11489", Count: int32(d), Rate: 100}}
		random = []mijingItem{{Type: 11490, Desc: fmt.Sprintf("×%d", max(1, d/2)), Code: "WP11490", Count: int32(max(1, d/2)), Rate: 40}}
	case 5: // 灵药谷：药材
		lv := min(d, 5)
		main = []mijingItem{{Type: 10059 + lv, Desc: "×3", Code: "WP" + strconv.Itoa(10059+lv), Count: 3, Rate: 100}}
		if lv < 5 {
			random = []mijingItem{{Type: 10060 + lv, Desc: "×1", Code: "WP" + strconv.Itoa(10060+lv), Count: 1, Rate: 30}}
		}
	case 6: // 天启台：三星装备 / 法术
		main = []mijingItem{{Type: 11488, Desc: "×1", Code: "WP11488", Count: 1, Rate: 100}}
		random = []mijingItem{{Type: 11487, Desc: "×1", Code: "WP11487", Count: 1, Rate: 10 + 10*d}}
	}
	return main, random
}

func mijingItemsMsg(l []mijingItem) []xym.Msg {
	out := []xym.Msg{}
	for _, it := range l {
		if it.Rate > 0 {
			out = append(out, xym.Msg{"type": it.Type, "desc": it.Desc})
		}
	}
	return out
}

// mijingEnemy 是难度 d 的敌人。
func mijingEnemy(d int) []*battle.Fighter {
	want := mijingGate[d-1]
	gids := make([]int, 0, len(gamedata.Gates))
	for gid := range gamedata.Gates {
		gids = append(gids, gid)
	}
	sort.Ints(gids)
	for _, gid := range gids {
		if gid >= want {
			return battle.EnemyTeam(gamedata.Gates[gid].Mode[1])
		}
	}
	if len(gids) > 0 {
		return battle.EnemyTeam(gamedata.Gates[gids[len(gids)-1]].Mode[1])
	}
	return nil
}

// mijingNow 是当前时间（测试固定星期几）。
var mijingNow = time.Now

// mijingEnemyFor 是取敌人的函数（测试换成极弱的）。
var mijingEnemyFor = mijingEnemy

func init() {
	Handle("QUEST_PTO.C_OPEN_MIJING_MAIN", func(s *Session, p *Player, m xym.Msg) { s.mijingMain(p) })
	Handle("QUEST_PTO.C_OPEN_MIJING_DETAIL", func(s *Session, p *Player, m xym.Msg) { s.mijingDetail(p, int(m["id"].(int64))) })
	Handle("QUEST_PTO.C_START_QUEST_FIGHT", onMijingFight)
}

func (s *Session) mijingMain(p *Player) {
	st := p.mijing()
	now := mijingNow()
	levels := []xym.Msg{}
	for i, mj := range mijings {
		id := i + 1
		open := mijingOpen(id, now)
		left := 0
		if open {
			left = mijingTimes - st.Used[id]
		}
		awards := []xym.Msg{}
		for _, a := range mj.Awards {
			awards = append(awards, xym.Msg{"id": a})
		}
		levels = append(levels, xym.Msg{"id": id, "title": mj.Title, "icon": mj.Icon, "open": mj.Open, "count": left,
			"status": b2i(open), "stars": 3, "activity_tag": "", "awards": awards})
	}
	s.SendExt("QUEST_PTO.S_OPEN_MIJING_MAIN", xym.Msg{"levels": levels})
}

func (s *Session) mijingDetail(p *Player, id int) {
	if id < 1 || id > len(mijings) {
		s.mijingMain(p)
		return
	}
	st := p.mijing()
	mj := mijings[id-1]
	best := 0
	diffs := []xym.Msg{}
	for d := 1; d <= mijingDiffs; d++ {
		if s.role.Grade >= mijingLevel[d-1] {
			best = d
		}
	}
	for d := 1; d <= mijingDiffs; d++ {
		main, random := mijingReward(id, d)
		diffs = append(diffs, xym.Msg{"num": id*100 + d, "open_level": mijingLevel[d-1], "recommend": b2i(d == best),
			"reward_main": 0, "reward_random": 0, "tips_title": "", "tips_content": "", "energy": mijingEnergy[d-1],
			"status": b2i(s.role.Grade >= mijingLevel[d-1]), "items_main": mijingItemsMsg(main),
			"items_random": mijingItemsMsg(random)})
	}
	sel := st.Select[id]
	if sel%100 > mijingDiffs { // 旧存档的 4 / 5 档
		sel = 0
	}
	s.SendExt("QUEST_PTO.S_OPEN_MIJING_DETAIL", xym.Msg{"id": id, "title": mj.Title, "desc": mj.Desc,
		"select": sel, "difficulty": diffs, "count_cur": st.Used[id], "count_max": mijingTimes})
}

// onMijingFight：挑战秘境 qid = 副本 × 100 + 难度。赢了扣体力、记次数、发奖励（S_ATTR_UPDATE、背包、S_REWARD_MSG{type 2}）。
// 客户端等的是 S_FIGHT_RECORD 的锁，拒绝时用 S_MSG 提示（与普通关卡一致）。
func onMijingFight(s *Session, p *Player, m xym.Msg) {
	qid := int(m["qid"].(int64))
	id, d := qid/100, qid%100
	st := p.mijing()
	switch {
	case !mijingOpen(id, mijingNow()) || d < 1 || d > mijingDiffs:
		s.sendMsg("这个秘境今天没有开放。")
		return
	case s.role.Grade < mijingLevel[d-1]:
		s.sendMsg(fmt.Sprintf("%d级开启。", mijingLevel[d-1]))
		return
	case st.Used[id] >= mijingTimes:
		s.sendMsg("今日挑战次数已用完。")
		return
	case s.role.TPoint < mijingEnergy[d-1]:
		s.retMsg("体力不足。")
		s.SendExt("ETC_PTO.S_OPEN_POINT_BUY", p.pointBuyMsg(pointTypeT))
		return
	}
	team1, _ := p.dyTeam(nil)
	team2 := mijingEnemyFor(d)
	if len(team1) == 0 || len(team2) == 0 {
		s.sendMsg("阵容为空，请先上阵。")
		return
	}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	rng := s.actRng()
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), 0}, battle.Formation{}, rng)
	rid := strconv.FormatInt(recordSeq.Add(1), 10)
	s.fightRecord(rid, mijings[id-1].Title, mijingLevel[d-1], r1, r2, res.Record)
	if !res.Win {
		s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(3, nil))
		return
	}
	p.track(evMijing, 1)
	st.Used[id]++
	st.Select[id] = qid
	s.mijingWin(p, id, d, rng, rid)
}

func (s *Session) mijingWin(p *Player, id, d int, rng *rand.Rand, rid string) {
	times := int32(1)
	if rng.IntN(100) < mijingSpecial {
		times = 2
	}
	main, random := mijingReward(id, d)
	delta := &bagDelta{}
	var cash, soul int32
	for _, it := range append(main, random...) {
		if it.Rate <= 0 || rng.IntN(100) >= it.Rate {
			continue
		}
		cash += it.Cash * times
		soul += it.Soul * times
		if it.Code != "" {
			p.giveCode(it.Code, it.Count*times, rng, delta)
		}
	}
	p.saveLog(s)
	energy := mijingEnergy[d-1]
	s.roleUpdate(func(ro *Role) bool {
		ro.TPoint -= energy
		ro.Cash += cash
		ro.SoulExp += soul
		return true
	}, "tpoint", "cash", "soulexp")
	list := s.sendDelta(p, delta)
	r := rewardMsg(2, list)
	r["cash"], r["soul"], r["record"], r["star"], r["no_fight_again"] = cash, soul, rid, 1, 0
	if times > 1 {
		s.retMsg("触发了特殊奖励，本次奖励翻倍！")
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
}
