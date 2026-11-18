package xymsrv

import (
	"testing"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

// dfBattleClockAt 让「现在」可控，返回一个设置函数（同一个用例里要跨时间段时用）。
func dfBattleClockAt(t *testing.T) func(time.Time) {
	t.Helper()
	old := dfBattleNow
	oldRaid := dfBattleRaidAsync
	var cur time.Time
	dfBattleNow = func() time.Time { return cur }
	dfBattleRaidAsync = false // 后台模拟会覆盖手造成绩，测试里默认关掉
	t.Cleanup(func() {
		dfBattleNow = old
		dfBattleRaidAsync = oldRaid
	})
	return func(v time.Time) { cur = v }
}

func dfBattleAt(y int, mo time.Month, d, hh, mm int) time.Time {
	return time.Date(y, mo, d, hh, mm, 0, 0, time.Local)
}

// 2026-11-17 是周二（开启日）；11-18 周三、11-19 周四、11-21 周六。
var (
	dfBattleTue = [3]int{2026, 11, 17}
	dfBattleWed = [3]int{2026, 11, 18}
)

// dfBattleWeakFoe 是 1 血 1 攻的守阵者，保证攻方必胜、且守方恰好死 1 个。
func dfBattleWeakFoe() *ldFoe {
	return &ldFoe{UID: 9001, Name: "守阵甲", Grade: 1,
		Team: []*battle.Fighter{{ID: 1, Pos: 101, Name: "守阵甲", Att: 1, HP: 1, MaxHP: 1, Speed: 1, Grade: 1}}}
}

// dfBattleUseWeakFoes 把所有人的阵容换成 1 血 1 攻（保留 uid，否则多个目标会撞成同一个）。
func dfBattleUseWeakFoes(t *testing.T) {
	t.Helper()
	old := dfBattleTeamOf
	dfBattleTeamOf = func(_ *Server, uid int32, _ *Player) *ldFoe {
		f := dfBattleWeakFoe()
		f.UID = uid
		return f
	}
	t.Cleanup(func() { dfBattleTeamOf = old })
}

// TestDfBattleSchedule：官方时段——周2/4/6 10:00 开启、12:00 布阵截止+匹配、18:30 挑战截止、19:00 结算。
func TestDfBattleSchedule(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want int
	}{
		{"周二 09:59 未开始", dfBattleAt(2026, 11, 17, 9, 59), dfBattleClosed},
		{"周二 10:00 布阵期", dfBattleAt(2026, 11, 17, 10, 0), dfBattleDeploy},
		{"周二 11:59 布阵期", dfBattleAt(2026, 11, 17, 11, 59), dfBattleDeploy},
		{"周二 12:00 挑战期", dfBattleAt(2026, 11, 17, 12, 0), dfBattleFight},
		{"周二 18:29 挑战期", dfBattleAt(2026, 11, 17, 18, 29), dfBattleFight},
		{"周二 18:30 等待结算", dfBattleAt(2026, 11, 17, 18, 30), dfBattlePending},
		{"周二 19:00 已结算", dfBattleAt(2026, 11, 17, 19, 0), dfBattleSettled},
		{"周三 15:00 不开", dfBattleAt(2026, 11, 18, 15, 0), dfBattleClosed},
		{"周四 13:00 挑战期", dfBattleAt(2026, 11, 19, 13, 0), dfBattleFight},
		{"周五 13:00 不开", dfBattleAt(2026, 11, 20, 13, 0), dfBattleClosed},
		{"周六 13:00 挑战期", dfBattleAt(2026, 11, 21, 13, 0), dfBattleFight},
	}
	for _, c := range cases {
		sc := dfBattleScheduleOf(c.at)
		if got := dfBattleStageAt(sc, c.at); got != c.want {
			t.Errorf("%s: stage=%d want %d", c.name, got, c.want)
		}
	}

	// 11:00 布阵期 → 距 12:00 还有 3600 秒
	now := dfBattleAt(2026, 11, 17, 11, 0)
	sc := dfBattleScheduleOf(now)
	if left := dfBattleLeft(sc, dfBattleDeploy, now); left != 3600 {
		t.Fatalf("布阵期 left=%d, want 3600", left)
	}
	// 周三 19:00 → 距周四 10:00 还有 15 小时
	now = dfBattleAt(2026, 11, 18, 19, 0)
	sc = dfBattleScheduleOf(now)
	if left := dfBattleLeft(sc, dfBattleClosed, now); left != 15*3600 {
		t.Fatalf("周三 19:00 距下次开启 left=%d, want %d", left, 15*3600)
	}
}

// TestDfBattleOpenAndFight：挑战期打开面板 → 自动开启（扣 50×等级 元宝）→ 打一场按护法死亡差拿分。
func TestDfBattleOpenAndFight(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 12, 30))

	dfBattleUseWeakFoes(t)

	c, p, srv, _ := terrSetup(t, "dfb1")
	df := dfOf(srv, p.UID)
	srv.dongfu.do(func() bool { df.Gold = 1000; return false })

	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	m := c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")

	// 自动开启：扣 50×1 元宝，并按 3×等级 选防守成员（这里只有 1 名成员）。
	df = dfOf(srv, p.UID)
	if df.Gold != 950 {
		t.Fatalf("开启应扣 50 元宝, gold=%d", df.Gold)
	}
	if df.Battle == nil || df.Battle.Day != "2026-11-17" {
		t.Fatalf("破阵应已开启: %+v", df.Battle)
	}
	// 12:30 属于挑战期，外面不该再推「开启前信息」。
	if num(m, "left_fight_times") != dfBattleUserTimes {
		t.Fatalf("初始剩余次数应为 %d, got %v", dfBattleUserTimes, m["left_fight_times"])
	}
	if m["vs_dongfu_name"] == "" || m["vs_dongfu_name"] == nil {
		t.Fatalf("对手洞府名不能为空")
	}

	// my_team 里有我自己，且在某个阵眼下。
	found := false
	for _, g := range m["my_team"].([]xym.Msg) {
		for _, mem := range g["list"].([]xym.Msg) {
			if num(mem, "usernum") == int(p.UID) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("my_team 里应该有自己（按战力自动选入防守）")
	}

	// team 是 3 个阵眼分组，敌方目标按 3×对手等级 生成。
	groups := m["team"].([]xym.Msg)
	if len(groups) != 3 {
		t.Fatalf("team 应为 3 个阵眼分组, got %d", len(groups))
	}
	var target int64
	for _, g := range groups {
		for _, x := range g["list"].([]xym.Msg) {
			if v := num(x, "usernum"); v != 0 {
				target = int64(v)
				break
			}
		}
		if target != 0 {
			break
		}
	}
	if target == 0 {
		t.Fatal("敌方阵中应该有可挑战的防守目标")
	}

	c.sendExt("DONGFU_PTO.C_START_DONGFU_BATTLE_FIGHT",
		xym.Msg{"type": "human", "vs_usernum": target})
	c.until(t, "DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")

	df = dfOf(srv, p.UID)
	b := df.Battle
	u := b.user(p.UID)
	if u.Times != 1 || u.Point != dfBattleWinPoint {
		t.Fatalf("打完一场 times=%d point=%d, want 1/%d", u.Times, u.Point, dfBattleWinPoint)
	}
	// 破阵积分 = 防守方阵亡 1 − 我方阵亡 0 = 1，记在被攻击的那个目标上。
	var hit *DFBattleDefender
	for _, d := range b.Versus {
		if d.UID == int32(target) {
			hit = d
		}
	}
	if hit == nil {
		t.Fatal("找不到被攻击的目标")
	}
	if hit.Score != 1 || hit.Challenged != 1 {
		t.Fatalf("目标积分/次数 = %d/%d, want 1/1", hit.Score, hit.Challenged)
	}
	if b.MyScore() != 1 {
		t.Fatalf("我方总分应为 1, got %d", b.MyScore())
	}
}

// TestDfBattleDeployStage：10:00–12:00 是布阵期，这时打不了；主界面要给（换阵容用），
// 规则栏同时要拿到 status=1 / 到 12:00 的倒计时。
func TestDfBattleDeployStage(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 10, 30))

	c, p, srv, _ := terrSetup(t, "dfb2")
	df := dfOf(srv, p.UID)
	srv.dongfu.do(func() bool { df.Gold = 1000; return false })

	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	all := c.until(t, "DONGFU_PTO.S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO")
	// 事件 → 破阵 打开的破阵主界面（PartyPoZhenView）只订阅 MAIN_INFO，布阵期也得有数据。
	if main, ok := all["DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO"]; !ok || len(main["my_team"].([]xym.Msg)) != len(dfBattleGates) {
		t.Fatalf("布阵期要给破阵主界面数据（按阵眼分组的 my_team）, got %v", main)
	}
	pre := all["DONGFU_PTO.S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO"]
	if num(pre, "status") != 1 {
		t.Fatalf("布阵期 status 应为 1（已开启）, got %v", pre["status"])
	}
	if left := num(pre, "left_time"); left != 5400 {
		t.Fatalf("10:30 距 12:00 应为 5400 秒, got %d", left)
	}

	// 布阵期开打要被拦。
	c.sendExt("DONGFU_PTO.C_START_DONGFU_BATTLE_FIGHT", xym.Msg{"type": "human"})
	c.until(t, "ETC_PTO.S_RET_MSG")
}

// TestDfBattleNoGold：洞府元宝不够 50×等级 就不开启，并给出提示。
func TestDfBattleNoGold(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 13, 0))

	c, p, srv, _ := terrSetup(t, "dfb3")
	df := dfOf(srv, p.UID)
	srv.dongfu.do(func() bool { df.Gold = 10; return false })

	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	all := c.until(t, "ETC_PTO.S_RET_MSG")
	if ret, ok := all["ETC_PTO.S_RET_MSG"]; !ok || ret["msg"] == "" {
		t.Fatal("元宝不足应给提示")
	}
	if df = dfOf(srv, p.UID); df.Battle != nil {
		t.Fatalf("元宝不足不该开启: %+v", df.Battle)
	}
	if df.Gold != 10 {
		t.Fatalf("元宝不足不该扣钱, gold=%d", df.Gold)
	}
}

// TestDfBattleOutsideDay：周三（非开启日）只回开启前信息，倒计时指向最近的开启日。
func TestDfBattleOutsideDay(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleWed[0], time.Month(dfBattleWed[1]), dfBattleWed[2], 15, 0))

	c, _, _, _ := terrSetup(t, "dfb4")

	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	all := c.until(t, "DONGFU_PTO.S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO")
	if _, ok := all["DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO"]; ok {
		t.Fatal("非开启日不该回主界面")
	}
	pre := all["DONGFU_PTO.S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO"]
	if num(pre, "status") != 0 {
		t.Fatalf("非开启日 status 应为 0, got %v", pre["status"])
	}
	// 周三 15:00 → 次日（周四）10:00 = 19 小时
	if left := num(pre, "left_time"); left != 19*3600 {
		t.Fatalf("距下次开启 left=%d, want %d", left, 19*3600)
	}

	// 洞府详情里的「破阵」栏走 C_GET_DONGFU_BATTLE_OPEN_INFO，客户端只订阅 PRE_OPEN_INFO。
	// 这条路径必须也回 status/left_time，否则界面上既没有状态也没有倒计时。
	c.sendExt("DONGFU_PTO.C_GET_DONGFU_BATTLE_OPEN_INFO", nil)
	m := c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO")
	if num(m, "status") != 0 {
		t.Fatalf("非开启日 status 应为 0, got %v", m["status"])
	}
	if left := num(m, "left_time"); left != 19*3600 {
		t.Fatalf("破阵栏倒计时 left=%d, want %d", left, 19*3600)
	}
}

// TestDfBattleFightLimits：每人 3 次、每个防守目标最多被挑战 5 次。
func TestDfBattleFightLimits(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 13, 0))

	dfBattleUseWeakFoes(t)

	c, p, srv, _ := terrSetup(t, "dfb5")
	df := dfOf(srv, p.UID)
	srv.dongfu.do(func() bool { df.Gold = 1000; return false })

	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")

	df = dfOf(srv, p.UID)
	ids := make([]int64, 0, 4)
	for _, d := range df.Battle.Versus {
		ids = append(ids, int64(d.UID))
	}
	if len(ids) < dfBattleUserTimes {
		t.Fatalf("对手目标不足 %d 个（%d），无法验证每人次数上限", dfBattleUserTimes, len(ids))
	}

	// 打 3 个不同目标 → 次数用满。
	for i := 0; i < dfBattleUserTimes; i++ {
		c.sendExt("DONGFU_PTO.C_START_DONGFU_BATTLE_FIGHT", xym.Msg{"type": "human", "vs_usernum": ids[i]})
		c.until(t, "DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")
	}
	if u := dfOf(srv, p.UID).Battle.user(p.UID); u.Times != dfBattleUserTimes {
		t.Fatalf("次数应为 %d, got %d", dfBattleUserTimes, u.Times)
	}

	// 第 4 次必须被拒（换一个还能打的目标，证明是「个人次数」拦的，不是目标次数）。
	before := dfOf(srv, p.UID).Battle.Versus[len(ids)-1].Challenged
	c.sendExt("DONGFU_PTO.C_START_DONGFU_BATTLE_FIGHT", xym.Msg{"type": "human", "vs_usernum": ids[len(ids)-1]})
	all := c.until(t, "ETC_PTO.S_RET_MSG")
	if ret, ok := all["ETC_PTO.S_RET_MSG"]; !ok || ret["msg"] == "" {
		t.Fatal("次数用完应给提示")
	}
	if after := dfOf(srv, p.UID).Battle.Versus[len(ids)-1].Challenged; after != before {
		t.Fatalf("次数用完不该再扣目标次数, %d -> %d", before, after)
	}

	// 把个人次数和该目标的已挑战次数都清零，单独验证「同一目标 5 次」上限。
	// 注意 dfOf 内部会 srv.dongfu.do(...)，不能在 do 里调它（同一把锁重入会死锁）。
	dfp := dfOf(srv, p.UID)
	dfp.Battle.user(p.UID).Times = 0
	for _, d := range dfp.Battle.Versus {
		if d.UID == int32(ids[0]) {
			d.Challenged, d.Score = 0, 0
		}
	}
	for i := 0; i < dfBattleDefChallenges; i++ {
		dfp.Battle.user(p.UID).Times = 0
		c.sendExt("DONGFU_PTO.C_START_DONGFU_BATTLE_FIGHT", xym.Msg{"type": "human", "vs_usernum": ids[0]})
		c.until(t, "DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")
	}
	dfp.Battle.user(p.UID).Times = 0
	var tgt *DFBattleDefender
	for _, d := range dfOf(srv, p.UID).Battle.Versus {
		if d.UID == int32(ids[0]) {
			tgt = d
		}
	}
	if tgt == nil || tgt.Challenged != dfBattleDefChallenges {
		t.Fatalf("同一目标应被打满 %d 次, got %+v", dfBattleDefChallenges, tgt)
	}
	// 第 6 次被拒。
	c.sendExt("DONGFU_PTO.C_START_DONGFU_BATTLE_FIGHT", xym.Msg{"type": "human", "vs_usernum": ids[0]})
	all = c.until(t, "ETC_PTO.S_RET_MSG")
	if ret, ok := all["ETC_PTO.S_RET_MSG"]; !ok || ret["msg"] == "" {
		t.Fatal("目标被挑战满 5 次后应给提示")
	}
	for _, d := range dfOf(srv, p.UID).Battle.Versus {
		if d.UID == int32(ids[0]) && d.Challenged != dfBattleDefChallenges {
			t.Fatalf("超过 5 次不该再计数, got %d", d.Challenged)
		}
	}
}

// TestDfBattleSettle：19:00 比总分定胜负，且同一目标取最高积分。
func TestDfBattleSettle(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 13, 0))

	dfBattleUseWeakFoes(t)

	c, p, srv, _ := terrSetup(t, "dfb6")
	df := dfOf(srv, p.UID)
	srv.dongfu.do(func() bool { df.Gold = 1000; return false })

	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")
	c.collect() // 主界面后面还跟一条规则栏 PRE_OPEN，丢掉免得干扰下面的断言

	// 人造成绩：我打 10 分，对方打 3 分（dfOf 内部会取洞府锁，这里直接改指针）。
	dfp := dfOf(srv, p.UID)
	b := dfp.Battle
	b.Versus[0].Score = 7
	b.Versus[1].Score = 3
	for _, d := range b.Mine {
		d.Score = 3
		break
	}
	b.VsRaided = true // 别让 raidLocked 覆盖人造成绩

	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 19, 30))
	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	m := c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")

	df = dfOf(srv, p.UID)
	if !df.Battle.Settled || df.Battle.Result != 1 {
		t.Fatalf("10:3 应判我方胜, settled=%v result=%d", df.Battle.Settled, df.Battle.Result)
	}
	if num(m, "total_beat_score") != 10 || num(m, "vs_total_beat_score") != 3 {
		t.Fatalf("总分显示 %v:%v, want 10:3", m["total_beat_score"], m["vs_total_beat_score"])
	}
}

// TestDfBattleSettleEmpty：双方都没打（0:0）不算平局，不给结果。
func TestDfBattleSettleEmpty(t *testing.T) {
	setNow := dfBattleClockAt(t)
	// 挑战期内开局（18:30 之后不再补开局，所以先在这里建局）。
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 13, 0))

	c, p, srv, _ := terrSetup(t, "dfb11")
	df := dfOf(srv, p.UID)
	srv.dongfu.do(func() bool { df.Gold = 1000; return false })

	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")
	c.collect()

	// 上面那次打开时对方已经模拟进攻过（raidLocked）。这里把两边都清零再结算，验证 0:0 不出结果。
	b := dfOf(srv, p.UID).Battle
	for _, d := range b.Mine {
		d.Score = 0
	}
	for _, d := range b.Versus {
		d.Score = 0
	}
	b.Settled, b.Result = false, 0
	b.VsRaided = true

	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 19, 30))
	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")
	if b = dfOf(srv, p.UID).Battle; !b.Settled || b.Result != 0 {
		t.Fatalf("0:0 不该判平局, settled=%v result=%d", b.Settled, b.Result)
	}
}

// TestDfBattleRaidAsync：对方进攻我方是后台任务，几秒内算完并写回分数（不能卡住请求）。
func TestDfBattleRaidAsync(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 13, 0))

	old := dfBattleRaidAsync
	dfBattleRaidAsync = true
	t.Cleanup(func() { dfBattleRaidAsync = old })
	dfBattleUseWeakFoes(t)

	c, p, srv, _ := terrSetup(t, "dfb12")
	df := dfOf(srv, p.UID)
	srv.dongfu.do(func() bool { df.Gold = 1000; return false })

	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")

	// 打开面板必须立刻回包（不能等后台算完），后台几秒内写回。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b := dfOf(srv, p.UID).Battle; b != nil && b.VsRaided {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	b := dfOf(srv, p.UID).Battle
	if !b.VsRaided || b.VsRaiding {
		t.Fatal("后台模拟应在几秒内完成")
	}
	if len(b.Mine) == 0 {
		t.Fatal("我方应有防守目标")
	}
	if b.VsScore() <= 0 {
		t.Fatalf("对方进攻应产生破阵积分, got %d", b.VsScore())
	}
}

// TestDfBattleChangeTeam：只有布阵期、且只有入阵成员能换阵容。
func TestDfBattleChangeTeam(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 10, 30))

	c, p, srv, _ := terrSetup(t, "dfb7")
	df := dfOf(srv, p.UID)
	srv.dongfu.do(func() bool { df.Gold = 1000; return false })

	// 布阵期打开会先自动开启（拉 PRE_OPEN）。
	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	c.until(t, "DONGFU_PTO.S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO")

	// 布阵期换阵容：成功。
	c.sendExt("DONGFU_PTO.C_BATTLE_CHANGE_TEAM_FIGHT_DATA", nil)
	all := c.until(t, "ETC_PTO.S_RET_MSG")
	if _, ok := all["DONGFU_PTO.S_SEND_DONGFU_BATTLE_TEAM_FIGHT_DATA"]; !ok {
		t.Fatal("换阵容应回 TEAM_FIGHT_DATA")
	}

	// 挑战期换阵容：拒绝。
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 13, 0))
	c.sendExt("DONGFU_PTO.C_BATTLE_CHANGE_TEAM_FIGHT_DATA", nil)
	all = c.until(t, "ETC_PTO.S_RET_MSG")
	if ret, ok := all["ETC_PTO.S_RET_MSG"]; !ok || ret["msg"] == "" {
		t.Fatal("挑战期换阵容应给提示")
	}
}

// TestDfBattleShop：破阵点不足不能买；够点且未超限购才能买，且限购生效。
func TestDfBattleShop(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 13, 0))

	c, p, srv, _ := terrSetup(t, "dfb8")
	df := dfOf(srv, p.UID)
	srv.dongfu.do(func() bool { df.Gold = 1000; return false })

	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")

	// 没有破阵点：买不了
	c.sendExt("DONGFU_PTO.C_BUY_BATTLE_POINT_SHOP_GOODS", xym.Msg{"goods_id": int64(1)})
	c.until(t, "ETC_PTO.S_RET_MSG")
	if dfOf(srv, p.UID).Battle.user(p.UID).Point != 0 {
		t.Fatal("点数不足时不该扣点")
	}

	// 给够点数再买
	dfOf(srv, p.UID).Battle.user(p.UID).Point = 100
	c.sendExt("DONGFU_PTO.C_BUY_BATTLE_POINT_SHOP_GOODS", xym.Msg{"goods_id": int64(1)})
	c.until(t, "DONGFU_PTO.S_SEND_DONGFU_BATTLE_POINT_SHOP_LIST")

	u := dfOf(srv, p.UID).Battle.user(p.UID)
	if u.Point != 80 {
		t.Fatalf("买 1 号商品应扣 20 点, got %d", u.Point)
	}
	if u.Shop[1] != 1 {
		t.Fatalf("限购计数应为 1, got %d", u.Shop[1])
	}
}

// TestDfBattleRankAndOpenInfo：排行与胜平负汇总都能回（避免客户端卡在空回包）。
func TestDfBattleRankAndOpenInfo(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 13, 0))

	c, p, srv, _ := terrSetup(t, "dfb9")
	df := dfOf(srv, p.UID)
	srv.dongfu.do(func() bool { df.Gold = 1000; return false })

	c.sendExt("DONGFU_PTO.C_GET_DONGFU_BATTLE_RANK", nil)
	rank := c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_RANK")
	if l := rank["list"].([]xym.Msg); len(l) != 2 {
		t.Fatalf("排行应有两方, got %d", len(l))
	}

	c.sendExt("DONGFU_PTO.C_GET_DONGFU_BATTLE_OPEN_INFO", nil)
	// 这条入口同时会先回一条 PRE_OPEN_INFO（破阵栏的状态与倒计时）。
	c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO")
	info := c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_OPEN_INFO")
	if _, ok := info["total_win_times"]; !ok {
		t.Fatalf("应回胜平负字段: %v", info)
	}
	if len(info["team"].([]xym.Msg)) != 3 {
		t.Fatalf("阵眼归属应有 3 组: %v", info["team"])
	}
}

// TestDfBattleAutoOff：关掉自动报名的成员不会被系统选入防守。
func TestDfBattleAutoOff(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 13, 0))

	c, p, srv, _ := terrSetup(t, "dfb10")
	df := dfOf(srv, p.UID)
	srv.dongfu.do(func() bool { df.Gold = 1000; return false })

	// 先关自动报名，再打开破阵。
	c.sendExt("DONGFU_PTO.C_BATTLE_SWITCH_AUTO_OPEN", nil)
	c.until(t, "ETC_PTO.S_RET_MSG")
	c.sendExt("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", nil)
	m := c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO")

	for _, g := range m["my_team"].([]xym.Msg) {
		for _, mem := range g["list"].([]xym.Msg) {
			if num(mem, "usernum") == int(p.UID) {
				t.Fatal("关掉自动报名后不该被选入防守")
			}
		}
	}
	if n := len(dfOf(srv, p.UID).Battle.Mine); n != 0 {
		t.Fatalf("防守目标应为 0, got %d", n)
	}
}

// TestDfBattleCreateUnderLockWithRealMember：复现 2026-11-17 线上「客户端全卡」事故。
// 多成员洞府在挑战期第一次打开破阵：dfBattleCreate → dfBattleDefenders →
// dfBattleTeamOf（洞府锁内）→ realFoe → dongfuOf → Dongfus.do 再次加同一把锁，
// 互斥锁不可重入 → 洞府锁被永久占住，登录/所有洞府请求排队，全服进不了游戏。
// 修复后走 realFoeLocked（锁内直接读 d.of）。修复前本用例 5 秒看门狗超时。
func TestDfBattleCreateUnderLockWithRealMember(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 12, 30))

	srv, _, _, pa, _ := dfWorld(t) // A、B 同府，B 是「另一个真实成员」
	giveDongfu(t, srv, 0, "对手洞府")

	type result struct {
		b     *DFBattle
		stage int
	}
	res := make(chan result, 1)
	go func() {
		srv.dongfu.do(func() bool {
			df, _ := srv.dongfu.of(pa.UID)
			b, stage, _ := srv.dfBattleGet(df, pa, dfBattleNow())
			res <- result{b, stage}
			return true
		})
	}()
	select {
	case r := <-res:
		if r.b == nil {
			t.Fatal("挑战期开局应返回一场破阵")
		}
		if len(r.b.Mine) < 1 {
			t.Fatalf("多成员洞府应有防守目标, got %d", len(r.b.Mine))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("洞府锁内开局死锁：dfBattleTeamOf 重入 Dongfus.do（2026-11-17 线上事故回归）")
	}
}

// TestDfBattleEventRow：事件列表必须带 dongfu_battle 数组——这是客户端「事件」里
// 破阵入口的唯一来源（EmergencyEventListView.refreshDongFuPoZhen，点它才发
// C_GET_DONGFU_BATTLE_MAIN_INFO 打开破阵主界面）。不填就只剩静态规则页，
// 玩家永远不知道开没开（2026-11-17 用户反馈）。
func TestDfBattleEventRow(t *testing.T) {
	setNow := dfBattleClockAt(t)

	// 周三不开：不该有条目。
	setNow(dfBattleAt(dfBattleWed[0], time.Month(dfBattleWed[1]), dfBattleWed[2], 13, 0))
	_, _, b, _, _ := dfWorld(t)
	b.sendExt("TREE_PTO.C_EVENT_LIST", nil)
	if l, ok := b.expectExt("TREE_PTO.S_EVENT_LIST")["dongfu_battle"].([]xym.Msg); ok && len(l) > 0 {
		t.Fatalf("不开的日子不该有破阵条目: %v", l)
	}

	// 周二挑战期：一条，left_time = 距 18:30，left_fight_times = 剩余次数。
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 13, 0))
	b.sendExt("TREE_PTO.C_EVENT_LIST", nil)
	l, ok := b.expectExt("TREE_PTO.S_EVENT_LIST")["dongfu_battle"].([]xym.Msg)
	if !ok || len(l) != 1 {
		t.Fatalf("挑战期事件列表应有破阵条目: %v", l)
	}
	if lt := num(l[0], "left_time"); lt != 5*3600+30*60 {
		t.Fatalf("left_time 应为距 18:30 的 5.5 小时, got %d", lt)
	}
	if n := num(l[0], "left_fight_times"); n != dfBattleUserTimes {
		t.Fatalf("剩余挑战次数应为 %d, got %d", dfBattleUserTimes, n)
	}
}

// TestDfBattleNoOpenAfterEnd：18:30 之后（等待结算/已结算）第一次访问不再补开局、
// 不扣洞府元宝——之前 19:34 会白扣一次并为一场打不了的战斗建局。
func TestDfBattleNoOpenAfterEnd(t *testing.T) {
	setNow := dfBattleClockAt(t)
	setNow(dfBattleAt(dfBattleTue[0], time.Month(dfBattleTue[1]), dfBattleTue[2], 20, 0))

	c, p, srv, _ := terrSetup(t, "dfb13")
	srv.dongfu.do(func() bool { dfOf2(srv, p.UID).Gold = 1000; return false })

	c.sendExt("DONGFU_PTO.C_GET_DONGFU_BATTLE_OPEN_INFO", nil)
	c.expectExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO")
	c.until(t, "ETC_PTO.S_RET_MSG") // 规则栏没有状态控件，必须回一句浮字说明开没开

	df := dfOf(srv, p.UID)
	if df.Gold != 1000 || df.Battle != nil {
		t.Fatalf("20:00 后不该补开破阵: gold=%d battle=%+v", df.Gold, df.Battle)
	}
}
