package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

func TestRecruitTenCashGift(t *testing.T) {
	c, p, srv := actWorld(t, "tengift")
	srv.roles.Update(p.UID, func(r *Role) bool { r.Cash = 1000000; r.FriendPoint = 5000; return true })
	p.mu.Lock()
	for _, typ := range []int{10114, 10317, 11461} {
		p.addItem(typ, 10)
	}
	p.mu.Unlock()
	want := int32(1000000)
	for _, typ := range []int{1, 2, 3, 4, 5} {
		c.sendExt("MALL_PTO.C_BUY_PET", xym.Msg{"type": typ + 10})
		got := c.until(t, "MALL_PTO.S_OPEN_PET_MALL")
		want += 500000 - recruitPools[typ].cash*10
		if role := srv.roles.Get(p.UID); role.Cash != want {
			t.Fatalf("pool %d cash=%d want=%d", typ, role.Cash, want)
		}
		rw := got["ETC_PTO.S_REWARD_MSG"]
		if num(rw, "cash") != 500000 || len(rw["list"].([]xym.Msg)) != 10 {
			t.Fatalf("pool %d reward=%v", typ, rw)
		}
	}
	c.sendExt("MALL_PTO.C_BUY_PET", xym.Msg{"type": 11})
	c.expectMsg(t, "请神符不足")
	if srv.roles.Get(p.UID).Cash != want {
		t.Fatal("refused draw awarded gift")
	}
	// The still-available free single draw must not award the ten-pull gift.
	c.sendExt("MALL_PTO.C_BUY_PET", xym.Msg{"type": 1})
	got := c.until(t, "MALL_PTO.S_OPEN_PET_MALL")
	if srv.roles.Get(p.UID).Cash != want || num(got["ETC_PTO.S_REWARD_MSG"], "cash") != 0 {
		t.Fatal("single draw awarded gift")
	}
	reloaded, err := OpenRoles(srv.roles.st)
	if err != nil || reloaded.Get(p.UID).Cash != want {
		t.Fatalf("gift was not persisted: %v", err)
	}
}
