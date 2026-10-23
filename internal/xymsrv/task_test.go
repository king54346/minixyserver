package xymsrv

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// decodeRecord / checkRecord：battle.DecodeRecord / battle.Replay 出错即判测试失败。
func decodeRecord(t *testing.T, b []byte) []battle.Cmd {
	t.Helper()
	cmds, err := battle.DecodeRecord(b)
	if err != nil {
		t.Fatal(err)
	}
	return cmds
}

func checkRecord(t *testing.T, team1, team2 []*battle.Fighter, rec []byte) bool {
	t.Helper()
	win, err := battle.Replay(team1, team2, rec)
	if err != nil {
		t.Fatal(err)
	}
	return win
}

// TestSimulateAllGates：初始卡和属性放大 200 倍的同一队，分别打全部关卡的普通与精英阵容。
// 每场战报都要逐字节可解析、血量回放自洽、胜负与结果一致——覆盖所有怪物身上的神通与法术。
func TestSimulateAllGates(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	n, wins := 0, 0
	all := xym.Msg{"pos1": int64(1), "pos2": int64(2), "pos3": int64(3), "pos4": int64(4), "pos5": int64(5)}
	for gid, g := range gamedata.Gates {
		for typ := range 2 {
			team2 := battle.EnemyTeam(g.Mode[typ])
			if len(team2) == 0 {
				continue
			}
			for _, strong := range []bool{false, true} {
				team1, _ := newPlayer(1).playerTeam(all)
				if strong {
					for _, f := range team1 {
						f.Att, f.HP, f.MaxHP, f.Grade = f.Att*200, f.HP*200, f.MaxHP*200, 100
					}
				}
				t1, t2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
				res := battle.Simulate(team1, battle.CloneTeam(team2), rng)
				if win := checkRecord(t, t1, t2, res.Record); win != res.Win {
					t.Fatalf("gate %d type %d: record win=%v, result %v", gid, typ, win, res.Win)
				}
				if strong && res.Win {
					wins++
				}
				n++
			}
		}
	}
	t.Logf("%d fights, strong team won %d of %d", n, wins, n/2)
}

// TestFirstGate：新号打第一关（10101）赢，拿到奖励、扣体力，下一关解锁；打完 101 的三关后任务通关。
func TestFirstGate(t *testing.T) {
	addr, _ := startServer(t, DefaultConfig())
	c := enterWorld(t, addr, "fighter")
	fight := func(gid int) xym.Msg {
		c.sendExt("TASK_PTO.C_START_FIGHT", xym.Msg{"gid": gid, "type": 0,
			"pos1": 1, "pos2": 2, "pos3": 3, "pos4": 4, "pos5": 5})
		rec := c.expectExt("FIGHT_PTO.S_FIGHT_RECORD")
		c.expectExt("ETC_PTO.S_REWARD_START")
		return rec
	}

	c.sendExt("TASK_PTO.C_APPLY_GATE_LIST", xym.Msg{"taskid": 101, "type": 0})
	if l := c.expectExt("TASK_PTO.S_GATE_LIST")["list"].([]xym.Msg); len(l) != 1 || l[0]["gid"] != int64(10101) {
		t.Fatalf("gate list %v", l)
	}
	for i, gid := range gamedata.TaskGates[101] {
		rec := fight(gid)
		if len(rec["team1"].([]xym.Msg)) != len(starterPets) {
			t.Fatalf("team1 %v", rec["team1"])
		}
		steps := decodeRecord(t, rec["record"].([]byte))
		if last := steps[len(steps)-1]; last.ID != battle.CmdWarEnd || last.Magic != 1 {
			t.Fatalf("gate %d lost (%d cmds)", gid, len(steps))
		}
		attr := c.expectExt("ETC_PTO.S_ATTR_UPDATE")["attr"].([]xym.Msg)
		if attr[0]["name"] != "tpoint" || attr[0]["val"] != int64(maxTPoint-2*(i+1)) {
			t.Fatalf("attr %v", attr)
		}
		c.expectExt("PET_PTO.S_UPDATE_PET")
		if gi := c.expectExt("TASK_PTO.S_GATE_INFO"); gi["gid"] != int64(gid) || gi["times"] != int64(1) {
			t.Fatalf("gate info %v", gi)
		}
		var rm xym.Msg
		for rm == nil { // 掉落的新卡 / 道具先到，再是 S_REWARD_MSG
			main, body := c.recv()
			_, spec, m, err := xym.DecodeExt(xym.S2C, body)
			if main != xym.MainExt || err != nil {
				t.Fatalf("main %d err %v", main, err)
			}
			if specName(spec) == "ETC_PTO.S_REWARD_MSG" {
				rm = m
			}
		}
		// 奖励表 item 字段里概率 10000 的必掉项都要在 list 里：101 任务的 GPDZ1（随机 1 星卡）、HP（魂魄）
		got := map[string]int{}
		for _, g := range rm["list"].([]xym.Msg) {
			typ := int32(g["type"].(int64))
			switch g["rtype"] {
			case int64(rtCard):
				got["GPDZ"+strconv.Itoa(int(gamedata.Pets[typ].Star))]++
			case int64(rtSoul):
				got["HP"+strconv.Itoa(int(typ))]++
			}
		}
		rw := gamedata.Rewards[gamedata.Gates[gid].Mode[0].Reward]
		for _, e := range strings.Split(rw.Drops[0], ",") {
			if code, prob, _ := parseDrop(e); prob == 10000 && (strings.HasPrefix(code, "GPDZ") || strings.HasPrefix(code, "HP")) && got[code] == 0 {
				t.Fatalf("gate %d missing %s: %v", gid, code, rm)
			}
		}
		if rm["cash"].(int64) <= 0 {
			t.Fatalf("reward %v", rm)
		}
		c.expectExt("ETC_PTO.S_REWARD_END")
	}
	c.sendExt("TASK_PTO.C_OPEN_MAP_2", xym.Msg{"mapid": 0, "type": 0})
	if m := c.expectExt("TASK_PTO.S_SEND_MAP_2"); m["curr_task"] != int64(101) || m["map_task_fin"] != int64(1) {
		t.Fatalf("map after task 101: %v", m)
	}
	c.sendExt("TASK_PTO.C_APPLY_GATE_LIST", xym.Msg{"taskid": 102, "type": 0})
	if l := c.expectExt("TASK_PTO.S_GATE_LIST")["list"].([]xym.Msg); len(l) != 1 || l[0]["gid"] != int64(10201) {
		t.Fatalf("task 102 gate list %v", l)
	}
}

// TestTalentCoverage 报告新手阶段（前 3 张地图的怪物 + 初始卡）神通出场次数的实现覆盖率。
func TestTalentCoverage(t *testing.T) {
	seen, missing := 0, map[string]int{}
	count := func(id int32) {
		ti := gamedata.Talents[id]
		if ti == nil {
			return
		}
		seen++
		if !battle.Implemented(ti) {
			missing[ti.Family]++
		}
	}
	for _, g := range gamedata.Gates {
		if g.Task/100 > 3 {
			continue
		}
		for _, m := range g.Mode {
			for _, id := range gamedata.Formations[m.CA] {
				if n := gamedata.NPCs[id]; n != nil {
					for _, tid := range n.Talents {
						count(tid)
					}
				}
			}
		}
	}
	for _, typ := range starterPets {
		for _, l := range gamedata.Pets[typ].Talents { // 各槽 1 级
			if len(l) > 0 {
				count(l[0])
			}
		}
	}
	miss := 0
	for _, n := range missing {
		miss += n
	}
	t.Logf("early-game talent occurrences: %d, implemented %.0f%%, %d families missing: %v",
		seen, 100*float64(seen-miss)/float64(seen), len(missing), missing)

	// 全部关卡怪物 + 全部卡牌（各槽所有等级）的神通族
	all, allMiss := map[string]int{}, map[string]int{}
	note := func(id int32) {
		if ti := gamedata.Talents[id]; ti != nil {
			all[ti.Family]++
			if !battle.Implemented(ti) {
				allMiss[ti.Family]++
			}
		}
	}
	for _, n := range gamedata.NPCs {
		for _, id := range n.Talents {
			note(id)
		}
	}
	for _, pi := range gamedata.Pets {
		for _, l := range pi.Talents {
			for _, id := range l {
				note(id)
			}
		}
	}
	t.Logf("all monsters + cards: %d families, %d implemented, missing %d: %v", len(all), len(all)-len(allMiss), len(allMiss), allMiss)
	if float64(miss) > 0.4*float64(seen) {
		t.Errorf("coverage below 60%%")
	}
}

// TestStarterVsMap1：初始卡打第一张地图普通关的胜率（平衡参考）。
func TestStarterVsMap1(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	all := xym.Msg{"pos1": int64(1), "pos2": int64(2), "pos3": int64(3), "pos4": int64(4), "pos5": int64(5)}
	for task := 101; task <= 115; task += 2 {
		wins, n := 0, 0
		for _, gid := range gamedata.TaskGates[task] {
			for range 20 {
				team1, _ := newPlayer(1).playerTeam(all)
				if battle.Simulate(team1, battle.EnemyTeam(gamedata.Gates[gid].Mode[0]), rng).Win {
					wins++
				}
				n++
			}
		}
		t.Logf("task %d: win %d/%d", task, wins, n)
	}
}

// TestCardExp：抓包里一张 5 星卡（35043）29 级 exp 2143，打一关得 1339 后变成 30 级 exp 449；
// 经验表与 mnxy/数据.xlsx（1~50 级）抽查一致；等级受人物等级×2 限制。
func TestCardExp(t *testing.T) {
	for _, c := range [][3]int32{{1, 1, 27}, {2, 1, 40}, {5, 29, 3033}, {1, 50, 10490}, {4, 41, 11812}, {5, 50, 31470}, {3, 99, 999999}, {3, 100, 0}} {
		if got := gamedata.CardNeed(int(c[0]), int(c[1])); got != c[2] {
			t.Errorf("CardNeed(%d星, %d级) = %d, want %d", c[0], c[1], got, c[2])
		}
	}
	if gamedata.Pets[35043].Star != 5 {
		t.Fatal("35043 should be 5 star")
	}
	pet := &Pet{Type: 35043, Grade: 29, Exp: 2143}
	if up := pet.addExp(1339, 100); up != 1 || pet.Grade != 30 || pet.Exp != 449 {
		t.Fatalf("after addExp: %+v up=%d", pet, up)
	}
	capped := &Pet{Type: 35043, Grade: 10}
	if capped.addExp(1_000_000, 10); capped.Grade != 10 {
		t.Fatalf("cap ignored: %+v", capped)
	}
	if capped.addExp(0, 12); capped.Grade != 12 {
		t.Fatalf("no catch-up after cap raised: %+v", capped)
	}
	if gamedata.RoleNeed(1) != 99 || gamedata.RoleNeed(48) != 905000 || gamedata.RoleNeed(49) != 999999 || gamedata.RoleNeed(50) != 0 || nextExp(50) != 999999 {
		t.Errorf("role exp: %d %d %d", gamedata.RoleNeed(1), gamedata.RoleNeed(49), gamedata.RoleNeed(50))
	}
}

// TestPetStatsMatchSheet：《攻击血量》表里没有属性类神通的卡，各等级攻击/血量与公式逐项一致。
func TestPetStatsMatchSheet(t *testing.T) {
	sheet := map[int32][][3]int32{ // 类型 -> {等级, 攻击, 血量}
		45022: {{1, 58, 381}, {20, 217, 1418}, {50, 468, 3055}, {100, 886, 5783}},  // 貔貅
		15007: {{1, 70, 305}, {20, 261, 1135}, {50, 564, 2446}, {100, 1068, 4630}}, // 杨贵妃
		15024: {{1, 70, 280}, {20, 260, 1040}, {50, 560, 2240}, {100, 1060, 4240}}, // 鬼谷子
		45018: {{1, 77, 252}, {20, 286, 936}, {50, 616, 2016}, {100, 1166, 3816}},  // 英招
		11002: {{1, 22, 81}, {20, 61, 222}, {50, 124, 446}, {100, 229, 818}},       // 东郭先生
	}
	for typ, rows := range sheet {
		pi := gamedata.Pets[typ]
		for _, r := range rows {
			if g := int(r[0]); pi.Att(g) != r[1] || pi.HP(g) != r[2] {
				t.Errorf("%s lv%d: att %d hp %d, sheet %d %d", pi.Name, g, pi.Att(g), pi.HP(g), r[1], r[2])
			}
		}
	}
}

// TestProgression：新号按顺序推普通关，打不过就回去刷上一关攒经验（模拟玩家行为，不计体力），
// 报告 3000 场内能推到哪个任务、卡牌多少级。平衡参考，不判失败。
func TestProgression(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 12))
	p := newPlayer(1)
	role := &Role{Grade: 1}
	all := xym.Msg{"pos1": int64(1), "pos2": int64(2), "pos3": int64(3), "pos4": int64(4), "pos5": int64(5)}
	fight := func(gid int) bool {
		team1, pets := p.playerTeam(all)
		mode := gamedata.Gates[gid].Mode[0]
		if !battle.Simulate(team1, battle.EnemyTeam(mode), rng).Win {
			return false
		}
		if rw := gamedata.Rewards[mode.Reward]; rw != nil {
			role.AddExp(int32(rw.MExp()))
			for _, pet := range pets {
				pet.addExp(int32(rw.PExp()), role.PetCap())
			}
		}
		p.clearGate(0, gid)
		return true
	}
	next := func() int {
		for _, gid := range gamedata.TaskGates[nextTask(p.Prog[0].Last)] {
			if p.Prog[0].Gates[gid] == 0 {
				return gid
			}
		}
		return 0
	}
	last, fights, stuck := 0, 0, 0
	for fights < 3000 {
		gid := next()
		if gid == 0 {
			break
		}
		fights++
		if fight(gid) {
			last, stuck = gid, 0
			continue
		}
		stuck++
		if last != 0 {
			fights++
			fight(last) // 刷上一关
		}
		if stuck > 300 {
			break
		}
	}
	grades := []int{}
	for _, pet := range p.Pets {
		grades = append(grades, pet.Grade)
	}
	t.Logf("after %d fights: role lv%d, last task %d, next gate %d (stuck %d), card grades %v", fights, role.Grade, p.Prog[0].Last, next(), stuck, grades)
}
