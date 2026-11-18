package xymsrv

import (
	"encoding/json"
	"testing"

	"turnrpg/internal/gamedata"
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

// 融魂腾出一个格子，保留同名数到下次获得本体。
func TestDropMergedPet(t *testing.T) {
	c, p, srv := actWorld(t, "dropmerged")
	p.mu.Lock()
	pet := p.gainPet(15031)
	p.gainPet(15031)
	p.gainPet(15031) // +2
	pet.SoulGrade = 0
	count := len(p.Pets)
	p.mu.Unlock()
	soul0 := srv.roles.Get(p.UID).SoulExp
	c.sendExt("PET_PTO.C_DROP_PET_SOUL", xym.Msg{"pets": []xym.Msg{{"npcnum": pet.NpcNum}}, "souls": []xym.Msg{}})
	got := c.until(t, "PET_PTO.S_UPDATE_CARD")
	p.mu.Lock()
	defer p.mu.Unlock()
	if got["PET_PTO.S_REMOVE_PET"] == nil || len(p.Pets) != count-1 ||
		p.petByType(15031) != nil || p.MeltedCopies[15031] != 2 ||
		srv.roles.Get(p.UID).SoulExp-soul0 != petSoulPoint[5] {
		t.Fatal("melt should remove the base, retain +2 and award soul once")
	}
	data, err := p.st.LoadPlayer(p.UID)
	var saved Player
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.MeltedCopies[15031] != 2 {
		t.Fatal("retained copies were not persisted")
	}
	if saved.dupSoulGain(15031) != 0 {
		t.Fatal("restoring the base should not award duplicate soul")
	}
	restored := saved.gainPet(15031)
	if restored.TLAdd != 2 || restored.TLPoint != 2*shenTongPoint[5] ||
		saved.MeltedCopies[15031] != 0 || saved.OwnedCopies[15031] != 4 {
		t.Fatalf("unexpected restored base: %+v", restored)
	}
}

// TestTransformSoul：5 星魂魄转 3 星魂魄（每个 6 个，30 元宝）；4 / 5 星魂魄转通用魂魄（魂蛋，花魂值）。
func TestTransformSoul(t *testing.T) {
	c, p, srv := actWorld(t, "soultrans")
	var from int32
	for k, r := range gamedata.SoulTransforms {
		if r.Count == 6 && gamedata.Pets[k] != nil {
			from = k
			break
		}
	}
	rule := gamedata.SoulTransforms[from]
	p.mu.Lock()
	p.addSoul(from, 3, &bagDelta{})
	p.mu.Unlock()
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 1000; return true })
	gold := srv.roles.Get(p.UID).Gold
	c.sendExt("PET_PTO.C_TRANSFORM_SOUL", xym.Msg{"from_type": int64(from), "count": int64(2)})
	c.until(t, "PET_PTO.S_TRANSFORM_SOUL")
	if p.Souls[from] != 1 || p.Souls[rule.To] < 12 || srv.roles.Get(p.UID).Gold != gold-2*rule.Gold {
		t.Fatalf("souls %d → %d gold %d", p.Souls[from], p.Souls[rule.To], gold-srv.roles.Get(p.UID).Gold)
	}
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.SoulExp = 1000; return true })
	c.sendExt("ITEM_PTO.C_ON_TRANSFER_FRAGMENT", xym.Msg{"op_type": int64(0), "type": int64(from), "count": int64(1), "cls": int64(5)})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	if p.Souls[from] != 0 || p.itemCount(soulWildItem[gamedata.Pets[from].Star]) != 1 {
		t.Fatalf("egg souls %d item %d", p.Souls[from], p.itemCount(soulWildItem[gamedata.Pets[from].Star]))
	}
}
