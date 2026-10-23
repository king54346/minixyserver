package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func TestUpTable(t *testing.T) {
	st := upTable(suitUpExp)
	if g, e := st.add(2, 1, 0, 13); g != 5 || e != 0 { // 1+3+4+5
		t.Fatalf("suit 2★ +13 = %d/%d", g, e)
	}
	if g, e := st.add(1, 9, 0, 100); g != 10 || e != 0 { // 1 星满级 10，多余经验清零
		t.Fatalf("suit 1★ max = %d/%d", g, e)
	}
	if g, e := upTable(magicUpExp).add(2, 1, 0, 7); g != 2 || e != 5 {
		t.Fatalf("magic 2★ +7 = %d/%d", g, e)
	}
	// 材料经验：2 星 3 级本级 2 点 = 4 + (1+3) + 2
	if v := (&Suit{Type: gamedata.SuitsByStar[2][0], Grade: 3, Exp: 2}).expValue(); v != 10 {
		t.Fatalf("expValue %d", v)
	}
}

// TestParts：装备强化、法术强化、碎片合成（含通用碎片补足）、拆碎、转化、通用碎片合成。
func TestParts(t *testing.T) {
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	enterWorld(t, addr, "parts").conn.Close()
	acc, _ := srv.accounts.Login("parts", "pw", "")
	srv.roles.Update(acc.UID, func(ro *Role) bool { ro.Grade, ro.Cash = 20, 100000; return true })
	p, _ := srv.players.Get(acc.UID)
	s2, s5, m2 := gamedata.SuitsByStar[2][0], gamedata.SuitsByStar[5][0], gamedata.BooksByStar[2][0]
	target := p.addSuit(s2)
	mat1, mat2 := p.addSuit(s2), p.addSuit(s2)
	worn := p.addSuit(s2)
	p.fightSlot(1).Suit[suitSlot[gamedata.Suits[s2].Kind]] = worn.Pos // 穿着的不能当材料
	book := p.addBook(m2)
	bookMat := p.addBook(m2)
	d := &bagDelta{}
	p.addIron(s2, 5, d)
	p.addIron(s5, 16, d)
	p.addBookPart(m2, 3, d)
	p.addItem(10084, 4)
	p.addItem(10302, 6)

	c := dial(t, addr)
	c.sendRaw(xym.CLogin, loginBody("parts", "pw"))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
	c.collect()

	c.sendExt("ITEM_PTO.C_GET_SUIT_IRONS", nil)
	if l := c.expectExt("ITEM_PTO.S_UPDATE_SUIT_IRON")["irons"].([]xym.Msg); len(l) != 2 {
		t.Fatalf("irons %v", l)
	}
	if l := c.expectExt("ITEM_PTO.S_UPDATE_SUIT_PART_ITEMS")["list"].([]xym.Msg); len(l) != 2 {
		t.Fatalf("suit part items %v", l)
	}

	// 装备强化：2 件 2 星（各 4）+ 5 个 2 星碎片（各 1）= 13 经验 → 5 级，花 13×100×2 银两
	c.sendExt("ITEM_PTO.C_REFINE_SUITS", xym.Msg{"itemnum": target.ItemNum, "to_xiuwei": 0, "from_xiuwei": 0, "cost": 0,
		"suits":      []xym.Msg{{"itemnum": mat1.ItemNum}, {"itemnum": mat2.ItemNum}, {"itemnum": worn.ItemNum}, {"itemnum": target.ItemNum}},
		"suit_irons": []xym.Msg{{"type": s2}}})
	got := c.until(t, "ITEM_PTO.S_UPDATE_SUIT")
	if u := got["ITEM_PTO.S_UPDATE_SUIT"]["suits"].([]xym.Msg)[0]; u["itemnum"] != int64(target.ItemNum) || u["grade"] != int64(5) || u["exp"] != int64(0) {
		t.Fatalf("refine %v", u)
	}
	if r := got["ITEM_PTO.S_REMOVE_SUIT"]["suits"].([]xym.Msg); len(r) != 2 || p.Irons[s2] != 0 || p.suitByNum(worn.ItemNum) == nil {
		t.Fatalf("materials %v irons %d", r, p.Irons[s2])
	}
	if role := srv.roles.Get(acc.UID); role.Cash != 100000-2600 {
		t.Fatalf("cash %d", role.Cash)
	}

	// 等级不能超过人物等级（20）：再喂 1000 点碎片经验会超，拒绝且不扣东西
	p.addIron(s5, 200, d)
	c.sendExt("ITEM_PTO.C_REFINE_SUITS", xym.Msg{"itemnum": target.ItemNum, "to_xiuwei": 0, "from_xiuwei": 0, "cost": 0,
		"suits": []xym.Msg{}, "suit_irons": []xym.Msg{{"type": s5}}})
	if main, _ := c.recv(); main != xym.SMsg {
		t.Fatal("over-grade refine should be refused")
	}
	if u := c.expectExt("ITEM_PTO.S_UPDATE_SUIT")["suits"].([]xym.Msg)[0]; u["grade"] != int64(5) || p.Irons[s5] != 216 {
		t.Fatalf("refused refine changed things: %v irons %d", u, p.Irons[s5])
	}
	p.addIron(s5, -200, d)

	// 法术强化：1 本 2 星（4）+ 3 个 2 星碎片（3）= 7 → 2 级 5 点
	c.sendExt("ITEM_PTO.C_UPGRADE_BOOK", xym.Msg{"target_pos": book.Pos, "target_type": book.Type, "to_xiuwei": 0, "from_xiuwei": 0, "cost": 0,
		"books": []xym.Msg{{"pos": bookMat.Pos, "type": bookMat.Type}}, "book_parts": []xym.Msg{{"type": m2}}})
	got = c.until(t, "ITEM_PTO.S_UPDATE_BOOK")
	if u := got["ITEM_PTO.S_UPDATE_BOOK"]["books"].([]xym.Msg)[0]; u["pos"] != int64(book.Pos) || u["grade"] != int64(2) || u["exp"] != int64(5) {
		t.Fatalf("upgrade book %v", u)
	}
	if got["ITEM_PTO.S_REMOVE_BOOK"] == nil || p.BookParts[m2] != 0 {
		t.Fatalf("book materials %v", got)
	}

	// 5 星碎片 16 个 + 五星装备碎片道具 4 个 = 20，合成一件
	n := len(p.Suits)
	c.sendExt("ITEM_PTO.C_MIX_SUIT", xym.Msg{"type": s5})
	got = c.until(t, "ETC_PTO.S_REWARD_MSG")
	if len(p.Suits) != n+1 || p.Irons[s5] != 0 || p.itemCount(10084) != 0 || got["ITEM_PTO.S_UPDATE_SUIT_PART_ITEMS"] == nil {
		t.Fatalf("mix: suits %d irons %d items %d %v", len(p.Suits), p.Irons[s5], p.itemCount(10084), got)
	}

	// 拆碎 2 星：得 4 个该装备碎片，不花元宝
	c.sendExt("ITEM_PTO.C_BREAK_SUIT", xym.Msg{"suits": []xym.Msg{{"itemnum": target.ItemNum}}})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	if p.Irons[s2] != 4 || p.suitByNum(target.ItemNum) != nil {
		t.Fatalf("break: irons %d", p.Irons[s2])
	}

	// 转化：5 星装备碎片 3 个 -> 五星装备碎片道具 3 个
	p.addIron(s5, 3, d)
	c.sendExt("ITEM_PTO.C_ON_TRANSFER_FRAGMENT", xym.Msg{"op_type": 1, "type": s5, "count": 3, "cls": 5})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	if p.Irons[s5] != 0 || p.itemCount(10084) != 3 {
		t.Fatalf("transfer: irons %d items %d", p.Irons[s5], p.itemCount(10084))
	}

	// 三星装备碎片 6 个「合成」随机 3 星装备
	pos := 0
	for _, it := range p.Items {
		if it.Type == 10302 {
			pos = it.Pos
		}
	}
	n = len(p.Suits)
	c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": pos, "type": 10302, "use_num": 1, "choose": 0, "usetype": 1})
	rm := c.until(t, "ETC_PTO.S_REWARD_MSG")["ETC_PTO.S_REWARD_MSG"]
	l := rm["list"].([]xym.Msg)
	if len(p.Suits) != n+1 || len(l) != 1 || l[0]["rtype"] != int64(rtEquip) || suitStar(int(l[0]["type"].(int64))) != 3 || p.itemCount(10302) != 0 {
		t.Fatalf("part item mix %v", rm)
	}

	// 天生法术强化（等 S_UPDATE_PET）
	pet := p.petAtFightPos(1)
	ms := magicStar(gamedata.Pets[pet.Type].Magic)
	p.addBookPart(m2, 100, d)
	c.sendExt("ITEM_PTO.C_UPGRADE_PET_MAGIC", xym.Msg{"npcnum": pet.NpcNum, "from_xiuwei": 0, "cost": 0,
		"books": []xym.Msg{}, "book_parts": []xym.Msg{{"type": m2}}})
	u := c.until(t, "PET_PTO.S_UPDATE_PET")["PET_PTO.S_UPDATE_PET"]["pets"].([]xym.Msg)[0]
	wantLv, wantExp := upTable(magicUpExp).add(ms, 1, 0, 100)
	if u["tal_magic_lev"] != int64(wantLv) || u["tal_magic_exp"] != int64(wantExp) || wantLv < 2 {
		t.Fatalf("pet magic %v, want %d/%d", u, wantLv, wantExp)
	}
}
