package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// TestPetPoint：培养面板；丹药不足提示；第 N 次消耗 N 颗，战力丹 +1 攻击、气血丹 +4 生命，S_UPDATE_PET 带 attadd / hpadd。
func TestPetPoint(t *testing.T) {
	c, p, _ := actWorld(t, "pointer")
	pet := p.sortedPets()[0]
	att0, hp0 := p.petStats(pet)
	npc := int64(pet.NpcNum)

	c.sendExt("PET_PTO.C_OPEN_POINT_PANEL", xym.Msg{"npcnum": npc})
	if m := c.expectExt("PET_PTO.S_OPEN_POINT_PANEL"); num(m, "attadd") != 0 || num(m, "attitem") != 0 {
		t.Fatalf("panel %v", m)
	}
	c.sendExt("PET_PTO.C_ADD_PET_POINT", xym.Msg{"npcnum": npc, "type": pointAttItem})
	c.expectMsg(t, "战力丹不足")
	c.collect()

	p.mu.Lock()
	p.addItem(pointAttItem, 3)
	p.addItem(pointHPItem, 1)
	p.mu.Unlock()
	for i := 1; i <= 2; i++ {
		c.sendExt("PET_PTO.C_ADD_PET_POINT", xym.Msg{"npcnum": npc, "type": pointAttItem})
		got := c.until(t, "PET_PTO.S_OPEN_POINT_PANEL")
		if m := got["PET_PTO.S_OPEN_POINT_PANEL"]; num(m, "attadd") != i || num(m, "attitem") != 3-i*(i+1)/2 {
			t.Fatalf("round %d panel %v", i, m)
		}
		if pm := got["PET_PTO.S_UPDATE_PET"]["pets"].([]xym.Msg)[0]; num(pm, "attadd") != i {
			t.Fatalf("update %v", pm)
		}
	}
	c.sendExt("PET_PTO.C_ADD_PET_POINT", xym.Msg{"npcnum": npc, "type": pointHPItem})
	c.until(t, "PET_PTO.S_OPEN_POINT_PANEL")
	if att, hp := p.petStats(pet); pet.AttAdd != 2 || pet.HPAdd != 1 || p.itemCount(pointAttItem) != 0 ||
		att <= att0 || hp <= hp0 {
		t.Fatalf("after: add %d/%d att %d→%d hp %d→%d", pet.AttAdd, pet.HPAdd, att0, att, hp0, hp)
	}
}

// TestLocks：护法、装备、法术书加锁 / 解锁，回客户端在等的 S_UPDATE_PET / S_UPDATE_SUIT / S_UPDATE_BOOK。
func TestLocks(t *testing.T) {
	c, p, _ := actWorld(t, "locker")
	p.mu.Lock()
	d := &bagDelta{}
	p.giveSuit(1001, d)
	p.giveBook(12101, d)
	pet := p.sortedPets()[0]
	su, bk := p.Suits[len(p.Suits)-1], p.Books[len(p.Books)-1]
	p.mu.Unlock()

	c.sendExt("PET_PTO.C_CHANGE_PET_LOCK", xym.Msg{"npcnum": int64(pet.NpcNum), "is_lock": 1})
	if m := c.expectExt("PET_PTO.S_UPDATE_PET")["pets"].([]xym.Msg)[0]; num(m, "is_lock") != 1 || !pet.Locked {
		t.Fatalf("pet lock %v", m)
	}
	c.sendExt("ITEM_PTO.C_CHANGE_SUIT_LOCK", xym.Msg{"itemnum": int64(su.ItemNum), "is_lock": 1})
	if m := c.expectExt("ITEM_PTO.S_UPDATE_SUIT")["suits"].([]xym.Msg); len(m) != 1 || num(m[0], "is_lock") != 1 || !su.Locked {
		t.Fatalf("suit lock %v", m)
	}
	c.sendExt("ITEM_PTO.C_CHANGE_MAGIC_LOCK", xym.Msg{"pos": int64(bk.Pos), "is_lock": 1})
	if m := c.expectExt("ITEM_PTO.S_UPDATE_BOOK")["books"].([]xym.Msg); len(m) != 1 || num(m[0], "is_lock") != 1 || !bk.Locked {
		t.Fatalf("book lock %v", m)
	}
	c.sendExt("ITEM_PTO.C_CHANGE_MAGIC_LOCK", xym.Msg{"pos": int64(bk.Pos), "is_lock": 0})
	c.expectExt("ITEM_PTO.S_UPDATE_BOOK")
	if bk.Locked {
		t.Fatal("book still locked")
	}
	// 找不到也要解锁
	c.sendExt("ITEM_PTO.C_CHANGE_SUIT_LOCK", xym.Msg{"itemnum": int64(999999), "is_lock": 1})
	c.expectExt("ITEM_PTO.S_UPDATE_SUIT")
}
