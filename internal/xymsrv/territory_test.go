package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

func terrOpenMsg(t *testing.T, c *client) xym.Msg {
	t.Helper()
	all := c.until(t, "DONGFU_TERRITORY_PTO.S_TERRITORY_OPEN_MAIN")
	return all["DONGFU_TERRITORY_PTO.S_TERRITORY_OPEN_MAIN"]
}

// resetTerr 让每个用例从干净状态开始（battle 是包级单例，测试间不共享）。
// 同时把旧战场标记为 over，让它们的 tick goroutine 尽快退出——否则上一个用例的战场
// 会在 10 秒边界继续给同一个 uid 的会话推 OPEN_MAIN，干扰下一个用例。
func resetTerr() {
	stop := func(b *terrBattle) {
		b.mu.Lock()
		b.over = true
		b.mu.Unlock()
	}
	terrMgr.mu.Lock()
	for _, b := range terrMgr.open {
		stop(b)
	}
	for _, b := range terrMgr.byUID {
		stop(b)
	}
	terrMgr.open = map[string]*terrBattle{}
	terrMgr.byUID = map[int32]*terrBattle{}
	terrMgr.mu.Unlock()
}

// TestTerritorySignAndMove：报名后进战场（攻方出生点 X），按邻接移动、不能乱跳。
func TestTerritorySignAndMove(t *testing.T) {
	resetTerr()
	c, p, _, foe := terrSetup(t, "terr1")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	m := terrOpenMsg(t, c)
	if num(m, "side") != 0 || m["gate_key"] != "X" {
		t.Fatalf("open main: side=%v gate=%v", m["side"], m["gate_key"])
	}
	// X 的相邻点有 G（低级怪物A）。
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "G"})
	m = terrOpenMsg(t, c)
	if m["gate_key"] != "G" {
		t.Fatalf("move to G: gate=%v", m["gate_key"])
	}
	// W（妖王）不与 X 相邻，不能直接跳。
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "W"})
	c.until(t, "ETC_PTO.S_RET_MSG")
	terrMgr.mu.Lock()
	b := terrMgr.byUID[p.UID]
	terrMgr.mu.Unlock()
	if b == nil {
		t.Fatal("battle lost")
	}
	b.mu.Lock()
	gate := b.players[p.UID].Gate
	b.mu.Unlock()
	if gate != "G" {
		t.Fatalf("illegal move changed gate to %q", gate)
	}
}

// TestTerritoryKillAndExchange：除妖给胜利点 + 仙气；奇异仙阵 50 仙气换 30 胜利点（每场一次）。
func TestTerritoryKillAndExchange(t *testing.T) {
	old := dyParamFor
	dyParamFor = func(int, int, int) int { return 1 } // 妖物压到最弱，保证除妖必胜
	t.Cleanup(func() { dyParamFor = old })
	resetTerr()
	c, p, _, foe := terrSetup(t, "terr2")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "G"})
	terrOpenMsg(t, c)

	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_KILL_MONSTER", nil)
	m := terrOpenMsg(t, c)
	// 官方：「挑战妖物和妖王是获得仙气的有效途径」——除妖只给仙气，胜利点来自擂台。
	if num(m, "my_score") != 0 || num(m, "xianqi") != 10 {
		t.Fatalf("kill monster score=%v xianqi=%v, want 0/10", m["my_score"], m["xianqi"])
	}

	terrMgr.mu.Lock()
	b := terrMgr.byUID[p.UID]
	terrMgr.mu.Unlock()
	b.mu.Lock()
	b.players[p.UID].Score = 120
	b.players[p.UID].Xianqi = 60
	b.mu.Unlock()
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_PING_GAME", nil)
	if num(terrOpenMsg(t, c), "has_exchange_score") != 1 {
		t.Fatal("has_exchange_score should be 1 with 60 xianqi")
	}

	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_EXCHANGE_SCORE", nil)
	m = terrOpenMsg(t, c)
	if num(m, "my_score") != 150 || num(m, "xianqi") != 10 {
		t.Fatalf("exchange: score=%v xianqi=%v, want 150/10", m["my_score"], m["xianqi"])
	}
	// 官方：每个玩家只能兑换一次 → 兑换后按钮标志位应为 0，再点也换不了。
	if num(m, "has_exchange_score") != 0 {
		t.Fatalf("has_exchange_score after exchange=%v, want 0", m["has_exchange_score"])
	}
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_EXCHANGE_SCORE", nil)
	c.until(t, "ETC_PTO.S_RET_MSG")
	if n := p.itemCount(terrSoulItem); n != 0 {
		t.Fatalf("Z exchange should not give 神魂, got %d", n)
	}
}

// TestTerritoryCultivate：攻击祭坛修炼 1 级花 15 仙气、+6% 攻击，并传送回出生点。
func TestTerritoryCultivate(t *testing.T) {
	resetTerr()
	c, p, _, foe := terrSetup(t, "terr9")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	// X → C（地界擂A）→ M（攻击祭坛）。
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "C"})
	terrOpenMsg(t, c)
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "M"})
	terrOpenMsg(t, c)
	terrMgr.mu.Lock()
	b := terrMgr.byUID[p.UID]
	terrMgr.mu.Unlock()
	b.mu.Lock()
	b.players[p.UID].Xianqi = 40
	b.mu.Unlock()

	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_ADD_PROP", nil)
	m := terrOpenMsg(t, c)
	if num(m, "attack_add") != 6 || num(m, "xianqi") != 25 || m["gate_key"] != "X" {
		t.Fatalf("cultivate: attack_add=%v xianqi=%v gate=%v, want 6/25/X", m["attack_add"], m["xianqi"], m["gate_key"])
	}
}

// TestSoulItemUse：使用「神魂」道具 → 洞府神魂 +1（洞府修为 20 级后每次修炼要 1 个）。
func TestSoulItemUse(t *testing.T) {
	c, p, _ := actWorld(t, "souluse")
	p.addItem(terrSoulItem, 3)
	pos := 0
	for _, it := range p.Items {
		if it.Type == terrSoulItem {
			pos = it.Pos
			break
		}
	}
	if pos == 0 {
		t.Fatal("item not added")
	}
	c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": int64(pos), "type": int64(terrSoulItem),
		"use_num": int64(1), "choose": int64(0), "usetype": int64(0)})
	c.until(t, "ETC_PTO.S_RET_MSG")
	if got := p.df().GodSoul; got != 1 {
		t.Fatalf("god_soul=%d, want 1", got)
	}
	if n := p.itemCount(terrSoulItem); n != 2 {
		t.Fatalf("left %d 神魂, want 2", n)
	}
}

// TestTerritoryOpenAutoJoin：直接打开领土界面（没有报名）也会自动进一场，避免客户端立刻收到 CLOSE_MAIN。
func TestTerritoryOpenAutoJoin(t *testing.T) {
	resetTerr()
	c, _, _, _ := terrSetup(t, "terr3")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_OPEN_MAIN", nil)
	m := terrOpenMsg(t, c)
	if m["gate_key"] != "X" || num(m, "side") != 0 {
		t.Fatalf("auto join: gate=%v side=%v", m["gate_key"], m["side"])
	}
}

// TestTerritoryChangeLineup：换阵界面数据来自 GET_LINEUP_INFO；切换只回主界面（不再重开换阵窗口），
// 未解锁的阵容会被拒绝。
func TestTerritoryChangeLineup(t *testing.T) {
	resetTerr()
	c, p, _, foe := terrSetup(t, "terr5")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)

	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_LINEUP_INFO", nil)
	all := c.until(t, "DONGFU_TERRITORY_PTO.S_TERRITORY_LINEUP_INFO")
	if num(all["DONGFU_TERRITORY_PTO.S_TERRITORY_LINEUP_INFO"], "current_lineup_no") != 1 {
		t.Fatalf("initial current_lineup_no=%v, want 1", all["DONGFU_TERRITORY_PTO.S_TERRITORY_LINEUP_INFO"]["current_lineup_no"])
	}

	// 新号只有 1 号阵容：切 2 号应只回提示，不重开换阵界面。
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_CHANGE_LINEUP", xym.Msg{"lineup_no": int64(2)})
	all = c.until(t, "ETC_PTO.S_RET_MSG")
	if _, ok := all["DONGFU_TERRITORY_PTO.S_TERRITORY_LINEUP_INFO"]; ok {
		t.Fatal("locked switch must not reopen change-team view")
	}

	// 解锁 2 号阵容后再切：只回主界面，lineup_no 变 2。
	p.Lineups = append(p.Lineups, &Lineup{No: 2})
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_CHANGE_LINEUP", xym.Msg{"lineup_no": int64(2)})
	m := terrOpenMsg(t, c)
	if num(m, "lineup_no") != 2 {
		t.Fatalf("open main lineup_no=%v, want 2", m["lineup_no"])
	}
}

// TestTerritoryKillLoseNoScore：除妖打输不结算（不给积分、不占点）。
func TestTerritoryKillLoseNoScore(t *testing.T) {
	old := dyParamFor
	dyParamFor = func(int, int, int) int { return 1 << 30 } // 妖物极强，必败
	t.Cleanup(func() { dyParamFor = old })
	resetTerr()
	c, p, _, foe := terrSetup(t, "terr6")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "G"})
	terrOpenMsg(t, c)

	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_KILL_MONSTER", nil)
	all := c.until(t, "ETC_PTO.S_RET_MSG")
	if _, ok := all["DONGFU_TERRITORY_PTO.S_TERRITORY_OPEN_MAIN"]; ok {
		t.Fatal("lost fight should not settle (open main received)")
	}
	terrMgr.mu.Lock()
	b := terrMgr.byUID[p.UID]
	terrMgr.mu.Unlock()
	b.mu.Lock()
	score := b.players[p.UID].Score
	left := 0
	if mob := b.mobs["G"]; mob != nil {
		left = mob.left
	}
	b.mu.Unlock()
	if score != 0 || left != 4 {
		t.Fatalf("after lost fight score=%d mobLeft=%d, want 0/4", score, left)
	}
}

// TestTerritoryTeamUsesLineup：战场里选中的阵容要真的用于战斗（不是一直用主界面的 1 号阵容）。
func TestTerritoryTeamUsesLineup(t *testing.T) {
	_, p, _ := actWorld(t, "terr7")
	before := p.FightNo
	t1, _ := p.terrTeam(1)
	if len(t1) == 0 {
		t.Fatal("lineup 1 should have starter pets")
	}
	if p.FightNo != before {
		t.Fatalf("FightNo not restored: %d -> %d", before, p.FightNo)
	}
	// 空的 2 号阵容建队应为空；证明 terrTeam 用的是指定阵容而不是 FightNo。
	p.Lineups = append(p.Lineups, &Lineup{No: 2})
	if t2, _ := p.terrTeam(2); len(t2) != 0 {
		t.Fatalf("lineup 2 is empty but team has %d fighters", len(t2))
	}
}

// TestTerritorySoulSettlement：胜利点换神魂的兜底——战场频道发「兑换神魂」，以及离场时按 50 点自动结算。
func TestTerritorySoulSettlement(t *testing.T) {
	resetTerr()
	c, p, _, foe := terrSetup(t, "terr8")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	terrMgr.mu.Lock()
	b := terrMgr.byUID[p.UID]
	terrMgr.mu.Unlock()
	b.mu.Lock()
	b.players[p.UID].Score = 120
	b.mu.Unlock()

	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SAY", xym.Msg{"msg": "兑换神魂", "kind": int64(0)})
	c.until(t, "ETC_PTO.S_SEND_TOP_MSG")
	if n := p.df().GodSoul; n != 2 {
		t.Fatalf("chat exchange gave %d 神魂, want 2", n)
	}
	if n := b.players[p.UID].Score; n != 20 {
		t.Fatalf("score after chat exchange=%d, want 20", n)
	}

	// 离场时剩 20 分：不够再折 1 个神魂，但正好达到官方的「≥20 胜利点离场奖励」→ +1。
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_LEAVE_GAME", nil)
	c.until(t, "DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_MAIN")
	if n := p.df().GodSoul; n != 3 {
		t.Fatalf("leave bonus: want 3 (2 兑换 + 1 离场奖励), got %d", n)
	}
}

// TestTerritoryAutoExchange：离场时仙气 ≥50 自动折算 30 胜利点，再按 50 点发神魂。
func TestTerritoryAutoExchange(t *testing.T) {
	resetTerr()
	c, p, _, foe := terrSetup(t, "terr10")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	terrMgr.mu.Lock()
	b := terrMgr.byUID[p.UID]
	terrMgr.mu.Unlock()
	b.mu.Lock()
	b.players[p.UID].Score = 100
	b.players[p.UID].Xianqi = 60
	b.mu.Unlock()

	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_LEAVE_GAME", nil)
	c.until(t, "DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_MAIN")
	// 100 + 30 = 130 → 折 2 个神魂（剩 30 分）→ 剩 30 ≥20 再拿 1 个离场奖励。
	if n := p.df().GodSoul; n != 3 {
		t.Fatalf("auto exchange + 离场奖励 gave %d 神魂, want 3", n)
	}
	b.mu.Lock()
	xianqi := 0
	if pl, ok := b.players[p.UID]; ok {
		xianqi = pl.Xianqi
	}
	b.mu.Unlock()
	if xianqi != 0 { // 已离场，battle 里没有这个玩家；仙气扣减在离场快照上
		t.Fatalf("unexpected player still in battle with xianqi=%d", xianqi)
	}
}

// TestTerrAccrue：每 10 秒自动折算——仙气先 50→30 胜利点（一次），胜利点满 50 折神魂。
func TestTerrAccrue(t *testing.T) {
	pl := &terrPlayer{Score: 120, Xianqi: 60}
	// 仙气不再自动折算（改由 Z 点按钮），120 胜利点 = 2 个神魂、余 20。
	if n := terrAccrue(pl); n != 2 {
		t.Fatalf("accrue souls=%d, want 2", n)
	}
	if pl.Score != 20 || pl.Souls != 2 || pl.Xianqi != 60 {
		t.Fatalf("after accrue: score=%d souls=%d xianqi=%d", pl.Score, pl.Souls, pl.Xianqi)
	}
	pl.Score = 49
	if n := terrAccrue(pl); n != 0 {
		t.Fatalf("49 score should not accrue, got %d", n)
	}
}

// TestTerritoryExchangeOnce：仙阵兑换按官方「每个玩家只能兑换一次」——第二次会被拒绝。
func TestTerritoryExchangeOnce(t *testing.T) {
	resetTerr()
	c, p, _, foe := terrSetup(t, "terr11")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	terrMgr.mu.Lock()
	b := terrMgr.byUID[p.UID]
	terrMgr.mu.Unlock()
	b.mu.Lock()
	b.players[p.UID].Xianqi = 120
	b.mu.Unlock()

	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_EXCHANGE_SCORE", nil)
	m := terrOpenMsg(t, c)
	if num(m, "xianqi") != 70 || num(m, "my_score") != 30 {
		t.Fatalf("第一次兑换: xianqi=%v score=%v, want 70/30", m["xianqi"], m["my_score"])
	}

	// 第二次：官方只允许一次，仙气还够也换不了。
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_EXCHANGE_SCORE", nil)
	all := c.until(t, "ETC_PTO.S_RET_MSG")
	if _, ok := all["DONGFU_TERRITORY_PTO.S_TERRITORY_OPEN_MAIN"]; ok {
		t.Fatal("第二次兑换不应该结算")
	}
	b.mu.Lock()
	xianqi, score := b.players[p.UID].Xianqi, b.players[p.UID].Score
	b.mu.Unlock()
	if xianqi != 70 || score != 30 {
		t.Fatalf("第二次兑换后 xianqi=%d score=%d, want 70/30", xianqi, score)
	}
}

// TestGMAddGodSoulAndScore：GM 直发——神魂写盘；胜利点要求角色在领土战场里。
func TestGMAddGodSoulAndScore(t *testing.T) {
	resetTerr()
	c, p, srv, foe := terrSetup(t, "gmterr")
	out, err := srv.gmAddGodSoul(p.UID, 10)
	if err != nil || out["god_soul"] != int32(10) {
		t.Fatalf("gmAddGodSoul: out=%v err=%v", out, err)
	}
	if _, err := srv.gmAddTerritoryScore(p.UID, 50); err == nil {
		t.Fatal("score add should fail outside battle")
	}
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	out, err = srv.gmAddTerritoryScore(p.UID, 50)
	if err != nil || out["score"] != 50 {
		t.Fatalf("gmAddTerritoryScore: out=%v err=%v", out, err)
	}
}
