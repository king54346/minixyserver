package xymsrv

import (
	"math/rand/v2"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 奇缘幻境（TURNTABLE_PTO）：12 格转盘（0 号格是起点，不出东西），每次「探宝」花 30 灵力，指针停在哪格就得哪格的
// 装备 / 法术 / 装备碎片；探宝累计 300 次后可进「宝藏幻境」，从一盘 5 星大奖里只能带走一件。
// 天地元阳、魂飞魄散、烛龙之息、放下屠刀、射日弓（gamedata.HuanjingOnly）只能在这里得到。
//
// 依据：view/TurnTableView.lua 与 ui/TurnTable_Help.lua（每次探宝 30 灵力；灵力不足时服务端回 S_OPEN_BUY_SCORE，
// 客户端弹购买框，1 元宝 1 灵力 → C_BUY_SCORE；得到幻境中的特定物品后可免费重置，否则重置花 100 灵力；
// 探宝 300 次后开启宝藏幻境，带走一件后退出并重置次数；宝藏幻境里「探宝」也是 C_START_TURN，
// 「携宝离开」C_GET_GUARANTEE_REWARD 后回 S_OPEN_TURNTABLE；S_OPEN_GUARANTEE_TABLE.cost 为 0 时显示「本次免费」，
// isvalid 0 的格子置灰）。S_MOVE 是带锁请求的回包，任何情况都要回。
// 抓包（3 盘）：1/4/7/10 格是 1–4 星装备、2/5/8/11 格是 1–4 星法术（同星一对），3/6/9 格是 2/3/4 星装备碎片，
// 另有两格被换成幻境专属的 5 星宝物；S_SEND_SCORE{score, cur_guarantee, base_guarantee 300, free_refresh}。
//
// 估计：各格落点权重（1–4 星 40/28/16/8，碎片 24/16/8，专属 1，约 0.4%）；碎片 4/3/2 个；转盘每天 0 点刷新；
// 每天第一次打开送 60 灵力（原服由活跃度换，本服未做活跃度）；宝藏幻境是 5 件专属 + 3 件 5 星装备 + 3 本
// 5 星法术，第一次探宝免费、之后每次 100 灵力，每转一次之前拿着的宝物作废（置灰），指针只停在没作废的格子上；
// 落到专属宝物后可免费重置（另外每天一次免费重置，对上抓包里当天首开的 free_refresh 1）。

const (
	ttCost        = 30
	ttResetCost   = 100
	ttGuarCost    = 100
	ttBaseGuar    = 300
	ttDailyScore  = 60
	ttSlots       = 12
	ttIconEquip   = 2
	ttIconMagic   = 3
	ttIconPart    = 5
	ttExclusiveWt = 1
)

var (
	ttTierWeight  = [5]int{0, 40, 28, 16, 8}
	ttPartWeight  = map[int]int{2: 24, 3: 16, 4: 8}
	ttPartCount   = map[int]int32{2: 4, 3: 3, 4: 2}
	ttPurpleShoes = []int{1507, 1508} // 乾坤天罡履、七星逐月靴
	ttExclusives  = []ttCell{{ID: 14506, Kind: ttIconMagic}, {ID: 14507, Kind: ttIconMagic},
		{ID: 14508, Kind: ttIconMagic}, {ID: 14509, Kind: ttIconMagic}, {ID: 1016, Kind: ttIconEquip}}
)

// ttCell 是转盘上的一格。
type ttCell struct {
	ID        int  `json:"id"`
	Kind      int  `json:"kind"` // icontype：2 装备、3 法术、5 装备碎片
	Count     int  `json:"count,omitempty"`
	Weight    int  `json:"w,omitempty"`
	Exclusive bool `json:"ex,omitempty"`
	Invalid   bool `json:"inv,omitempty"` // 宝藏幻境里已作废
}

// TurntableState 是奇缘幻境存档（Player.Turntable）。
type TurntableState struct {
	Score    int32    `json:"score"`
	Guar     int32    `json:"guar"` // 累计探宝次数（到 300 可进宝藏幻境）
	Free     bool     `json:"free"` // 可免费重置
	Day      string   `json:"day"`  // 转盘生成的日期
	Cells    []ttCell `json:"cells"`
	Pos      int      `json:"pos"`
	Times    int      `json:"times"` // 今天探宝次数
	ScoreDay string   `json:"score_day,omitempty"`

	InGuar   bool     `json:"in_guar,omitempty"` // 正在宝藏幻境里
	GuarCell []ttCell `json:"guar_cells,omitempty"`
	GuarPos  int      `json:"guar_pos,omitempty"`
	GuarSpin int      `json:"guar_spin,omitempty"`
}

func (p *Player) turntable(rng *rand.Rand) *TurntableState {
	if p.Turntable == nil {
		p.Turntable = &TurntableState{}
	}
	t := p.Turntable
	if d := today(); t.Day != d {
		t.Day, t.Times, t.Free = d, 0, true // 抓包两次首开都是 free_refresh 1：每天有一次免费重置
		t.Cells, t.Pos = newTurntable(rng), 0
	}
	if d := today(); t.ScoreDay != d {
		t.ScoreDay = d
		t.Score += ttDailyScore
	}
	return t
}

func ttPick[T any](rng *rand.Rand, l []T) (T, bool) {
	var zero T
	if len(l) == 0 {
		return zero, false
	}
	return l[rng.IntN(len(l))], true
}

// newTurntable 生成一盘：0 号格空；1–4 星装备 / 法术各一对，2–4 星碎片三格，再随机两格换成专属宝物。
func newTurntable(rng *rand.Rand) []ttCell {
	cells := make([]ttCell, ttSlots)
	for star := 1; star <= 4; star++ {
		base := (star - 1) * 3
		if id, ok := ttPick(rng, gamedata.SuitsByStar[star]); ok {
			if star == 4 && rng.IntN(2) == 0 { // 紫鞋（用户要求加入幻境）：4 星装备格一半换成紫鞋
				id = ttPurpleShoes[rng.IntN(len(ttPurpleShoes))]
			}
			cells[base+1] = ttCell{ID: id, Kind: ttIconEquip, Count: 1, Weight: ttTierWeight[star]}
		}
		if id, ok := ttPick(rng, gamedata.BooksByStar[star]); ok {
			cells[base+2] = ttCell{ID: int(id), Kind: ttIconMagic, Count: 1, Weight: ttTierWeight[star]}
		}
		if star >= 2 {
			if id, ok := ttPick(rng, gamedata.SuitsByStar[star]); ok {
				cells[base] = ttCell{ID: id, Kind: ttIconPart, Count: int(ttPartCount[star]), Weight: ttPartWeight[star]}
			}
		}
	}
	ex := rng.Perm(len(ttExclusives))[:2]
	slots := []int{1, 2, 4, 5, 7, 8, 10, 11}
	rng.Shuffle(len(slots), func(i, j int) { slots[i], slots[j] = slots[j], slots[i] })
	for i, e := range ex {
		c := ttExclusives[e]
		c.Count, c.Weight, c.Exclusive = 1, ttExclusiveWt, true
		cells[slots[i]] = c
	}
	return cells
}

// newGuarTable 是宝藏幻境：5 件专属 + 3 件 5 星装备 + 3 本 5 星法术，打乱放在 1–11 格。
func newGuarTable(rng *rand.Rand) []ttCell {
	var l []ttCell
	for _, c := range ttExclusives {
		c.Count, c.Exclusive = 1, true
		l = append(l, c)
	}
	for _, i := range rng.Perm(len(gamedata.SuitsByStar[5]))[:min(3, len(gamedata.SuitsByStar[5]))] {
		l = append(l, ttCell{ID: gamedata.SuitsByStar[5][i], Kind: ttIconEquip, Count: 1})
	}
	for _, i := range rng.Perm(len(gamedata.BooksByStar[5]))[:min(3, len(gamedata.BooksByStar[5]))] {
		l = append(l, ttCell{ID: int(gamedata.BooksByStar[5][i]), Kind: ttIconMagic, Count: 1})
	}
	rng.Shuffle(len(l), func(i, j int) { l[i], l[j] = l[j], l[i] })
	return append([]ttCell{{}}, l...)
}

func (c ttCell) name() string {
	switch c.Kind {
	case ttIconEquip, ttIconPart:
		if si := gamedata.Suits[c.ID]; si != nil {
			if c.Kind == ttIconPart {
				return si.Name + "碎片"
			}
			return si.Name
		}
	case ttIconMagic:
		if m := gamedata.Magics[int32(c.ID)]; m != nil {
			return m.Name
		}
	}
	return ""
}

func (c ttCell) give(p *Player, d *bagDelta) {
	switch c.Kind {
	case ttIconEquip:
		p.giveSuit(c.ID, d)
	case ttIconMagic:
		p.giveBook(int32(c.ID), d)
	case ttIconPart:
		p.addIron(c.ID, int32(c.Count), d)
	}
}

func secondsToMidnight() int64 {
	now := time.Now()
	y, m, dd := now.Date()
	return int64(time.Date(y, m, dd+1, 0, 0, 0, 0, now.Location()).Sub(now) / time.Second)
}

func (t *TurntableState) openMsg() xym.Msg {
	list := make([]xym.Msg, 0, ttSlots)
	for i, c := range t.Cells {
		list = append(list, xym.Msg{"name": c.name(), "pos": i, "icon": c.ID, "icontype": c.Kind, "is_closed": 0, "id": c.ID})
	}
	return xym.Msg{"pos": t.Pos, "time": secondsToMidnight(), "day_times": t.Times, "list": list}
}

func (t *TurntableState) guarCost() int32 {
	if t.GuarSpin == 0 {
		return 0
	}
	return ttGuarCost
}

func (t *TurntableState) guarMsg() xym.Msg {
	list := make([]xym.Msg, 0, ttSlots)
	for i, c := range t.GuarCell {
		list = append(list, xym.Msg{"name": c.name(), "pos": i, "icon": c.ID, "icontype": c.Kind, "isvalid": b2i(!c.Invalid)})
	}
	return xym.Msg{"pos": t.GuarPos, "cost": t.guarCost(), "list": list}
}

func (s *Session) sendTTScore(t *TurntableState) {
	s.SendExt("TURNTABLE_PTO.S_SEND_SCORE", xym.Msg{"score": t.Score, "cur_guarantee": min(t.Guar, ttBaseGuar),
		"base_guarantee": ttBaseGuar, "free_refresh": b2i(t.Free)})
}

func init() {
	Handle("TURNTABLE_PTO.C_OPEN_TURNTABLE", onTTOpen)
	Handle("TURNTABLE_PTO.C_APPLY_SCORE", func(s *Session, p *Player, m xym.Msg) { s.sendTTScore(p.turntable(s.actRng())) })
	Handle("TURNTABLE_PTO.C_START_TURN", onTTTurn)
	Handle("TURNTABLE_PTO.C_REFERSH_TURNTABLE", onTTRefresh)
	Handle("TURNTABLE_PTO.C_BUY_SCORE", onTTBuy)
	Handle("TURNTABLE_PTO.C_OPEN_GUARANTEE_TABLE", onTTOpenGuar)
	Handle("TURNTABLE_PTO.C_GET_GUARANTEE_REWARD", onTTTakeGuar)
}

func onTTOpen(s *Session, p *Player, m xym.Msg) {
	t := p.turntable(s.actRng())
	t.InGuar = false
	p.saveLog(s)
	s.SendExt("TURNTABLE_PTO.S_OPEN_TURNTABLE", t.openMsg())
	s.sendTTScore(t)
}

// weightedPos 按权重挑一格（跳过 skip 返回 true 的格子），没有可选的返回 -1。
func weightedPos(rng *rand.Rand, cells []ttCell, weight func(i int, c ttCell) int) int {
	total := 0
	for i, c := range cells {
		total += weight(i, c)
	}
	if total <= 0 {
		return -1
	}
	r := rng.IntN(total)
	for i, c := range cells {
		if r -= weight(i, c); r < 0 {
			return i
		}
	}
	return -1
}

func onTTTurn(s *Session, p *Player, m xym.Msg) {
	rng := s.actRng()
	t := p.turntable(rng)
	if t.InGuar {
		ttGuarTurn(s, p, t, rng)
		return
	}
	if t.Score < ttCost {
		s.SendExt("TURNTABLE_PTO.S_OPEN_BUY_SCORE", xym.Msg{})
		s.SendExt("TURNTABLE_PTO.S_MOVE", xym.Msg{"pos": t.Pos})
		return
	}
	pos := weightedPos(rng, t.Cells, func(i int, c ttCell) int {
		if i == 0 {
			return 0
		}
		return c.Weight
	})
	if pos < 0 {
		s.SendExt("TURNTABLE_PTO.S_MOVE", xym.Msg{"pos": t.Pos})
		return
	}
	t.Score -= ttCost
	t.Guar++
	t.Times++
	t.Pos = pos
	c := t.Cells[pos]
	if c.Exclusive {
		t.Free = true
	}
	d := &bagDelta{}
	c.give(p, d)
	p.saveLog(s)
	s.SendExt("TURNTABLE_PTO.S_MOVE", xym.Msg{"pos": pos})
	s.sendTTScore(t)
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, s.sendDelta(p, d)))
}

// ttGuarTurn 是宝藏幻境里转一次：之前拿着的作废，指针落到剩下的某一件上。
func ttGuarTurn(s *Session, p *Player, t *TurntableState, rng *rand.Rand) {
	reply := func() { s.SendExt("TURNTABLE_PTO.S_MOVE", xym.Msg{"pos": t.GuarPos}) }
	cost := t.guarCost()
	if t.Score < cost {
		s.SendExt("TURNTABLE_PTO.S_OPEN_BUY_SCORE", xym.Msg{})
		reply()
		return
	}
	pos := weightedPos(rng, t.GuarCell, func(i int, c ttCell) int {
		if i == 0 || i == t.GuarPos || c.Invalid {
			return 0
		}
		return 1
	})
	if pos < 0 {
		s.retMsg("宝藏幻境里已经没有别的宝物了，请携宝离开。")
		reply()
		return
	}
	if t.GuarPos > 0 {
		t.GuarCell[t.GuarPos].Invalid = true
	}
	t.Score -= cost
	t.GuarSpin++
	t.GuarPos = pos
	p.saveLog(s)
	s.SendExt("TURNTABLE_PTO.S_MOVE", xym.Msg{"pos": pos})
	s.SendExt("TURNTABLE_PTO.S_OPEN_GUARANTEE_TABLE", t.guarMsg())
	s.sendTTScore(t)
}

func onTTRefresh(s *Session, p *Player, m xym.Msg) {
	rng := s.actRng()
	t := p.turntable(rng)
	switch {
	case t.Free:
		t.Free = false
	case t.Score >= ttResetCost:
		t.Score -= ttResetCost
	default:
		s.SendExt("TURNTABLE_PTO.S_OPEN_BUY_SCORE", xym.Msg{})
		s.SendExt("TURNTABLE_PTO.S_OPEN_TURNTABLE", t.openMsg()) // 解开 LOCK
		return
	}
	t.Cells, t.Pos = newTurntable(rng), 0
	p.saveLog(s)
	s.SendExt("TURNTABLE_PTO.S_OPEN_TURNTABLE", t.openMsg())
	s.sendTTScore(t)
}

func onTTBuy(s *Session, p *Player, m xym.Msg) {
	n := int32(m["score"].(int64))
	if n <= 0 || n > 10000 {
		return
	}
	t := p.turntable(s.actRng())
	if !s.roleUpdate(func(ro *Role) bool {
		if ro.Gold < n {
			return false
		}
		ro.Gold -= n
		return true
	}, "gold") {
		s.retMsg("元宝不足。")
		return
	}
	t.Score += n
	p.saveLog(s)
	s.sendTTScore(t)
}

func onTTOpenGuar(s *Session, p *Player, m xym.Msg) {
	rng := s.actRng()
	t := p.turntable(rng)
	if t.Guar < ttBaseGuar {
		s.retMsg("探宝次数还不够，无法开启宝藏幻境。")
		return
	}
	if t.GuarCell == nil {
		t.GuarCell, t.GuarPos, t.GuarSpin = newGuarTable(rng), 0, 0
	}
	t.InGuar = true
	p.saveLog(s)
	s.SendExt("TURNTABLE_PTO.S_OPEN_GUARANTEE_TABLE", t.guarMsg())
	s.sendTTScore(t)
}

// onTTTakeGuar：携宝离开，拿走指针所指的宝物（没有就空手），退出宝藏幻境并清零探宝次数。
func onTTTakeGuar(s *Session, p *Player, m xym.Msg) {
	t := p.turntable(s.actRng())
	if !t.InGuar || t.GuarCell == nil {
		s.SendExt("TURNTABLE_PTO.S_OPEN_TURNTABLE", t.openMsg())
		return
	}
	d := &bagDelta{}
	got := t.GuarPos > 0
	if got {
		t.GuarCell[t.GuarPos].give(p, d)
	}
	t.InGuar, t.GuarCell, t.GuarPos, t.GuarSpin = false, nil, 0, 0
	t.Guar = 0
	p.saveLog(s)
	s.SendExt("TURNTABLE_PTO.S_OPEN_TURNTABLE", t.openMsg())
	s.sendTTScore(t)
	if got {
		s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, s.sendDelta(p, d)))
	}
}
