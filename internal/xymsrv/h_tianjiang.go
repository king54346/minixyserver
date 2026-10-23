package xymsrv

import (
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 天将协议（CARD_SET_PTO），规则见 tianjiang.go。
//
// 客户端流程（view/TianJiangDetailView.lua）：C_ON_TJUP 后挂锁等 S_SEND_TJ_UP_OK，ok=1 时再发
// C_GET_TJINFO、等 S_SEND_TIANJIANG_INFO，然后弹升阶成功界面；ok≠1 直接结束。所以失败也要回
// S_SEND_TJ_UP_OK{ok:0}。分解（TianJiangSuitSoulView / MagicSoulView）不挂锁，回 S_SEND_TJ_TOSOUL_OK。
// 天将信息缓存挂在图鉴缓存（CardSetView.S_OPEN_CARD_SET）上：没先收到图鉴，S_SEND_TIANJIANG_INFO 存不进去，
// 所以登录后照官方顺序推：图鉴 → 金装数 → 天将信息（装备、法术各一组）。

func init() {
	Handle("CARD_SET_PTO.C_GET_TJINFO", func(s *Session, p *Player, m xym.Msg) {
		kind := int(m["type"].(int64))
		if kind != tjTypeSuit && kind != tjTypeMagic {
			kind = tjTypeSuit
		}
		s.SendExt("CARD_SET_PTO.S_SEND_TIANJIANG_INFO", p.tjInfoMsg(kind, s.role, time.Now()))
	})
	Handle("CARD_SET_PTO.C_OPEN_CARD_SET", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("CARD_SET_PTO.S_OPEN_CARD_SET", p.cardSetMsg(int(m["type"].(int64))))
		s.SendExt("ETC_PTO.S_ATTR_UPDATE", p.goldAttrMsg())
	})
	Handle("CARD_SET_PTO.C_ON_TJUP", onTJUp)
	Handle("CARD_SET_PTO.C_ON_TRANSFER_TO_SOUL", onTJToSoul)
}

// pushCardSets：登录后推送图鉴与天将信息（抓包顺序：S_OPEN_CARD_SET、S_ATTR_UPDATE、S_SEND_TIANJIANG_INFO，
// 装备一组、法术一组）。
func (s *Session) pushCardSets() {
	p := s.player
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for _, kind := range []int{tjTypeSuit, tjTypeMagic} {
		s.SendExt("CARD_SET_PTO.S_OPEN_CARD_SET", p.cardSetMsg(kind))
		s.SendExt("ETC_PTO.S_ATTR_UPDATE", p.goldAttrMsg())
		s.SendExt("CARD_SET_PTO.S_SEND_TIANJIANG_INFO", p.tjInfoMsg(kind, s.role, now))
	}
}

func onTJUp(s *Session, p *Player, m xym.Msg) {
	kind, typ := int(m["type"].(int64)), int(m["target_type"].(int64))
	method, costType := int(m["method"].(int64)), int(m["cost_type"].(int64))
	fail := func(msg string) {
		if msg != "" {
			s.sendMsg(msg)
		}
		s.SendExt("CARD_SET_PTO.S_SEND_TJ_UP_OK", xym.Msg{"type": kind, "ok": 0})
	}
	star := tjStar(kind, typ)
	switch {
	case star == 0:
		fail("只有四星、五星装备和法术可以升阶。")
		return
	case s.role.Grade < tjOpenGrade:
		fail("天匠功能将在30级时开启")
		return
	case p.goldCount(kind) < tjNeedGold:
		fail("金装备 / 金法术数量达到10件即开启天匠功能（含同名）。")
		return
	}
	t := p.tj()
	bk := t.book(kind)
	e := bk.entry(typ)
	limit := tjGradeCap(s.role.Grade)
	now := time.Now()
	switch {
	case e.Lv >= tjMaxLv:
		fail("已经是最高阶了。")
		return
	case e.Lv >= limit:
		fail("玩家等级30/40/50的玩家，天匠等级不可超过[武]/[圣]/[神]，故无法进行操作。")
		return
	case e.CoolUntil > now.Unix():
		fail("升阶冷却中，请稍后再试。")
		return
	case star == 4 && method != tjMethodSame:
		fail("四星只能使用同名升阶。")
		return
	}
	cost, _ := tjCost(kind, star, e)

	var suits []*Suit
	var books []*Book
	var add, soul int32
	newLv := e.Lv + 1
	switch method {
	case tjMethodSoul:
		if bk.Soul < cost {
			fail("器魂 / 法魂不足。")
			return
		}
		soul, add = cost, cost
	case tjMethodSame:
		reqs, _ := m["list"].([]xym.Msg)
		seen := map[int64]bool{}
		for _, r := range reqs {
			id := r["item"].(int64)
			if seen[id] {
				continue
			}
			seen[id] = true
			if kind == tjTypeSuit {
				x := p.suitByNum(int32(id))
				if x == nil || x.Type != typ || x.Locked || p.suitWorn(x.Pos) {
					fail("选中的同名装备不可用（已锁定或已穿戴）。")
					return
				}
				suits = append(suits, x)
			} else {
				x := p.bookByPos(int(id))
				if x == nil || int(x.Type) != typ || x.Locked || p.bookWorn(x.Pos) {
					fail("选中的同名法术不可用（已锁定或已穿戴）。")
					return
				}
				books = append(books, x)
			}
		}
		n := int32(len(suits) + len(books))
		if n == 0 {
			fail("请选择同名材料。")
			return
		}
		add = n * tjUnit(kind, star)
		newLv = max(tjLevelFor(kind, star, e.Points+add, limit), e.Lv) // 5 星装备 0 阶同名可直接到「武」
		if newLv <= e.Lv {
			fail("同名数量不足。")
			return
		}
	default:
		fail("")
		return
	}
	newLv = min(newLv, limit)
	var lingqi int32
	for l := e.Lv + 1; l <= newLv; l++ {
		lingqi += tjLingqi[l]
	}
	switch costType {
	case tjCostLingqi:
		if t.Lingqi < lingqi {
			fail("灵气不足。")
			return
		}
		t.Lingqi -= lingqi
	case tjCostGold:
		if !s.spend(0, lingqi*10) {
			fail("")
			return
		}
	default:
		fail("")
		return
	}

	if len(suits) > 0 {
		var l []xym.Msg
		for _, x := range suits {
			p.removeSuit(x)
			l = append(l, xym.Msg{"itemnum": x.ItemNum})
		}
		s.SendExt("ITEM_PTO.S_REMOVE_SUIT", xym.Msg{"suits": l})
	}
	if len(books) > 0 {
		var l []xym.Msg
		for _, x := range books {
			p.removeBook(x)
			l = append(l, xym.Msg{"pos": x.Pos})
		}
		s.SendExt("ITEM_PTO.S_REMOVE_BOOK", xym.Msg{"books": l})
	}
	bk.Soul -= soul
	e.Points += add
	e.Lv = newLv
	if tjCooldown > 0 {
		e.CoolUntil = now.Unix() + tjCooldown
	}
	p.saveLog(s)
	// 阶级按种类生效：出战卡的攻击 / 生命 / 先攻可能都变了
	var pets []xym.Msg
	for _, pet := range p.sortedPets() {
		if p.fightPos(pet.BagPos) > 0 {
			pets = append(pets, p.petFull(pet))
		}
	}
	if len(pets) > 0 {
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": pets})
	}
	s.SendExt("ETC_PTO.S_ATTR_UPDATE", p.goldAttrMsg())
	s.SendExt("CARD_SET_PTO.S_SEND_TJ_UP_OK", xym.Msg{"type": kind, "ok": 1})
}

// onTJToSoul：5 星装备 / 法术整件分解 20 点，5 星碎片整堆按数量计点（TianJiangSuitSoulView：选中碎片即整堆）。
func onTJToSoul(s *Session, p *Player, m xym.Msg) {
	kind := int(m["type"].(int64))
	if kind != tjTypeSuit && kind != tjTypeMagic {
		s.SendExt("CARD_SET_PTO.S_SEND_TJ_TOSOUL_OK", xym.Msg{"type": kind, "ok": 0})
		return
	}
	bk := p.tj().book(kind)
	d := &bagDelta{}
	var got int32
	var removed []xym.Msg
	items, _ := m["itemlist"].([]xym.Msg)
	for _, r := range items {
		id := r["item"].(int64)
		if kind == tjTypeSuit {
			x := p.suitByNum(int32(id))
			if x == nil || x.Locked || p.suitWorn(x.Pos) || gamedata.Suits[x.Type] == nil || gamedata.Suits[x.Type].Star != 5 {
				continue
			}
			p.removeSuit(x)
			removed = append(removed, xym.Msg{"itemnum": x.ItemNum})
		} else {
			x := p.bookByPos(int(id))
			if x == nil || x.Locked || p.bookWorn(x.Pos) || magicStar(x.Type) != 5 {
				continue
			}
			p.removeBook(x)
			removed = append(removed, xym.Msg{"pos": x.Pos})
		}
		got += tjSoulPerWhole
	}
	parts, _ := m["parts"].([]xym.Msg)
	for _, r := range parts {
		t := int(r["ptype"].(int64))
		if kind == tjTypeSuit {
			if n := p.Irons[t]; n > 0 && suitStar(t) == 5 {
				p.addIron(t, -n, d)
				got += n
			}
		} else if n := p.BookParts[int32(t)]; n > 0 && magicStar(int32(t)) == 5 {
			p.addBookPart(int32(t), -n, d)
			got += n
		}
	}
	if got == 0 {
		s.sendMsg("没有可以融合的五星装备 / 法术或碎片（已穿戴或已锁定的不能融合）。")
		s.SendExt("CARD_SET_PTO.S_SEND_TJ_TOSOUL_OK", xym.Msg{"type": kind, "ok": 0, "soul": bk.Soul})
		return
	}
	bk.Soul += got
	p.saveLog(s)
	if len(removed) > 0 {
		if kind == tjTypeSuit {
			s.SendExt("ITEM_PTO.S_REMOVE_SUIT", xym.Msg{"suits": removed})
		} else {
			s.SendExt("ITEM_PTO.S_REMOVE_BOOK", xym.Msg{"books": removed})
		}
	}
	s.sendDelta(p, d)
	s.SendExt("ETC_PTO.S_ATTR_UPDATE", p.goldAttrMsg())
	s.SendExt("CARD_SET_PTO.S_SEND_TJ_TOSOUL_OK", xym.Msg{"type": kind, "ok": 1, "soul": bk.Soul})
}
