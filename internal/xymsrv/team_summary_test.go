package xymsrv

import (
	"math"
	"testing"
	"time"

	"turnrpg/internal/xym"
)

func TestLoginAndSIDUseSavedActiveLeader(t *testing.T) {
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	enterWorld(t, addr, "leaderinit").conn.Close()
	acc, _ := srv.accounts.Login("leaderinit", "pw", "")
	srv.roles.Update(acc.UID, func(r *Role) bool { r.Grade = 50; return true })
	p, _ := srv.players.Get(acc.UID)
	p.mu.Lock()
	p.role = srv.roles.Get(acc.UID)
	p.ensureLineups()
	lu := p.lineup(2)
	for i := range lu.Slots {
		lu.Slots[i] = &Slot{}
	}
	a, b := p.addPet(25001), p.addPet(35001)
	lu.Slots[0].Pet, lu.Slots[2].Pet, lu.Leader, p.FightNo = a.BagPos, b.BagPos, 3, 2
	if err := p.save(); err != nil {
		t.Fatal(err)
	}
	p.mu.Unlock()
	c := dial(t, addr)
	c.sendRaw(xym.CLogin, loginBody("leaderinit", "pw"))
	sid := c.expectExt("LOGIN_PTO.S_SEND_SID")
	if m := c.expectExt("LOGIN_PTO.S_LOGIN_SUC"); num(m, "leader") != 3 {
		t.Fatalf("initial login leader: %v", m)
	}
	c.collect()
	c.conn.Close()
	c = dial(t, addr)
	c.sendRaw(xym.CSidLogin, sidBody(sid["uid"].(int64), sid["sid"].(string)))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	if m := c.expectExt("LOGIN_PTO.S_LOGIN_SUC"); num(m, "leader") != 3 {
		t.Fatalf("SID login leader: %v", m)
	}
	c.collect()
}

func TestFriendPowerUsesActiveTeamWithoutActivitySnapshot(t *testing.T) {
	srv, a, b := twoPlayers(t)
	const uid = 20002
	p, _ := srv.players.Get(uid)
	dbBoard.Lock()
	old := dbBoard.foes[uid]
	delete(dbBoard.foes, uid)
	dbBoard.Unlock()
	defer func() {
		dbBoard.Lock()
		if old != nil {
			dbBoard.foes[uid] = old
		}
		dbBoard.Unlock()
	}()
	p.mu.Lock()
	first := p.refreshTeamSummary()
	p.mu.Unlock()
	if first.Power <= 0 {
		t.Fatal("fixture team empty")
	}
	srv.social.do(func() bool { srv.social.makeFriends(20001, uid); return true })
	a.sendExt("FRIEND_PTO.C_QUERY_ALL_FRIENDS", nil)
	m := a.expectExt("FRIEND_PTO.S_FRIEND_INFO")
	rows := m["friends"].([]xym.Msg)
	if len(rows) != 1 || num(rows[0], "fight_point") != first.Power || num(rows[0], "leader_skl") != int(first.Skill) {
		t.Fatal(m)
	}
	a.collect()
	b.collect()
	// A busy friend returns a published snapshot without blocking or reading
	// mutable equipment/lineup data. The social lock also remains available.
	p.mu.Lock()
	done := make(chan xym.Msg, 1)
	go func() { done <- srv.friendInfo(uid, 0, 0) }()
	select {
	case info := <-done:
		if info["fight_point"].(int) != first.Power {
			p.mu.Unlock()
			t.Fatal(info)
		}
	case <-time.After(time.Second):
		p.mu.Unlock()
		t.Fatal("friend query waited on another player's lock")
	}
	pet := p.petAtFightPos(1)
	pet.AttAdd += 100
	p.saveLog(&Session{role: srv.roles.Get(uid)})
	second := p.publicTeam.Load()
	if second.Power <= first.Power || first == second {
		p.mu.Unlock()
		t.Fatal("gear/stat change did not publish a new snapshot")
	}
	done = make(chan xym.Msg, 1)
	go func() { done <- srv.friendInfo(uid, 0, 0) }()
	select {
	case info := <-done:
		if info["fight_point"].(int) != second.Power {
			p.mu.Unlock()
			t.Fatal(info)
		}
	case <-time.After(time.Second):
		p.mu.Unlock()
		t.Fatal("updated snapshot blocked")
	}
	p.mu.Unlock()
}

func TestCapacityPartyRateMatchesDisplayedEffectLevels(t *testing.T) {
	p := &Player{role: &Role{Grade: 50}, DF: &DFPlayer{Exp: map[string]int32{"human": 10000, "hp": 10000}, Caps: map[string]int{"human": 5, "hp": 3}}}
	for race, want := range map[int]float64{1: 1.01175, 2: 1.0055, 3: 1.0055, 4: 1.0055} {
		if got := p.capPartyRate(race); math.Abs(got-want) > 1e-10 {
			t.Fatalf("race %d: %f want %f", race, got, want)
		}
	}
}
