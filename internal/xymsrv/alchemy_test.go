package xymsrv

import (
	"maps"
	"math/rand/v2"
	"slices"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestXianling：10 条货、4 条热点；交齐材料换到魂魄、得贡献、该条消失；缺材料按件扣元宝；变更热点 3 选 1。
func TestXianling(t *testing.T) {
	c, p, srv := actWorld(t, "xl")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 1000; return true })
	c.sendExt("ETC_PTO.C_OPEN_XIANLING_SHOP", nil)
	m := c.expectExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO")
	l := m["list"].([]xym.Msg)
	hot := 0
	for _, g := range l {
		hot += num(g, "recommend")
		if n := len(g["need"].([]xym.Msg)); n < 1 || n > 3 || num(g, "contribute") != int(xlContribute[petStar(int32(num(g, "goods")))]) {
			t.Fatalf("goods %v", g)
		}
	}
	if len(l) != xlEntries || hot < xlHotEntries || num(m, "left_refresh") != xlFreeRefresh {
		t.Fatalf("shop %v", m)
	}

	// 第 0 条：缺最后一件时弹框问是否补元宝直接换（这里选取消）；花元宝忽略它后交齐其余的就能换
	g := l[0]
	need := g["need"].([]xym.Msg)
	var give []xym.Msg
	p.mu.Lock()
	for _, n := range need[:len(need)-1] {
		if n["kind"] == "suit" {
			give = append(give, xym.Msg{"kind": "suit", "item": int64(p.addSuit(num(n, "type")).ItemNum)})
		} else {
			give = append(give, xym.Msg{"kind": "magic", "item": int64(p.addBook(int32(num(n, "type"))).Pos)})
		}
	}
	nSuits, nBooks := len(p.Suits), len(p.Books)
	soul := p.Souls[int32(num(g, "goods"))]
	p.mu.Unlock()
	c.sendExt("ETC_PTO.C_XIANLING_SHOP_EXCHANGE_GOODS", xym.Msg{"pos": 0, "need": give})
	ask := c.expectExt("ETC_PTO.S_ASK_BOX")
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": ask["session"], "opt": 1})
	last := need[len(need)-1]
	lastPrice := xlIgnorePrice(xlNeed{Type: num(last, "type"), Kind: last["kind"].(string)})
	c.sendExt("ETC_PTO.C_XIANLING_SHOP_IGNORE_STUFF", xym.Msg{"type": last["type"], "stuff_kind": last["kind"], "pos": 0})
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	if l := c.expectExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO")["list"].([]xym.Msg); len(l[0]["need"].([]xym.Msg)) != len(need)-1 {
		t.Fatalf("ignore %v", l[0])
	}
	c.sendExt("ETC_PTO.C_XIANLING_SHOP_EXCHANGE_GOODS", xym.Msg{"pos": 0, "need": give})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	info := got["ETC_PTO.S_SEND_XIANLING_SHOP_INFO"]
	if len(info["list"].([]xym.Msg)) != xlEntries-1 || num(info, "contribute") != num(g, "contribute") {
		t.Fatalf("after exchange %v", info)
	}
	if p.Souls[int32(num(g, "goods"))] != soul+1 || len(p.Suits)+len(p.Books) != nSuits+nBooks-len(give) {
		t.Fatalf("soul %d suits %d books %d", p.Souls[int32(num(g, "goods"))], len(p.Suits), len(p.Books))
	}
	if gold := srv.roles.Get(p.UID).Gold; gold != 1000-lastPrice {
		t.Fatalf("gold %d", gold)
	}
	// 新的第 0 条：什么都不交，弹框确认后全部用元宝代替直接换
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 10000; return true })
	g2 := info["list"].([]xym.Msg)[0]
	var price int32
	for _, n := range g2["need"].([]xym.Msg) {
		price += xlIgnorePrice(xlNeed{Type: num(n, "type"), Kind: n["kind"].(string)})
	}
	soul2 := p.Souls[int32(num(g2, "goods"))]
	c.sendExt("ETC_PTO.C_XIANLING_SHOP_EXCHANGE_GOODS", xym.Msg{"pos": 0, "need": []xym.Msg{}})
	ask = c.expectExt("ETC_PTO.S_ASK_BOX")
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": ask["session"], "opt": 0})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	if p.Souls[int32(num(g2, "goods"))] != soul2+1 || srv.roles.Get(p.UID).Gold != 10000-price {
		t.Fatalf("gold exchange: soul %d gold %d price %d", p.Souls[int32(num(g2, "goods"))], srv.roles.Get(p.UID).Gold, price)
	}

	c.sendExt("ETC_PTO.C_CHANGE_HOT_ITEM", nil)
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	r := c.expectExt("ETC_PTO.S_UPDATE_RANDOM_PET")["random_pets"].([]xym.Msg)
	if len(r) != 3 {
		t.Fatalf("random pets %v", r)
	}
	c.sendExt("ETC_PTO.C_ON_REFRESH_XIANLING_HOT", xym.Msg{"id": r[1]["id"]})
	if info := c.expectExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO"); info["recommend"] != r[1]["id"] {
		t.Fatalf("hot %v want %v", info["recommend"], r[1]["id"])
	}
}

// TestRefineStoneAndElement：3 颗 1 级太阳石 → 1 颗 2 级（扣 10000 银两）；淬元沙洗成粒子、再还原。
func TestRefineStoneAndElement(t *testing.T) {
	c, p, srv := actWorld(t, "gem")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Cash, ro.Gold = 100000, 1000; return true })
	p.mu.Lock()
	p.addItem(10141, 4)
	p.addItem(cuiYuanSha, 5)
	p.mu.Unlock()
	c.sendExt("ITEM_PTO.C_REFINE_STONE", xym.Msg{"type": 10141, "num": 1})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	if p.itemCount(10141) != 1 || p.itemCount(10142) != 1 || srv.roles.Get(p.UID).Cash != 90000 {
		t.Fatalf("stone %d %d cash %d", p.itemCount(10141), p.itemCount(10142), srv.roles.Get(p.UID).Cash)
	}
	p.mu.Lock()
	p.addItem(10149, 3)
	p.mu.Unlock()
	c.sendExt("ITEM_PTO.C_REFINE_STONE", xym.Msg{"type": 10149, "num": 1})
	c.expectMsg(t, "不能再合成")

	c.sendExt("ETC_PTO.C_ON_GET_ELEMENT_PRICE", nil)
	c.expectExt("ETC_PTO.S_SEND_ELEMENT_PRICE_INFO")
	if gamedata.Items[10132] == nil {
		t.Fatal("no element item 10132")
	}
	c.sendExt("ITEM_PTO.C_REFRESH_ELEMENT", xym.Msg{"type": 10132, "count": 3})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	if p.itemCount(10132) != 3 || p.itemCount(cuiYuanSha) != 2 {
		t.Fatalf("element %d sand %d", p.itemCount(10132), p.itemCount(cuiYuanSha))
	}
	c.sendExt("ITEM_PTO.C_ELEMENT_REFRESH_BACK", xym.Msg{"itemid": 10132, "count": 2})
	c.until(t, "ITEM_PTO.S_REFRESH_BACK_OK")
	if p.itemCount(10132) != 1 || p.itemCount(cuiYuanSha) != 4 {
		t.Fatalf("back element %d sand %d", p.itemCount(10132), p.itemCount(cuiYuanSha))
	}
}

// TestHonglu：六格；回炉一件 5 星武器扣 4 颗浴火丹，换到亮着的一格、那格变灰；刷新用免费次数并进入冷却。
func TestHonglu(t *testing.T) {
	c, p, _ := actWorld(t, "hl")
	c.sendExt("ETC_PTO.C_OPEN_HONGLU", xym.Msg{"type": hlTypeSuit})
	m := c.expectExt("ETC_PTO.S_SEND_HONGLU_INFO")
	if len(m["list"].([]xym.Msg)) != hlSlots || num(m, "left_refresh") != hlFreeRefresh || num(m, "today_time") != 0 {
		t.Fatalf("open %v", m)
	}
	var weapon int
	for _, id := range gamedata.SuitsByStar[5] {
		if gamedata.Suits[id].Kind == "ITK_WEAPON" {
			weapon = id
			break
		}
	}
	p.mu.Lock()
	old := p.addSuit(weapon)
	p.addItem(yuhuoDan, 10)
	p.mu.Unlock()
	c.sendExt("ETC_PTO.C_HONGLU_HUILU", xym.Msg{"type": hlTypeSuit, "suit_num": old.ItemNum, "magic_pos": 0})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	info := got["ETC_PTO.S_SEND_HONGLU_INFO"]
	gray := 0
	var newType int
	for _, x := range info["list"].([]xym.Msg) {
		if num(x, "available") == 0 {
			gray++
			newType = num(x, "equip_type")
		}
	}
	if gray != 1 || num(info, "today_time") != 1 || p.itemCount(yuhuoDan) != 6 || p.suitByNum(old.ItemNum) != nil {
		t.Fatalf("huilu %v dan %d", info, p.itemCount(yuhuoDan))
	}
	if r := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg); len(r) != 1 || num(r[0], "type") != newType {
		t.Fatalf("reward %v want %d", r, newType)
	}
	c.sendExt("ETC_PTO.C_REFRESH_HONGLU", xym.Msg{"type": hlTypeMagic})
	if m := c.expectExt("ETC_PTO.S_SEND_HONGLU_INFO"); num(m, "left_refresh") != hlFreeRefresh-1 || num(m, "left_time") <= 0 {
		t.Fatalf("refresh %v", m)
	}
}

// TestOneClickEquip：有缘装备优先于战力更高的同部位装备；其余空位按战力补上。
func TestOneClickEquip(t *testing.T) {
	c, p, _ := actWorld(t, "oc")
	// 找一张有「有缘装备」缘分的卡（有缘武器不能同时是阵上其他卡的缘分），换到出战第 1 位
	others := map[int32]bool{}
	for fp := 2; fp <= 8; fp++ {
		if pet := p.petAtFightPos(fp); pet != nil {
			for _, rid := range gamedata.Pets[pet.Type].Relate {
				if r := gamedata.Relates[rid]; r != nil {
					for _, e := range r.NEquip {
						others[e] = true
					}
				}
			}
		}
	}
	var petType int32
	var relSuit int
	ids := slices.Sorted(maps.Keys(gamedata.Pets))
	for _, id := range ids {
		for _, rid := range gamedata.Pets[id].Relate {
			if r := gamedata.Relates[rid]; r != nil && len(r.NEquip) > 0 && !others[r.NEquip[0]] && gamedata.Suits[int(r.NEquip[0])] != nil &&
				gamedata.Suits[int(r.NEquip[0])].Kind == "ITK_WEAPON" && len(r.Att)+len(r.HP) > 0 {
				petType, relSuit = id, int(r.NEquip[0])
			}
		}
		if petType != 0 {
			break
		}
	}
	p.mu.Lock()
	pet := p.petAtFightPos(1)
	pet.Type = petType
	rel := p.addSuit(relSuit)
	var strong *Suit
	for _, id := range gamedata.SuitsByStar[5] {
		if si := gamedata.Suits[id]; si.Kind == "ITK_WEAPON" && id != relSuit {
			strong = p.addSuit(id)
			strong.Grade = 50
			break
		}
	}
	p.mu.Unlock()
	c.sendExt("ITEM_PTO.C_ON_ONE_CLICK_EQUIP", nil)
	got := c.until(t, "LINEUP_PTO.S_SEND_LINEUP")
	if got["ITEM_PTO.S_WEAR_BOOK"] == nil || got["ITEM_PTO.S_WEAR_SUIT"] == nil {
		t.Fatalf("replies %v", got)
	}
	sl := p.fightSlot(1)
	if sl.Suit[0] != rel.Pos {
		t.Fatalf("slot1 weapon pos %d, relate suit %d, strong %d", sl.Suit[0], rel.Pos, strong.Pos)
	}
	worn := false
	for fp := 2; fp <= 8; fp++ {
		if s := p.fightSlot(fp); s != nil && s.Suit[0] == strong.Pos {
			worn = true
		}
	}
	if !worn && p.petAtFightPos(2) != nil {
		t.Fatal("strong weapon not given to another pet")
	}
}

// TestYunyou：9 件货；贡献够时买到魂魄 / 护法、扣可用贡献但称号不降、该格售罄；贡献不足拒绝。
func TestYunyou(t *testing.T) {
	c, p, _ := actWorld(t, "yy")
	p.mu.Lock()
	p.xianling(rand.New(rand.NewPCG(1, 1))).Contribute = 1000
	p.mu.Unlock()
	c.sendExt("ETC_PTO.C_YUNYOU_SHOP_OPEN", nil)
	m := c.expectExt("ETC_PTO.S_SEND_YUNYOU_SHOP_INFO")
	l := m["list"].([]xym.Msg)
	if len(l) != 9 || num(m, "money") != 1000 || num(m, "left_refresh") != yyFreeRefresh {
		t.Fatalf("open %v", m)
	}
	soul := l[3] // 热点 5 星魂魄
	before := p.Souls[int32(num(soul, "goods_id"))]
	c.sendExt("ETC_PTO.C_YUNYOU_SHOP_BUY_GOODS", xym.Msg{"pos": 3, "type": soul["type"], "goods_id": soul["goods_id"]})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	info := got["ETC_PTO.S_SEND_YUNYOU_SHOP_INFO"]
	if num(info, "money") != 1000-num(soul, "money") || num(info["list"].([]xym.Msg)[3], "state") != 1 ||
		p.Souls[int32(num(soul, "goods_id"))] != before+int32(num(soul, "num")) || p.Xianling.title() != 4 {
		t.Fatalf("buy %v", info)
	}
	c.sendExt("ETC_PTO.C_YUNYOU_SHOP_BUY_GOODS", xym.Msg{"pos": 3, "type": soul["type"], "goods_id": soul["goods_id"]})
	c.expectMsg(t, "卖完")
	c.expectExt("ETC_PTO.S_SEND_YUNYOU_SHOP_INFO")
	card := l[0] // 5 星护法 1500 > 剩余贡献
	c.sendExt("ETC_PTO.C_YUNYOU_SHOP_BUY_GOODS", xym.Msg{"pos": 0, "type": card["type"], "goods_id": card["goods_id"]})
	c.expectMsg(t, "贡献不足")
	c.expectExt("ETC_PTO.S_SEND_YUNYOU_SHOP_INFO")
	card = l[1] // 4 星护法 500
	c.sendExt("ETC_PTO.C_YUNYOU_SHOP_BUY_GOODS", xym.Msg{"pos": 1, "type": card["type"], "goods_id": card["goods_id"]})
	got = c.until(t, "ETC_PTO.S_REWARD_MSG")
	if r := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg); len(r) != 1 || num(r[0], "rtype") != rtCard || p.petByType(int32(num(card, "goods_id"))) == nil {
		t.Fatalf("card %v", r)
	}
}
