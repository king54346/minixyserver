package xymsrv

import (
	"strings"
	"testing"
	"time"

	"turnrpg/internal/xym"
)

func TestTiantiBusyOpponentAfterColdStart(t *testing.T) {
	c, p, srv := actWorld(t, "ttcold")
	defer c.conn.Close()
	uid := p.UID + 100
	if _, err := srv.roles.Create(uid, 1, "ColdFoe"); err != nil {
		t.Fatal(err)
	}
	target, err := srv.players.Get(uid)
	if err != nil {
		t.Fatal(err)
	}
	target.mu.Lock()
	defer target.mu.Unlock()
	original := target.Pets[0].Type
	target.Pets[0].Type = 0 // Busy in-memory edits are neither locked nor durable for readers.
	defer func() { target.Pets[0].Type = original }()
	f := srv.realFoe(uid, p)
	if f == nil || len(f.Team) == 0 || f.Faces[0] == 0 || f.FP <= 0 {
		t.Fatal("cold/busy persisted team unavailable")
	}
	for _, fighter := range f.Team {
		if fighter.Icon == 0 {
			t.Fatal("read a partially edited shared team")
		}
	}
}

func TestTiantiDailyPairLimitAndDefenseFreeze(t *testing.T) {
	if ttDayLimit != 25 {
		t.Fatal("daily limit must match the 25-pair patch")
	}
	c, p, srv := actWorld(t, "ttdaily")
	const uid int32 = 30001
	if _, err := srv.roles.Create(uid, 1, "RealFoe"); err != nil {
		t.Fatal(err)
	}
	target, err := srv.players.Get(uid)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < ttDayLimit; i++ {
		// A completed battle clears Foe; each iteration represents a fresh match.
		srv.tianti.do(func() bool {
			if u := srv.tianti.Users[p.UID]; u != nil {
				u.Foe = 0
			}
			return true
		})
		c.sendExt("LADDER_PTO.C_START_PAIR", nil)
		got := c.until(t, "LADDER_PTO.S_OPEN_LADDER")
		if num(got["LADDER_PTO.S_OPEN_FIGHT"], "uid") != int(uid) || num(got["LADDER_PTO.S_OPEN_FIGHT"], "icon1") == 0 {
			t.Fatal("real opponent preview missing")
		}
		if i == 0 {
			target.mu.Lock()
			target.Pets = nil
			target.mu.Unlock()
		}
	}
	srv.tianti.do(func() bool { srv.tianti.Users[p.UID].Foe = 0; return true })
	c.sendExt("LADDER_PTO.C_START_PAIR", nil)
	if msg := c.expectExt("ETC_PTO.S_SEND_NO_CACHE_MSG"); msg["msg"] != "次数已经用完，请明天再战。" {
		t.Fatalf("limit popup: %v", msg)
	}
	// Reload the persisted global state: restarting cannot reset today's quota.
	reloaded := openTianti(srv.tianti.st)
	if u := reloaded.Users[p.UID]; u == nil || u.DayN != 25 || u.Day != ttNow().Format("2006-01-02") {
		t.Fatal("daily quota was not persisted")
	}
	c.sendExt("LADDER_PTO.C_START_PAIR", nil)
	c.expectExt("ETC_PTO.S_SEND_NO_CACHE_MSG")
	srv.tianti.do(func() bool {
		u := srv.tianti.Users[p.UID]
		if u.DayN != 25 {
			t.Fatalf("pair count=%d", u.DayN)
		}
		u.Day = "2000-01-01"
		return true
	})
	c.sendExt("LADDER_PTO.C_START_PAIR", nil)
	c.until(t, "LADDER_PTO.S_OPEN_LADDER")
	srv.tianti.do(func() bool {
		if srv.tianti.Users[p.UID].DayN != 1 {
			t.Fatal("daily pair limit did not reset")
		}
		return false
	})
}

func TestTiantiScoreSeasonRules(t *testing.T) {
	for _, tc := range []struct {
		mine, foe int
		win       bool
		want      int
	}{{1000, 1000, true, 10}, {1000, 1000, false, -10}, {1000, 1010, true, 11}, {1000, 1010, false, -9}, {1000, 2000, true, 15}, {1000, 2000, false, -1}, {2000, 1000, true, 1}, {2000, 1000, false, -15}} {
		if got := ttScoreDelta(tc.mine, tc.foe, tc.win); got != tc.want {
			t.Fatalf("score %+v=%d", tc, got)
		}
	}
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	before := time.Date(2026, 11, 1, 22, 1, 59, 0, loc)
	if ttWeek(before) != "2026-10-25" || ttWeek(before.Add(time.Second)) != "2026-11-01" {
		t.Fatal("season boundary mismatch")
	}
	tt := &Tianti{Week: "test", Users: map[int32]*ttUser{1: {Grade: 30, Score: 1801, Name: "ThreeBoards"}, 2: {Grade: 40, Score: 1000}, 3: {Grade: 50, Score: 900}}}
	if len(tt.rows(50)) != 3 || len(tt.rows(40)) != 2 || len(tt.rows(30)) != 1 {
		t.Fatal("cumulative rank filters mismatch")
	}
	var found bool
	for _, mail := range ttSettleSeason(tt) {
		if mail.to == 1 {
			found = true
			if mail.spec.Gold != 309288 || mail.spec.Cash != 31930000 || mail.spec.Soul != 248063 {
				t.Fatalf("cumulative rewards: %+v", mail.spec)
			}
		}
	}
	if !found {
		t.Fatal("rank award missing")
	}
	if ttDrawDelta(1000, 1008) != 0 || ttDrawDelta(1000, 1009) != 1 || ttDrawDelta(1009, 1000) != -1 {
		t.Fatal("draw score limits")
	}
}

func TestTiantiLegacyWeekPreservesPlayers(t *testing.T) {
	c, p, srv := actWorld(t, "ttlegacy")
	defer c.conn.Close()
	srv.tianti.mu.Lock()
	srv.tianti.Week = "2026-44"
	srv.tianti.Users = map[int32]*ttUser{p.UID: {Grade: 30, Score: 1234}}
	srv.tianti.mu.Unlock()
	srv.tianti.do(func() bool {
		if srv.tianti.Users[p.UID] == nil || srv.tianti.Users[p.UID].Score != 1234 {
			t.Fatal("legacy migration cleared season")
		}
		return false
	})
}

func TestTiantiRuleUsesCurrentProtocolAndSections(t *testing.T) {
	c, _, _ := actWorld(t, "ttrules")
	c.sendExt("ETC_PTO.C_OPEN_RULE_TIPS", xym.Msg{"text_id": 1008})
	text := c.expectExt("ETC_PTO.S_OPEN_RULE_TIPS")["text"].(string)
	if strings.Split(text, "#title")[0] != "天梯玩法规则" || strings.Count(text, "#title") != 6 || !strings.Contains(text, "#r") {
		t.Fatal("rule title/section format incorrect")
	}
	if !strings.Contains(text, "每天最多新匹配 25 次") {
		t.Fatal("displayed daily quota differs from the server")
	}
}

func TestTiantiNoOpponentDoesNotConsumeQuota(t *testing.T) {
	c, p, srv := actWorld(t, "ttempty")
	c.sendExt("LADDER_PTO.C_START_PAIR", nil)
	c.expectMsg(t, "暂时没有可以匹配的对手")
	srv.tianti.do(func() bool {
		if u := srv.tianti.Users[p.UID]; u != nil && u.DayN != 0 {
			t.Fatal("empty opponent pool consumed quota")
		}
		return false
	})
}
