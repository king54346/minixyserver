package xymsrv

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

func TestPrestigeRetainsBaseAddsCumulativeVIPAndCurrentLundao(t *testing.T) {
	ro := &Role{UID: 1, Grade: 30, VIP: 15}
	p := &Player{Prog: [2]*Progress{{Gates: map[int]int{10101: 50, 10102: 0}}, {Gates: map[int]int{10101: 1}}}}
	if got := prestigeOf(ro, p); got != 300+2+360 {
		t.Fatal(got)
	}
	srv := &Server{ladder: &Ladder{Groups: map[int][]int32{30: {ro.UID, 2}}}}
	if got := srv.prestigeOf(ro, p); got != 300+2+360+190 {
		t.Fatal(got)
	}
	srv.ladder.Groups[30] = []int32{2, ro.UID}
	if got := srv.prestigeOf(ro, p); got != 300+2+360+171 {
		t.Fatalf("old rank prestige retained: %d", got)
	}
	srv.ladder.Groups[30] = nil
	if got := srv.prestigeOf(ro, p); got != 300+2+360 {
		t.Fatalf("unranked prestige: %d", got)
	}
}

func TestPrestigeRulesValidationAndCurrentRank(t *testing.T) {
	bands := []PrestigeBand{{1, 100}, {10, 50}, {100, 10}}
	for rank, want := range map[int]int{0: 0, -1: 0, 1: 100, 2: 50, 10: 50, 11: 10, 101: 0} {
		if got := prestigeAtRank(bands, rank); got != want {
			t.Fatalf("rank %d: %d", rank, got)
		}
	}
	file := filepath.Join(t.TempDir(), "rules.json")
	for _, text := range []string{`{"tianti":[{"through":10,"points":1},{"through":1,"points":2}]}`, `{"stars":[{"through":1,"points":-1}]}`, `{"typo":[]}`, `{} {}`} {
		os.WriteFile(file, []byte(text), 0600)
		if _, err := LoadPrestigeRules(file); err == nil {
			t.Fatalf("accepted invalid rules: %s", text)
		}
	}
	os.WriteFile(file, []byte(`{"qujing":[],"stars":[],"shilian":[],"tianti":[{"through":1,"points":100}]}`), 0600)
	if rules, err := LoadPrestigeRules(file); err != nil || prestigeAtRank(rules.Tianti, 1) != 100 {
		t.Fatal(rules, err)
	}
}

func TestSimulateVersusUsesPrestigeAndInitiatorTie(t *testing.T) {
	p := &Player{}
	s := &Session{role: &Role{Grade: 50}, player: p}
	for _, tc := range []struct{ foeW, speed, actor int }{{500, 5, 1}, {501, 5, 101}, {499, 5, 1}, {501, 4, 1}, {499, 6, 101}} {
		one := []*battle.Fighter{{ID: 1, Pos: 1, Att: 10, HP: 10000, MaxHP: 10000, Speed: 5}}
		two := []*battle.Fighter{{ID: 2, Pos: 101, Att: 10, HP: 10000, MaxHP: 10000, Speed: tc.speed}}
		res := s.simulateVersus(p, one, two, [2]int32{}, battle.Formation{}, tc.foeW, rand.New(rand.NewPCG(1, 2)))
		cmds, err := battle.DecodeRecord(res.Record)
		if err != nil {
			t.Fatal(err)
		}
		actor := 0
		for _, c := range cmds {
			if c.ID == battle.CmdActDo {
				actor = c.Actor
				break
			}
		}
		if actor != tc.actor {
			t.Fatalf("foe prestige/speed %d/%d: first %d want %d", tc.foeW, tc.speed, actor, tc.actor)
		}
	}
}

func TestPrestigeAchievementProtocol(t *testing.T) {
	c, p, srv := actWorld(t, "prestige")
	srv.ladder.join(p.UID, p.grade())
	c.sendExt("ACHIEVE_PTO.C_GET_ALL_ACH", nil)
	m := c.expectExt("ACHIEVE_PTO.S_ACH_OPEN")
	rows := m["achieve"].([]xym.Msg)
	if len(rows) != 2 || num(rows[0], "aid") != 11115 || num(rows[1], "aid") < 10201 || num(m, "wei") < 860 {
		t.Fatal(m)
	}
}

func TestExtraRankPrestigeFollowsCurrentBoards(t *testing.T) {
	c, p, srv := actWorld(t, "rankwei")
	defer c.conn.Close()
	oldBoard := slRank
	slRank = &slBoard{}
	defer func() { slRank = oldBoard }()
	srv.cfg.PrestigeRules = PrestigeRules{Qujing: []PrestigeBand{{1, 10}}, Stars: []PrestigeBand{{1, 20}}, Shilian: []PrestigeBand{{1, 30}}, Tianti: []PrestigeBand{{1, 40}}}
	p.mu.Lock()
	defer p.mu.Unlock()
	ro := srv.roles.Get(p.UID)
	srv.qjRankMu.Lock()
	b := srv.qjRankBoard()
	b.Users[p.UID] = &qjRankRow{UID: p.UID, Name: ro.Name, Best: map[int][2]int{1: {1, 1}}}
	srv.qjRankMu.Unlock()
	slRank.put(slRow{UID: p.UID, Grade: ro.Grade, Star: 100, Stage: 10})
	srv.tianti.do(func() bool { srv.tianti.Users[p.UID] = &ttUser{Grade: ro.Grade, Score: 10000}; return false })
	// Add a real cleared gate so the character qualifies for the star board.
	p.Prog[0].Gates[10101] = 1
	base := prestigeOf(ro, p)
	if got := srv.prestigeOf(ro, p); got != base+100 {
		t.Fatalf("four first-place bonuses: %d want %d", got, base+100)
	}
	srv.qjRankMu.Lock()
	b.Users[1] = &qjRankRow{UID: 1, Name: "ahead", Best: map[int][2]int{1: {99, 99}}}
	srv.qjRankMu.Unlock()
	slRank.put(slRow{UID: 1, Grade: ro.Grade, Star: 999, Stage: 99})
	srv.tianti.do(func() bool {
		srv.tianti.Users[p.UID].Score = 0
		// 真人榜不再用假玩家垫底，明确增加一位更高分参赛者。
		srv.tianti.Users[1] = &ttUser{Grade: ro.Grade, Score: 10001}
		return false
	})
	// A newer star-board snapshot places another player ahead.
	srv.lrCache.Lock()
	srv.lrCache.list = append(srv.lrCache.list, &lrEntry{UID: 1, Star: 999999})
	srv.lrCache.Unlock()
	if got := srv.prestigeOf(ro, p); got != base {
		t.Fatalf("old rank bonuses retained: %d want %d", got, base)
	}
}
