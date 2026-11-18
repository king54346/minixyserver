package xymsrv

import (
	"slices"
	"testing"
	"turnrpg/internal/battle"
)

// TestSpecialRelate：5 星项羽和虞姬同时上阵，5 星虞姬同名 5 张 → 项羽带挥扫5；虞姬不上阵也生效。
func TestSpecialRelate(t *testing.T) {
	_, p, _ := actWorld(t, "xiangyu")
	p.mu.Lock()
	defer p.mu.Unlock()
	xy, yj := p.addPet(15023), p.addPet(15031)
	yj.TLAdd = 5
	lu := p.lineup(p.FightNo)
	lu.Slots[0].Pet, lu.Slots[1].Pet = xy.BagPos, yj.BagPos
	team, _ := p.dyTeam(nil)
	if !slices.Contains(team[0].Talents, 20255) {
		t.Fatalf("项羽 talents %v", team[0].Talents)
	}
	lu.Slots[1].Pet = 0
	team, _ = p.dyTeam(nil)
	if !slices.Contains(team[0].Talents, 20255) {
		t.Fatal("bench 虞姬 did not grant 挥扫")
	}
}

func TestSpecialRelatesOwnedCopiesWithoutPartnerInTeam(t *testing.T) {
	for _, r := range specialRelates {
		for _, copies := range []int{0, 1, 5, 12} {
			_, p, _ := actWorld(t, "ownedrelate")
			p.mu.Lock()
			src := p.addPet(r.source)
			src.TLAdd = copies
			for _, receiver := range r.receivers {
				f := &battle.Fighter{Icon: receiver}
				p.applySpecialRelates([]*battle.Fighter{f})
				want := r.base + int32(min(copies, 10)) - 1
				if copies == 0 {
					if len(f.Talents) != 0 {
						t.Fatal("zero copies granted skill")
					}
				} else if !slices.Contains(f.Talents, want) {
					t.Fatalf("receiver=%d copies=%d talents=%v", receiver, copies, f.Talents)
				}
			}
			p.mu.Unlock()
		}
	}
}
