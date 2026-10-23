package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestTJRules：点数 / 灵气与抓包里的 S_SEND_TIANJIANG_INFO 对得上，等级上限照客户端。
func TestTJRules(t *testing.T) {
	cases := []struct {
		kind, star int
		e          tjEntry
		cost, lq   int32
	}{
		{tjTypeSuit, 4, tjEntry{}, 50, 20},                      // 抓包：4 星 0 阶 cost 50 / 灵气 20
		{tjTypeMagic, 4, tjEntry{}, 50, 20},                     // 同上
		{tjTypeMagic, 5, tjEntry{Lv: 2, Points: 100}, 100, 90},  // 抓包：5 星法术 2 阶 cost 100 / 90
		{tjTypeMagic, 5, tjEntry{Lv: 4, Points: 300}, 200, 150}, // 抓包：5 星法术 4 阶 cost 200 / 150
		{tjTypeSuit, 5, tjEntry{Lv: 4, Points: 300}, 200, 150},  // 抓包：5 星装备 4 阶 cost 200 / 150
		{tjTypeSuit, 5, tjEntry{Lv: 5, Points: 500}, 0, 0},      // 满阶全 0
		{tjTypeSuit, 5, tjEntry{Lv: 1, Points: 100}, 0, 60},     // 器魂到「勇」后，「武」不再要点数
		{tjTypeSuit, 4, tjEntry{Lv: 2, Points: 100}, 100, 90},
	}
	for _, c := range cases {
		e := c.e
		if cost, lq := tjCost(c.kind, c.star, &e); cost != c.cost || lq != c.lq {
			t.Errorf("kind %d star %d %+v: cost %d lingqi %d, want %d %d", c.kind, c.star, c.e, cost, lq, c.cost, c.lq)
		}
	}
	// 用户给的累计件数：5 星装备 1/1/2/3/5 件（×100），其余 1/2/4/6/10 件（×50）
	for i, n := range []int32{1, 1, 2, 3, 5} {
		if lv := tjLevelFor(tjTypeSuit, 5, n*100, 5); lv < i+1 {
			t.Errorf("5★ suit %d items -> lv %d, want >= %d", n, lv, i+1)
		}
	}
	for i, n := range []int32{1, 2, 4, 6, 10} {
		if lv := tjLevelFor(tjTypeMagic, 5, n*50, 5); lv != i+1 {
			t.Errorf("5★ magic %d items -> lv %d, want %d", n, lv, i+1)
		}
	}
	for grade, want := range map[int]int{29: 0, 30: 2, 39: 2, 40: 4, 49: 4, 50: 5} {
		if got := tjGradeCap(grade); got != want {
			t.Errorf("grade %d cap %d, want %d", grade, got, want)
		}
	}
}

// TestTianJiang：50 级号，10 件同名 5 星武器（1 件穿在 1 号位）、5 星 / 4 星法术书：
// 同名直升「武」、器魂不足拒绝、分解得器魂、器魂升「尊」、4 星只能同名、加成生效。
func TestTianJiang(t *testing.T) {
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	enterWorld(t, addr, "tj").conn.Close()
	acc, _ := srv.accounts.Login("tj", "pw", "")
	srv.roles.Update(acc.UID, func(ro *Role) bool { ro.Grade, ro.Gold = 50, 10000; return true })
	p, _ := srv.players.Get(acc.UID)
	weapon := 0
	for _, id := range gamedata.SuitsByStar[5] {
		if si := gamedata.Suits[id]; si.Kind == "ITK_WEAPON" && si.Prop == "att" {
			weapon = id
			break
		}
	}
	var weapons []*Suit
	for range 10 {
		weapons = append(weapons, p.addSuit(weapon))
	}
	worn := weapons[0]
	p.fightSlot(1).Suit[suitSlot["ITK_WEAPON"]] = worn.Pos
	m5, m4 := gamedata.BooksByStar[5][0], gamedata.BooksByStar[4][0]
	var b5 []*Book
	for range 11 {
		b5 = append(b5, p.addBook(m5))
	}
	b4a, b4b := p.addBook(m4), p.addBook(m4)
	p.fightSlot(1).Book[0] = b5[0].Pos
	d := &bagDelta{}
	p.addIron(weapon, 5, d)
	pet := p.petAtFightPos(1)
	att0, _ := p.petStats(pet)
	bonus := gamedata.Suits[weapon].Bonus(worn.Grade)

	c := dial(t, addr)
	c.sendRaw(xym.CLogin, loginBody("tj", "pw"))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
	c.collect()

	c.sendExt("CARD_SET_PTO.C_GET_TJINFO", xym.Msg{"type": tjTypeSuit})
	info := c.expectExt("CARD_SET_PTO.S_SEND_TIANJIANG_INFO")
	l := info["list"].([]xym.Msg)
	if len(l) != 1 || l[0]["itype"] != int64(weapon) || l[0]["cost"] != int64(100) || l[0]["lingqi_cost"] != int64(20) || l[0]["lv"] != int64(0) {
		t.Fatalf("tj info %v", info)
	}
	upAll := func(req xym.Msg) map[string]xym.Msg {
		t.Helper()
		c.sendExt("CARD_SET_PTO.C_ON_TJUP", req)
		return c.untilSkipMsg(t, "CARD_SET_PTO.S_SEND_TJ_UP_OK")
	}
	up := func(req xym.Msg) xym.Msg { t.Helper(); return upAll(req)["CARD_SET_PTO.S_SEND_TJ_UP_OK"] }

	// 5 星装备 0 阶，1 件同名 → 直接「武」，元宝 (20+60)×10
	got := upAll(xym.Msg{"type": tjTypeSuit, "target_type": weapon, "method": tjMethodSame,
		"cost_type": tjCostGold, "list": []xym.Msg{{"item": weapons[1].ItemNum}}})
	if got["CARD_SET_PTO.S_SEND_TJ_UP_OK"]["ok"] != int64(1) || got["ITEM_PTO.S_REMOVE_SUIT"] == nil || p.suitTJ(weapon) != 2 {
		t.Fatalf("same-name up: %v lv %d", got, p.suitTJ(weapon))
	}
	if role := srv.roles.Get(acc.UID); role.Gold != 10000-800 {
		t.Fatalf("gold %d", role.Gold)
	}
	if att, _ := p.petStats(pet); att != att0+bonus*2/10 {
		t.Fatalf("att %d -> %d, want +%d", att0, att, bonus*2/10)
	}

	// 器魂不足（0）→ ok 0，什么都不扣
	if r := up(xym.Msg{"type": tjTypeSuit, "target_type": weapon, "method": tjMethodSoul, "cost_type": tjCostGold}); r["ok"] != int64(0) || p.suitTJ(weapon) != 2 {
		t.Fatalf("soul up without soul: %v", r)
	}
	// 穿着的不能当材料
	if r := up(xym.Msg{"type": tjTypeSuit, "target_type": weapon, "method": tjMethodSame, "cost_type": tjCostGold,
		"list": []xym.Msg{{"item": worn.ItemNum}}}); r["ok"] != int64(0) {
		t.Fatalf("worn as material: %v", r)
	}

	// 分解：5 件整件 ×20 + 5 个碎片 = 105 器魂
	var items []xym.Msg
	for _, w := range weapons[2:7] {
		items = append(items, xym.Msg{"item": w.ItemNum})
	}
	c.sendExt("CARD_SET_PTO.C_ON_TRANSFER_TO_SOUL", xym.Msg{"type": tjTypeSuit, "itemlist": items,
		"parts": []xym.Msg{{"ptype": weapon}}})
	ok := c.until(t, "CARD_SET_PTO.S_SEND_TJ_TOSOUL_OK")["CARD_SET_PTO.S_SEND_TJ_TOSOUL_OK"]
	if ok["ok"] != int64(1) || ok["soul"] != int64(105) || p.Irons[weapon] != 0 || len(p.Suits) != 10-1-5 {
		t.Fatalf("to soul %v irons %d suits %d", ok, p.Irons[weapon], len(p.Suits))
	}
	// 器魂 100 升「尊」（武→尊 差 100 点）
	if r := up(xym.Msg{"type": tjTypeSuit, "target_type": weapon, "method": tjMethodSoul, "cost_type": tjCostGold}); r["ok"] != int64(1) || p.suitTJ(weapon) != 3 || p.TianJiang.Suit.Soul != 5 {
		t.Fatalf("soul up: %v lv %d soul %d", r, p.suitTJ(weapon), p.TianJiang.Suit.Soul)
	}

	// 4 星法术：不能用法魂；1 本同名 → 「勇」
	if r := up(xym.Msg{"type": tjTypeMagic, "target_type": m4, "method": tjMethodSoul, "cost_type": tjCostGold}); r["ok"] != int64(0) {
		t.Fatalf("4★ soul up: %v", r)
	}
	if r := up(xym.Msg{"type": tjTypeMagic, "target_type": m4, "method": tjMethodSame, "cost_type": tjCostGold,
		"list": []xym.Msg{{"item": b4b.Pos}}}); r["ok"] != int64(1) || p.magicTJ(m4) != 1 || p.bookByPos(b4a.Pos) == nil {
		t.Fatalf("4★ same up: %v lv %d", r, p.magicTJ(m4))
	}
	// 5 星法术 4 本同名 → 0+200 点 = 「尊」；穿着的那本的几率加成 2×3
	var bl []xym.Msg
	for _, b := range b5[1:5] {
		bl = append(bl, xym.Msg{"item": b.Pos})
	}
	if r := up(xym.Msg{"type": tjTypeMagic, "target_type": m5, "method": tjMethodSame, "cost_type": tjCostGold, "list": bl}); r["ok"] != int64(1) || p.magicTJ(m5) != 3 {
		t.Fatalf("5★ magic up: %v lv %d", r, p.magicTJ(m5))
	}
	team, _ := p.playerTeam(xym.Msg{"pos1": int64(1)})
	if len(team) != 1 || len(team[0].BookRate) != 1 || team[0].BookRate[0] != 6 {
		t.Fatalf("book rate %+v", team)
	}

	// 等级上限：降到 35 级，「尊」已超过「武」，不能再升
	srv.roles.Update(acc.UID, func(ro *Role) bool { ro.Grade = 35; return true })
	c.conn.Close()
	c = dial(t, addr)
	c.sendRaw(xym.CLogin, loginBody("tj", "pw"))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
	c.collect()
	if r := up(xym.Msg{"type": tjTypeSuit, "target_type": weapon, "method": tjMethodSame, "cost_type": tjCostGold,
		"list": []xym.Msg{{"item": weapons[7].ItemNum}}}); r["ok"] != int64(0) {
		t.Fatalf("over grade cap: %v", r)
	}
}

// untilSkipMsg 同 until，但跳过提示文字（失败路径先弹提示、再回解锁消息）。
func (c *client) untilSkipMsg(t *testing.T, name string) map[string]xym.Msg {
	t.Helper()
	got := map[string]xym.Msg{}
	for {
		main, body := c.recv()
		if main == xym.SMsg {
			continue
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

// TestPetTJ：神通激发等级 = 所穿有缘 5 星法术书 / 衣服 / 武器的天将阶（官方抓包：东华帝君 tlxlv 2/5/5）。
func TestPetTJ(t *testing.T) {
	p := newPlayer(1)
	p.role = &Role{Grade: 50}
	pet := p.addPet(35042) // 东华帝君：缘分有 大鹏展翅 14505、郁金流苏簪 1102、神农尺 1040
	sl := p.fightSlot(1)
	sl.Pet = pet.BagPos
	var books []*Book
	for range 10 {
		books = append(books, p.addBook(14505))
	}
	var shirts, weapons []*Suit
	for range 5 {
		shirts = append(shirts, p.addSuit(1102))
		weapons = append(weapons, p.addSuit(1040))
	}
	sl.Book[0] = books[0].Pos
	sl.Suit[suitSlot["ITK_SHIRT"]] = shirts[0].Pos
	sl.Suit[suitSlot["ITK_WEAPON"]] = weapons[0].Pos
	tj := p.tj()
	tj.book(tjTypeMagic).entry(14505).Lv = 2
	tj.book(tjTypeSuit).entry(1102).Lv = 5
	tj.book(tjTypeSuit).entry(1040).Lv = 3
	if got := p.petTJ(pet); got != [3]int{2, 5, 3} {
		t.Fatalf("tlxlv %v, want [2 5 3]", got)
	}
	if m := p.petFull(pet); m["tlxlv1"] != 2 || m["tlxlv2"] != 5 || m["tlxlv3"] != 3 {
		t.Fatalf("S_UPDATE_PET tlxlv %v %v %v", m["tlxlv1"], m["tlxlv2"], m["tlxlv3"])
	}
	pi := gamedata.Pets[35042]
	pet.SoulGrade = 5 // 三个神通都开启（pet_talent.go）
	if _, got := p.activeTalents(pet, pi); len(got) != 3 || got[0] != 2 {
		t.Fatalf("talent TJ %v", got)
	}

	p.role.Grade = 29 // 30 级开放
	if got := p.petTJ(pet); got != [3]int{} {
		t.Fatalf("grade 29: %v", got)
	}
	p.role.Grade = 50
	sl.Book[0] = p.addBook(13401).Pos // 4 星书不激发
	if got := p.petTJ(pet); got[0] != 0 {
		t.Fatalf("4-star book awakened: %v", got)
	}
}
