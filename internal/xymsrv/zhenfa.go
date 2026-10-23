package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"slices"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 阵法
// 战斗效果见 battle/zhenfa.go。
//
//   - 人物 40 级开放；功能开关 zhenfa 由服务端打开（switchOn）。
//   - 学习：消耗 100 本对应阵法书（道具 10432–10439 = 天覆、地载、风扬、云垂、龙飞、虎翼、鸟翔、蛇蟠，zhenfa.md），
//     学会后 C_USE_ZHENFA{type} 使用（type 0 = 不用）；还没用阵法时学会的自动使用。
//   - 阵魂培养（全阵法通用，至少学会一个阵法）：五个阵位各 1–10 级，每点经验消耗 1 张阵魂符篆（10440）+ 1 魂值；
//     lv → lv+1 的经验按当前品阶（表 enhance_cost[lv]）。C_ENHANCE_ZHENFA{pos, count} 给一个阵位；
//     C_QUICK_ENHANCE{count} 给全部阵位：每一点给本阶累计投入最少的阵位（相同取序号小的）。超出能升满的量拒绝。
//   - 升阶：五个阵位都满 10 级后 C_UPGRADE_ZHENFA，消耗 300 本阵魂图册（10442，zhenfa.md「一阶升二阶」，更高阶同样），
//     品阶 +1（最高 5），阵位回到 1 级。
//   - 重置本阶：C_RESET_ZHENFA 阵位回到 1 级，全额退回本阶培养用掉的阵魂符篆和魂值。
//   - 获取：阵法书在荡妖功德商店（随机阵法书，200 功德）、论道商店（龙飞 / 虎翼，1800 积分）；阵魂符篆关卡 / 荡妖
//     掉落；阵魂图册在道具商城。

const (
	zfGrade     = 40
	zfBookNeed  = 100
	zfBook0     = 10431 // 阵法书 = zfBook0 + 类型
	zfFuzhuan   = 10440 // 阵魂符篆
	zfAtlas     = 10442 // 阵魂图册
	zfAtlasNeed = 300
	zfFuCost    = 1 // 每点培养经验
	zfSoulCost  = 1
)

// ZhenfaState 是玩家的阵法存档（Player.Zhenfa）。
type ZhenfaState struct {
	Cur   int    `json:"cur,omitempty"`   // 使用中的阵法类型（0 = 不用）
	Stair int    `json:"stair,omitempty"` // 品阶 1–5
	Lv    [5]int `json:"lv"`              // 阵位等级 1–10
	Exp   [5]int `json:"exp"`             // 阵位当前经验
	Open  []int  `json:"open,omitempty"`  // 已学会的阵法
	Spent int64  `json:"spent,omitempty"` // 本阶培养用掉的经验点（重置时按它退回）
}

func (p *Player) zhenfa() *ZhenfaState {
	if p.Zhenfa == nil {
		p.Zhenfa = &ZhenfaState{}
	}
	if p.Zhenfa.Stair < 1 {
		p.Zhenfa.Stair = 1
	}
	for i := range p.Zhenfa.Lv {
		p.Zhenfa.Lv[i] = max(p.Zhenfa.Lv[i], 1)
	}
	return p.Zhenfa
}

// formation 是玩家出战用的阵法（没学 / 没用为空）。
func (p *Player) formation() battle.Formation {
	z := p.Zhenfa
	if z == nil || z.Cur == 0 || !slices.Contains(z.Open, z.Cur) {
		return battle.Formation{}
	}
	return battle.Formation{Type: z.Cur, Stair: max(z.Stair, 1), Lv: z.Lv}
}

// zfFightPoint 是阵法带来的战力（zhenfa_mgr.getZhenFaAdd：基础 + 每级 × 五阵位等级和）。
func (p *Player) zfFightPoint() int {
	f := p.formation()
	info := gamedata.ZhenfaOf(f.Type, f.Stair)
	if info == nil {
		return 0
	}
	sum := 0
	for _, l := range f.Lv {
		sum += l
	}
	return info.FightPoint[0] + info.FightPoint[1]*sum
}

// simulate 打一场带阵法的战斗：我方用 p 的阵法，敌方用 foe（玩家对手的阵法快照，打怪为空）。
// 阵法显示参数记在会话上，发 S_FIGHT_RECORD 时补进 otherparam（session.go SendExt）。
func (s *Session) simulate(p *Player, team1, team2 []*battle.Fighter, leaders [2]int32, foe battle.Formation, rng *rand.Rand) battle.Result {
	return s.simulateWith(team1, team2, battle.Options{Leaders: leaders, Formations: [2]battle.Formation{p.formation(), foe}}, rng)
}

func (s *Session) simulateWith(team1, team2 []*battle.Fighter, opts battle.Options, rng *rand.Rand) battle.Result {
	res := battle.SimulateWith(team1, team2, opts, rng)
	s.zfParam = res.ZfParam
	return res
}

// zfNeed 是阵位从 lv 级升到 lv+1 要的经验（满级 0）。
func zfNeed(stair, lv int) int {
	if lv >= gamedata.ZhenfaMaxLv {
		return 0
	}
	if info := gamedata.ZhenfaOf(1, stair); info != nil {
		return info.Enhance[lv]
	}
	return 0
}

// addExp 给阵位 i 加 n 点经验，返回实际用掉的（满级后不再收）。
func (z *ZhenfaState) addExp(i, n int) int {
	used := 0
	for n > 0 {
		need := zfNeed(z.Stair, z.Lv[i])
		if need == 0 {
			break
		}
		take := min(n, need-z.Exp[i])
		z.Exp[i] += take
		n -= take
		used += take
		if z.Exp[i] >= need {
			z.Lv[i]++
			z.Exp[i] = 0
		}
	}
	return used
}

// invested 是阵位 i 本阶累计投入的经验。
func (z *ZhenfaState) invested(i int) int {
	n := z.Exp[i]
	for lv := 1; lv < z.Lv[i]; lv++ {
		n += zfNeed(z.Stair, lv)
	}
	return n
}

// room 是阵位 i 到满级还差的经验。
func (z *ZhenfaState) room(i int) int {
	n := 0
	for lv := z.Lv[i]; lv < gamedata.ZhenfaMaxLv; lv++ {
		n += zfNeed(z.Stair, lv)
	}
	return n - z.Exp[i]
}

func (z *ZhenfaState) full() bool {
	for i := range z.Lv {
		if z.Lv[i] < gamedata.ZhenfaMaxLv {
			return false
		}
	}
	return true
}

// switchOn 是本服打开 / 改动的功能开关（覆盖官方抓包 static.json 里的 quest_switch）。
var switchOn = map[string]int{"zhenfa": 1}

func init() {
	Handle("ZHENFA_PTO.C_OPEN_ALL_ZHENFA", func(s *Session, p *Player, m xym.Msg) { s.zfOpen(p) })
	Handle("ZHENFA_PTO.C_LEARN_ZHENFA", onZfLearn)
	Handle("ZHENFA_PTO.C_USE_ZHENFA", onZfUse)
	Handle("ZHENFA_PTO.C_ENHANCE_ZHENFA", func(s *Session, p *Player, m xym.Msg) {
		s.zfEnhance(p, int(m["pos"].(int64)), int(m["count"].(int64)))
	})
	Handle("ZHENFA_PTO.C_QUICK_ENHANCE", func(s *Session, p *Player, m xym.Msg) {
		s.zfEnhance(p, 0, int(m["count"].(int64)))
	})
	Handle("ZHENFA_PTO.C_UPGRADE_ZHENFA", onZfUpgrade)
	Handle("ZHENFA_PTO.C_RESET_ZHENFA", onZfReset)
}

func (s *Session) zfOpen(p *Player) {
	z := p.zhenfa()
	pos := make([]xym.Msg, 0, 5)
	for i := range z.Lv {
		pos = append(pos, xym.Msg{"pos": i + 1, "lvl": z.Lv[i], "exp": z.Exp[i]})
	}
	open := make([]xym.Msg, 0, len(z.Open))
	for _, t := range z.Open {
		open = append(open, xym.Msg{"type": t})
	}
	s.SendExt("ZHENFA_PTO.S_OPEN_ALL_ZHENFA", xym.Msg{"cur_zhenfa": z.Cur, "big_lvl": z.Stair,
		"fuzhuan_cost": zfFuCost, "soul_cost": zfSoulCost, "pos_lvl": pos, "open_list": open})
}

// zfReady：等级够才能操作（不够时提示并回界面数据解锁）。
func (s *Session) zfReady(p *Player) bool {
	if s.role.Grade < zfGrade {
		s.retMsg(fmt.Sprintf("阵法于%d级开放。", zfGrade))
		s.zfOpen(p)
		return false
	}
	return true
}

func onZfLearn(s *Session, p *Player, m xym.Msg) {
	typ := int(m["type"].(int64))
	z := p.zhenfa()
	switch {
	case !s.zfReady(p):
		return
	case typ < 1 || typ > gamedata.ZhenfaTypes:
		s.retMsg("没有这个阵法。")
	case slices.Contains(z.Open, typ):
		s.retMsg("已经学会这个阵法了。")
	default:
		book := zfBook0 + typ
		ch, rm := p.takeItems(book, zfBookNeed)
		if ch == nil && rm == nil {
			name := "阵法书"
			if it := gamedata.Items[book]; it != nil {
				name = it.Name
			}
			s.retMsg(fmt.Sprintf("学习需要%s×%d（现有%d）。", name, zfBookNeed, p.itemCount(book)))
			break
		}
		s.sendDelta(p, &bagDelta{items: ch, removed: rm})
		z.Open = append(z.Open, typ)
		if z.Cur == 0 {
			z.Cur = typ
		}
		p.saveLog(s)
		if info := gamedata.ZhenfaOf(typ, 1); info != nil {
			s.retMsg(fmt.Sprintf("学会了%s。", string([]rune(info.Name)[:3])))
		}
	}
	s.zfOpen(p)
}

func onZfUse(s *Session, p *Player, m xym.Msg) {
	typ := int(m["type"].(int64))
	z := p.zhenfa()
	switch {
	case typ == 0:
		z.Cur = 0
		p.saveLog(s)
	case !slices.Contains(z.Open, typ):
		s.retMsg("还没有学会这个阵法。")
	default:
		z.Cur = typ
		p.saveLog(s)
	}
	s.zfOpen(p)
}

// zfEnhance：阵魂培养 count 点经验（pos 0 = 一键培养，分给全部阵位）。
func (s *Session) zfEnhance(p *Player, pos, count int) {
	if !s.zfReady(p) {
		return
	}
	z := p.zhenfa()
	if len(z.Open) == 0 {
		s.retMsg("请先学会一个阵法。")
		s.zfOpen(p)
		return
	}
	room := 0
	if pos == 0 {
		for i := range z.Lv {
			room += z.room(i)
		}
	} else if pos >= 1 && pos <= 5 {
		room = z.room(pos - 1)
	}
	n := count
	switch {
	case n <= 0 || room == 0 || n > room:
		s.retMsg("培养次数或阵位无效，或本阶已经满层。")
		s.zfOpen(p)
		return
	case int(p.itemCount(zfFuzhuan)) < n*zfFuCost || int(s.role.SoulExp) < n*zfSoulCost:
		s.retMsg("阵魂符篆或魂值不足。")
		s.zfOpen(p)
		return
	}
	if pos == 0 {
		for range n { // 每一点给本阶累计投入最少的阵位
			best := -1
			for i := range z.Lv {
				if z.Lv[i] < gamedata.ZhenfaMaxLv && (best < 0 || z.invested(i) < z.invested(best)) {
					best = i
				}
			}
			z.addExp(best, 1)
		}
	} else {
		z.addExp(pos-1, n)
	}
	ch, rm := p.takeItems(zfFuzhuan, int32(n*zfFuCost))
	s.roleUpdate(func(ro *Role) bool { ro.SoulExp -= int32(n * zfSoulCost); return true }, "soulexp")
	z.Spent += int64(n)
	p.saveLog(s)
	s.sendDelta(p, &bagDelta{items: ch, removed: rm})
	s.zfOpen(p)
}

func onZfUpgrade(s *Session, p *Player, m xym.Msg) {
	if !s.zfReady(p) {
		return
	}
	z := p.zhenfa()
	switch {
	case z.Stair >= gamedata.ZhenfaStairs:
		s.retMsg("阵法已经是最高品阶了。")
	case !z.full():
		s.retMsg("五个阵位都满10级后才能升阶。")
	default:
		ch, rm := p.takeItems(zfAtlas, zfAtlasNeed)
		if ch == nil && rm == nil {
			s.retMsg(fmt.Sprintf("升阶需要阵魂图册×%d（现有%d）。", zfAtlasNeed, p.itemCount(zfAtlas)))
			break
		}
		s.sendDelta(p, &bagDelta{items: ch, removed: rm})
		z.Stair++
		z.Lv, z.Exp, z.Spent = [5]int{1, 1, 1, 1, 1}, [5]int{}, 0
		p.saveLog(s)
		s.retMsg(fmt.Sprintf("阵法升到了%d阶。", z.Stair))
	}
	s.zfOpen(p)
}

func onZfReset(s *Session, p *Player, m xym.Msg) {
	z := p.zhenfa()
	if z.Spent == 0 {
		s.retMsg("本阶还没有培养过。")
		s.zfOpen(p)
		return
	}
	n := z.Spent
	z.Lv, z.Exp, z.Spent = [5]int{1, 1, 1, 1, 1}, [5]int{}, 0
	d := &bagDelta{items: p.addItem(zfFuzhuan, int32(n*zfFuCost))}
	s.roleUpdate(func(ro *Role) bool { ro.SoulExp += int32(n * zfSoulCost); return true }, "soulexp")
	p.saveLog(s)
	s.sendDelta(p, d)
	s.retMsg(fmt.Sprintf("已重置本阶，退回阵魂符篆×%d、魂值%d。", n*zfFuCost, n*zfSoulCost))
	s.zfOpen(p)
}
