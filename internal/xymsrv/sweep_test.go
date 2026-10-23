package xymsrv

import (
	"strings"
	"testing"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestSweep：没通关不能扫；通关后扫荡 5 次（先用券，不够花元宝）扣体力、发奖励、回 S_GATE_LIST 带券数；
// 花满 100 元宝得 1 张扫荡券。
func TestSweep(t *testing.T) {
	c, p, srv := actWorld(t, "sweep")
	c.sendExt("TASK_PTO.C_EXPLORE", xym.Msg{"gateid": 10101, "type": 0})
	c.expectExt("ETC_PTO.S_RET_MSG")

	p.mu.Lock()
	p.clearGate(0, 10101)
	p.SweepTickets = 2
	p.mu.Unlock()
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.TPoint, ro.Gold = 60, 1000; return true })
	before := srv.roles.Get(p.UID)
	c.sendExt("TASK_PTO.C_EXPLORE", xym.Msg{"gateid": 10101, "type": 1})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	after := srv.roles.Get(p.UID)
	if gi := got["TASK_PTO.S_GATE_INFO"]; num(gi, "times") != 6 || num(gi, "tickets") != 0 { // 今日次数：之前 1 次 + 扫荡 5 次；S_GATE_INFO 也要带券数，否则客户端显示成 0
		t.Fatalf("gate info %v", gi)
	}
	if gl := got["TASK_PTO.S_GATE_LIST"]; gl == nil || num(gl, "tickets") != 0 {
		t.Fatalf("gate list %v", gl)
	}
	if after.Gold != before.Gold-3*sweepGold || after.TPoint >= before.TPoint || after.Cash <= before.Cash || p.Prog[0].Gates[10101] != 6 {
		t.Fatalf("sweep gold %d→%d tp %d→%d cash %d→%d times %d", before.Gold, after.Gold, before.TPoint, after.TPoint,
			before.Cash, after.Cash, p.Prog[0].Gates[10101])
	}

	p.mu.Lock()
	p.trackGoldSpent(150)
	p.trackGoldSpent(60)
	p.mu.Unlock()
	if p.SweepTickets != 2 || p.GoldSpent != 16 { // 扫荡花的 6 元宝也算：6+150+60 = 216
		t.Fatalf("tickets %d rem %d", p.SweepTickets, p.GoldSpent)
	}
}

// TestSkillFull：神通满的卡在战报里 skillfull 1。
func TestSkillFull(t *testing.T) {
	f := &battle.Fighter{SkillFull: true}
	if f.TeamMsg()["skillfull"] != 1 {
		t.Fatal("skillfull")
	}
}

// TestBuyBag：扩充护法背包先弹 S_ASK_BOX，点「购买」（opt 0）扣 300 元宝、上限 +5；点关闭（opt 1）不扣。
func TestBuyBag(t *testing.T) {
	c, p, srv := actWorld(t, "bag")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 1000; return true })
	before := p.BagLimit[bagPet]
	c.sendExt("TASK_PTO.C_BUY_BAG", xym.Msg{"type": bagPet})
	ask := c.expectExt("ETC_PTO.S_ASK_BOX")
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": ask["session"], "opt": 1})
	c.sendExt("TASK_PTO.C_BUY_BAG", xym.Msg{"type": bagPet})
	ask = c.expectExt("ETC_PTO.S_ASK_BOX")
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": ask["session"], "opt": 0})
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	if m := c.expectExt("ETC_PTO.S_SEND_BAG_COUNT"); num(m, "count") != before+bagExpandStep || srv.roles.Get(p.UID).Gold != 1000-bagExpandGold {
		t.Fatalf("bag %v gold %d", m, srv.roles.Get(p.UID).Gold)
	}
}

// TestDailyPityCount：每日推荐的「再招募N次必得金卡」随未出 5 星的抽数减少。
func TestDailyPityCount(t *testing.T) {
	p := newPlayer(1)
	now := time.Now()
	if n := p.mallMsg(now)["daily_left_2_baodi"]; n != 10 {
		t.Fatalf("fresh %v", n)
	}
	p.mall(now).Pity[5] = 3
	if n := p.mallMsg(now)["daily_left_2_baodi"]; n != 7 {
		t.Fatalf("after 3 %v", n)
	}
}

// TestSweepReset：有每日次数限制的关卡扫荡到次数用完后，再点扫荡弹「重置挑战次数并继续扫荡」，确认后扣元宝、直接扫荡。
func TestSweepReset(t *testing.T) {
	gid := 0
	for id, g := range gamedata.Gates {
		if m := g.Mode[0]; m.Limit > 0 && (gid == 0 || id < gid) {
			gid = id
		}
	}
	if gid == 0 {
		t.Skip("no limited gate")
	}
	limit := gamedata.Gates[gid].Mode[0].Limit
	c, p, srv := actWorld(t, "sweepreset")
	p.mu.Lock()
	if p.Prog[0] == nil {
		p.Prog[0] = &Progress{Gates: map[int]int{}, Stars: map[int]int{}}
	}
	for range limit {
		p.clearGate(0, gid)
	}
	p.mu.Unlock()
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.TPoint, ro.Gold, ro.Grade = 200, 1000, 30; return true })
	c.sendExt("TASK_PTO.C_EXPLORE", xym.Msg{"gateid": gid, "type": 0})
	ask := c.until(t, "ETC_PTO.S_ASK_BOX")["ETC_PTO.S_ASK_BOX"]
	if !strings.Contains(ask["text"].(string), "继续扫荡") {
		t.Fatalf("ask %v", ask)
	}
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": ask["session"], "opt": 0})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	if g := srv.roles.Get(p.UID).Gold; g != 1000-gateResetGold-sweepGold || p.Prog[0].today(gid) != 1 {
		t.Fatalf("gold %d today %d", g, p.Prog[0].today(gid))
	}
}
