package xymsrv

import (
	"testing"
	"turnrpg/internal/xym"
)

func TestSweepKeepsSelectedNormalModeAfterEliteClear(t *testing.T) {
	c, p, srv := actWorld(t, "sweepmode")
	const gid = 20303
	p.mu.Lock()
	p.clearGate(0, gid)
	p.clearGate(1, gid)
	normal, elite := p.Prog[0].today(gid), p.Prog[1].today(gid)
	p.mu.Unlock()
	srv.roles.Update(p.UID, func(r *Role) bool { r.Grade = 40; r.TPoint = 100; r.Gold = 1000; return true })
	// Last visited list selects the mode; C_EXPLORE.type is batch size.
	c.sendExt("TASK_PTO.C_APPLY_GATE_LIST", xym.Msg{"taskid": 203, "type": 0})
	c.expectExt("TASK_PTO.S_GATE_LIST")
	c.sendExt("TASK_PTO.C_EXPLORE", xym.Msg{"gateid": gid, "type": 1})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	if num(got["TASK_PTO.S_GATE_INFO"], "type") != 0 || p.Prog[0].today(gid) != normal+5 || p.Prog[1].today(gid) != elite {
		t.Fatal("normal sweep consumed elite quota or used its two-fight limit")
	}
	c.sendExt("TASK_PTO.C_APPLY_GATE_LIST", xym.Msg{"taskid": 203, "type": 1})
	c.expectExt("TASK_PTO.S_GATE_LIST")
	c.sendExt("TASK_PTO.C_EXPLORE", xym.Msg{"gateid": gid, "type": 0})
	got = c.until(t, "ETC_PTO.S_REWARD_MSG")
	if num(got["TASK_PTO.S_GATE_INFO"], "type") != 1 || p.Prog[0].today(gid) != normal+5 || p.Prog[1].today(gid) != elite+1 {
		t.Fatal("elite sweep consumed normal quota")
	}
}
