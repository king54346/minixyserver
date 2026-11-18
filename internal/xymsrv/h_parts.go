package xymsrv

import (
	"math/rand/v2"
	"strconv"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 强化（C_REFINE_SUITS / C_UPGRADE_BOOK / C_UPGRADE_PET_MAGIC）、碎片合成（C_MIX_SUIT /
// C_MIX_BOOK_PART）、拆碎（C_BREAK_SUIT / C_BREAK_MAGIC）、碎片转化（C_ON_TRANSFER_FRAGMENT：op 0 魂魄→通用魂魄、1 装备、2 法术）。
// 规则见 parts.go。抓包里没有这些请求，回包顺序按客户端等的锁定：强化界面在本地挂了
// S_UPDATE_SUIT / S_UPDATE_BOOK / S_UPDATE_PET 的锁，回调取 suits[1] / books[1] 当作强化目标，
// 所以材料的删除、碎片和银两先发，目标那条最后发。
//
// 「累计修为」强化（from_xiuwei=1）要修为系统，本服还没有，直接拒绝。

func init() {
	Handle("ITEM_PTO.C_GET_SUIT_IRONS", func(s *Session, p *Player, _ xym.Msg) {
		s.SendExt("ITEM_PTO.S_UPDATE_SUIT_IRON", p.ironsMsg(nil))
		s.SendExt("ITEM_PTO.S_UPDATE_SUIT_PART_ITEMS", p.partItemsMsg(suitPartItem))
	})
	Handle("ITEM_PTO.C_GET_BOOK_PARTS", func(s *Session, p *Player, _ xym.Msg) {
		s.SendExt("ITEM_PTO.S_UPDATE_BOOK_PART", p.bookPartsMsg(nil))
		s.SendExt("ITEM_PTO.S_UPDATE_MAGIC_PART_ITEMS", p.partItemsMsg(magicPartItem))
	})
	Handle("ITEM_PTO.C_REFINE_SUITS", onRefineSuit)
	Handle("ITEM_PTO.C_UPGRADE_BOOK", onUpgradeBook)
	Handle("ITEM_PTO.C_UPGRADE_PET_MAGIC", onUpgradePetMagic)
	Handle("ITEM_PTO.C_MIX_SUIT", onMixSuit)
	Handle("ITEM_PTO.C_MIX_BOOK_PART", onMixBookPart)
	Handle("ITEM_PTO.C_BREAK_SUIT", onBreakSuit)
	Handle("ITEM_PTO.C_BREAK_MAGIC", onBreakMagic)
	Handle("ITEM_PTO.C_ON_TRANSFER_FRAGMENT", onTransferFragment)
}

func onMixSuit(s *Session, p *Player, m xym.Msg) {
	typ := int(m["type"].(int64))
	if gamedata.Suits[typ] == nil {
		s.sendMsg("装备不存在。")
		return
	}
	if len(p.Suits) >= p.BagLimit[bagSuit] {
		s.sendMsg("装备背包已满。")
		return
	}
	star := suitStar(typ)
	d := &bagDelta{}
	if !p.takeParts(star, p.Irons[typ], suitPartItem[star], func(n int32) { p.addIron(typ, -n, d) }, d) {
		s.sendMsg("碎片不足。")
		return
	}
	d.gains = nil
	p.giveSuit(typ, d)
	s.finishMix(p, d)
}

func onMixBookPart(s *Session, p *Player, m xym.Msg) {
	typ := int32(m["magic"].(int64))
	if gamedata.Magics[typ] == nil {
		s.sendMsg("法术不存在。")
		return
	}
	if len(p.Books) >= p.BagLimit[bagBook] {
		s.sendMsg("法术背包已满。")
		return
	}
	star := magicStar(typ)
	d := &bagDelta{}
	if !p.takeParts(star, p.BookParts[typ], magicPartItem[star], func(n int32) { p.addBookPart(typ, -n, d) }, d) {
		s.sendMsg("碎片不足。")
		return
	}
	d.gains = nil
	p.giveBook(typ, d)
	s.finishMix(p, d)
}

func (s *Session) finishMix(p *Player, d *bagDelta) {
	p.saveLog(s)
	list := s.sendDelta(p, d)
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxNormal, "list": list})
}

// onBreakSuit 拆碎：每件得 NEED_IRON[星级] 个该装备的碎片，3 星以上每件 1 元宝（CSuitView getBreakCost）。
func onBreakSuit(s *Session, p *Player, m xym.Msg) {
	var picked []*Suit
	var gold int32
	seen := map[*Suit]bool{}
	reqs, _ := m["suits"].([]xym.Msg)
	for _, r := range reqs {
		x := p.suitByNum(int32(r["itemnum"].(int64)))
		if x == nil || seen[x] || x.Locked || p.suitWorn(x.Pos) || gamedata.Suits[x.Type] == nil {
			continue
		}
		seen[x] = true
		picked = append(picked, x)
		if suitStar(x.Type) >= 3 {
			gold++
		}
	}
	if len(picked) == 0 {
		s.sendMsg("没有可以拆碎的装备（已穿戴或已锁定的不能拆碎）。")
		return
	}
	if !s.spend(0, gold) {
		return
	}
	d := &bagDelta{}
	var removed []xym.Msg
	for _, x := range picked {
		p.removeSuit(x)
		removed = append(removed, xym.Msg{"itemnum": x.ItemNum})
		p.addIron(x.Type, needPart[suitStar(x.Type)], d)
	}
	p.saveLog(s)
	s.SendExt("ITEM_PTO.S_REMOVE_SUIT", xym.Msg{"suits": removed})
	list := s.sendDelta(p, d)
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxNormal, "list": list})
}

func onBreakMagic(s *Session, p *Player, m xym.Msg) {
	var picked []*Book
	var gold int32
	seen := map[*Book]bool{}
	reqs, _ := m["books"].([]xym.Msg)
	for _, r := range reqs {
		x := p.bookByPos(int(r["pos"].(int64)))
		if x == nil || seen[x] || x.Locked || p.bookWorn(x.Pos) || gamedata.Magics[x.Type] == nil {
			continue
		}
		seen[x] = true
		picked = append(picked, x)
		if magicStar(x.Type) >= 3 {
			gold++
		}
	}
	if len(picked) == 0 {
		s.sendMsg("没有可以拆碎的法术（已穿戴或已锁定的不能拆碎）。")
		return
	}
	if !s.spend(0, gold) {
		return
	}
	d := &bagDelta{}
	var removed []xym.Msg
	for _, x := range picked {
		p.removeBook(x)
		removed = append(removed, xym.Msg{"pos": x.Pos})
		p.addBookPart(x.Type, needPart[magicStar(x.Type)], d)
	}
	p.saveLog(s)
	s.SendExt("ITEM_PTO.S_REMOVE_BOOK", xym.Msg{"books": removed})
	list := s.sendDelta(p, d)
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxNormal, "list": list})
}

// onTransferFragment「我的碎片 - 转化」：把 4、5 星某种装备 / 法术的碎片换成同星通用碎片道具。
// op_type 1 装备、2 法术；客户端没写比例，按 1:1（估计）。
func onTransferFragment(s *Session, p *Player, m xym.Msg) {
	op, typ, n := m["op_type"].(int64), int(m["type"].(int64)), int32(m["count"].(int64))
	d := &bagDelta{}
	var star, item int
	switch op {
	case 0: // 魂魄 → 同星级的通用魂魄（「魂蛋」，4 / 5 星，每个花 25 / 100 魂值：CHunPoZhuanHuaView costPerc）
		pi := gamedata.Pets[int32(typ)]
		if pi == nil || n < 1 || p.Souls[int32(typ)] < n || soulWildItem[pi.Star] == 0 {
			s.sendMsg("魂魄不足。")
			s.SendExt("PET_PTO.S_UPDATE_CARD", p.soulsMsg([]int32{int32(typ)}))
			return
		}
		star = pi.Star
		cost := map[int]int32{4: 25, 5: 100}[star] * n
		if !s.roleUpdate(func(ro *Role) bool {
			if ro.SoulExp < cost {
				return false
			}
			ro.SoulExp -= cost
			return true
		}, "soulexp") {
			s.sendMsg("魂值不足。")
			s.SendExt("PET_PTO.S_UPDATE_CARD", p.soulsMsg([]int32{int32(typ)}))
			return
		}
		item = soulWildItem[star]
		p.addSoul(int32(typ), -n, d)
	case 1:
		star = suitStar(typ)
		if gamedata.Suits[typ] == nil || n < 1 || p.Irons[typ] < n || star < 4 {
			s.sendMsg("碎片不足。")
			return
		}
		item = suitPartItem[star]
		p.addIron(typ, -n, d)
	case 2:
		star = magicStar(int32(typ))
		if gamedata.Magics[int32(typ)] == nil || n < 1 || p.BookParts[int32(typ)] < n || star < 4 {
			s.sendMsg("碎片不足。")
			return
		}
		item = magicPartItem[star]
		p.addBookPart(int32(typ), -n, d)
	default:
		s.sendMsg("碎片不足。")
		return
	}
	d.items = append(d.items, p.addItem(item, n)...)
	d.gains = []gain{{rtItem, item, n}}
	p.saveLog(s)
	list := s.sendDelta(p, d)
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxNormal, "list": list})
}

// usePartItem：通用碎片道具「合成」（C_USE_ITEM）——凑够 6/15/20 个出该星级随机一件。
// 返回 false 表示 typ 不是通用碎片道具。
func (s *Session) usePartItem(p *Player, it *Item) bool {
	star, suit, ok := partItemStar(it.Type)
	if !ok {
		return false
	}
	need := needPart[star]
	if p.itemCount(it.Type) < need {
		s.sendMsg("碎片不足。")
		return true
	}
	limit, have, code := p.BagLimit[bagBook], len(p.Books), "GPFS"
	if suit {
		limit, have, code = p.BagLimit[bagSuit], len(p.Suits), "GPZB"
	}
	if have >= limit {
		s.sendMsg("背包已满。")
		return true
	}
	d := &bagDelta{}
	ch, rm := p.takeItems(it.Type, need)
	d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(p.UID)))
	p.giveCode(code+itoa(star), 1, rng, d)
	p.saveLog(s)
	list := s.sendDelta(p, d)
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxItem, "item": it.Type, "list": list})
	return true
}

// itoa 是十进制整数转字符串。
// 早先写成 string(rune('0'+n))，只能处理 0-9，10 以上会变成乱码字符（20 → "D"），
// 破阵积分/所需元宝/战报 id 等玩家可见文案都会显示错。
func itoa(n int) string { return strconv.Itoa(n) }
