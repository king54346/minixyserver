package xymsrv

import (
	"testing"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func TestStopUpgradeExperienceRouting(t *testing.T) {
	for _, grade := range []int{30, 40, 50} {
		r := Role{Grade: grade, StopUpgrade: 1, Exp: 123, Xiuwei: 79999}
		if n := r.GainExp(160002); n != 0 || r.Grade != grade || r.Exp != 123 || r.XGrade != 3 || r.Xiuwei != 1 {
			t.Fatalf("grade %d: %+v", grade, r)
		}
		r.GainExp(-1)
		if r.Xiuwei != 1 {
			t.Fatal("negative experience accepted")
		}
	}
	r := Role{Grade: 30, Exp: gamedata.RoleNeed(30) - 1}
	r.GainExp(2)
	if r.Grade != 31 || r.Exp != 1 || r.Xiuwei != 0 {
		t.Fatal("normal upgrade diverted into peak")
	}
	r = Role{Grade: 49, Exp: gamedata.RoleNeed(49) - 1}
	r.AddExp(80002)
	if r.Grade != 50 || r.Exp != 0 || r.XGrade != 1 || r.Xiuwei != 1 {
		t.Fatalf("level 50 overflow lost: %+v", r)
	}
	r = Role{Grade: 50, XGrade: 99, Xiuwei: 79999}
	r.AddExp(1000000)
	if r.XGrade != 100 || r.Xiuwei != 0 {
		t.Fatal("peak cap overflow")
	}
}

func TestStopUpgradeTogglePersistLoginAndSweep(t *testing.T) {
	c, p, srv := actWorld(t, "stopgrade")
	srv.roles.Update(p.UID, func(r *Role) bool { r.Grade = 30; r.Exp = 123; r.Xiuwei = 79999; r.Gold = 100; return true })
	c.sendExt("ETC_PTO.C_STOP_USER_UPGRADE", nil)
	msg := c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	if len(msg["attr"].([]xym.Msg)) != 1 || num(msg["attr"].([]xym.Msg)[0], "val") != 1 {
		t.Fatal("toggle did not update client")
	}
	loaded, err := OpenRoles(p.st)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Get(p.UID).StopUpgrade != 1 {
		t.Fatal("toggle not durable")
	}
	login := loaded.Get(p.UID).LoginSuc("stopgrade", time.Now())
	if login["stop_upgrade"] != int64(1) {
		t.Fatal("login lost toggle or used unsupported int8 encoding")
	}
	if _, err := xym.ByName("LOGIN_PTO.S_LOGIN_SUC").Encode(login); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.clearGate(0, 10101)
	p.mu.Unlock()
	c.sendExt("TASK_PTO.C_EXPLORE", xym.Msg{"gateid": 10101, "type": 0})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	r := srv.roles.Get(p.UID)
	if r.Grade != 30 || r.Exp != 123 || r.XGrade == 0 {
		t.Fatal("sweep bypassed peak experience routing")
	}
	attrs := got["ETC_PTO.S_ATTR_UPDATE"]["attr"].([]xym.Msg)
	seen := map[string]bool{}
	for _, a := range attrs {
		seen[a["name"].(string)] = true
	}
	if !seen["xiuwei"] || !seen["xgrade"] {
		t.Fatal("sweep did not synchronize peak progress")
	}
	c.sendExt("ETC_PTO.C_STOP_USER_UPGRADE", nil)
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	if srv.roles.Get(p.UID).StopUpgrade != 0 {
		t.Fatal("toggle did not turn off")
	}
}

func TestPeakLevelsOpenFormation(t *testing.T) {
	c, p, srv := actWorld(t, "peakform")
	srv.roles.Update(p.UID, func(r *Role) bool { r.Grade = 30; r.XGrade = 10; return true })
	p.mu.Lock()
	p.addItem(zfBook0+1, zfBookNeed)
	p.mu.Unlock()
	c.sendExt("ZHENFA_PTO.C_LEARN_ZHENFA", xym.Msg{"type": 1})
	c.until(t, "ZHENFA_PTO.S_OPEN_ALL_ZHENFA")
	// A grade rejection would send no learnt formation.
	if len(p.zhenfa().Open) == 0 {
		t.Fatal("combined level did not unlock formation")
	}
}
