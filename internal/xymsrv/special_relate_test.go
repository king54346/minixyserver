package xymsrv

import (
	"slices"
	"testing"
)

// TestSpecialRelate：5 星项羽和虞姬同时上阵，5 星虞姬同名 5 张 → 项羽带挥扫5；虞姬不上阵则没有。
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
	if slices.Contains(team[0].Talents, 20255) {
		t.Fatal("挥扫 without 虞姬 on team")
	}
}
