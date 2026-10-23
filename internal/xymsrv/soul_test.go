package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// TestSoulToPet：魂魄够数聚魂得卡、扣魂魄；5 星魂魄 16 个时用通用魂魄 10117 补足；融魂给魂值。
func TestSoulToPet(t *testing.T) {
	c, p, srv := actWorld(t, "juhun")
	p.mu.Lock()
	p.addSoul(33001, 6, &bagDelta{})  // 3 星小白龙：要 6 个
	p.addSoul(15031, 16, &bagDelta{}) // 5 星虞姬：要 20 个，16 个 + 4 个通用魂魄
	p.addItem(10117, 4)
	before := len(p.Pets)
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_SOUL_TO_PET", xym.Msg{"type": 33001, "no_update": 0})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	c.sendExt("PET_PTO.C_SOUL_TO_PET", xym.Msg{"type": 15031, "no_update": 1})
	c.until(t, "PET_PTO.S_UPDATE_CARD")
	c.collect()
	if p.Souls[33001] != 0 || p.Souls[15031] != 0 || p.itemCount(10117) != 0 || p.petByType(15031) == nil || len(p.Pets) < before+1 {
		t.Fatalf("souls %d %d wild %d pets %d", p.Souls[33001], p.Souls[15031], p.itemCount(10117), len(p.Pets))
	}
	c.sendExt("PET_PTO.C_SOUL_TO_PET", xym.Msg{"type": 33001, "no_update": 0}) // 不够了
	c.until(t, "PET_PTO.S_UPDATE_CARD")

	// 融魂：一个 5 星魂魄 5 魂值 + 一张没上阵的 5 星卡 100 魂值（境界 0）
	p.mu.Lock()
	p.addSoul(15031, 1, &bagDelta{})
	pet := p.petByType(15031)
	pet.SoulGrade = 0
	p.mu.Unlock()
	soul0 := srv.roles.Get(p.UID).SoulExp
	c.sendExt("PET_PTO.C_DROP_PET_SOUL", xym.Msg{"pets": []xym.Msg{{"npcnum": pet.NpcNum}}, "souls": []xym.Msg{{"type": 15031, "amount": 1}}})
	c.until(t, "PET_PTO.S_UPDATE_CARD")
	if got := srv.roles.Get(p.UID).SoulExp - soul0; got != petSoulPoint[5]+soulSoulPoint[5] || p.petByType(15031) != nil {
		t.Fatalf("soul exp +%d", got)
	}
}
