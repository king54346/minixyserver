package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func TestOneClickEquipRejectsMaterials(t *testing.T) {
	c, p, _ := actWorld(t, "matgear")
	p.mu.Lock()
	p.Suits, p.Books = nil, nil
	for typ := 2001; typ <= 2004; typ++ {
		p.addSuit(typ)
	}
	for typ := int32(17000); typ <= 17004; typ++ {
		p.addBook(typ)
	}
	p.mu.Unlock()
	check := func() {
		t.Helper()
		c.sendExt("ITEM_PTO.C_ON_ONE_CLICK_EQUIP", xym.Msg{})
		got := c.until(t, "LINEUP_PTO.S_SEND_LINEUP")
		if got["ITEM_PTO.S_WEAR_BOOK"] == nil || got["ITEM_PTO.S_WEAR_SUIT"] == nil {
			t.Fatal("one-click equipment did not unlock client")
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, sl := range p.lineup(p.FightNo).Slots {
			if sl == nil {
				continue
			}
			for _, pos := range sl.Suit {
				if su := p.suitByPos(pos); su != nil && isSuitMaterial(su.Type) {
					t.Fatal("one-click equipped an enhancement crystal")
				}
			}
			for _, pos := range sl.Book {
				if bk := p.bookByPos(pos); bk != nil && isBookMaterial(bk.Type) {
					t.Fatal("one-click equipped a magic enhancement material")
				}
			}
		}
		if len(p.Suits) < 4 || len(p.Books) < 5 {
			t.Fatal("materials were consumed rather than excluded")
		}
	}
	check() // 背包只有材料时，空武器位必须保持空，不能用材料填满。
	p.mu.Lock()
	var weapon *Suit
	for typ, info := range gamedata.Suits {
		if info.Kind == "ITK_WEAPON" && !isSuitMaterial(typ) {
			weapon = p.addSuit(typ)
			break
		}
	}
	p.mu.Unlock()
	if weapon == nil {
		t.Fatal("no real weapon fixture")
	}
	check() // 有正常装备时仍应分配正常装备，同时保留材料。
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.suitWorn(weapon.Pos) {
		t.Fatal("real weapon was not equipped")
	}
}
