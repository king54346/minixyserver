package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestPerfect：完美账号——全护法金边 + 第四神通、全装备 / 法术满级，全量表下发能收齐；能正常开战。
func TestPerfect(t *testing.T) {
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	uid, err := srv.MakePerfect(PerfectSpec{URS: "perfect", Pwd: "pw", Code: "abcd1234"})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := srv.players.Get(uid)
	if len(p.Pets) != len(gamedata.Pets) || srv.roles.Get(uid).Grade != gamedata.MaxRoleGrade {
		t.Fatalf("pets %d grade %d", len(p.Pets), srv.roles.Get(uid).Grade)
	}
	for _, pet := range p.Pets {
		pi := gamedata.Pets[pet.Type]
		if len(pi.Talents[0]) > 0 && !pet.talentsFull(pi) || pi.Star == 5 && len(pi.ExTalents[0]) > 0 && p.extlState(pet) != 3 {
			t.Fatalf("pet %d not full", pet.Type)
		}
		if spentPoints(pet, pi) > 0 && pet.TLAdd == 0 && pi.Star >= 4 {
			t.Fatalf("pet %d full talents without 同名", pet.Type)
		}
	}
	for _, b := range p.Books {
		if b.Grade != maxMagicGrade || gamedata.Magics[b.Type].JXBuff != 0 && b.JX != maxMagicGrade {
			t.Fatalf("book %+v", b)
		}
	}
	c := dial(t, addr)
	c.sendRaw(xym.CLogin, loginBody("perfect", "pw"))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
	c.collect()
	c.sendExt("PET_PTO.C_APPLY_PET_LIST", nil)
	got, packs := 0, 0
	for got < len(p.Pets) {
		m := c.expectExt("PET_PTO.S_UPDATE_PET")
		if packs == 0 && num(m, "is_all") != 1 || packs > 0 && num(m, "is_all") != 0 {
			t.Fatalf("pack %d is_all %v", packs, m["is_all"])
		}
		got += len(m["pets"].([]xym.Msg))
		packs++
	}
	c.collect()
	c.sendExt("TASK_PTO.C_START_FIGHT", xym.Msg{"gid": 10101, "type": 0, "pos1": 1, "pos2": 2, "pos3": 3, "pos4": 4, "pos5": 5})
	if rec := c.expectExt("FIGHT_PTO.S_FIGHT_RECORD"); rec == nil {
		t.Fatal("no record")
	}
}

// TestPerfectExisting：已有角色按 uid / 账号名直接改成完美账号，在线的先被踢下线。
func TestPerfectExisting(t *testing.T) {
	_, p, srv := actWorld(t, "pex")
	if _, err := srv.PerfectExisting(0, "nobody"); err == nil {
		t.Fatal("unknown account accepted")
	}
	uid, err := srv.PerfectExisting(0, "pex")
	if err != nil || uid != p.UID {
		t.Fatalf("uid %d err %v", uid, err)
	}
	if srv.sessionOf(uid) != nil {
		t.Fatal("still online")
	}
	q, _ := srv.players.Get(uid)
	if len(q.Pets) != len(gamedata.Pets) || srv.roles.Get(uid).Grade != gamedata.MaxRoleGrade {
		t.Fatalf("pets %d grade %d", len(q.Pets), srv.roles.Get(uid).Grade)
	}
}

// TestPerfectNumericURS：纯数字账号名（不是 uid）也能找到。
func TestPerfectNumericURS(t *testing.T) {
	_, p, srv := actWorld(t, "492103785")
	uid, err := srv.PerfectExisting(492103785, "492103785")
	if err != nil || uid != p.UID {
		t.Fatalf("uid %d err %v", uid, err)
	}
}

// TestGMRoleAndCatalog：GM 改角色数值（在线先踢）、拒绝未知字段和负数；物品目录四类都有。
func TestGMRoleAndCatalog(t *testing.T) {
	_, p, srv := actWorld(t, "gmrole")
	ro, err := srv.gmSetRole(p.UID, map[string]int64{"gold": 12345, "grade": 999, "vip": 3})
	if err != nil || ro.Gold != 12345 || ro.Grade != gamedata.MaxRoleGrade || ro.VIP != 3 || srv.sessionOf(p.UID) != nil {
		t.Fatalf("role %+v err %v", ro, err)
	}
	if _, err := srv.gmSetRole(p.UID, map[string]int64{"hack": 1}); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := srv.gmSetRole(p.UID, map[string]int64{"gold": -1}); err == nil {
		t.Fatal("negative accepted")
	}
	kinds := map[string]int{}
	for _, r := range gmCatalog() {
		kinds[r.Kind]++
	}
	if kinds["道具"] == 0 || kinds["护法"] == 0 || kinds["装备"] == 0 || kinds["法术书"] == 0 {
		t.Fatalf("catalog %v", kinds)
	}
	if info, err := srv.gmPlayer(p.UID); err != nil || info["pets"] == nil {
		t.Fatalf("player %v %v", info, err)
	}
}
