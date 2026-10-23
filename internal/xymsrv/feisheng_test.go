package xymsrv

import (
	"maps"
	"slices"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestFeisheng：3 星护法 + 4 张同名卡（5×6=30 元）+ 3 件 3 星材料 + 100 筑基丹 → 飞升成 4 星，回 S_PET_UPGRADE；
// 材料、同名卡、筑基丹扣掉；进度不够时回 S_RET_MSG 解锁。
func TestFeisheng(t *testing.T) {
	c, p, srv := actWorld(t, "fs")
	var typ int32
	for _, id := range slices.Sorted(maps.Keys(gamedata.Pets)) {
		if pi := gamedata.Pets[id]; pi.Star == 3 && pi.Upgrade != 0 && p.petByType(id) == nil {
			typ = id
			break
		}
	}
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Cash, ro.SoulExp, ro.Gold = 1000000, 1000, 1000; return true })
	p.mu.Lock()
	pet := p.gainPet(typ)
	var stuff []xym.Msg
	for _, id := range gamedata.SuitsByStar[3][:3] {
		stuff = append(stuff, xym.Msg{"type": "suit", "key": int64(p.addSuit(id).ItemNum), "stuff_type": int64(id)})
	}
	p.addItem(10095, 100)
	p.mu.Unlock()
	req := xym.Msg{"pet_num": pet.NpcNum, "star3_pet": 5, "star3_soul": 0, "star4_pet": 0, "star4_soul": 0, "star5_soul": 0,
		"force_gold": 0, "stuff_list": stuff, "stuff_list_ext": []xym.Msg{}}
	c.sendExt("PET_PTO.C_UPGRADE_CARD_EX", req)
	c.expectExt("ETC_PTO.S_RET_MSG") // 只有 1 张，不够 30

	p.mu.Lock()
	for range 6 { // 共 7 张：用 5 张，剩 2 张留成原类型
		p.gainPet(typ)
	}
	suits := len(p.Suits)
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_UPGRADE_CARD_EX", req)
	got := c.until(t, "ETC_PTO.S_PET_UPGRADE")
	if num(got["ETC_PTO.S_PET_UPGRADE"], "npcnum") != int(pet.NpcNum) || pet.Type != gamedata.Pets[typ].Upgrade || pet.TLAdd != 0 {
		t.Fatalf("upgrade type %d tladd %d", pet.Type, pet.TLAdd)
	}
	if rest := p.petByType(typ); rest == nil || rest == pet || rest.TLAdd != 1 {
		t.Fatalf("leftover same-name cards not kept as %d: %+v", typ, rest)
	}
	if len(p.Suits) != suits-3 || p.itemCount(10095) != 0 || gamedata.Pets[pet.Type].Star != 4 {
		t.Fatalf("suits %d dan %d", len(p.Suits), p.itemCount(10095))
	}
}

// TestFeishengMerge：已有 4 星同类护法时，飞升出来的卡并进去成一张（同名 +1），回 S_REMOVE_PET。
func TestFeishengMerge(t *testing.T) {
	c, p, srv := actWorld(t, "fsm")
	var typ int32
	for _, id := range slices.Sorted(maps.Keys(gamedata.Pets)) {
		if pi := gamedata.Pets[id]; pi.Star == 3 && pi.Upgrade != 0 && p.petByType(id) == nil && p.petByType(pi.Upgrade) == nil {
			typ = id
			break
		}
	}
	up := gamedata.Pets[typ].Upgrade
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Cash, ro.SoulExp, ro.Gold = 1000000, 1000, 1000; return true })
	p.mu.Lock()
	existing := p.gainPet(up)
	pet := p.gainPet(typ)
	for range 4 {
		p.gainPet(typ)
	}
	p.addItem(10095, 100)
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_UPGRADE_CARD_EX", xym.Msg{"pet_num": pet.NpcNum, "star3_pet": 5, "star3_soul": 0, "star4_pet": 0,
		"star4_soul": 0, "star5_soul": 0, "force_gold": 1, "stuff_list": []xym.Msg{}, "stuff_list_ext": []xym.Msg{}})
	got := c.until(t, "ETC_PTO.S_PET_UPGRADE")
	n := 0
	for _, x := range p.Pets {
		if x.Type == up {
			n++
		}
	}
	if num(got["ETC_PTO.S_PET_UPGRADE"], "npcnum") != int(existing.NpcNum) { // 结果界面要指向留下的那张
		t.Fatalf("upgrade npcnum %v want %d", got["ETC_PTO.S_PET_UPGRADE"], existing.NpcNum)
	}
	if n != 1 || existing.TLAdd != 1 || got["PET_PTO.S_REMOVE_PET"] == nil {
		t.Fatalf("upgraded copies %d, existing +%d, removed %v", n, existing.TLAdd, got["PET_PTO.S_REMOVE_PET"])
	}
}
