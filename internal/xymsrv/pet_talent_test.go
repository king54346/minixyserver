package xymsrv

import (
	"strings"
	"testing"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestPetTalent：境界 0 没有神通；提升境界扣魂值 / 银两、得神通点、开神通；神通升级扣 need 点；
// 没开的槽不能升；洗点 30 元宝返还已花的点。数值照官方抓包 45033（5 星）。
func TestPetTalent(t *testing.T) {
	c, p, srv := actWorld(t, "talenter")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.SoulExp, ro.Cash, ro.Gold = 10000, 10000000, 100; return true })
	p.mu.Lock()
	pet := p.addPet(45033)
	pi := gamedata.Pets[45033]
	if ids, _ := p.activeTalents(pet, pi); len(ids) != 0 {
		t.Fatalf("soul 0 talents %v", ids)
	}
	if m := p.petFull(pet); m["tlgrade1"] != 0 || m["tlgrade2"] != 0 || m["tlgrade3"] != 0 { // 官方：境界 0 发 0
		t.Fatalf("soul 0 tlgrade %v %v %v", m["tlgrade1"], m["tlgrade2"], m["tlgrade3"])
	}
	p.mu.Unlock()

	up := func() xym.Msg {
		t.Helper()
		c.sendExt("PET_PTO.C_PET_UPGRADE_SOUL", xym.Msg{"bag_pos": pet.BagPos, "npcnum": pet.NpcNum})
		c.expectExt("ETC_PTO.S_ATTR_UPDATE")
		return c.expectExt("PET_PTO.S_UPDATE_PET")["pets"].([]xym.Msg)[0]
	}
	for g := 1; g <= 6; g++ {
		m := up()
		want := g // 5 星：1–5 境界各 1 点，6 起 2 点
		if g == 6 {
			want = 7
		}
		if num(m, "soul_grade") != g || num(m, "tlpoint") != want {
			t.Fatalf("soul %d: %v", g, m)
		}
	}
	soul, cash := srv.roles.Get(p.UID).SoulExp, srv.roles.Get(p.UID).Cash
	up() // 6 → 7：魂值 300、银两 150000（抓包）
	if r := srv.roles.Get(p.UID); soul-r.SoulExp != 300 || cash-r.Cash != 150000 {
		t.Fatalf("6→7 cost soul %d cash %d", soul-r.SoulExp, cash-r.Cash)
	}
	if ids, _ := p.activeTalents(pet, pi); len(ids) != 3 || ids[0] != pi.Talents[0][0] {
		t.Fatalf("soul 7 talents %v", ids)
	}

	need := talentNeed(pi.Talents[0][0])
	before := pet.TLPoint
	c.sendExt("PET_PTO.C_UPGRADE_SKILL", xym.Msg{"npcnum": pet.NpcNum, "tlskill": 1})
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	m := c.expectExt("PET_PTO.S_UPDATE_PET")["pets"].([]xym.Msg)[0]
	if num(m, "tlgrade1") != 2 || num(m, "tlpoint") != before-need {
		t.Fatalf("upgrade skill %v (need %d)", m, need)
	}
	if ids, _ := p.activeTalents(pet, pi); ids[0] != pi.Talents[0][1] {
		t.Fatalf("level 2 talent %v", ids)
	}

	p.mu.Lock()
	pet.TLPoint = 0
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_UPGRADE_SKILL", xym.Msg{"npcnum": pet.NpcNum, "tlskill": 1})
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "神通点不足") {
		t.Fatalf("no points %v", m)
	}
	c.expectExt("PET_PTO.S_UPDATE_PET")

	gold := srv.roles.Get(p.UID).Gold
	c.sendExt("PET_PTO.C_RESET_POINT", xym.Msg{"npcnum": pet.NpcNum})
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	m = c.expectExt("PET_PTO.S_UPDATE_PET")["pets"].([]xym.Msg)[0]
	if num(m, "tlgrade1") != 1 || num(m, "tlpoint") != need || srv.roles.Get(p.UID).Gold != gold-resetPointGold {
		t.Fatalf("reset %v gold %d", m, srv.roles.Get(p.UID).Gold)
	}

	low := p.addPet(11001) // 境界 0：一个神通都没开
	c.sendExt("PET_PTO.C_UPGRADE_SKILL", xym.Msg{"npcnum": low.NpcNum, "tlskill": 1})
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "尚未开启") {
		t.Fatalf("locked slot %v", m)
	}
	c.expectExt("PET_PTO.S_UPDATE_PET")
}

// TestTalentGates：神通槽按境界 1 / 3 / 5 开放，且要人物 3 / 9 / 13 级。
func TestTalentGates(t *testing.T) {
	p := &Player{role: &Role{Grade: 50}}
	pet := &Pet{Type: 25001}
	pi := gamedata.Pets[25001]
	for soul, want := range map[int]int{0: 0, 1: 1, 2: 1, 3: 2, 4: 2, 5: 3, 10: 3} {
		pet.SoulGrade = soul
		if ids, _ := p.activeTalents(pet, pi); len(ids) != want {
			t.Errorf("soul %d: %d talents, want %d", soul, len(ids), want)
		}
	}
	pet.SoulGrade = 10
	for grade, want := range map[int]int{2: 0, 3: 1, 8: 1, 9: 2, 12: 2, 13: 3} {
		p.role.Grade = grade
		if ids, _ := p.activeTalents(pet, pi); len(ids) != want {
			t.Errorf("grade %d: %d talents, want %d", grade, len(ids), want)
		}
	}
}

// TestSameNameCard：拿到已有护法的卡并进原卡（5 星 +2 神通点、tlpoint_add +1），不占背包；读档时合并旧的重复卡。
func TestSameNameCard(t *testing.T) {
	p := newPlayer(1)
	n := len(p.Pets)
	first := p.gainPet(25001)
	again := p.gainPet(25001)
	if again != first || len(p.Pets) != n+1 || first.TLPoint != 2 || first.TLAdd != 1 {
		t.Fatalf("merge: pets %d point %d add %d", len(p.Pets), first.TLPoint, first.TLAdd)
	}
	three := p.gainPet(12005) // 3 星每张 1 点
	p.gainPet(12005)
	if three.TLPoint != 1 || three.TLAdd != 1 {
		t.Fatalf("3-star 12005 point %d add %d", three.TLPoint, three.TLAdd)
	}

	// 旧存档：两张同名卡，阵容里的那张保留
	q := newPlayer(2)
	a, b := q.addPet(35001), q.addPet(35001)
	q.Lineups[0].Slots[7].Pet = b.BagPos
	if !q.mergeDuplicates() || len(q.Pets) != len(newPlayer(3).Pets)+1 || q.petByNpc(a.NpcNum) != nil || b.TLAdd != 1 {
		t.Fatalf("migration kept %d pets, b add %d", len(q.Pets), b.TLAdd)
	}
}

// TestRecordParams：战报 otherparam 4 / 8 / 9（站位:等级:法术:法术等级:神通…、法术书、装备），格式照官方战报。
func TestRecordParams(t *testing.T) {
	f := &battle.Fighter{Pos: 3, Grade: 80, Magic: 15113, MagicLv: 3, Talents: []int32{20785, 20745},
		Books: [][2]int32{{13502, 4}}, BookTJ: []int{5}, Suits: [][5]int32{{1, 1145, 30, 5}}}
	e := &battle.Fighter{Pos: 101, Grade: 1, Magic: 0}
	got := map[int64]string{}
	for _, m := range recordParams([]*battle.Fighter{f}, []*battle.Fighter{e}) {
		got[int64(m["idx"].(int))] = m["value"].(string)
	}
	if got[4] != "3:80:15113:3:20785:20745|101:1:0:1" || got[8] != "3#13502 4 0 0 5" || got[9] != "3#1 1145 30 0 5" {
		t.Fatalf("otherparam %v", got)
	}
}
