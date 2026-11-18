package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// Re-synthesizing a deliberately removed card restores its base even though
// its ownership history remains. A subsequent duplicate still merges normally.
func TestSoulReacquiresMissingHistoricalPet(t *testing.T) {
	c, p, srv := actWorld(t, "dupauto")

	// 首次获得：聚魂合 5 星虞姬 → 进背包 + 永久记录，不发魂值（第一张手动融魂才拿）
	p.mu.Lock()
	p.addSoul(15031, 20, &bagDelta{})
	p.mu.Unlock()
	soul0 := srv.roles.Get(p.UID).SoulExp
	c.sendExt("PET_PTO.C_SOUL_TO_PET", xym.Msg{"type": 15031, "no_update": 0})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	pet := p.petByType(15031)
	if pet == nil || !p.OwnedHistory[15031] || srv.roles.Get(p.UID).SoulExp != soul0 {
		t.Fatalf("first copy: pet %v hist %v soul %d", pet != nil, p.OwnedHistory[15031], srv.roles.Get(p.UID).SoulExp)
	}

	// 当天手动融魂第一张：拿全额 100
	p.mu.Lock()
	pet.SoulGrade = 0
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_DROP_PET_SOUL", xym.Msg{"pets": []xym.Msg{{"npcnum": pet.NpcNum}}, "souls": []xym.Msg{}})
	c.until(t, "PET_PTO.S_UPDATE_CARD")
	if got := srv.roles.Get(p.UID).SoulExp - soul0; got != petSoulPoint[5] {
		t.Fatalf("manual soul +%d want %d", got, petSoulPoint[5])
	}

	// 本体已不存在：聚魂必须补回本体，不能因为永久记录而自动消失。
	p.mu.Lock()
	p.addSoul(15031, 20, &bagDelta{})
	n := len(p.Pets)
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_SOUL_TO_PET", xym.Msg{"type": 15031, "no_update": 0})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	restored := p.petByType(15031)
	if restored == nil || restored.NpcNum == pet.NpcNum || len(p.Pets) != n+1 {
		t.Fatalf("missing base was not restored: pets %d", len(p.Pets))
	}
	if got := srv.roles.Get(p.UID).SoulExp - soul0; got != petSoulPoint[5] {
		t.Fatalf("restored base incorrectly converted to soul: +%d", got)
	}
	p.mu.Lock()
	p.addSoul(15031, 20, &bagDelta{})
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_SOUL_TO_PET", xym.Msg{"type": 15031, "no_update": 0})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	if p.petByType(15031) != restored || restored.TLAdd != 1 || srv.roles.Get(p.UID).SoulExp-soul0 != 2*petSoulPoint[5] {
		t.Fatal("existing duplicate no longer merges")
	}
}

// TestDupCardHoardOneGain：同名卡自动融魂（错误操作）——第一张囤着不手动融，
// 第二张同名卡自动转化只得一份魂值 100（第一张没有手动融魂收益），并入本体神通点。
func TestDupCardHoardOneGain(t *testing.T) {
	p := newPlayer(1)
	pet := p.gainPet(15031) // 首张进背包

	// 第二张同名卡：本体在 → 并入（TLAdd +1、神通点 +shenTongPoint[5]），自动转化魂值只发一份
	p.mu.Lock()
	n := len(p.Pets)
	d := &bagDelta{}
	p.giveCard(15031, d)
	p.mu.Unlock()
	if len(p.Pets) != n || pet.TLAdd != 1 || pet.TLPoint != shenTongPoint[5] || d.soul != petSoulPoint[5] {
		t.Fatalf("hoard merge: pets %d tladd %d tlpoint %d soul %d want %d",
			len(p.Pets), pet.TLAdd, pet.TLPoint, d.soul, petSoulPoint[5])
	}
}

// TestDupCardNoMailWhenFull：背包满时同名卡不寄邮件（自动融魂不占格子）：
// 本体在则并入 + 魂值；本体已融则需要恢复卡牌，背包满时改寄邮件。
func TestDupCardNoMailWhenFull(t *testing.T) {
	p := newPlayer(1)
	pet := p.gainPet(15031)
	p.mu.Lock()
	p.BagLimit[bagPet] = 1 // 模拟背包满（已有 15031 就满了）
	d := &bagDelta{}
	p.giveCard(15031, d) // 同名卡：背包满也不该寄邮件
	p.mu.Unlock()
	if d.mailed != nil || d.soul != petSoulPoint[5] || pet.TLAdd != 1 {
		t.Fatalf("full bag dup: mailed %v soul %d tladd %d", d.mailed, d.soul, pet.TLAdd)
	}

	// 当前无本体而且包已满：定向奖励应寄邮件，不能自动吞卡。
	p.mu.Lock()
	p.Pets = nil
	p.addPet(12005)
	d = &bagDelta{}
	p.giveCard(15031, d)
	n := len(p.Pets)
	p.mu.Unlock()
	if len(d.mailed) != 1 || d.soul != 0 || n != 1 {
		t.Fatalf("full bag dup after drop: mailed %v soul %d pets %d", d.mailed, d.soul, n)
	}
}
