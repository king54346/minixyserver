package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// TestLevelRank：星级 / 进度 / 录像三个排行都能拉到，自己上榜时 self_pos 指向自己那一行。
func TestLevelRank(t *testing.T) {
	c, p, _ := actWorld(t, "lvrank")
	p.mu.Lock()
	gid := 0
	for g := range p.Prog[0].Gates {
		gid = max(gid, g)
	}
	if gid == 0 {
		gid = 10101
		p.Prog[0].Gates[gid] = 1
	}
	star, task, _ := progressOf(p)
	p.mu.Unlock()
	c.sendExt("TASK_PTO.C_APPLY_STAR_RANK", nil)
	m := c.expectExt("TASK_PTO.S_SEND_STAR_RANK")
	l := m["list"].([]xym.Msg)
	if num(m, "self_pos") < 1 || num(l[num(m, "self_pos")-1], "star") != star || num(m, "self_star") != star {
		t.Fatalf("star rank %v", m)
	}
	c.sendExt("TASK_PTO.C_APPLY_TASK_RANK", nil)
	if m := c.expectExt("TASK_PTO.S_SEND_TASK_RANK"); num(m, "self_taskid") != task || num(m, "self_pos") < 1 {
		t.Fatalf("task rank %v", m)
	}
	c.sendExt("TASK_PTO.C_APPLY_VIDEO_RANK", nil)
	c.expectExt("TASK_PTO.S_SEND_VIDEO_RANK")
	c.sendExt("TASK_PTO.C_APPLY_VIDEO_LIST", xym.Msg{"uid": int64(p.UID), "page": int64(1)})
	c.expectExt("TASK_PTO.S_SEND_VIDEO_LIST")
}
