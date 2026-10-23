package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 荡妖 / 功德商店协议（DANGYAO_PTO）。规则与依据见 dangyao.go；回包顺序照官方抓包 session-01。

func init() {
	Handle("DANGYAO_PTO.C_OPEN_MAP", onDyOpenMap)
	Handle("DANGYAO_PTO.C_REFRESH_MAP", onDyRefresh)
	Handle("DANGYAO_PTO.C_OPEN_GRID", onDyOpenGrid)
	Handle("DANGYAO_PTO.C_CLICK_GRID", onDyClickGrid)
	Handle("DANGYAO_PTO.C_ONE_KEY_DANGYAO", onDyOneKey)
	Handle("DANGYAO_PTO.C_RELIVE_ALL", onDyReliveAll)
	Handle("DANGYAO_PTO.C_RELIVE_POS", onDyRelivePos)
	Handle("DANGYAO_PTO.C_LIST_DIE", func(s *Session, p *Player, m xym.Msg) { s.dyDieList(p) })
	Handle("DANGYAO_PTO.C_END_MAP", onDyEscape)
	Handle("DANGYAO_PTO.C_FAIL_MAP", onDyGiveUp)
	Handle("DANGYAO_PTO.C_APPLY_GONGDE", func(s *Session, p *Player, m xym.Msg) { s.dyGongde(p) })
	Handle("DANGYAO_PTO.C_QUERY_BAODI_RATIO", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("DANGYAO_PTO.S_SEND_BAODI_RATIO", xym.Msg{"base_ratio": p.dy().Ratio})
	})
	Handle("DANGYAO_PTO.C_OPEN_SHOP", func(s *Session, p *Player, m xym.Msg) { s.dyShop(p, int(m["type"].(int64))) })
	Handle("DANGYAO_PTO.C_BUY_ITEM", onDyBuy)
}

func (s *Session) dyRng() *rand.Rand {
	return rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(s.role.UID)))
}

func (s *Session) dyGongde(p *Player) {
	d := p.dy()
	s.SendExt("DANGYAO_PTO.S_SEND_GONGDE", xym.Msg{"gongde": d.Gongde, "level": d.gongdeLevel()})
}

func (s *Session) dyDieList(p *Player) {
	d := p.dy()
	s.SendExt("DANGYAO_PTO.S_DIE_LIST", xym.Msg{"free": d.Free, "list": posList(d.Dead)})
}

func (s *Session) dyReward(p *Player) {
	ling := 0
	if r := p.dy().Run; r != nil {
		ling = r.Ling
	}
	s.SendExt("DANGYAO_PTO.S_SEND_REWARD", xym.Msg{"cash": 0, "ling": ling, "pet": 0, "soul": 0, "item": 0, "magic": 0})
}

func posList(l []int) []xym.Msg {
	out := []xym.Msg{}
	for _, x := range l {
		out = append(out, xym.Msg{"pos": x})
	}
	return out
}

// retOK 是空的 S_RET_MSG：官方在走楼梯、复活后都回一条，用来解开客户端等待锁。
func (s *Session) retOK() { s.SendExt("ETC_PTO.S_RET_MSG", xym.Msg{"type": 0, "msg": ""}) }

func (s *Session) retMsg(text string) {
	s.SendExt("ETC_PTO.S_RET_MSG", xym.Msg{"type": 0, "msg": text})
}

// dySpend 扣人物的元宝 / 体力，成功后回 S_ATTR_UPDATE（不够时只返回 false，提示由调用方发）。
func (s *Session) dySpend(gold, tpoint int) bool {
	if gold == 0 && tpoint == 0 {
		return true
	}
	ok := false
	role, err := s.srv.roles.Update(s.role.UID, func(ro *Role) bool { // 在存档上判余额（会话缓存可能是旧的）
		if ro.Gold < int32(gold) || ro.TPoint < tpoint {
			return false
		}
		ro.Gold -= int32(gold)
		ro.TPoint -= tpoint
		ok = true
		return true
	})
	if err != nil {
		s.logErr("save role", err)
		return false
	}
	if role != nil {
		s.role = role
	}
	if !ok {
		return false
	}
	var attr []xym.Msg
	if gold > 0 {
		attr = append(attr, xym.Msg{"name": "gold", "val": role.Gold})
	}
	if tpoint > 0 {
		attr = append(attr, xym.Msg{"name": "tpoint", "val": role.TPoint})
	}
	s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": attr})
	return true
}

// onDyOpenMap：有进行中的一轮就打开它的当前层（不论点的是哪张图，抓包如此），否则 S_OPEN_FAIL 让玩家重置。
func onDyOpenMap(s *Session, p *Player, m xym.Msg) {
	d := p.dy()
	if d.Run == nil {
		stage := int(m["mapid"].(int64)) / 100
		s.SendExt("DANGYAO_PTO.S_OPEN_FAIL", xym.Msg{"stage": stage, "ref": 0})
		return
	}
	s.SendExt("DANGYAO_PTO.S_OPEN_DANGYAO", xym.Msg{"mapid": dyMapID(d.Run.Stage, d.Run.Floor), "list": openList(d.floor())})
	s.dyDieList(p)
	s.dyReward(p)
}

// onDyRefresh：重置妖窟开始新一轮（特殊妖窟扣 50 元宝），功德截到称号上限，妖气 +25，商店重新上架。
func onDyRefresh(s *Session, p *Player, m xym.Msg) {
	stage := int(m["stage"].(int64))
	if !p.dyStageOpen(stage) {
		s.retMsg("这个妖窟还没有开放。")
		return
	}
	cost := 0
	if stage > 100 {
		cost = dySpecialCost
	}
	if !s.dySpend(cost, 0) {
		s.retMsg(fmt.Sprintf("元宝不足：重置特殊妖窟需要 %d 元宝。", cost))
		return
	}
	d := p.dy()
	rng := s.dyRng()
	d.Gongde = min(d.Gongde, d.gongdeMax())
	d.Ratio = min(d.Ratio+dyRatioStep, dyRatioMax)
	boss := d.Ratio >= dyRatioMax
	d.Run = &dyRun{Stage: stage, Floor: 1, Floors: map[int]*dyFloor{1: newFloor(1, dyFloors(stage), boss, rng)}}
	hadDead := len(d.Dead) > 0
	d.Dead = nil // 新一轮护法全部复原（之前沿用上一轮的阵亡）
	dyRollShop(d, stage > 100, rng)
	p.saveLog(s)
	s.SendExt("DANGYAO_PTO.S_OPEN_DANGYAO", xym.Msg{"mapid": dyMapID(stage, 1), "list": openList(d.floor())})
	s.dyGongde(p)
	s.SendExt("DANGYAO_PTO.S_SEND_BAODI_RATIO", xym.Msg{"base_ratio": d.Ratio})
	if hadDead { // 让客户端清掉上一轮的阵亡标记
		s.dyDieList(p)
	}
}

// dyAt 取当前一轮当前层的格子（mapid 不是当前层或 grid 越界返回 nil）。
func (p *Player) dyAt(m xym.Msg) (*DangyaoState, *dyGrid, int) {
	d := p.dy()
	if d.Run == nil || int(m["mapid"].(int64)) != dyMapID(d.Run.Stage, d.Run.Floor) {
		return d, nil, 0
	}
	g := int(m["grid"].(int64))
	if g < 0 || g >= dyGrids {
		return d, nil, 0
	}
	return d, &d.floor().Grids[g], g
}

// onDyOpenGrid：翻开一格（扣 1 体力）。
func onDyOpenGrid(s *Session, p *Player, m xym.Msg) {
	d, g, i := p.dyAt(m)
	if g == nil {
		s.retMsg("妖窟已变化，请重新进入。")
		return
	}
	if !g.Open {
		if !s.dySpend(0, 1) {
			s.retMsg("体力不足。")
			return
		}
		g.Open = true
		p.saveLog(s)
	}
	s.SendExt("DANGYAO_PTO.S_OPEN_GRID", xym.Msg{"mapid": dyMapID(d.Run.Stage, d.Run.Floor), "grid": i,
		"type": g.Type, "times": gridMsg(i, *g)["times"], "max": gridMsg(i, *g)["max"]})
}

// onDyClickGrid：点已翻开的格子——怪物战斗、宝箱拿功、楼梯上下层。客户端在等 S_FIGHT_RECORD 锁，
// 不打架的格子回空 S_RET_MSG 解锁（抓包：走楼梯是 S_OPEN_DANGYAO + S_RET_MSG）。
func onDyClickGrid(s *Session, p *Player, m xym.Msg) {
	d, g, i := p.dyAt(m)
	if g == nil || !g.Open {
		s.retMsg("妖窟已变化，请重新进入。")
		return
	}
	r := d.Run
	switch g.Type {
	case dyMonster, dyLeader, dyBoss:
		s.dyFight(p, g, i)
		return
	case dyBox:
		r.Ling += dyLing[dyBox]
		g.Type = dyBlank
		p.saveLog(s)
		s.SendExt("DANGYAO_PTO.S_OPEN_GRID", xym.Msg{"mapid": dyMapID(r.Stage, r.Floor), "grid": i, "type": dyBlank,
			"times": 0, "max": 0})
		s.dyReward(p)
	case dyDown, dyUp:
		if g.Type == dyDown {
			r.Floor++
		} else if r.Floor > 1 {
			r.Floor--
		}
		if r.Floors[r.Floor] == nil {
			r.Floors[r.Floor] = newFloor(r.Floor, dyFloors(r.Stage), d.Ratio >= dyRatioMax, s.dyRng())
		}
		p.saveLog(s)
		s.SendExt("DANGYAO_PTO.S_OPEN_DANGYAO", xym.Msg{"mapid": dyMapID(r.Stage, r.Floor), "list": openList(d.floor())})
	}
	s.retOK()
}

// dyFight 打格子 i 的怪（g 为其格子）。
func (s *Session) dyFight(p *Player, g *dyGrid, i int) {
	d := p.dy()
	r := d.Run
	team1, _ := p.dyTeam(d.Dead)
	if len(team1) == 0 {
		s.retMsg("出战护法全部阵亡，请先复活。")
		return
	}
	rng := s.dyRng()
	team2 := dyEnemy(r.Stage, r.Floor, g.Type, rng)
	if len(team2) == 0 {
		s.retMsg("妖窟配置缺失。")
		return
	}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), 0}, battle.Formation{}, rng)
	alive, err := battle.Survivors(r1, r2, res.Record)
	if err != nil {
		s.logErr("dangyao replay", err)
	}
	newDead := false
	for _, f := range r1 {
		if !alive[f.Pos] {
			d.Dead = append(d.Dead, f.Pos)
			newDead = true
		}
	}
	d.Fights++
	p.track(evDangyao, 1)
	rid := fmt.Sprintf("dangyao-%d-%d", s.role.UID, d.Fights)
	s.SendExt("FIGHT_PTO.S_FIGHT_RECORD", xym.Msg{
		"canop": 14, "rid": rid,
		"w1": s.prestige(), "level1": s.role.Grade, "name1": s.role.Name, "team1": teamMsgs(r1),
		"level2": 1, "name2": "妖窟", "team2": teamMsgs(r2),
		"otherparam": recordParams(r1, r2), "record": res.Record,
	})
	ling := 0
	if res.Win {
		ling = dyLing[g.Type]
		r.Ling += ling
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"cash": 0, "mexp": 0, "pexp": 0, "ling": ling, "gold": 0, "soul": 0,
		"type": map[bool]int{true: boxFightWin, false: boxFightLose}[res.Win], "item": 0, "no_fight_again": 1, "star": map[bool]int{true: 1}[res.Win], "list": []xym.Msg{}})
	if !res.Win {
		p.saveLog(s)
		s.dyDieList(p)
		if left, _ := p.dyTeam(d.Dead); len(left) == 0 { // 全军覆没：结算界面（可复活再战 / 携宝潜逃 / 打道回府）
			s.SendExt("DANGYAO_PTO.S_DANGYAO_END", xym.Msg{"state": dyStateFail, "stage": r.Stage, "free": d.Free,
				"list": posList(d.Dead)})
		}
		return
	}
	last := r.Floor == dyFloors(r.Stage) && (g.Type == dyLeader || g.Type == dyBoss)
	if g.Type == dyBoss {
		d.Ratio = 0
	}
	g.Type = dyBlank
	s.SendExt("DANGYAO_PTO.S_OPEN_GRID", xym.Msg{"mapid": dyMapID(r.Stage, r.Floor), "grid": i, "type": dyBlank,
		"times": 0, "max": 0})
	s.dyReward(p)
	if newDead { // 赢了也可能有人阵亡：之前只在输时发 S_DIE_LIST，复活界面就看不到这些阵亡
		s.dyDieList(p)
	}
	if last {
		s.dyFinish(p)
		return
	}
	p.saveLog(s)
}

// dyFinish 带着本轮的功结算：功德 += 功（官方抓包不截上限），一轮结束。
func (s *Session) dyFinish(p *Player) {
	d := p.dy()
	r := d.Run
	d.Gongde += r.Ling
	d.Run = nil
	p.trackMax(dyMapKey(r.Stage), 1) // 主线「完成N图普通 / 精英妖窟」
	p.saveLog(s)
	s.SendExt("DANGYAO_PTO.S_DANGYAO_END", xym.Msg{"state": dyStateSuccess, "stage": r.Stage, "free": d.Free,
		"list": posList(d.Dead)})
	s.dyGongde(p)
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"cash": 0, "mexp": 0, "pexp": 0, "ling": r.Ling, "gold": 0, "soul": 0,
		"type": 4, "item": 0, "no_fight_again": 1, "star": 0, "list": []xym.Msg{}})
}

// onDyOneKey：一键荡妖——扣「未翻开格数」点体力，翻开全层，小怪和宝箱直接清掉拿功。
func onDyOneKey(s *Session, p *Player, m xym.Msg) {
	d := p.dy()
	if d.Run == nil || int(m["mapid"].(int64)) != dyMapID(d.Run.Stage, d.Run.Floor) {
		s.retMsg("妖窟已变化，请重新进入。")
		return
	}
	f := d.floor()
	need := 0
	for _, g := range f.Grids {
		if !g.Open {
			need++
		}
	}
	if !s.dySpend(0, need) {
		s.retMsg(fmt.Sprintf("体力不足：一键荡妖需要 %d 点体力。", need))
		return
	}
	list := []xym.Msg{}
	for i := range f.Grids {
		g := &f.Grids[i]
		g.Open = true
		if g.Type == dyMonster || g.Type == dyBox {
			d.Run.Ling += dyLing[g.Type]
			g.Type = dyBlank
		}
		list = append(list, xym.Msg{"grid": i, "type": g.Type})
	}
	p.saveLog(s)
	s.SendExt("DANGYAO_PTO.S_SEND_ONE_KEY_DANGYAO", xym.Msg{"mapid": dyMapID(d.Run.Stage, d.Run.Floor), "list": list})
	s.dyReward(p)
}

// reliveCost：复活 n 名阵亡护法先用免费次数，其余每名 2 元宝。
func (d *DangyaoState) reliveCost(n int) (free, gold int) {
	free = min(n, d.Free)
	return free, (n - free) * dyReliveGold
}

func onDyReliveAll(s *Session, p *Player, m xym.Msg) {
	d := p.dy()
	free, gold := d.reliveCost(len(d.Dead))
	if !s.dySpend(gold, 0) {
		s.retMsg(fmt.Sprintf("元宝不足：全员复活需要 %d 元宝。", gold))
		return
	}
	d.Free -= free
	d.Dead = nil
	p.saveLog(s)
	s.SendExt("DANGYAO_PTO.S_RELIVE_SUC", xym.Msg{"free": d.Free, "list": []xym.Msg{}})
	s.retOK()
}

func onDyRelivePos(s *Session, p *Player, m xym.Msg) {
	d := p.dy()
	pos := int(m["pos"].(int64))
	idx := -1
	for i, x := range d.Dead {
		if x == pos {
			idx = i
		}
	}
	if idx >= 0 {
		free, gold := d.reliveCost(1)
		if !s.dySpend(gold, 0) {
			s.retMsg(fmt.Sprintf("元宝不足：复活需要 %d 元宝。", gold))
			s.dyDieList(p) // 解锁
			return
		}
		d.Free -= free
		d.Dead = append(d.Dead[:idx], d.Dead[idx+1:]...)
		p.saveLog(s)
	}
	s.dyDieList(p)
}

// onDyEscape：携宝潜逃（遁地）——用 1 张遁地符或 50 元宝，带着本轮的功结算。
func onDyEscape(s *Session, p *Player, m xym.Msg) {
	d := p.dy()
	if d.Run == nil {
		s.retMsg("当前没有进行中的妖窟。")
		return
	}
	if changed, removed := p.takeItems(dyEscapeItem, 1); changed != nil || removed != nil {
		s.sendDelta(p, &bagDelta{items: changed, removed: removed})
	} else if !s.dySpend(dyEscapeGold, 0) {
		s.retMsg("没有遁地符，元宝也不足 50，无法携宝潜逃。")
		return
	}
	s.dyFinish(p)
}

// onDyGiveUp：打道回府——放弃本轮，什么都不拿。
func onDyGiveUp(s *Session, p *Player, m xym.Msg) {
	d := p.dy()
	stage := 0
	if d.Run != nil {
		stage = d.Run.Stage
	}
	d.Run = nil
	p.saveLog(s)
	s.SendExt("DANGYAO_PTO.S_DANGYAO_END", xym.Msg{"state": dyStateFail, "stage": stage, "free": d.Free,
		"list": posList(d.Dead)})
}

// dyShop：S_OPEN_SHOP（只列已上架且还能买的商品）。
func (s *Session) dyShop(p *Player, typ int) {
	d := p.dy()
	if d.Shop == nil {
		dyRollShop(d, false, s.dyRng())
	}
	list := []xym.Msg{}
	for _, sl := range d.Shop[typ] {
		if it := dyShopItem(sl.Pos); it != nil && sl.Left != 0 {
			list = append(list, xym.Msg{"order": it.Order, "pos": it.Pos, "name": it.Name, "price": it.Price,
				"icon": it.Icon, "desc": it.Desc, "left": sl.Left})
		}
	}
	s.SendExt("DANGYAO_PTO.S_OPEN_SHOP", xym.Msg{"gongde": d.Gongde, "max_gongde": d.Spent, "level": d.gongdeLevel(),
		"type": typ, "list": list})
}

// onDyBuy：功德兑换。回包顺序照抓包：S_ATTR_UPDATE、背包更新、S_SEND_GONGDE、S_OPEN_SHOP、S_REWARD_MSG{type 4}。
func onDyBuy(s *Session, p *Player, m xym.Msg) {
	d := p.dy()
	typ, pos, count := int(m["type"].(int64)), int(m["pos"].(int64)), int(m["count"].(int64))
	var slot *dyShopSlot
	for _, sl := range d.Shop[typ] {
		if sl.Pos == pos {
			slot = sl
		}
	}
	it := dyShopItem(pos)
	switch {
	case slot == nil || it == nil || count <= 0:
		s.retMsg("这件商品已下架。")
		s.dyShop(p, typ)
		return
	case slot.Left >= 0 && count > slot.Left:
		s.retMsg("超出今日限购数量。")
		s.dyShop(p, typ)
		return
	case d.Gongde < it.Price*count:
		s.retMsg("功德不足。")
		s.dyShop(p, typ)
		return
	}
	cost := it.Price * count
	d.Gongde -= cost
	d.Spent += int64(cost)
	if slot.Left > 0 {
		slot.Left -= count
	}
	rng := s.dyRng()
	delta := &bagDelta{}
	soul := 0
	for range count {
		if it.CA == 0 { // 本周固定 x 星魂
			if typ := weeklySoul(it.Order, time.Now()); typ != 0 {
				p.addSoul(typ, 1, delta)
			}
			continue
		}
		if rw := gamedata.Rewards[it.CA]; rw != nil {
			p.rollDrops(rw, rng, delta)
			soul += rw.Soul
		}
	}
	if soul > 0 {
		role, err := s.srv.roles.Update(s.role.UID, func(ro *Role) bool { ro.SoulExp += int32(soul); return true })
		if err != nil {
			s.logErr("save role", err)
		} else {
			s.role = role
		}
	}
	p.saveLog(s)
	s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{
		{"name": "cash", "val": s.role.Cash}, {"name": "gold", "val": s.role.Gold}, {"name": "soulexp", "val": s.role.SoulExp},
		{"name": "grade", "val": s.role.Grade}, {"name": "exp", "val": s.role.Exp}, {"name": "next_exp", "val": nextExp(s.role.Grade)},
	}})
	list := s.sendDelta(p, delta)
	s.dyGongde(p)
	s.dyShop(p, typ)
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"cash": 0, "mexp": 0, "pexp": 0, "ling": 0, "gold": 0, "soul": soul,
		"type": 4, "item": it.Icon, "no_fight_again": 1, "star": 0, "list": list})
}
