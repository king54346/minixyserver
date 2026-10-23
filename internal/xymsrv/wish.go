package xymsrv

import (
	"slices"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 缘魂（WISH_PTO）：选一张自己的 4 星以上护法（from）和它的一位有缘护法（to，auto/wish.lua），
// 在 8 张翻牌里求缘，翻出 to 或 from 的魂魄、或魂值。
//
// 依据：view/YuanxuView.lua（「唯有四星及以上护法才能求缘」、有缘表 wish、8 格（客户端 1–9 去掉中间第 5 格，
// 请求里 pos 0–7）、格子类型 1 = 1 个 to 魂魄、2 = 3 个、3 = 50 魂值、4 = 1 个 from 魂魄、5 = 3 个、
// 「今日剩余求缘次数」= 8 − 已翻格数、「每日均可免费缘魂 1 次」「首次免费」、left 显示求缘珠数量）；
// 抓包 S_OPEN_WISH 一整盘：1,3,4,3,1,2,1,5（三张 1 个 to 魂魄、一张 3 个、两张魂值、from 魂魄 1 个和 3 个各一张）。
//
// 估计：每盘就是这 8 张洗乱；每天第一次翻免费，其余每次 1 颗求缘珠（10059）；每天重新洗一盘；
// 一盘没翻完不能重选，翻完或还没翻时可以重选护法（重选后今天这盘翻完了仍不能再翻）。

const (
	wishGrids  = 8
	wishItem   = 10059 // 求缘珠
	wishIsoul  = 50    // 魂值格的数量
	wishSoul1  = 1
	wishSoul3  = 2
	wishISoul  = 3
	wishMSoul1 = 4
	wishMSoul3 = 5
)

// wishDeck 是一盘的 8 张（抓包）。
var wishDeck = []int{wishSoul1, wishSoul1, wishSoul1, wishSoul3, wishISoul, wishISoul, wishMSoul1, wishMSoul3}

// WishState 是缘魂存档（Player.Wish）。
type WishState struct {
	Day     string     `json:"day"`
	Free    int        `json:"free"`
	From    int32      `json:"from"` // 卡牌类型
	To      int32      `json:"to"`
	Deck    []int      `json:"deck,omitempty"` // 没翻开的牌（已洗）
	Current []wishGrid `json:"current,omitempty"`
}

type wishGrid struct {
	Pos  int `json:"pos"`
	Type int `json:"type"`
}

func (p *Player) wish(s *Session) *WishState {
	if p.Wish == nil {
		p.Wish = &WishState{}
	}
	w := p.Wish
	if d := today(); w.Day != d {
		w.Day, w.Free, w.Current, w.Deck = d, 1, nil, nil
	}
	if w.Deck == nil && len(w.Current) == 0 {
		w.Deck = slices.Clone(wishDeck)
		rng := s.actRng()
		rng.Shuffle(len(w.Deck), func(i, j int) { w.Deck[i], w.Deck[j] = w.Deck[j], w.Deck[i] })
	}
	return w
}

func (w *WishState) canRechoose() bool {
	return w.From != 0 && (len(w.Current) == 0 || len(w.Current) >= wishGrids)
}

func (s *Session) wishMsg(p *Player) {
	w := p.wish(s)
	cur := []xym.Msg{}
	for _, g := range w.Current {
		cur = append(cur, xym.Msg{"pos": g.Pos, "type": g.Type})
	}
	s.SendExt("WISH_PTO.S_OPEN_WISH", xym.Msg{"from_type": w.From, "to_type": w.To, "left": p.itemCount(wishItem),
		"free_left": w.Free, "is_return": 0, "can_rechoose": b2i(w.canRechoose()), "current": cur})
}

func init() {
	Handle("WISH_PTO.C_OPEN_WISH", onOpenWish)
	Handle("WISH_PTO.C_OPEN_GRID", onWishGrid)
	Handle("WISH_PTO.C_RECHOOSE_WISH", onWishRechoose)
}

// onOpenWish：from_npc = 0 只查询；否则选定求缘的护法与有缘护法。
func onOpenWish(s *Session, p *Player, m xym.Msg) {
	w := p.wish(s)
	npc, to := int32(m["from_npc"].(int64)), int32(m["to_type"].(int64))
	if npc != 0 {
		pet := p.petByNpc(npc)
		switch {
		case pet == nil || gamedata.Pets[pet.Type] == nil || gamedata.Pets[pet.Type].Star < 4:
			s.retMsg("唯有四星及以上护法才能求缘。")
		case !slices.Contains(gamedata.Wish[pet.Type], to):
			s.retMsg("您选择的护法找不到有缘人。")
		case w.From != 0 && !w.canRechoose():
			s.retMsg("本轮求缘还没有结束。")
		default:
			w.From, w.To = pet.Type, to
			p.saveLog(s)
		}
	}
	s.wishMsg(p)
}

// onWishGrid：翻一格（第一次免费，之后 1 颗求缘珠），发魂魄 / 魂值，回 S_OPEN_WISH（客户端靠新增的格子播翻牌）。
func onWishGrid(s *Session, p *Player, m xym.Msg) {
	w := p.wish(s)
	pos := int(m["pos"].(int64))
	opened := slices.ContainsFunc(w.Current, func(g wishGrid) bool { return g.Pos == pos })
	switch {
	case w.From == 0:
		s.retMsg("请先选择求缘的护法。")
		s.wishMsg(p)
		return
	case pos < 0 || pos >= wishGrids || opened || len(w.Deck) == 0:
		s.retMsg("今日求缘次数已用完。")
		s.wishMsg(p)
		return
	}
	delta := &bagDelta{}
	if w.Free > 0 {
		w.Free--
	} else {
		changed, removed := p.takeItems(wishItem, 1)
		if changed == nil && removed == nil {
			s.retMsg("求缘珠不足。")
			s.wishMsg(p)
			return
		}
		delta.items, delta.removed = changed, removed
	}
	t := w.Deck[0]
	w.Deck = w.Deck[1:]
	w.Current = append(w.Current, wishGrid{Pos: pos, Type: t})
	p.track(evWish, 1)
	soul := int32(0)
	switch t {
	case wishSoul1:
		p.addSoul(w.To, 1, delta)
	case wishSoul3:
		p.addSoul(w.To, 3, delta)
	case wishMSoul1:
		p.addSoul(w.From, 1, delta)
	case wishMSoul3:
		p.addSoul(w.From, 3, delta)
	case wishISoul:
		soul = wishIsoul
	}
	p.saveLog(s)
	if soul > 0 {
		s.roleUpdate(func(ro *Role) bool { ro.SoulExp += soul; return true }, "soulexp")
	}
	list := s.sendDelta(p, delta)
	r := rewardMsg(1, list)
	r["soul"] = soul
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
	s.wishMsg(p)
}

// onWishRechoose：重选护法（一盘翻到一半不行）。客户端先关掉界面，回 S_OPEN_WISH{from_type 0} 打开选护法页。
func onWishRechoose(s *Session, p *Player, m xym.Msg) {
	w := p.wish(s)
	if !w.canRechoose() {
		s.retMsg("本轮求缘还没有结束，不能重选。")
	} else {
		w.From, w.To = 0, 0
		p.saveLog(s)
	}
	s.wishMsg(p)
}
