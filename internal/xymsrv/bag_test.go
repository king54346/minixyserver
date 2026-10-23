package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// until 一直读到 name 为止，返回途中收到的各条消息（同名取最后一条）；收到提示文字就失败。
func (c *client) until(t *testing.T, name string) map[string]xym.Msg {
	t.Helper()
	got := map[string]xym.Msg{}
	for {
		main, body := c.recv()
		if main == xym.SMsg {
			t.Fatalf("waiting %s, got message %q", name, body)
		}
		_, spec, m, err := xym.DecodeExt(xym.S2C, body)
		if err != nil {
			t.Fatal(err)
		}
		got[specName(spec)] = m
		if specName(spec) == name {
			return got
		}
	}
}

// TestBag：开宝箱（宝箱钥匙各扣一个、得魂值）、黄金十连、银锭、多选一礼包、穿装备加属性、出售。
func TestBag(t *testing.T) {
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	enterWorld(t, addr, "bagger").conn.Close()
	acc, _ := srv.accounts.Login("bagger", "pw", "")
	p, _ := srv.players.Get(acc.UID)
	var attSuit int
	for id, si := range gamedata.Suits {
		if si.Prop == "att" && si.Star == 1 && (attSuit == 0 || id < attSuit) {
			attSuit = id
		}
	}
	p.addItem(10006, 2) // 黑铁宝箱 ×2，钥匙只有 1 把
	p.addItem(10007, 1)
	p.addItem(10002, 10) // 黄金宝箱、钥匙各 10：十连
	p.addItem(10003, 10)
	p.addItem(10092, 3) // 小银锭（1000）
	p.addItem(11477, 1) // 多选一
	suit := p.addSuit(attSuit)
	cash0 := srv.roles.Get(acc.UID).Cash

	c := dial(t, addr)
	c.sendRaw(xym.CLogin, loginBody("bagger", "pw"))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
	c.collect()

	c.sendExt("ITEM_PTO.C_GET_ITEMS", nil)
	items := map[int64]xym.Msg{}
	for _, it := range c.expectExt("ITEM_PTO.S_UPDATE_ITEM")["items"].([]xym.Msg) {
		items[it["type"].(int64)] = it
	}
	if len(items) != 6 {
		t.Fatalf("items %v", items)
	}
	use := func(typ int64, n int64, choose int64) map[string]xym.Msg {
		t.Helper()
		c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": items[typ]["pos"], "type": typ, "use_num": n, "choose": choose, "usetype": 0})
		return c.until(t, "ETC_PTO.S_REWARD_MSG")
	}

	got := use(10006, 2, 0) // 只有 1 把钥匙：开 1 个，回 BOX_ITEM 带魂值
	rm := got["ETC_PTO.S_REWARD_MSG"]
	if l := rm["list"].([]xym.Msg); len(l) != 1 || rm["type"] != int64(boxItem) || rm["soul"].(int64) < 5 || rm["soul"].(int64) > 6 {
		t.Fatalf("chest reward %v", rm)
	}
	if got["ITEM_PTO.S_REMOVE_ITEM"] == nil || p.itemCount(10007) != 0 || p.itemCount(10006) != 1 {
		t.Fatalf("key/chest not consumed: key %d chest %d", p.itemCount(10007), p.itemCount(10006))
	}
	if a := got["ETC_PTO.S_ATTR_UPDATE"]["attr"].([]xym.Msg)[0]; a["name"] != "soulexp" || a["val"] != rm["soul"] {
		t.Fatalf("soulexp %v", a)
	}

	c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": items[10006]["pos"], "type": 10006, "use_num": 1, "choose": 0, "usetype": 0})
	if main, _ := c.recv(); main != xym.SMsg { // 没钥匙了
		t.Fatal("chest without key should be refused")
	}

	// 黄金十连：钥匙发起，回 BOX_TEN（10 项）+ 魂值那条
	c.sendExt("ETC_PTO.C_OPEN_TEN_BOX", xym.Msg{"type": 10003})
	got = c.until(t, "ETC_PTO.S_REWARD_MSG")
	if rm := got["ETC_PTO.S_REWARD_MSG"]; rm["type"] != int64(boxTen) || rm["item"] != int64(10003) || len(rm["list"].([]xym.Msg)) != 10 {
		t.Fatalf("ten %v", rm)
	}
	if rmv, upd := got["ITEM_PTO.S_REMOVE_ITEM"], got["ITEM_PTO.S_UPDATE_ITEM"]; rmv != nil && upd != nil {
		for _, a := range rmv["items"].([]xym.Msg) { // 扣光的格子被新道具占用时不能再删
			for _, b := range upd["items"].([]xym.Msg) {
				if a["pos"] == b["pos"] {
					t.Fatalf("pos %v both updated and removed", a["pos"])
				}
			}
		}
	}
	if rm := c.expectExt("ETC_PTO.S_REWARD_MSG"); rm["soul"].(int64) < 200 || rm["soul"].(int64) > 230 {
		t.Fatalf("ten soul %v", rm)
	}
	if p.itemCount(10003) != 0 || p.itemCount(10002) > 10 {
		t.Fatalf("gold key %d box %d", p.itemCount(10003), p.itemCount(10002))
	}

	if rm := use(10092, 3, 0)["ETC_PTO.S_REWARD_MSG"]; rm["list"].([]xym.Msg)[0]["type"] != int64(itemCash) || rm["list"].([]xym.Msg)[0]["count"] != int64(3000) {
		t.Fatalf("silver %v", rm)
	}
	choose := int64(gamedata.Items[11477].Choose[0])
	if rm := use(11477, 1, choose)["ETC_PTO.S_REWARD_MSG"]; len(rm["list"].([]xym.Msg)) == 0 {
		t.Fatalf("choose reward %v", rm)
	}
	if role := srv.roles.Get(acc.UID); role.Cash < cash0+3000 {
		t.Fatalf("cash %d -> %d", cash0, role.Cash)
	}

	// 穿装备：出战位 1 的卡攻击增加 add[0]+add[1]*grade
	pet := p.petAtFightPos(1)
	att0, _ := p.petStats(pet)
	c.sendExt("ITEM_PTO.C_SUIT_ON", xym.Msg{"fight_pos": 1, "itemnum": suit.ItemNum})
	got = c.until(t, "PET_PTO.S_UPDATE_PET")
	if got["ITEM_PTO.S_WEAR_SUIT"] == nil {
		t.Fatal("no S_WEAR_SUIT")
	}
	att1, _ := p.petStats(pet)
	if want := att0 + int32(gamedata.Suits[attSuit].Bonus(suit.Grade)); att1 != want || want == att0 {
		t.Fatalf("att %d -> %d, want %d", att0, att1, want)
	}

	// 穿着的不能卖；脱下后能卖
	c.sendExt("ITEM_PTO.C_SELL_SUIT", xym.Msg{"suits": []xym.Msg{{"itemnum": suit.ItemNum}}})
	if main, _ := c.recv(); main != xym.SMsg {
		t.Fatal("selling worn suit should be refused")
	}
	c.sendExt("ITEM_PTO.C_SUIT_OFF", xym.Msg{"fight_pos": 1, "wear_pos": suitSlot[gamedata.Suits[attSuit].Kind] + 1})
	c.until(t, "PET_PTO.S_UPDATE_PET")
	if a, _ := p.petStats(pet); a != att0 {
		t.Fatalf("att after off %d, want %d", a, att0)
	}
	c.sendExt("ITEM_PTO.C_SELL_SUIT", xym.Msg{"suits": []xym.Msg{{"itemnum": suit.ItemNum}}})
	c.until(t, "ETC_PTO.S_ATTR_UPDATE")
	if p.suitByNum(suit.ItemNum) != nil {
		t.Fatal("suit not sold")
	}
}

// TestBagExpandPrice：装备 / 法术背包扩充 100 元宝起、每次 ×3，600 格封顶；护法固定 300。
func TestBagExpandPrice(t *testing.T) {
	p := newPlayer(1)
	for i, want := range []int32{100, 300, 900, 2700} {
		if got := p.bagExpandPrice(bagSuit); got != want {
			t.Fatalf("buy %d: %d, want %d", i, got, want)
		}
		p.BagBuys[bagSuit]++
	}
	p.BagLimit[bagBook] = gearBagMax
	if p.bagExpandPrice(bagBook) != 0 || p.bagExpandPrice(bagPet) != bagExpandGold {
		t.Fatal("cap / pet price")
	}
}
