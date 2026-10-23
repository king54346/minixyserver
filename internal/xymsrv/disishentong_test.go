package xymsrv

import (
	"maps"
	"slices"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestDisishentong：三神通未满不能激活；满了用无字天书激活（某候选 0→1，state 3）；选中后进战斗神通。
func TestDisishentong(t *testing.T) {
	c, p, _ := actWorld(t, "dsst")
	var typ int32
	for _, id := range slices.Sorted(maps.Keys(gamedata.Pets)) {
		if pi := gamedata.Pets[id]; pi.Star == 5 && len(pi.ExTalents[0]) > 0 && p.petByType(id) == nil {
			typ = id
			break
		}
	}
	p.mu.Lock()
	pet := p.gainPet(typ)
	pet.SoulGrade = 10
	p.addItem(wuziTianshu, 3)
	p.mu.Unlock()
	pi := gamedata.Pets[typ]

	c.sendExt("PET_PTO.C_OPEN_TIANSHU_NEW", xym.Msg{"pet_id": typ})
	if m := c.expectExt("ETC_PTO.S_OPEN_DISISHENTONG"); num(m, "extlstate") != 1 {
		t.Fatalf("open %v", m)
	}
	c.sendExt("PET_PTO.C_USE_TIANSHU", xym.Msg{"pet_type": typ})
	c.expectExt("ETC_PTO.S_RET_MSG")

	p.mu.Lock()
	for i, l := range pi.Talents {
		pet.TalGrade[i] = len(l)
	}
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_USE_TIANSHU", xym.Msg{"pet_type": typ})
	got := c.until(t, "ETC_PTO.S_OPEN_DISISHENTONG")
	m := got["ETC_PTO.S_OPEN_DISISHENTONG"]
	up := num(m, "lastUpIdx")
	if num(m, "extlstate") != 3 || num(m, "flag") != 1 || up < 1 || num(m, "extlgrade"+string(rune('0'+up))) != 1 || p.itemCount(wuziTianshu) != 2 {
		t.Fatalf("activate %v", m)
	}

	c.sendExt("PET_PTO.C_SWITCH_EXTALENT", xym.Msg{"pet_type": typ, "extalent_idx": up})
	c.expectExt("PET_PTO.S_UPDATE_PET")
	c.expectExt("ETC_PTO.S_OPEN_DISISHENTONG")
	tals, _ := p.activeTalents(pet, pi)
	if !slices.Contains(tals, pi.ExTalents[up-1][0]) {
		t.Fatalf("battle talents %v want %d", tals, pi.ExTalents[up-1][0])
	}
}

// TestSuitSkillAt20：装备强化到 20 级自动有炼神效果（客户端才显示「炼神」按钮）。
func TestSuitSkillAt20(t *testing.T) {
	su := &Suit{ItemNum: 1, Type: 1001, Grade: 19}
	if su.ensureSkill() || su.Skill != 0 {
		t.Fatal("skill before 20")
	}
	su.Grade = 20
	if !su.ensureSkill() {
		t.Fatal("no skill at 20")
	}
	if base, _ := lsInfo(su.Skill); base == 0 {
		t.Fatalf("bad skill %d", su.Skill)
	}
}

// TestAutoHelpFight：结算界面「开启自动挑战」发的 C_AUTO_HELP_FIGHT 按出战阵容打下一关。
func TestAutoHelpFight(t *testing.T) {
	c, _, _ := actWorld(t, "auto")
	c.sendExt("TASK_PTO.C_AUTO_HELP_FIGHT", xym.Msg{"gid": 10101, "type": 0, "ach": 0})
	if r := c.expectExt("FIGHT_PTO.S_FIGHT_RECORD"); num(r, "w1") <= 0 { // w1 为 0 时客户端不显示神通满徽标
		t.Fatalf("w1 %v", r["w1"])
	}
}
