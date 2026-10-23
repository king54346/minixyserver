package xymsrv

import (
	"sort"
	"testing"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestGateStar：通关后成就挑战 1→2→3 星（S_GATE_INFO / S_REWARD_MSG 的 star），满星再挑战被拒、重置回 1 星；
// 过关与两档成就录像都进 S_RECORD_LIST，C_PLAY_RECORD{type 2} 重发录像。
func TestGateStar(t *testing.T) {
	old := enemyTeamFor
	enemyTeamFor = func(gamedata.GateMode) []*battle.Fighter { return weakTeam() }
	t.Cleanup(func() { enemyTeamFor = old })
	c, p, _ := actWorld(t, "starer")

	gids := make([]int, 0, len(gamedata.Gates))
	for gid := range gamedata.Gates {
		gids = append(gids, gid)
	}
	sort.Ints(gids)
	gid := gids[0]
	task := gamedata.Gates[gid].Task
	if gamedata.Achieves[gamedata.Gates[gid].Mode[0].Achieve[0]] == nil {
		t.Fatalf("gate %d has no achieve", gid)
	}
	var team []xym.Msg
	for k, pet := range p.sortedPets()[:3] {
		team = append(team, xym.Msg{"fight_pos": int64(k + 1), "npcnum": int64(pet.NpcNum), "wear_pos": int64(k + 1)})
	}
	fight := func() map[string]xym.Msg {
		t.Helper()
		c.sendExt("TASK_PTO.C_START_FIGHT_WITH_TMP_TEAM", xym.Msg{"gid": int64(gid), "type": int64(0), "ach": int64(1),
			"leader_num": team[0]["npcnum"], "tmp_team": team})
		return c.until(t, "ETC_PTO.S_REWARD_MSG")
	}

	c.sendExt("TASK_PTO.C_START_FIGHT_WITH_TMP_TEAM", xym.Msg{"gid": int64(gid), "type": int64(0), "ach": int64(1),
		"leader_num": team[0]["npcnum"], "tmp_team": team})
	c.expectMsg(t, "先通过此关")

	// 普通通关：1 星 + 过关录像
	c.sendExt("TASK_PTO.C_START_FIGHT", xym.Msg{"gid": int64(gid), "type": int64(0), "pos1": int64(1), "pos2": int64(2), "pos3": int64(3)})
	c.until(t, "ETC_PTO.S_REWARD_END")
	if p.Prog[0].gateStar(gid) != 1 {
		t.Fatalf("star after clear %d", p.Prog[0].gateStar(gid))
	}
	for want := 2; want <= 3; want++ {
		got := fight()
		if num(got["ETC_PTO.S_REWARD_MSG"], "type") != 2 || num(got["ETC_PTO.S_REWARD_MSG"], "star") != want ||
			num(got["TASK_PTO.S_GATE_INFO"], "star") != want || got["FIGHT_PTO.S_FIGHT_RECORD"] == nil {
			t.Fatalf("challenge to %d: %v", want, got)
		}
	}
	if len(gamedata.TaskGates[task]) > 1 && p.Prog[0].Stars[task] != 0 { // 任务下其他关没通：任务仍未完成
		t.Fatalf("task star %d", p.Prog[0].Stars[task])
	}
	c.sendExt("TASK_PTO.C_START_FIGHT_WITH_TMP_TEAM", xym.Msg{"gid": int64(gid), "type": int64(0), "ach": int64(1),
		"leader_num": team[0]["npcnum"], "tmp_team": team})
	c.expectMsg(t, "都已达成")

	c.sendExt("TASK_PTO.C_APPLY_RECORD", xym.Msg{"type": int64(0), "gid": int64(gid)})
	if l := c.expectExt("TASK_PTO.S_RECORD_LIST")["list"].([]xym.Msg); len(l) != 3 || l[0]["name"] != "starer" || num(l[1], "fight_point") <= 0 {
		t.Fatalf("records %v", l)
	}
	c.sendExt("FIGHT_PTO.C_PLAY_RECORD", xym.Msg{"type": int64(2), "gid": int64(gid), "ttype": int64(0), "rdtype": int64(3)})
	if r := c.expectExt("FIGHT_PTO.S_FIGHT_RECORD"); r["name1"] != "starer" || len(r["record"].([]byte)) == 0 {
		t.Fatalf("play %v", r)
	}

	c.sendExt("TASK_PTO.C_CLEAR_GATE_STAR", xym.Msg{"gid": int64(gid), "type": int64(0)})
	if m := c.expectExt("TASK_PTO.S_GATE_INFO"); num(m, "star") != 1 {
		t.Fatalf("clear %v", m)
	}
}
