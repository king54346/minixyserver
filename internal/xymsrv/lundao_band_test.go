package xymsrv

import (
	"testing"
	"turnrpg/internal/xym"
)

func TestLundaoChallengeRefreshesAfterOpponentCrossesBand(t *testing.T) {
	c, p, srv := actWorld(t, "rebander")
	uid := p.UID + 1000
	if _, err := srv.roles.Create(uid, 1, "RebandTarget"); err != nil {
		t.Fatal(err)
	}
	grade := p.grade()
	srv.roles.Update(uid, func(r *Role) bool { r.Grade = grade; return true })
	group := ldGroup(grade)
	srv.ladder.do(func() bool {
		srv.ladder.Groups[group] = []int32{uid, p.UID}
		return true
	})
	c.sendExt("LUNDAO_PTO.C_OPEN_LUNDAO", nil)
	open := c.expectExt("LUNDAO_PTO.S_OPEN_LUNDAO")
	c.expectExt("LUNDAO_PTO.S_SEND_TIMES")
	if rows := open["list"].([]xym.Msg); len(rows) != 2 || num(rows[0], "uid") != int(uid) {
		t.Fatalf("initial ranks: %v", rows)
	}
	newGrade := 30
	if group == 30 {
		newGrade = 31
	}
	srv.roles.Update(uid, func(r *Role) bool { r.Grade = newGrade; return true })
	c.sendExt("LUNDAO_PTO.C_FIGHT_LUNDAO", xym.Msg{"rank": 1})
	c.expectMsg(t, "论道排名已变化")
	open = c.expectExt("LUNDAO_PTO.S_OPEN_LUNDAO")
	if rows := open["list"].([]xym.Msg); len(rows) != 1 || num(rows[0], "uid") != int(p.UID) || num(rows[0], "rank") != 1 {
		t.Fatalf("refreshed ranks: %v", rows)
	}
	if times := c.expectExt("LUNDAO_PTO.S_SEND_TIMES"); num(times, "times") != ldDailyTimes {
		t.Fatalf("stale request used an attempt: %v", times)
	}
	srv.ladder.do(func() bool {
		ranks := srv.ladder.Groups[ldGroup(newGrade)]
		if len(ranks) == 0 || ranks[len(ranks)-1] != uid {
			t.Fatal("cross-band opponent was not appended to the new group")
		}
		return false
	})
}

func TestLundaoBandFollowsCurrentGradeWhileSnapshotBusy(t *testing.T) {
	c, p, srv := actWorld(t, "bandwatch")
	uid := p.UID + 1000
	if _, err := srv.roles.Create(uid, 1, "BandTarget"); err != nil {
		t.Fatal(err)
	}
	srv.roles.Update(uid, func(r *Role) bool { r.Grade = 30; return true })
	target, err := srv.players.Get(uid)
	if err != nil {
		t.Fatal(err)
	}
	old := srv.realFoe(uid, nil)
	if old == nil || old.Grade != 30 {
		t.Fatal("missing initial snapshot")
	}
	srv.ladder.do(func() bool { srv.ladder.Groups = map[int][]int32{30: {uid, uid, p.UID}}; return true })
	for _, grade := range []int{30, 35, 40, 41, 50} {
		srv.roles.Update(uid, func(r *Role) bool { r.Grade = grade; return true })
		target.mu.Lock()
		for _, band := range []int{30, 40, 50} {
			c.sendExt("LUNDAO_PTO.C_OPEN_TOP", xym.Msg{"type": band})
			rows := c.expectExt("LUNDAO_PTO.S_OPEN_TOP")["list"].([]xym.Msg)
			seen := 0
			for _, r := range rows {
				if !ldInBand(num(r, "grade"), band) {
					t.Fatalf("grade %v leaked into band %d", r["grade"], band)
				}
				if num(r, "uid") == int(uid) {
					seen++
					if num(r, "grade") != grade {
						t.Fatalf("stale grade: %v", r)
					}
				}
			}
			want := 0
			if ldInBand(grade, band) {
				want = 1
			}
			if seen != want {
				t.Fatalf("grade %d band %d target count %d want %d", grade, band, seen, want)
			}
		}
		target.mu.Unlock()
	}
	if old.Grade != 30 {
		t.Fatal("shared snapshot mutated while being read")
	}
	if len(srv.ladder.Groups[30]) != 3 {
		t.Fatal("filtering rewrote global ladder")
	}
}
