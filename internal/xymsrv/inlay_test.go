package xymsrv

import (
	"math/rand/v2"
	"slices"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func suitOfKind(kind string, star int) int {
	for _, id := range gamedata.SuitsByStar[star] {
		if gamedata.Suits[id].Kind == kind {
			return id
		}
	}
	return 0
}

// TestInlay：武器只能镶太阳石 / 红玛瑙；换更高级的返还原宝石，低级的拒绝；凿晶石取出。
func TestInlay(t *testing.T) {
	c, p, _ := actWorld(t, "gem2")
	p.mu.Lock()
	w := p.addSuit(suitOfKind("ITK_WEAPON", 5))
	w.Grade = 20
	p.addItem(10145, 1) // 5 级太阳石
	p.addItem(10148, 1) // 8 级太阳石
	p.addItem(10143, 1)
	p.addItem(10165, 1) // 光芒石（衣服用）
	p.addItem(zaojingStone, 1)
	p.mu.Unlock()
	add := func(gem int) {
		c.sendExt("ITEM_PTO.C_ADD_PROP_STONE", xym.Msg{"itemnum": w.ItemNum, "stone_type": gem})
	}
	add(10165)
	c.expectExt("ETC_PTO.S_RET_MSG")
	add(10145)
	got := c.until(t, "ITEM_PTO.S_UPDATE_SUIT")
	if s := got["ITEM_PTO.S_UPDATE_SUIT"]["suits"].([]xym.Msg)[0]; num(s, "stone") != 10145 {
		t.Fatalf("inlay %v", s)
	}
	add(10143)
	c.expectExt("ETC_PTO.S_RET_MSG")
	add(10148)
	c.until(t, "ITEM_PTO.S_UPDATE_SUIT")
	if w.Stone != 10148 || p.itemCount(10145) != 1 || p.itemCount(10148) != 0 {
		t.Fatalf("upgrade stone %d back %d", w.Stone, p.itemCount(10145))
	}
	c.sendExt("ITEM_PTO.C_PULL_OUT_SUIT_STONE", xym.Msg{"itemnum": w.ItemNum})
	c.until(t, "ITEM_PTO.S_UPDATE_SUIT")
	if w.Stone != 0 || p.itemCount(10148) != 1 || p.itemCount(zaojingStone) != 0 {
		t.Fatalf("pull out %d", w.Stone)
	}
}

// TestLianshen：20 级以下不能炼；免费次数后扣炼神石；存备用扣凝神石；替换；两件同效果 + 宝石出套装神通。
func TestLianshen(t *testing.T) {
	c, p, _ := actWorld(t, "ls")
	p.mu.Lock()
	w := p.addSuit(suitOfKind("ITK_WEAPON", 5))
	sh := p.addSuit(suitOfKind("ITK_SHIRT", 5))
	p.addItem(lianshenStone, 5)
	p.addItem(ningshenStone, 20)
	p.mu.Unlock()
	c.sendExt("ITEM_PTO.C_REFRESH_SUIT_SKILL", xym.Msg{"itemnum": w.ItemNum})
	c.expectExt("ETC_PTO.S_RET_MSG")
	c.expectExt("ITEM_PTO.S_UPDATE_SUIT")

	p.mu.Lock()
	w.Grade = 50
	p.mu.Unlock()
	for i := range lsFreeDaily + 1 {
		c.sendExt("ITEM_PTO.C_REFRESH_SUIT_SKILL", xym.Msg{"itemnum": w.ItemNum})
		got := c.until(t, "ITEM_PTO.S_REFRESH_SUIT_SKILL")
		if base, _ := lsInfo(w.Skill); base == 0 || num(got["ITEM_PTO.S_UPDATE_SUIT"]["suits"].([]xym.Msg)[0], "skill") != int(w.Skill) {
			t.Fatalf("refresh %d: skill %d", i, w.Skill)
		}
	}
	if p.itemCount(lianshenStone) != 4 {
		t.Fatalf("lianshen stones %d", p.itemCount(lianshenStone))
	}

	_, cost := lsInfo(w.Skill)
	c.sendExt("ITEM_PTO.C_SAVE_SUIT_SKILL", xym.Msg{"itemnum": w.ItemNum})
	if m := c.until(t, "ITEM_PTO.S_REFRESH_SUIT_SKILL")["ITEM_PTO.S_REFRESH_SUIT_SKILL"]; len(m["save_skill"].([]xym.Msg)) != 1 || p.itemCount(ningshenStone) != 20-cost {
		t.Fatalf("save %v", m)
	}
	saved := w.Skill
	c.sendExt("ITEM_PTO.C_REFRESH_SUIT_SKILL", xym.Msg{"itemnum": w.ItemNum})
	c.until(t, "ITEM_PTO.S_REFRESH_SUIT_SKILL")
	c.sendExt("ITEM_PTO.C_REPLACE_SUIT_SKILL", xym.Msg{"itemnum": w.ItemNum, "pos": 1})
	c.until(t, "ITEM_PTO.S_REFRESH_SUIT_SKILL")
	if w.Skill != saved {
		t.Fatalf("replace %d want %d", w.Skill, saved)
	}

	// 套装：武器、衣服同为不侵 5，各镶 9 级宝石、鞋没镶 → ⌊(9+9+0)/3⌋ = 6 级 → 不侵 6
	p.mu.Lock()
	w.Skill, sh.Skill = 20035, 20035
	w.Stone, sh.Stone = 10149, 10169
	sl := p.fightSlot(1)
	sl.Suit[0], sl.Suit[1] = w.Pos, sh.Pos
	tals, gems := p.gearExtras(1)
	p.mu.Unlock()
	if !slices.Equal(tals, []int32{20036}) || gems[0] != 9 || gems[2] != 9 {
		t.Fatalf("set talents %v gems %v", tals, gems)
	}
}

// TestSetLevelFormula：套装效果等级 = ⌊三件宝石等级和/3⌋（短板拉低），不低于炼神效果自身等级。
func TestSetLevelFormula(t *testing.T) {
	_, p, _ := actWorld(t, "setlv")
	p.mu.Lock()
	defer p.mu.Unlock()
	w := p.addSuit(suitOfKind("ITK_WEAPON", 5))
	sh := p.addSuit(suitOfKind("ITK_SHIRT", 5))
	so := p.addSuit(suitOfKind("ITK_SHOE", 4))
	sl := p.fightSlot(1)
	sl.Suit = [3]int{w.Pos, sh.Pos, so.Pos}
	w.Skill, sh.Skill = 20081, 20081 // 兽性 1
	for _, c := range []struct {
		gems [3]int
		want int32
	}{
		{[3]int{10145, 10165, 10185}, 20085}, // 5+5+5 = 15 → 5 级
		{[3]int{10146, 10166, 10182}, 20084}, // 6+6+2 = 14 → 4 级（短板）
		{[3]int{0, 0, 0}, 20081},             // 没宝石：自身 1 级
	} {
		w.Stone, sh.Stone, so.Stone = c.gems[0], c.gems[1], c.gems[2]
		if tals, _ := p.gearExtras(1); !slices.Equal(tals, []int32{c.want}) {
			t.Fatalf("gems %v: %v want %d", c.gems, tals, c.want)
		}
	}
}

// TestShownSetSkill：套装激活后 S_UPDATE_SUIT 的 skill 显示宝石提升后的等级（三件 8 级宝石 → 兽性 8），
// 存档里的炼神效果不变；不再成套时恢复原等级。
func TestShownSetSkill(t *testing.T) {
	_, p, _ := actWorld(t, "shown")
	p.mu.Lock()
	defer p.mu.Unlock()
	w := p.addSuit(suitOfKind("ITK_WEAPON", 5))
	sh := p.addSuit(suitOfKind("ITK_SHIRT", 5))
	so := p.addSuit(suitOfKind("ITK_SHOE", 4))
	p.fightSlot(1).Suit = [3]int{w.Pos, sh.Pos, so.Pos}
	// 武器突袭1、防具突袭1、鞋子仙师1，三颗 5 级宝石 → 都显示 5 级；突袭成套生效，仙师只有一件不生效（客户端标未激活）
	w.Skill, sh.Skill, so.Skill = 21621, 21621, 21201
	w.Stone, sh.Stone, so.Stone = 10145, 10165, 10185
	if ch := p.refreshShownSkills(); len(ch) != 3 || w.msg()["skill"] != int32(21625) || so.msg()["skill"] != int32(21205) || w.Skill != 21621 {
		t.Fatalf("shown %v / %v skill %d", w.msg()["skill"], so.msg()["skill"], w.Skill)
	}
	if tals, _ := p.gearExtras(1); len(tals) != 1 || tals[0] != 21625 {
		t.Fatalf("set talents %v", tals)
	}
	sh.Skill = 20091 // 不成套了：显示仍是宝石等级，但不生效
	p.refreshShownSkills()
	if w.msg()["skill"] != int32(21625) {
		t.Fatalf("after break %v", w.msg()["skill"])
	}
	if tals, _ := p.gearExtras(1); len(tals) != 0 {
		t.Fatalf("set talents after break %v", tals)
	}
}

// TestLianshenLevel1：炼神只出 1 级效果；套装等级由宝石决定（0 宝石也是 1 级，三颗 9 级 → 9 级）；旧存档高级效果归 1 级。
func TestLianshenLevel1(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		if sk := rollSuitSkill(50, rng); sk != func() int32 { b, _ := lsInfo(sk); return b }() {
			t.Fatalf("rolled level>1 skill %d", sk)
		}
	}
	if setBoost(21621+4, 0) != 21621 || setBoost(21621, 27) != 21621+8 || setBoost(21621, 5) != 21621 || setBoost(21621, 30) != 21621+9 {
		t.Fatal("setBoost levels")
	}
	su := &Suit{Skill: 21621 + 7, Saved: []int32{20071 + 4, 20081}}
	if !su.normalizeSkills() || su.Skill != 21621 || su.Saved[0] != 20071 {
		t.Fatalf("normalize %+v", su)
	}
}
