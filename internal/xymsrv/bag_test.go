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
	// 穿脱后也回 S_SEND_LINEUP：客户端的多套阵容数据要跟着更新，否则切换阵容时把旧装备存回来（装备「自己掉了」）
	got = c.until(t, "LINEUP_PTO.S_SEND_LINEUP")
	if got["ITEM_PTO.S_WEAR_SUIT"] == nil || got["PET_PTO.S_UPDATE_PET"] == nil {
		t.Fatal("no S_WEAR_SUIT / S_UPDATE_PET")
	}
	if info := got["LINEUP_PTO.S_SEND_LINEUP"]["lineups"].([]xym.Msg)[0]["info"].([]xym.Msg)[0]; info["spos1"] != int64(suit.Pos) &&
		info["spos2"] != int64(suit.Pos) && info["spos3"] != int64(suit.Pos) {
		t.Fatalf("lineup gear not updated: %v", info)
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
	c.until(t, "LINEUP_PTO.S_SEND_LINEUP")
	if a, _ := p.petStats(pet); a != att0 {
		t.Fatalf("att after off %d, want %d", a, att0)
	}
	c.sendExt("ITEM_PTO.C_SELL_SUIT", xym.Msg{"suits": []xym.Msg{{"itemnum": suit.ItemNum}}})
	c.until(t, "ETC_PTO.S_ATTR_UPDATE")
	if p.suitByNum(suit.ItemNum) != nil {
		t.Fatal("suit not sold")
	}

	// 强化材料（碎晶 2001-2004，Kind 占位 ITK_WEAPON）不能穿戴；历史误穿的登录自愈清掉
	mat := p.addSuit(2001)
	c.sendExt("ITEM_PTO.C_SUIT_ON", xym.Msg{"fight_pos": 1, "itemnum": mat.ItemNum})
	if main, _ := c.recv(); main != xym.SMsg { // 弹提示「强化材料不能穿戴」
		t.Fatal("material wear should be refused with message")
	}
	c.until(t, "ITEM_PTO.S_WEAR_SUIT")
	if sl := p.fightSlot(1); sl.Suit[0] == mat.Pos || sl.Suit[1] == mat.Pos || sl.Suit[2] == mat.Pos {
		t.Fatalf("material worn: %v", sl.Suit)
	}
	p.fightSlot(1).Suit[0] = mat.Pos // 伪造旧存档里的误穿数据
	p.fixAllGear()
	if sl := p.fightSlot(1); sl.Suit[0] == mat.Pos || sl.Suit[1] == mat.Pos || sl.Suit[2] == mat.Pos {
		t.Fatalf("material not healed: %v", sl.Suit)
	}

	// 法术强化材料（17000-17004）同样不能穿戴；历史误穿的登录自愈清掉
	bk := p.addBook(17002)
	c.sendExt("ITEM_PTO.C_BOOK_ON", xym.Msg{"fight_pos": 1, "itemnum": bk.Pos})
	if main, _ := c.recv(); main != xym.SMsg {
		t.Fatal("book material wear should be refused with message")
	}
	c.until(t, "ITEM_PTO.S_WEAR_BOOK")
	if sl := p.fightSlot(1); sl.Book[0] == bk.Pos || sl.Book[1] == bk.Pos {
		t.Fatalf("book material worn: %v", sl.Book)
	}
	p.fightSlot(1).Book[0] = bk.Pos // 伪造旧存档里的误穿数据
	p.fixAllGear()
	if sl := p.fightSlot(1); sl.Book[0] == bk.Pos || sl.Book[1] == bk.Pos {
		t.Fatalf("book material not healed: %v", sl.Book)
	}
}

// TestBagExpandPrice：装备 / 法术背包初始 300 格，每次 100 元宝扩 20 格，1000 格封顶；护法固定 300。
func TestBagExpandPrice(t *testing.T) {
	p := newPlayer(1)
	if p.BagLimit[bagSuit] != gearBagSize || p.BagLimit[bagBook] != gearBagSize {
		t.Fatalf("initial gear bags %v", p.BagLimit)
	}
	for i := range 4 { // 每次都是 100 元宝（不再 ×3）
		if got := p.bagExpandPrice(bagSuit); got != gearBagGold {
			t.Fatalf("buy %d: %d", i, got)
		}
		p.BagBuys[bagSuit]++
	}
	p.BagLimit[bagBook] = gearBagMax
	if p.bagExpandPrice(bagBook) != 0 || p.bagExpandPrice(bagPet) != bagExpandGold {
		t.Fatal("cap / pet price")
	}
}

// TestTianshuPartMix：无字天书碎片按道具说明 20:1 合成无字天书（客户端没有独立合成界面，
// 走「使用」判定）。不足 20 个只提示不扣；use_num 指定份数时按份数合。
func TestTianshuPartMix(t *testing.T) {
	c, p, _ := actWorld(t, "tianshu")
	p.addItem(tianshuPartItem, 19)
	pos := func() int64 {
		for _, it := range p.Items {
			if it.Type == tianshuPartItem {
				return int64(it.Pos)
			}
		}
		return 0
	}()

	c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": pos, "type": tianshuPartItem, "use_num": 1, "choose": 0, "usetype": 0})
	c.expectMsg(t, "需要 20 个无字天书碎片")
	if n := p.itemCount(tianshuPartItem); n != 19 {
		t.Fatalf("不足 20 个不该扣碎片, got %d", n)
	}
	if n := p.itemCount(wuziTianshu); n != 0 {
		t.Fatalf("不足 20 个不该给天书, got %d", n)
	}

	// 补到 25 个：合 1 部（use_num=1），剩 5 个。
	p.addItem(tianshuPartItem, 6)
	c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": pos, "type": tianshuPartItem, "use_num": 1, "choose": 0, "usetype": 0})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	if n := p.itemCount(wuziTianshu); n != 1 {
		t.Fatalf("应合成 1 部无字天书, got %d", n)
	}
	if n := p.itemCount(tianshuPartItem); n != 5 {
		t.Fatalf("应剩 5 个碎片, got %d", n)
	}

	// 再补到 45 个（5+40）：每次「使用」合 1 部，连用两次。
	p.addItem(tianshuPartItem, 40)
	for range 2 {
		c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": pos, "type": tianshuPartItem, "use_num": 1, "choose": 0, "usetype": 0})
		c.until(t, "ETC_PTO.S_REWARD_MSG")
	}
	if n := p.itemCount(wuziTianshu); n != 3 {
		t.Fatalf("应累计 3 部无字天书, got %d", n)
	}
	if n := p.itemCount(tianshuPartItem); n != 5 {
		t.Fatalf("45 个应合 2 部剩 5 个, got %d", n)
	}
}
