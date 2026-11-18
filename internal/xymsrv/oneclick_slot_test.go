package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
)

func TestOneClickSlotTypesAndMissingEquipment(t *testing.T) {
	for _, kind := range []string{"ITK_WEAPON", "ITK_SHIRT", "ITK_SHOE"} {
		t.Run(kind, func(t *testing.T) {
			p := newPlayer(1)
			p.Suits = nil
			for typ, info := range gamedata.Suits {
				if info.Kind == kind && !isSuitMaterial(typ) {
					p.addSuit(typ)
					break
				}
			}
			lu := p.lineup(p.FightNo)
			p.applyPlan(lu, p.planEquip(lu.Slots))
			worn := 0
			for _, sl := range lu.Slots {
				for k, pos := range sl.Suit {
					if pos == 0 {
						continue
					}
					worn++
					if k != suitSlot[kind] || gamedata.Suits[p.suitByPos(pos).Type].Kind != kind {
						t.Fatal("one-click filled an incompatible slot")
					}
				}
			}
			if worn != 1 {
				t.Fatalf("worn=%d, want exactly one compatible item", worn)
			}
		})
	}
}

func TestApplyPlanRejectsWeaponInShoeSlot(t *testing.T) {
	p := newPlayer(1)
	weapon := p.addSuit(1001)
	lu := p.lineup(p.FightNo)
	op := &ocPet{fp: 1, pet: p.Pets[0], suits: [3]*Suit{nil, nil, weapon}}
	p.applyPlan(lu, []*ocPet{op})
	if lu.Slots[0].Suit != [3]int{} {
		t.Fatal("weapon was accepted in the shoe slot")
	}
}
