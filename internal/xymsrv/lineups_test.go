package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// TestMultiLineups：50 级 3 套免费阵容；买第 4 套扣 500 元宝；计算战力；切换阵容里的一键装备只回结果不存档。
func TestMultiLineups(t *testing.T) {
	c, p, srv := actWorld(t, "lu")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 2000; return true })
	c.sendExt("LINEUP_PTO.C_GET_LINEUPS", nil)
	m := c.expectExt("LINEUP_PTO.S_SEND_LINEUP")
	if num(m, "count") != 3 || len(m["lineups"].([]xym.Msg)) != 3 {
		t.Fatalf("lineups %v", m)
	}

	c.sendExt("LINEUP_PTO.C_BUY_LINEUP", xym.Msg{"times": 1})
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	if m := c.expectExt("LINEUP_PTO.S_SEND_LINEUP"); num(m, "count") != 4 || srv.roles.Get(p.UID).Gold != 1500 {
		t.Fatalf("buy %v gold %d", m, srv.roles.Get(p.UID).Gold)
	}
	c.sendExt("LINEUP_PTO.C_BUY_LINEUP", xym.Msg{"times": 1}) // 重复的 times
	c.expectExt("ETC_PTO.S_RET_MSG")
	c.expectExt("LINEUP_PTO.S_SEND_LINEUP")

	team := p.lineupsMsg()["lineups"].([]xym.Msg)[0]
	req := xym.Msg{"cno": 2, "lineups": []xym.Msg{{"lineup_no": 2, "leader": 1, "info": team["info"]}}}
	c.sendExt("LINEUP_PTO.C_CALC_FIGHTPOINT", req)
	if fp := c.expectExt("LINEUP_PTO.S_SEND_FIGHTPOINT"); num(fp, "cno") != 2 || num(fp, "fightpoint") <= 0 {
		t.Fatalf("fightpoint %v", fp)
	}

	p.mu.Lock()
	w := p.addSuit(1001)
	p.mu.Unlock()
	c.sendExt("LINEUP_PTO.C_ONE_CLICK_EQUIP", req)
	r := c.expectExt("LINEUP_PTO.S_SEND_ONE_CLICK_EQUIP_LINEUP")
	lus := r["lineups"].([]xym.Msg)
	worn := false
	for _, x := range lus[0]["info"].([]xym.Msg) {
		worn = worn || num(x, "spos1") == w.Pos
	}
	if num(r, "cno") != 2 || !worn || p.lineup(2).Slots[0].Pet != 0 {
		t.Fatalf("one click %v", r)
	}
}

// TestLuckyItems：奥义秘录给 5 星法术碎片 / 法术 / 奥义残片之一；神兵精铁满 20 合成一件 5 星装备，不满提示。
func TestLuckyItems(t *testing.T) {
	c, p, _ := actWorld(t, "lucky")
	p.mu.Lock()
	it := p.addItem(11106, 3)[0]
	p.mu.Unlock()
	c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": it.Pos, "type": 11106, "use_num": 3, "choose": 0, "usetype": 1})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	if l := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg); len(l) == 0 || p.itemCount(11106) != 0 {
		t.Fatalf("秘录 %v left %d", l, p.itemCount(11106))
	}

	p.mu.Lock()
	it = p.addItem(11129, 19)[0]
	p.mu.Unlock()
	c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": it.Pos, "type": 11129, "use_num": 20, "choose": 0, "usetype": 1})
	c.expectExt("ETC_PTO.S_RET_MSG")
	p.mu.Lock()
	p.addItem(11129, 1)
	suits := len(p.Suits)
	p.mu.Unlock()
	c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": it.Pos, "type": 11129, "use_num": 20, "choose": 0, "usetype": 1})
	got = c.until(t, "ETC_PTO.S_REWARD_MSG")
	if l := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg); len(l) != 1 || num(l[0], "rtype") != rtEquip || len(p.Suits) != suits+1 || p.itemCount(11129) != 0 {
		t.Fatalf("精铁 %v", l)
	}
}

// TestSwitchLineupWear：切到另一套阵容，S_WEAR_SUIT 发新阵容的穿戴；切回来恢复原来的。
func TestSwitchLineupWear(t *testing.T) {
	c, p, _ := actWorld(t, "swl")
	p.mu.Lock()
	w := p.addSuit(1001)
	p.fightSlot(1).Suit[0] = w.Pos
	l1 := p.lineupsMsg()["lineups"].([]xym.Msg)
	p.mu.Unlock()
	empty := xym.Msg{"lineup_no": 2, "leader": 1, "info": []xym.Msg{{"pos": 1, "pet": l1[0]["info"].([]xym.Msg)[0]["pet"],
		"spos1": 0, "s_talent_1": 0, "spos2": 0, "s_talent_2": 0, "spos3": 0, "s_talent_3": 0, "mpos1": 0, "mpos2": 0}}}
	weapon := func(got map[string]xym.Msg) int {
		return num(got["ITEM_PTO.S_WEAR_SUIT"]["wear"].([]xym.Msg)[0], "weapon_item")
	}
	c.sendExt("LINEUP_PTO.C_SET_LINEUPS", xym.Msg{"fno": 2, "lineups": []xym.Msg{l1[0], empty}})
	if got := c.until(t, "LINEUP_PTO.S_SEND_LINEUP"); weapon(got) != 0 {
		t.Fatalf("lineup 2 weapon %d", weapon(got))
	}
	c.sendExt("LINEUP_PTO.C_SET_LINEUPS", xym.Msg{"fno": 1, "lineups": []xym.Msg{l1[0], empty}})
	if got := c.until(t, "LINEUP_PTO.S_SEND_LINEUP"); weapon(got) != int(w.ItemNum) {
		t.Fatalf("back to lineup 1 weapon %d want %d", weapon(got), w.ItemNum)
	}
}

// TestLineupNeedLeader：阵容有护法但队长位是空的（或没给队长）时拒绝保存，回原阵容；空阵容可以保存。
func TestLineupNeedLeader(t *testing.T) {
	c, p, _ := actWorld(t, "noleader")
	info := []xym.Msg{}
	for pos := 1; pos <= lineupSlots; pos++ {
		pet := 0
		if pos == 2 {
			pet = 2
		}
		info = append(info, xym.Msg{"pos": pos, "pet": pet})
	}
	before := p.lineup(1).Slots[0].Pet
	c.sendExt("LINEUP_PTO.C_SET_LINEUPS", xym.Msg{"fno": 1, "lineups": []xym.Msg{{"lineup_no": 1, "leader": 1, "info": info}}})
	c.expectMsg(t, "设置队长")
	c.expectExt("LINEUP_PTO.S_SEND_LINEUP")
	if p.lineup(1).Slots[0].Pet != before {
		t.Fatal("lineup saved without leader")
	}
	c.sendExt("LINEUP_PTO.C_SET_LINEUPS", xym.Msg{"fno": 1, "lineups": []xym.Msg{{"lineup_no": 1, "leader": 2, "info": info}}})
	c.until(t, "LINEUP_PTO.S_SEND_LINEUP")
	if p.lineup(1).Slots[1].Pet != 2 || p.lineup(1).Leader != 2 {
		t.Fatalf("lineup with leader not saved: %+v", p.lineup(1))
	}
}
