package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 背包协议（ITEM_PTO）：拉取、使用道具、穿脱装备 / 法术书、出售。
//
// 使用道具照客户端 view/ItemListView.lua 的判断：kind 0 不能用；kind 3 多选一（choose 为选中的
// 奖励号）；宝箱见 box.go；
// etc 非 0 的发该奖励（礼包）；银锭发描述里的银两。出售价：装备 星级×1000、法术书 星级×500
// （suit_mgr / magic_mgr 的 sell_price）。

// S_REWARD_MSG.type（logic/rewardview_mgr.lua）。奖励界面不显示 cash 字段（只有 FIGHT_WIN_PACKET
// 合并时才用），list 项又只有 rtype/type/count、带不了 REWARD_TYPE_CASH 要的 param，所以银两按
// 「银两」展示道具（logic/reward_mgr.lua specialRTypeToType：CASH -> 20010）放进 list。
const (
	boxNormal    = 1
	boxFightWin  = 2
	boxFightLose = 3
	boxItem      = 4 // 用道具得的奖励：data.item 还有剩时多一个「继续使用」
	boxTen       = 8
	itemCash     = 20010
)

func init() {
	Handle("PET_PTO.C_GET_CARDS", func(s *Session, p *Player, _ xym.Msg) {
		var types []int32
		for t := range p.Souls {
			types = append(types, t)
		}
		slices.Sort(types)
		s.SendExt("PET_PTO.S_UPDATE_CARD", p.soulsMsg(types))
	})
	Handle("ITEM_PTO.C_GET_ITEMS", func(s *Session, p *Player, _ xym.Msg) {
		l := []xym.Msg{}
		for _, it := range sortedItems(p.Items) {
			l = append(l, it.msg())
		}
		s.sendAll("ITEM_PTO.S_UPDATE_ITEM", "items", l)
	})
	Handle("ITEM_PTO.C_GET_SUITS", func(s *Session, p *Player, _ xym.Msg) {
		l := []xym.Msg{}
		for _, x := range p.Suits {
			l = append(l, x.msg())
		}
		s.sendAll("ITEM_PTO.S_UPDATE_SUIT", "suits", l)
	})
	Handle("ITEM_PTO.C_GET_BOOKS", func(s *Session, p *Player, _ xym.Msg) {
		l := []xym.Msg{}
		for _, x := range p.Books {
			l = append(l, x.msg())
		}
		s.sendAll("ITEM_PTO.S_UPDATE_BOOK", "books", l)
	})
	Handle("ITEM_PTO.C_GET_WEAR_SUITS", func(s *Session, p *Player, _ xym.Msg) {
		s.SendExt("ITEM_PTO.S_WEAR_SUIT", p.wearSuitMsg())
	})
	Handle("ITEM_PTO.C_GET_WEAR_BOOKS", func(s *Session, p *Player, _ xym.Msg) {
		s.SendExt("ITEM_PTO.S_WEAR_BOOK", p.wearBookMsg())
	})
	Handle("ITEM_PTO.C_USE_ITEM", onUseItem)
	Handle("ITEM_PTO.C_SUIT_ON", onSuitOn)
	Handle("ITEM_PTO.C_SUIT_OFF", func(s *Session, p *Player, m xym.Msg) {
		fp, wp := int(m["fight_pos"].(int64)), int(m["wear_pos"].(int64))
		if sl := p.fightSlot(fp); sl != nil && wp >= 1 && wp <= 3 {
			sl.Suit[wp-1] = 0
		}
		s.gearChanged(p, fp, true)
	})
	Handle("ITEM_PTO.C_BOOK_ON", onBookOn)
	Handle("ITEM_PTO.C_BOOK_OFF", func(s *Session, p *Player, m xym.Msg) {
		fp, wp := int(m["fight_pos"].(int64)), bookSlot(int(m["wear_pos"].(int64)))
		if sl := p.fightSlot(fp); sl != nil && wp >= 0 {
			sl.Book[wp] = 0
		}
		s.gearChanged(p, fp, false)
	})
	Handle("ITEM_PTO.C_SELL_SUIT", onSellSuit)
	Handle("ITEM_PTO.C_SELL_MAGIC", onSellMagic)
}

// bookSlot：客户端 magic_mgr.putOnMagic 把第 2/3 格（第 1 格是天生法术）减 2 后发，即 0/1；
// 也兼容直接发 1/2 或 2/3 的情况。返回 0 或 1，非法为 -1。
func bookSlot(wp int) int {
	switch wp {
	case 0, 1:
		return wp
	case 2, 3:
		return wp - 2
	}
	return -1
}

// gearChanged：穿脱后回 S_WEAR_SUIT / S_WEAR_BOOK（客户端等这把锁）和该位卡牌的新属性。
func (s *Session) gearChanged(p *Player, _ int, suit bool) {
	p.saveLog(s)
	if suit {
		s.SendExt("ITEM_PTO.S_WEAR_SUIT", p.wearSuitMsg())
	} else {
		s.SendExt("ITEM_PTO.S_WEAR_BOOK", p.wearBookMsg())
	}
	// 出战的卡都回：装备 / 法术书可能是从另一位身上挪过来的，那一位的属性、缘分和神通激发（tlxlv）也变了
	var l []xym.Msg
	for _, pet := range p.sortedPets() {
		if p.fightPos(pet.BagPos) > 0 {
			l = append(l, p.petFull(pet))
		}
	}
	if len(l) > 0 {
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": l})
	}
	// 客户端的多套阵容数据（切换阵容时 C_SET_LINEUPS 原样发回）只从 S_SEND_LINEUP 来：穿脱后不发的话，它手里出战
	// 阵容还是穿脱之前的装备，一切换阵容就把旧数据存回来，刚穿上的装备「自己掉了」（用户报）
	s.SendExt("LINEUP_PTO.S_SEND_LINEUP", p.lineupsMsg())
}

// onSuitOn：把装备穿到出战位 fight_pos（按部位放；原来在别的位置上的先脱下）。
func onSuitOn(s *Session, p *Player, m xym.Msg) {
	fp := int(m["fight_pos"].(int64))
	suit := p.suitByNum(int32(m["itemnum"].(int64)))
	sl := p.fightSlot(fp)
	if suit == nil || sl == nil || sl.Pet == 0 || gamedata.Suits[suit.Type] == nil {
		s.sendMsg("装备数据有误，请重试。")
		s.SendExt("ITEM_PTO.S_WEAR_SUIT", p.wearSuitMsg()) // 解锁
		return
	}
	k := suitSlot[gamedata.Suits[suit.Type].Kind]
	if sl.Suit[k] == suit.Pos { // 装备列表里对已穿在这一位的装备点「卸下」，客户端发的也是 C_SUIT_ON：即脱下
		sl.Suit[k] = 0
		s.gearChanged(p, fp, true)
		return
	}
	for _, x := range p.lineup(p.FightNo).Slots { // 同一阵容里一件装备只能穿在一个位置（不同阵容可以共用）
		if x != nil && x.Suit[k] == suit.Pos {
			x.Suit[k] = 0
		}
	}
	sl.Suit[k] = suit.Pos
	s.gearChanged(p, fp, true)
}

// onBookOn：把法术书穿到出战位 fight_pos 的第 wear_pos 本书位。
func onBookOn(s *Session, p *Player, m xym.Msg) {
	fp, wp := int(m["fight_pos"].(int64)), bookSlot(int(m["wear_pos"].(int64)))
	book := p.bookByPos(int(m["pos"].(int64)))
	sl := p.fightSlot(fp)
	if book == nil || sl == nil || sl.Pet == 0 || wp < 0 {
		s.sendMsg("法术数据有误，请重试。")
		s.SendExt("ITEM_PTO.S_WEAR_BOOK", p.wearBookMsg())
		return
	}
	if sl.Book[wp] == book.Pos { // 同上：对已穿在这一格的书再点一次 = 卸下
		sl.Book[wp] = 0
		s.gearChanged(p, fp, false)
		return
	}
	for _, x := range p.lineup(p.FightNo).Slots { // 同一阵容里一本书只能穿在一个位置
		if x == nil {
			continue
		}
		for i := range x.Book {
			if x.Book[i] == book.Pos {
				x.Book[i] = 0
			}
		}
	}
	sl.Book[wp] = book.Pos
	s.gearChanged(p, fp, false)
}

func onSellSuit(s *Session, p *Player, m xym.Msg) {
	var cash int32
	var removed []xym.Msg
	reqs, _ := m["suits"].([]xym.Msg)
	for _, r := range reqs {
		suit := p.suitByNum(int32(r["itemnum"].(int64)))
		if suit == nil || suit.Locked || p.suitWorn(suit.Pos) {
			continue
		}
		if si := gamedata.Suits[suit.Type]; si != nil {
			cash += int32(si.Star) * 1000
		}
		removed = append(removed, xym.Msg{"itemnum": suit.ItemNum})
		p.removeSuit(suit)
	}
	s.finishSell(p, cash, "ITEM_PTO.S_REMOVE_SUIT", "suits", removed)
}

func onSellMagic(s *Session, p *Player, m xym.Msg) {
	var cash int32
	var removed []xym.Msg
	reqs, _ := m["books"].([]xym.Msg)
	for _, r := range reqs {
		book := p.bookByPos(int(r["pos"].(int64)))
		if book == nil || book.Locked || p.bookWorn(book.Pos) {
			continue
		}
		if mi := gamedata.Magics[book.Type]; mi != nil {
			cash += int32(mi.Star) * 500
		}
		removed = append(removed, xym.Msg{"pos": book.Pos})
		p.removeBook(book)
	}
	s.finishSell(p, cash, "ITEM_PTO.S_REMOVE_BOOK", "books", removed)
}

func (p *Player) removeSuit(x *Suit) {
	p.unrefGear(x.Pos, 0)
	kept := p.Suits[:0]
	for _, s := range p.Suits {
		if s != x {
			kept = append(kept, s)
		}
	}
	p.Suits = kept
}

func (p *Player) removeBook(x *Book) {
	p.unrefGear(0, x.Pos)
	kept := p.Books[:0]
	for _, b := range p.Books {
		if b != x {
			kept = append(kept, b)
		}
	}
	p.Books = kept
}

func (s *Session) finishSell(p *Player, cash int32, msg, field string, removed []xym.Msg) {
	if len(removed) == 0 {
		s.sendMsg("没有可以出售的（已穿戴或已锁定的不能出售）。")
		return
	}
	p.saveLog(s)
	s.SendExt(msg, xym.Msg{field: removed})
	if role := s.addCash(cash); role != nil {
		s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "cash", "val": role.Cash}}})
	}
}

// addCash 改银两并落盘（失败只记日志），返回新角色。
func (s *Session) addCash(n int32) *Role {
	role, err := s.srv.roles.Update(s.role.UID, func(ro *Role) bool {
		ro.Cash += n
		return n != 0
	})
	if err != nil {
		s.logErr("save role", err)
		return nil
	}
	s.role = role
	return role
}

// onUseItem：C_USE_ITEM{pos, type, use_num, choose, usetype}。客户端不等回包，结果用
// S_UPDATE_ITEM / S_REMOVE_ITEM、新得的东西和 S_REWARD_MSG 告诉它。
func onUseItem(s *Session, p *Player, m xym.Msg) {
	it := p.itemByPos(int(m["pos"].(int64)))
	n := int32(max(m["use_num"].(int64), 1))
	if it == nil || it.Type != int(m["type"].(int64)) || it.Amount < 1 {
		s.sendMsg("道具不存在。")
		return
	}
	info := gamedata.Items[it.Type]
	if info == nil || info.Kind == 0 {
		s.sendMsg("该道具不能直接使用。")
		return
	}
	if s.usePartItem(p, it) { // 通用碎片「合成」
		return
	}
	if s.usePointItem(p, it.Type, min(n, p.itemCount(it.Type))) { // 包子 / 百味酒（energy.go）
		return
	}
	n = min(n, p.itemCount(it.Type))
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(p.UID)))
	d := &bagDelta{}
	var cash int32
	choose := int32(m["choose"].(int64))

	switch {
	case luckyItems[it.Type].per > 0: // 奥义秘录、神兵宝鼎等随机道具（lucky_items.go）
		_, used := p.useLucky(it.Type, n, rng, d)
		if used == 0 {
			s.retMsg(fmt.Sprintf("需要 %d 个才能合成。", luckyItems[it.Type].per))
			return
		}
		n = used
	case info.Kind == 3: // 多选一：choose 必须在可选列表里
		if !containsInt32(info.Choose, choose) || gamedata.Rewards[choose] == nil {
			s.sendMsg("请先选择要领取的奖励。")
			return
		}
		for range n {
			cash += p.grantReward(gamedata.Rewards[choose], rng, d)
		}
	case boxOf(it.Type) != nil: // 宝箱 / 钥匙：开 n 次，各扣一个
		bk := boxOf(it.Type)
		if n = min(n, p.boxCount(bk)); n < 1 {
			s.sendMsg("宝箱和钥匙要各有一个才能开启。")
			return
		}
		s.openBoxReply(p, bk, it.Type, n)
		return
	case info.Etc != 0 && gamedata.Rewards[info.Etc] != nil: // 礼包
		for range n {
			cash += p.grantReward(gamedata.Rewards[info.Etc], rng, d)
		}
	case info.Cash > 0: // 银锭
		cash += int32(info.Cash) * n
	default:
		s.sendMsg("「" + strings.TrimSpace(info.Name) + "」暂时无法在这里使用。")
		return
	}

	ch, rm := p.takeItems(it.Type, n)
	d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	p.saveLog(s)
	list := s.sendDelta(p, d)
	if cash > 0 {
		if role := s.addCash(cash); role != nil {
			s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "cash", "val": role.Cash}}})
		}
	}
	if cash > 0 {
		list = append(list, xym.Msg{"rtype": rtItem, "type": itemCash, "count": cash})
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxItem, "item": it.Type, "list": list})
}

// grantReward 发一条奖励表奖励：掉落编码进背包，返回其中的银两（cashb，由调用方统一加）。
func (p *Player) grantReward(rw *gamedata.RewardInfo, rng *rand.Rand, d *bagDelta) int32 {
	p.rollDrops(rw, rng, d)
	return int32(rw.Cash())
}

func containsInt32(l []int32, v int32) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
