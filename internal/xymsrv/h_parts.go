package xymsrv

import (
	"math/rand/v2"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 强化（C_REFINE_SUITS / C_UPGRADE_BOOK / C_UPGRADE_PET_MAGIC）、碎片合成（C_MIX_SUIT /
// C_MIX_BOOK_PART）、拆碎（C_BREAK_SUIT / C_BREAK_MAGIC）、碎片转化（C_ON_TRANSFER_FRAGMENT）。
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

// spend 扣银两 / 元宝并回 S_ATTR_UPDATE；不够时提示并返回 false。
func (s *Session) spend(cash, gold int32) bool {
	if s.role.Cash < cash {
		s.sendMsg("银两不足。")
		return false
	}
	if s.role.Gold < gold {
		s.sendMsg("元宝不足。")
		return false
	}
	if cash == 0 && gold == 0 {
		return true
	}
	role, err := s.srv.roles.Update(s.role.UID, func(ro *Role) bool {
		ro.Cash -= cash
		ro.Gold -= gold
		return true
	})
	if err != nil {
		s.logErr("save role", err)
		s.sendMsg("保存失败，请重试。")
		return false
	}
	s.role = role
	var attr []xym.Msg
	if cash != 0 {
		attr = append(attr, xym.Msg{"name": "cash", "val": role.Cash})
	}
	if gold != 0 {
		attr = append(attr, xym.Msg{"name": "gold", "val": role.Gold})
	}
	s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": attr})
	return true
}

// materials 收集强化材料：装备 / 法术书（去重、排除目标、已穿戴和已锁定的）与整堆碎片。
type materials struct {
	suits []*Suit
	books []*Book
	irons []int
	parts []int32
	exp   int32
}

func (p *Player) suitMaterials(m xym.Msg, target *Suit) *materials {
	mt := &materials{}
	seen := map[*Suit]bool{target: true}
	reqs, _ := m["suits"].([]xym.Msg)
	for _, r := range reqs {
		x := p.suitByNum(int32(r["itemnum"].(int64)))
		if x == nil || seen[x] || x.Locked || p.suitWorn(x.Pos) {
			continue
		}
		seen[x] = true
		mt.suits = append(mt.suits, x)
		mt.exp += x.expValue()
	}
	used := map[int]bool{}
	irons, _ := m["suit_irons"].([]xym.Msg)
	for _, r := range irons {
		t := int(r["type"].(int64))
		if n := p.Irons[t]; n > 0 && !used[t] {
			used[t] = true
			mt.irons = append(mt.irons, t)
			mt.exp += partExp[suitStar(t)] * n
		}
	}
	return mt
}

func (p *Player) bookMaterials(m xym.Msg, target *Book) *materials {
	mt := &materials{}
	seen := map[*Book]bool{}
	if target != nil {
		seen[target] = true
	}
	reqs, _ := m["books"].([]xym.Msg)
	for _, r := range reqs {
		x := p.bookByPos(int(r["pos"].(int64)))
		if x == nil || seen[x] || x.Locked || p.bookWorn(x.Pos) {
			continue
		}
		seen[x] = true
		mt.books = append(mt.books, x)
		mt.exp += x.expValue()
	}
	used := map[int32]bool{}
	parts, _ := m["book_parts"].([]xym.Msg)
	for _, r := range parts {
		t := int32(r["type"].(int64))
		if n := p.BookParts[t]; n > 0 && !used[t] {
			used[t] = true
			mt.parts = append(mt.parts, t)
			mt.exp += partExp[magicStar(t)] * n
		}
	}
	return mt
}

// consume 删掉材料并发 S_REMOVE_SUIT / S_REMOVE_BOOK 和碎片变化。
func (s *Session) consume(p *Player, mt *materials) {
	d := &bagDelta{}
	if len(mt.suits) > 0 {
		var l []xym.Msg
		for _, x := range mt.suits {
			p.removeSuit(x)
			l = append(l, xym.Msg{"itemnum": x.ItemNum})
		}
		s.SendExt("ITEM_PTO.S_REMOVE_SUIT", xym.Msg{"suits": l})
	}
	if len(mt.books) > 0 {
		var l []xym.Msg
		for _, x := range mt.books {
			p.removeBook(x)
			l = append(l, xym.Msg{"pos": x.Pos})
		}
		s.SendExt("ITEM_PTO.S_REMOVE_BOOK", xym.Msg{"books": l})
	}
	for _, t := range mt.irons {
		p.addIron(t, -p.Irons[t], d)
	}
	for _, t := range mt.parts {
		p.addBookPart(t, -p.BookParts[t], d)
	}
	s.sendDelta(p, d)
}

func onRefineSuit(s *Session, p *Player, m xym.Msg) {
	target := p.suitByNum(int32(m["itemnum"].(int64)))
	unlock := func(msg string) {
		if msg != "" {
			s.sendMsg(msg)
		}
		var l []xym.Msg
		if target != nil {
			l = append(l, target.msg())
		}
		s.SendExt("ITEM_PTO.S_UPDATE_SUIT", xym.Msg{"is_all": 0, "suits": l})
	}
	switch {
	case target == nil || gamedata.Suits[target.Type] == nil:
		unlock("装备不存在。")
		return
	case m["from_xiuwei"] != int64(0):
		unlock("修为强化暂未开放。")
		return
	}
	star := suitStar(target.Type)
	mt := p.suitMaterials(m, target)
	if mt.exp <= 0 {
		unlock("您还未选择消耗的装备，无法强化！")
		return
	}
	grade, exp := upTable(suitUpExp).add(star, target.Grade, target.Exp, mt.exp)
	if grade > s.role.Grade {
		unlock("装备等级不能超过人物等级。")
		return
	}
	if !s.spend(mt.exp*100*int32(star), 0) {
		unlock("")
		return
	}
	s.consume(p, mt)
	target.Grade, target.Exp = grade, exp
	target.ensureSkill() // 升到 20 级开启炼神效果（inlay.go）
	p.saveLog(s)
	if fp := p.suitFightPos(target.Pos); fp > 0 {
		if pet := p.petAtFightPos(fp); pet != nil {
			s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
		}
	}
	s.SendExt("ITEM_PTO.S_UPDATE_SUIT", xym.Msg{"is_all": 0, "suits": []xym.Msg{target.msg()}})
}

// suitFightPos 是穿着背包位 pos 装备的出战位（当前阵容），没穿为 0。
func (p *Player) suitFightPos(pos int) int {
	lu := p.lineup(p.FightNo)
	if lu == nil {
		return 0
	}
	for i, sl := range lu.Slots {
		if sl != nil && sl.Pet != 0 {
			for _, x := range sl.Suit {
				if x == pos {
					return i + 1
				}
			}
		}
	}
	return 0
}

func onUpgradeBook(s *Session, p *Player, m xym.Msg) {
	target := p.bookByPos(int(m["target_pos"].(int64)))
	unlock := func(msg string) {
		if msg != "" {
			s.sendMsg(msg)
		}
		var l []xym.Msg
		if target != nil {
			l = append(l, target.msg())
		}
		s.SendExt("ITEM_PTO.S_UPDATE_BOOK", xym.Msg{"is_all": 0, "books": l})
	}
	switch {
	case target == nil || gamedata.Magics[target.Type] == nil:
		unlock("法术不存在。")
		return
	case m["from_xiuwei"] != int64(0):
		unlock("修为强化暂未开放。")
		return
	case target.Grade >= maxMagicGrade:
		unlock("您的法术已经达到最高级！")
		return
	}
	star := magicStar(target.Type)
	mt := p.bookMaterials(m, target)
	if mt.exp <= 0 {
		unlock("请选择强化材料!")
		return
	}
	grade, exp := upTable(magicUpExp).add(star, target.Grade, target.Exp, mt.exp)
	if !s.spend(mt.exp*100*int32(star), 0) {
		unlock("")
		return
	}
	s.consume(p, mt)
	target.Grade, target.Exp = min(grade, maxMagicGrade), exp
	p.saveLog(s)
	s.SendExt("ITEM_PTO.S_UPDATE_BOOK", xym.Msg{"is_all": 0, "books": []xym.Msg{target.msg()}})
}

// onUpgradePetMagic 强化卡牌的天生法术（客户端 curr_magic_pos > 10000 时即 npcnum），等 S_UPDATE_PET。
func onUpgradePetMagic(s *Session, p *Player, m xym.Msg) {
	pet := p.petByNpc(int32(m["npcnum"].(int64)))
	unlock := func(msg string) {
		if msg != "" {
			s.sendMsg(msg)
		}
		var l []xym.Msg
		if pet != nil {
			l = append(l, p.petFull(pet))
		}
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": l})
	}
	if pet == nil || gamedata.Pets[pet.Type] == nil || gamedata.Magics[gamedata.Pets[pet.Type].Magic] == nil {
		unlock("法术不存在。")
		return
	}
	if m["from_xiuwei"] != int64(0) {
		unlock("修为强化暂未开放。")
		return
	}
	if pet.magicLv() >= maxMagicGrade {
		unlock("您的法术已经达到最高级！")
		return
	}
	star := magicStar(gamedata.Pets[pet.Type].Magic)
	mt := p.bookMaterials(m, nil)
	if mt.exp <= 0 {
		unlock("请选择强化材料!")
		return
	}
	grade, exp := upTable(magicUpExp).add(star, pet.magicLv(), pet.MagicExp, mt.exp)
	if !s.spend(mt.exp*100*int32(star), 0) {
		unlock("")
		return
	}
	s.consume(p, mt)
	pet.MagicLv, pet.MagicExp = min(grade, maxMagicGrade), exp
	p.saveLog(s)
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
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

func itoa(n int) string { return string(rune('0' + n)) }
