package xymsrv

import (
	"strings"
	"testing"

	"turnrpg/internal/xym"
)

func TestTiantiChangeQuotaFeesAndStaleConfirmations(t *testing.T) {
	c, p, srv := actWorld(t, "ttchanges")
	for _, uid := range []int32{30001, 30002} {
		if _, err := srv.roles.Create(uid, 1, "Foe"+string(rune('A'+uid-30001))); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.players.Get(uid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := srv.roles.Update(p.UID, func(r *Role) bool { r.Gold = 1000; return true }); err != nil {
		t.Fatal(err)
	}
	match := func() { c.sendExt("LADDER_PTO.C_START_PAIR", nil); c.until(t, "LADDER_PTO.S_OPEN_LADDER") }
	counts := func(day, changes, paid int, gold int32) {
		t.Helper()
		srv.tianti.do(func() bool {
			u := srv.tianti.Users[p.UID]
			if u.DayN != day || u.ChangeN != changes || u.ChangePaid != paid {
				t.Fatalf("counts: %+v", u)
			}
			return false
		})
		if got := srv.roles.Get(p.UID).Gold; got != gold {
			t.Fatalf("gold=%d want=%d", got, gold)
		}
	}
	match()
	for i := 1; i <= 3; i++ {
		match()
		counts(1, i, 0, 1000)
	}
	ask := func() xym.Msg {
		c.sendExt("LADDER_PTO.C_START_PAIR", nil)
		m := c.expectExt("ETC_PTO.S_ASK_BOX")
		if !strings.Contains(m["text"].(string), "50元宝") {
			t.Fatalf("wrong price: %v", m)
		}
		return m
	}
	a := ask()
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": a["session"], "opt": 1})
	c.sendExt("LADDER_PTO.C_OPEN_LADDER", nil)
	c.until(t, "LADDER_PTO.S_GET_PAIR_TIME")
	counts(1, 3, 0, 1000)
	// Two open confirmations for the same state: only one may spend.
	a, stale := ask(), ask()
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": a["session"], "opt": 0})
	c.until(t, "LADDER_PTO.S_OPEN_LADDER")
	counts(1, 4, 1, 950)
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": stale["session"], "opt": 0})
	c.expectMsg(t, "匹配状态已变化")
	counts(1, 4, 1, 950)
	if _, err := srv.roles.Update(p.UID, func(r *Role) bool { r.Gold = 49; return true }); err != nil {
		t.Fatal(err)
	}
	a = ask()
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": a["session"], "opt": 0})
	c.expectMsg(t, "元宝不足")
	counts(1, 4, 1, 49)
	srv.roles.Update(p.UID, func(r *Role) bool { r.Gold = 1000; return true })
	for i := 5; i <= 8; i++ {
		a = ask()
		c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": a["session"], "opt": 0})
		c.until(t, "LADDER_PTO.S_OPEN_LADDER")
		counts(1, i, i-3, 1000-int32(i-4)*50)
	}
	c.sendExt("LADDER_PTO.C_START_PAIR", nil)
	if m := c.expectExt("ETC_PTO.S_SEND_NO_CACHE_MSG"); !strings.Contains(m["msg"].(string), "更换对手次数已用完") {
		t.Fatal(m)
	}
	loaded := openTianti(srv.tianti.st).Users[p.UID]
	if loaded.ChangeN != 8 || loaded.ChangePaid != 5 || loaded.DayN != 1 {
		t.Fatal("change quota did not persist")
	}
	srv.tianti.do(func() bool { srv.tianti.Users[p.UID].Day = "2000-01-01"; return true })
	match()
	counts(0, 1, 0, 800) // Existing opponent means a free change, not a new match.
}

func TestTiantiUnusableChangeDoesNotSpend(t *testing.T) {
	c, p, srv := actWorld(t, "ttbadfoe")
	const uid int32 = 30001
	srv.roles.Create(uid, 1, "BadFoe")
	foe, err := srv.players.Get(uid)
	if err != nil {
		t.Fatal(err)
	}
	foe.mu.Lock()
	foe.Pets = nil
	if err := foe.save(); err != nil {
		t.Fatal(err)
	}
	foe.mu.Unlock()
	srv.roles.Update(p.UID, func(r *Role) bool { r.Gold = 1000; return true })
	srv.tianti.do(func() bool {
		u := &ttUser{Day: ttNow().Format("2006-01-02"), DayN: 25, Foe: uid, ChangeN: 3, Score: 1000}
		srv.tianti.Users[p.UID] = u
		return true
	})
	c.sendExt("LADDER_PTO.C_START_PAIR", nil)
	a := c.expectExt("ETC_PTO.S_ASK_BOX")
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": a["session"], "opt": 0})
	c.expectMsg(t, "阵容暂不可用")
	c.until(t, "LADDER_PTO.S_GET_PAIR_TIME")
	if srv.roles.Get(p.UID).Gold != 1000 {
		t.Fatal("charged an unusable opponent")
	}
	srv.tianti.do(func() bool {
		u := srv.tianti.Users[p.UID]
		if u.DayN != 25 || u.ChangeN != 3 || u.ChangePaid != 0 || u.Foe != uid {
			t.Fatal("failed change altered state")
		}
		return false
	})
}
