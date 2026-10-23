package xymsrv

import (
	"fmt"
	"strings"
	"time"

	"turnrpg/internal/xym"
)

// 体力 / 活力：自然回复、获取体力 / 活力面板（ETC_PTO.C_OPEN_POINT_BUY{type 1 体力 / 2 活力} →
// S_OPEN_POINT_BUY{type, goldBuyTimes 今日剩余元宝购买次数, foundCount, findMaxCount, overflowPoint}）、
// C_BUY_POINT_CONFIRM{type, buy_type 1 元宝购买 / 2 溢出回购 / 3 一键吃包子}。
//
// 依据：view/GetEnergyView.lua；官方热更 21_1.5.403.lua（体力第三行改成「一键吃满」：吃大笼包子 10076、包子
// 10043 吃到满，发 C_BUY_POINT_CONFIRM{1, 3}）；道具描述 包子 +5、大笼包子 +10 体力、百味酒 10075 +10 活力
// （百味酒在面板上走 C_USE_ITEM）。抓包：吃包子回 S_RET_MSG「吃了包子×12，体力+60，当前体力60/60」，有浪费时
// 再加「，浪费了3点」，然后 S_ATTR_UPDATE{tpoint, max_tpoint}；体力上限 60、活力上限 20；VIP 档的元宝购买次数
// 体力 10 / 15、活力 15；买一次活力 −25 元宝、活力补满。
// 用户给定：每 10 分钟自然回复 1 点体力，到上限停止。
//
// 估计：活力每 30 分钟回 1 点；元宝购买一次 体力 +60、活力 +20（可超上限），每天 15 次，价格按当天第几次
// 体力 50/50/100/100/150…、活力 25/25/50/50/75…；没有溢出存储（体力回购恒为「暂无溢出」）。
//
// 体力不足时开战斗：回 S_RET_MSG（它会清掉客户端所有等待锁，否则「连接中」要转到超时）并直接弹获取体力面板。

const (
	tpRegen      = 10 * 60 // 秒
	hpRegen      = 30 * 60
	pointBuys    = 15
	tpBuyAmount  = 60
	hpBuyAmount  = 20
	tpBuyBase    = 50
	hpBuyBase    = 25
	bunSmall     = 10043
	bunBig       = 10076
	wineItem     = 10075
	pointTypeT   = 1
	pointTypeH   = 2
	buyTypeGold  = 1
	buyTypeBack  = 2
	buyTypeItems = 3
)

// EnergyState 是体力 / 活力的回复计时和当天购买次数（Player.Energy）。
type EnergyState struct {
	TPAt  int64  `json:"tp_at"` // 体力回复计时起点（unix 秒）
	HPAt  int64  `json:"hp_at"`
	Day   string `json:"day"`
	TBuys int    `json:"t_buys"`
	HBuys int    `json:"h_buys"`
}

func (p *Player) energy() *EnergyState {
	if p.Energy == nil {
		now := time.Now().Unix()
		p.Energy = &EnergyState{TPAt: now, HPAt: now}
	}
	e := p.Energy
	if d := today(); e.Day != d {
		e.Day, e.TBuys, e.HBuys = d, 0, 0
	}
	return e
}

// regenStep 按间隔 step 秒给 cur 回复，最多到 maxV；返回新值和新的计时起点。满了计时停在 now。
func regenStep(cur, maxV int, at, now, step int64) (int, int64) {
	if cur >= maxV {
		return cur, now
	}
	if at <= 0 || at > now {
		return cur, now
	}
	n := (now - at) / step
	if n <= 0 {
		return cur, at
	}
	cur = min(maxV, cur+int(n))
	if cur >= maxV {
		return cur, now
	}
	return cur, at + n*step
}

// applyRegen 结算自然回复；有变化时存档并推 S_ATTR_UPDATE。调用方持有 p.mu。
func (s *Session) applyRegen(p *Player) {
	e := p.energy()
	now := time.Now().Unix()
	var names []string
	s.roleUpdate(func(ro *Role) bool {
		tp, tat := regenStep(ro.TPoint, maxTPoint, e.TPAt, now, tpRegen)
		hp, hat := regenStep(ro.HPoint, maxHPoint, e.HPAt, now, hpRegen)
		e.TPAt, e.HPAt = tat, hat
		if tp != ro.TPoint {
			names = append(names, "tpoint")
		}
		if hp != ro.HPoint {
			names = append(names, "hpoint")
		}
		ro.TPoint, ro.HPoint = tp, hp
		return len(names) > 0
	}, "tpoint", "hpoint")
}

// regenLoop 每分钟给在线玩家结算一次自然回复，顶栏不用等玩家操作就会涨。
func (srv *Server) regenLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		srv.mu.Lock()
		closed := srv.closed
		srv.mu.Unlock()
		if closed {
			return
		}
		for _, s := range srv.onlineSessions() {
			p := s.player
			if p == nil || s.role == nil {
				continue
			}
			p.mu.Lock()
			if ro := srv.roles.Get(s.role.UID); ro != nil {
				s.role = ro
			}
			s.applyRegen(p)
			p.mu.Unlock()
		}
	}
}

func buyPrice(base, nth int) int32 { return int32(base * (nth/2 + 1)) }

func (p *Player) pointBuyMsg(typ int) xym.Msg {
	e := p.energy()
	used := e.TBuys
	if typ == pointTypeH {
		used = e.HBuys
	}
	return xym.Msg{"type": typ, "goldBuyTimes": max(0, pointBuys-used), "foundCount": 0, "findMaxCount": 0, "overflowPoint": 0}
}

func init() {
	Handle("ETC_PTO.C_OPEN_POINT_BUY", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("ETC_PTO.S_OPEN_POINT_BUY", p.pointBuyMsg(int(m["type"].(int64))))
	})
	Handle("ETC_PTO.C_BUY_POINT_CONFIRM", onBuyPoint)
}

func onBuyPoint(s *Session, p *Player, m xym.Msg) {
	typ, how := int(m["type"].(int64)), int(m["buy_type"].(int64))
	e := p.energy()
	switch {
	case how == buyTypeGold:
		used, base, amount, name, attr := &e.TBuys, tpBuyBase, tpBuyAmount, "体力", "tpoint"
		if typ == pointTypeH {
			used, base, amount, name, attr = &e.HBuys, hpBuyBase, hpBuyAmount, "活力", "hpoint"
		}
		if *used >= pointBuys {
			s.retMsg("今天的购买次数已用完。")
			return
		}
		price := buyPrice(base, *used)
		if !s.roleUpdate(func(ro *Role) bool {
			if ro.Gold < price {
				return false
			}
			ro.Gold -= price
			if typ == pointTypeH {
				ro.HPoint += amount
			} else {
				ro.TPoint += amount
			}
			return true
		}, "gold", attr) {
			s.retMsg("元宝不足。")
			return
		}
		*used++
		p.saveLog(s)
		s.retMsg(fmt.Sprintf("花费%d元宝，%s+%d。", price, name, amount))
	case how == buyTypeItems && typ == pointTypeT:
		s.eatBuns(p)
	case how == buyTypeBack:
		s.retMsg("暂无溢出的体力。")
	}
}

// eatBuns：一键吃包子到体力满——先吃大笼包子（+10），差不到 10 点时改吃包子（+5），包子没了再用大笼包子补。
func (s *Session) eatBuns(p *Player) {
	need := maxTPoint - s.role.TPoint
	if need <= 0 {
		s.retMsg("体力已满。")
		return
	}
	big, small := p.itemCount(bunBig), p.itemCount(bunSmall)
	var nb, ns int32
	gain := 0
	for gain < need && (big > nb || small > ns) {
		switch {
		case need-gain >= 10 && big > nb, small <= ns:
			nb++
			gain += 10
		default:
			ns++
			gain += 5
		}
	}
	if gain == 0 {
		s.retMsg("没有包子了。")
		return
	}
	d := &bagDelta{}
	for _, x := range []struct {
		id int
		n  int32
	}{{bunBig, nb}, {bunSmall, ns}} {
		if x.n > 0 {
			ch, rm := p.takeItems(x.id, x.n)
			d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
		}
	}
	add := min(gain, need)
	s.roleUpdate(func(ro *Role) bool { ro.TPoint += add; return true })
	p.saveLog(s)
	s.sendDelta(p, d)
	var ate []string
	if nb > 0 {
		ate = append(ate, fmt.Sprintf("大笼包子×%d", nb))
	}
	if ns > 0 {
		ate = append(ate, fmt.Sprintf("包子×%d", ns))
	}
	msg := fmt.Sprintf("吃了%s，体力+%d，当前体力%d/%d", strings.Join(ate, "、"), add, s.role.TPoint, maxTPoint)
	if gain > add {
		msg += fmt.Sprintf("，浪费了%d点", gain-add)
	}
	s.retMsg(msg + "。")
	s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "tpoint", "val": s.role.TPoint}, {"name": "max_tpoint", "val": maxTPoint}}})
}

// usePointItem 是背包里直接使用包子 / 大笼包子 / 百味酒（C_USE_ITEM）：每个回复 5 / 10 体力、10 活力，
// 吃到上限为止（多出的不吃）。返回是否处理了。
func (s *Session) usePointItem(p *Player, typ int, n int32) bool {
	per, attr := 0, "tpoint"
	switch typ {
	case bunSmall:
		per = 5
	case bunBig:
		per = 10
	case wineItem:
		per, attr = 10, "hpoint"
	default:
		return false
	}
	cur, maxV := s.role.TPoint, maxTPoint
	if attr == "hpoint" {
		cur, maxV = s.role.HPoint, maxHPoint
	}
	if cur >= maxV {
		s.retMsg("已经满了，不用再吃了。")
		return true
	}
	n = min(n, int32((maxV-cur+per-1)/per))
	d := &bagDelta{}
	ch, rm := p.takeItems(typ, n)
	d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	add := min(int(n)*per, maxV-cur)
	s.roleUpdate(func(ro *Role) bool {
		if attr == "hpoint" {
			ro.HPoint += add
		} else {
			ro.TPoint += add
		}
		return true
	}, attr)
	p.saveLog(s)
	s.sendDelta(p, d)
	return true
}
