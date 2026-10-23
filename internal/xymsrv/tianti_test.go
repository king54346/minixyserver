package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// TestTianti：初始 1000 分、50 级组榜 81 人；匹配回对手 + 换对手次数 + state 1；打一场扣 1 点活力、
// 积分 ±20、弹 type 15；活力用完提示。
func TestTianti(t *testing.T) {
	c, p, srv := actWorld(t, "ttplayer")
	c.sendExt("LADDER_PTO.C_OPEN_LADDER", nil)
	if m := c.expectExt("LADDER_PTO.S_OPEN_LADDER"); num(m, "score") != ttInitScore || num(m, "state") != 0 {
		t.Fatalf("open %v", m)
	}
	c.expectExt("LADDER_PTO.S_GET_PAIR_TIME")
	c.sendExt("LADDER_PTO.C_OPEN_RANK", xym.Msg{"type": 50})
	if l := c.expectExt("LADDER_PTO.S_OPEN_RANK")["list"].([]xym.Msg); len(l) != 81 || // 50 级组：80 个假玩家（45、50 级）+ 自己
		num(l[0], "pos") != 1 ||
		num(l[0], "score") < num(l[1], "score") || l[0]["name"] == "" {
		t.Fatalf("rank %v", l[:2])
	}
	c.sendExt("LADDER_PTO.C_START_FIGHT", nil) // 没匹配
	c.until(t, "LADDER_PTO.S_GET_PAIR_TIME")

	c.sendExt("LADDER_PTO.C_START_PAIR", nil)
	f := c.expectExt("LADDER_PTO.S_OPEN_FIGHT")
	if f["name"] == "" || num(f, "icon1") == 0 || num(f, "fightpoint") <= 0 || f["servername"] != ttServerName {
		t.Fatalf("pair %v", f)
	}
	if m := c.expectExt("LADDER_PTO.S_GET_PAIR_TIME"); num(m, "times") != 0 {
		t.Fatalf("times %v", m)
	}
	if m := c.expectExt("LADDER_PTO.S_OPEN_LADDER"); num(m, "state") != 1 {
		t.Fatalf("state %v", m)
	}
	c.sendExt("LADDER_PTO.C_START_PAIR", nil)
	c.expectExt("LADDER_PTO.S_OPEN_FIGHT")
	if m := c.expectExt("LADDER_PTO.S_GET_PAIR_TIME"); num(m, "times") != 1 {
		t.Fatalf("times %v", m)
	}
	c.collect()

	hp := srv.roles.Get(p.UID).HPoint
	c.sendExt("LADDER_PTO.C_START_FIGHT", nil)
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	r := got["ETC_PTO.S_REWARD_MSG"]
	open := got["LADDER_PTO.S_OPEN_LADDER"]
	win := got["LADDER_PTO.S_SEND_SHARE_INFO"] != nil
	wantType := boxFightLose // 输了是战斗失败界面，赢了是天梯胜利（15）
	if win {
		wantType = 15
	}
	if got["FIGHT_PTO.S_FIGHT_RECORD"] == nil || num(r, "type") != wantType || r["record"] == "" {
		t.Fatalf("fight %v", got)
	}
	wantScore, wantGold := ttInitScore-ttStep, ttLoseGold
	if win {
		wantScore, wantGold = ttInitScore+ttStep, ttWinGold
	}
	if num(open, "state") != 0 || num(open, "score") != wantScore || num(r, "gold") != wantGold ||
		srv.roles.Get(p.UID).HPoint != hp-1 || p.tasks().Today[evTianti] != 1 {
		t.Fatalf("after fight %v %v", open, r)
	}

	srv.roles.Update(p.UID, func(ro *Role) bool { ro.HPoint = 0; return true })
	c.sendExt("LADDER_PTO.C_START_PAIR", nil)
	c.until(t, "LADDER_PTO.S_OPEN_LADDER")
	c.sendExt("LADDER_PTO.C_START_FIGHT", nil)
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); m["msg"] != "活力不足，天梯每场战斗消耗一点活力。" {
		t.Fatalf("no hp %v", m)
	}
}
