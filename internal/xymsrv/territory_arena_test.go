package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// ---- 测试脚手架：两个洞府 / 两个真实玩家 ----

// giveDongfu 直接往 Dongfus 里塞一个洞府（不走入府协议），uid=0 表示没有成员的「对手洞府」。
func giveDongfu(t *testing.T, srv *Server, uid int32, name string) int32 {
	t.Helper()
	var id int32
	srv.dongfu.do(func() bool {
		if srv.dongfu.List == nil {
			srv.dongfu.List = map[int32]*Dongfu{}
		}
		srv.dongfu.NextID++
		id = srv.dongfu.NextID
		df := &Dongfu{ID: id, Name: name, Icon: 1, Lv: 1}
		if uid != 0 {
			df.Members = []*DFMember{{UID: uid, Position: "owner"}}
		}
		srv.dongfu.List[id] = df
		return false // 测试里不需要落盘（do 返回 true 会触发 saveGlobal）
	})
	return id
}

// terrSetup 是单玩家用例的入口：建好自己的洞府 + 一个空对手洞府，返回可用的报名消息。
func terrSetup(t *testing.T, urs string) (*client, *Player, *Server, int64) {
	t.Helper()
	c, p, srv := actWorld(t, urs)
	giveDongfu(t, srv, p.UID, "洞府"+urs)
	foe := giveDongfu(t, srv, 0, "对手洞府")
	return c, p, srv, int64(foe)
}

// worldN 起一个服务器、登 N 个角色（各 100 级护法）。
func worldN(t *testing.T, names ...string) (*Server, []*client, []*Player) {
	t.Helper()
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	cs := make([]*client, len(names))
	ps := make([]*Player, len(names))
	for i, urs := range names {
		enterWorld(t, addr, urs).conn.Close()
		acc, _ := srv.accounts.Login(urs, "pw", "")
		srv.roles.Update(acc.UID, func(ro *Role) bool { ro.Grade, ro.TPoint, ro.HPoint = 50, 60, 20; return true })
		p, _ := srv.players.Get(acc.UID)
		p.mu.Lock()
		for _, pet := range p.Pets {
			pet.Grade = 100
		}
		p.mu.Unlock()
		c := dial(t, addr)
		c.sendRaw(xym.CLogin, loginBody(urs, "pw"))
		c.expectExt("LOGIN_PTO.S_SEND_SID")
		c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
		c.collect()
		cs[i], ps[i] = c, p
	}
	return srv, cs, ps
}

// world2 是两个角色版本的便捷封装。
func world2(t *testing.T, ursA, ursB string) (*client, *Player, *client, *Player, *Server) {
	t.Helper()
	srv, cs, ps := worldN(t, ursA, ursB)
	return cs[0], ps[0], cs[1], ps[1], srv
}

// setPetGrade 把玩家所有护法的等级调成 grade（制造强弱差，让战斗结果确定）。
func setPetGrade(t *testing.T, srv *Server, uid int32, grade int) {
	t.Helper()
	p, _ := srv.players.Get(uid)
	if p == nil {
		t.Fatal("player not found")
	}
	p.mu.Lock()
	for _, pet := range p.Pets {
		pet.Grade = grade
	}
	p.mu.Unlock()
}

// terrArenaBattle 取玩家所在的战场。
func terrArenaBattle(t *testing.T, uid int32) *terrBattle {
	t.Helper()
	terrMgr.mu.Lock()
	b := terrMgr.byUID[uid]
	terrMgr.mu.Unlock()
	if b == nil {
		t.Fatal("battle lost")
	}
	return b
}

// ---- 擂台 ----

// TestTerrArenaStage：关卡名 → 擂台档次（人界 1 / 地界 2 / 天界 3）。
func TestTerrArenaStage(t *testing.T) {
	cases := map[string]int{
		"人界擂A": 1, "人界擂B": 1,
		"地界擂A": 2, "地界擂B": 2,
		"天界擂A": 3, "天界擂B": 3,
	}
	for name, want := range cases {
		if got := terrArenaStage(name); got != want {
			t.Fatalf("%s: tier=%d, want %d", name, got, want)
		}
	}
}

// TestTerritoryPvPMatch：报名以洞府为单位——A 打 B 的洞府建立一场，B 打开界面自动进同一场、站守方出生点。
func TestTerritoryPvPMatch(t *testing.T) {
	resetTerr()
	c1, p1, c2, p2, srv := world2(t, "pvpA", "pvpB")
	giveDongfu(t, srv, p1.UID, "洞府甲")
	df2 := giveDongfu(t, srv, p2.UID, "洞府乙")

	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": int64(df2), "gold": int64(0)})
	m1 := terrOpenMsg(t, c1)
	if num(m1, "side") != 0 || m1["gate_key"] != "X" {
		t.Fatalf("攻方: side=%v gate=%v, want 0/X", m1["side"], m1["gate_key"])
	}
	if m1["dongfu_name"] != "洞府甲" || m1["vs_dongfu_name"] != "洞府乙" {
		t.Fatalf("洞府名: %v vs %v", m1["dongfu_name"], m1["vs_dongfu_name"])
	}

	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_OPEN_MAIN", nil)
	m2 := terrOpenMsg(t, c2)
	if num(m2, "side") != 1 || m2["gate_key"] != "Y" {
		t.Fatalf("守方: side=%v gate=%v, want 1/Y", m2["side"], m2["gate_key"])
	}
	if m2["dongfu_name"] != "洞府乙" || m2["vs_dongfu_name"] != "洞府甲" {
		t.Fatalf("守方洞府名: %v vs %v", m2["dongfu_name"], m2["vs_dongfu_name"])
	}
	if num(m2, "side_user_num") != 1 || num(m2, "vs_side_user_num") != 1 {
		t.Fatalf("人数: %v / %v, want 1/1", m2["side_user_num"], m2["vs_side_user_num"])
	}
	if terrArenaBattle(t, p1.UID) != terrArenaBattle(t, p2.UID) {
		t.Fatal("两个洞府应该在同一场战场")
	}

	// gate_list 的人数是按收件人阵营算的：双方各自看到自己那侧是 user_num、对面是 vs_user_num。
	// A 那条 OPEN_MAIN 是 B 进场之前发的，先刷新一次再比对。
	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_PING_GAME", nil)
	m1 = terrOpenMsg(t, c1)
	gateNums := func(m xym.Msg, key string) (int, int) {
		for _, g := range m["gate_list"].([]xym.Msg) {
			if g["key"] == key {
				return num(g, "user_num"), num(g, "vs_user_num")
			}
		}
		t.Fatalf("gate %s 不在 gate_list 里", key)
		return 0, 0
	}
	if u, v := gateNums(m1, "X"); u != 1 || v != 0 {
		t.Fatalf("攻方看 X: user=%d vs=%d, want 1/0", u, v)
	}
	if u, v := gateNums(m1, "Y"); u != 0 || v != 1 {
		t.Fatalf("攻方看 Y: user=%d vs=%d, want 0/1", u, v)
	}
	if u, v := gateNums(m2, "Y"); u != 1 || v != 0 {
		t.Fatalf("守方看 Y: user=%d vs=%d, want 1/0", u, v)
	}
	if u, v := gateNums(m2, "X"); u != 0 || v != 1 {
		t.Fatalf("守方看 X: user=%d vs=%d, want 0/1", u, v)
	}
}

// TestTerritoryPairNoCrossPairing：三个洞府时不能出现「A 打 B、B 打 C」各打各的。
// 先开的那一方定下对战组合，后开的一方如果自己洞府已经在打，必须进同一场。
func TestTerritoryPairNoCrossPairing(t *testing.T) {
	resetTerr()
	c1, p1, c2, p2, srv := world2(t, "pairA", "pairB")
	// 故意让「花果山」的 id 最大：修复前它会去打 id 最小的「迷你西游」，
	// 而「迷你西游」自己开界面时会去打第二小的「清风与酒」，于是两边各打各的。
	_ = giveDongfu(t, srv, p2.UID, "迷你西游")
	_ = giveDongfu(t, srv, 0, "清风与酒")
	df3 := giveDongfu(t, srv, p1.UID, "花果山")

	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_OPEN_MAIN", nil)
	m1 := terrOpenMsg(t, c1)
	if m1["vs_dongfu_name"] != "迷你西游" {
		t.Fatalf("花果山应打 id 最小的迷你西游, got %v", m1["vs_dongfu_name"])
	}

	// 迷你西游自己打开界面：必须进花果山那场，而不是另开一场打清风与酒。
	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_OPEN_MAIN", nil)
	m2 := terrOpenMsg(t, c2)
	if m2["vs_dongfu_name"] != "花果山" {
		t.Fatalf("迷你西游应该对上花果山, got %v", m2["vs_dongfu_name"])
	}
	if num(m2, "side") != 1 {
		t.Fatalf("迷你西游应是守方 side=1, got %v", m2["side"])
	}
	if terrArenaBattle(t, p1.UID) != terrArenaBattle(t, p2.UID) {
		t.Fatal("两个洞府必须在同一场战场（不能各打各的）")
	}
	_ = df3
}

// TestTerritoryFoeBusy：指定的对手正在打别人时，不能把第三个洞府塞进同一场。
func TestTerritoryFoeBusy(t *testing.T) {
	resetTerr()
	srv, cs, ps := worldN(t, "busyA", "busyB", "busyC")
	dfA := giveDongfu(t, srv, ps[0].UID, "甲府")
	dfB := giveDongfu(t, srv, ps[1].UID, "乙府")
	dfC := giveDongfu(t, srv, ps[2].UID, "丙府")

	// 甲 打 乙，建立一场
	cs[0].sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": int64(dfB), "gold": int64(0)})
	terrOpenMsg(t, cs[0])

	// 丙 指定打 甲：甲已在战斗中，应被拒绝并改打空闲的洞府（或提示无空闲）。
	cs[2].sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": int64(dfA), "gold": int64(0)})
	all := cs[2].until(t, "ETC_PTO.S_RET_MSG")
	if chat, ok := all["DONGFU_TERRITORY_PTO.S_TERRITORY_BROADCAST_MSG"]; ok {
		if msg, _ := chat["msg"].(string); msg == "" {
			t.Fatalf("拒绝提示内容为空: %v", chat)
		}
	} else {
		t.Fatal("指定的对手正在战斗时必须给提示")
	}
	terrMgr.mu.Lock()
	inA := terrMgr.byUID[ps[2].UID]
	terrMgr.mu.Unlock()
	if inA != nil && (inA.dfA == dfA && inA.dfB == dfB || inA.dfA == dfB && inA.dfB == dfA) {
		t.Fatal("丙不应该被塞进甲乙那一场")
	}
	_ = dfC
}

// TestTerritoryArenaPvP：A 先站上人界擂成为擂主，B 走进该擂台自动开打；B 更强则抢下擂台。
func TestTerritoryArenaPvP(t *testing.T) {
	resetTerr()
	c1, p1, c2, p2, srv := world2(t, "pvpC", "pvpD")
	giveDongfu(t, srv, p1.UID, "洞府甲")
	df2 := giveDongfu(t, srv, p2.UID, "洞府乙")
	setPetGrade(t, srv, p1.UID, 1) // A 弱（守擂快照很弱），B 保持 100 级必赢

	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": int64(df2), "gold": int64(0)})
	terrOpenMsg(t, c1)
	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_OPEN_MAIN", nil)
	terrOpenMsg(t, c2)

	// A：X → A（人界擂A），没人守 → A 成为擂主并得仙气。
	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "A"})
	m1 := terrOpenMsg(t, c1)
	if num(m1, "xianqi") != terrOwnerReward[1] {
		t.Fatalf("成为擂主后仙气=%v, want %d", m1["xianqi"], terrOwnerReward[1])
	}
	b := terrArenaBattle(t, p1.UID)
	b.mu.Lock()
	ownerUID := int32(0)
	if o := b.owners["A"]; o != nil {
		ownerUID = o.UID
	}
	b.mu.Unlock()
	if ownerUID != p1.UID {
		t.Fatalf("A 应该是人界擂A 的擂主，got uid=%d", ownerUID)
	}

	// B：Y → D（地界擂B）→ N（防守祭坛）→ A。
	// 注意 Y 的出口只有 D、F 两个擂台，所以 B 路上会在 D 当一次擂主（也拿一份擂主仙气），
	// 因此进 A 之前的仙气当基线，只断言「抢下 A 这一步」的增量。
	for _, g := range []string{"D", "N"} {
		c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": g})
		terrOpenMsg(t, c2)
	}
	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_PING_GAME", nil)
	before := num(terrOpenMsg(t, c2), "xianqi")

	// 踏进 A：A 是敌方擂主，自动开打。
	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "A"})
	m2 := terrOpenMsg(t, c2)

	b.mu.Lock()
	newOwner := int32(0)
	if o := b.owners["A"]; o != nil {
		newOwner = o.UID
	}
	gateOwner := b.gates["A"].Owner
	b.mu.Unlock()
	if newOwner != p2.UID {
		t.Fatalf("B 打赢后应抢下擂台，got owner=%d", newOwner)
	}
	if gateOwner != 1 {
		t.Fatalf("人界擂A 归属=%d, want 1（守方占点）", gateOwner)
	}

	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_PING_GAME", nil)
	m2 = terrOpenMsg(t, c2)
	if num(m2, "my_score") != terrArenaReward[1][0] {
		t.Fatalf("B 抢擂胜利点=%v, want %d", m2["my_score"], terrArenaReward[1][0])
	}
	if gain := num(m2, "xianqi") - before; gain != terrArenaReward[1][1]+terrOwnerReward[1] {
		t.Fatalf("B 抢擂仙气增量=%d, want %d", gain, terrArenaReward[1][1]+terrOwnerReward[1])
	}
	if num(m2, "win_times") != 1 {
		t.Fatalf("win_times=%v, want 1", m2["win_times"])
	}
}

// TestTerritoryArenaRecordSnapshot：战报里的双方阵容必须是「开打之前」的状态。
// 之前把 simulate 之后的 team2 直接发出去，客户端渲染出来是全部阵亡 → 卡片空白/错位。
func TestTerritoryArenaRecordSnapshot(t *testing.T) {
	resetTerr()
	c1, p1, c2, p2, srv := world2(t, "recA", "recB")
	giveDongfu(t, srv, p1.UID, "洞府甲")
	df2 := giveDongfu(t, srv, p2.UID, "洞府乙")
	setPetGrade(t, srv, p1.UID, 1) // A 弱，让 B 必胜，守方阵容会被打空

	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": int64(df2), "gold": int64(0)})
	terrOpenMsg(t, c1)
	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_OPEN_MAIN", nil)
	terrOpenMsg(t, c2)

	// A 先占住人界擂A；B 走进去自动开打。
	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "A"})
	terrOpenMsg(t, c1)
	for _, g := range []string{"D", "N"} {
		c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": g})
		terrOpenMsg(t, c2)
	}
	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "A"})
	all := c2.until(t, "FIGHT_PTO.S_FIGHT_RECORD")
	rec, ok := all["FIGHT_PTO.S_FIGHT_RECORD"]
	if !ok {
		t.Fatal("擂台战应回战报")
	}
	for _, side := range []string{"team1", "team2"} {
		team, _ := rec[side].([]xym.Msg)
		if len(team) == 0 {
			t.Fatalf("%s 为空，客户端只会画出空卡片", side)
		}
		alive := 0
		for _, f := range team {
			if num(f, "hp") > 0 {
				alive++
			}
		}
		if alive == 0 {
			t.Fatalf("%s 全员 0 血：发了模拟之后的阵容，应为打之前的快照", side)
		}
		if num(team[0], "icon") == 0 && num(team[0], "id") == 0 {
			t.Fatalf("%s 首卡没有 id/icon，客户端无法取图: %v", side, team[0])
		}
	}
	// 双方威望都要有值（w1/w2 的整数类型不固定，这里按 int/int32/int64 都认）。
	intOf := func(k string) int {
		switch v := rec[k].(type) {
		case int:
			return v
		case int32:
			return int(v)
		case int64:
			return int(v)
		}
		return 0
	}
	if w := intOf("w1"); w <= 0 {
		t.Fatalf("攻方威望应为正数, got %v", rec["w1"])
	}
	if w := intOf("w2"); w <= 0 {
		t.Fatalf("守方威望应为正数（之前恒为 0，界面显示「威望 0」）, got %v", rec["w2"])
	}

	// 守方站位必须 100+（客户端按站位分左右两排；用 1-9 会把两边画到同一侧 = 卡片错位）。
	for _, f := range rec["team2"].([]xym.Msg) {
		if num(f, "pos") < 100 {
			t.Fatalf("守方站位应 100+，got %v", f["pos"])
		}
	}
	for _, f := range rec["team1"].([]xym.Msg) {
		if num(f, "pos") >= 100 {
			t.Fatalf("攻方站位应 1-9，got %v", f["pos"])
		}
	}
}

// TestTerritoryArenaListFields：挑战列表要带头像 id、等级、战力（原来全是 0，头像框是空的）。
func TestTerritoryArenaListFields(t *testing.T) {
	resetTerr()
	c1, p1, c2, p2, srv := world2(t, "lstA", "lstB")
	giveDongfu(t, srv, p1.UID, "洞府甲")
	df2 := giveDongfu(t, srv, p2.UID, "洞府乙")
	setPetGrade(t, srv, p2.UID, 1) // B 弱：走进擂台会打输，A 保持擂主

	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": int64(df2), "gold": int64(0)})
	terrOpenMsg(t, c1)
	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_OPEN_MAIN", nil)
	terrOpenMsg(t, c2)

	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "A"})
	terrOpenMsg(t, c1)
	for _, g := range []string{"D", "N", "A"} {
		c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": g})
		terrOpenMsg(t, c2)
	}

	// B 现在站在 A 点、A 是擂主 → 列表里应有 A 的等级与战力。
	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_CHALLENGE_USER_LIST", xym.Msg{"type": int64(0)})
	all := c2.until(t, "DONGFU_TERRITORY_PTO.S_TERRITORY_CHALLENGE_USER_LIST")
	l := all["DONGFU_TERRITORY_PTO.S_TERRITORY_CHALLENGE_USER_LIST"]["list"].([]xym.Msg)
	if len(l) != 1 {
		t.Fatalf("应在列表里看到 1 名敌方擂主, got %d", len(l))
	}
	if num(l[0], "grade") != 50 {
		t.Fatalf("等级应为 50（角色等级），got %v", l[0]["grade"])
	}
	if num(l[0], "fight_point") <= 0 {
		t.Fatalf("战力应大于 0（原来恒为 0，界面显示「战力: 0」）: %v", l[0])
	}
	if num(l[0], "is_owner") != 1 {
		t.Fatalf("当前擂主要标 is_owner=1: %v", l[0])
	}
}

// TestTerritoryArenaEject：同一据点上的敌方玩家可以被驱逐回出生点。
func TestTerritoryArenaEject(t *testing.T) {
	resetTerr()
	c1, p1, c2, p2, srv := world2(t, "pvpE", "pvpF")
	giveDongfu(t, srv, p1.UID, "洞府甲")
	df2 := giveDongfu(t, srv, p2.UID, "洞府乙")
	setPetGrade(t, srv, p1.UID, 1)

	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": int64(df2), "gold": int64(0)})
	terrOpenMsg(t, c1)
	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_OPEN_MAIN", nil)
	terrOpenMsg(t, c2)

	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "A"})
	terrOpenMsg(t, c1)
	for _, g := range []string{"D", "N", "A"} {
		c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": g})
		terrOpenMsg(t, c2)
	}

	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_EJECT_USER_LIST", nil)
	all := c2.until(t, "DONGFU_TERRITORY_PTO.S_TERRITORY_EJECT_USER_LIST")
	l := all["DONGFU_TERRITORY_PTO.S_TERRITORY_EJECT_USER_LIST"]["list"].([]xym.Msg)
	if len(l) != 1 || num(l[0], "vs_usernum") != int(p1.UID) {
		t.Fatalf("驱逐列表: %v", l)
	}

	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_EJECT_USER", xym.Msg{"vs_usernum": int64(p1.UID)})
	all2 := c2.until(t, "ETC_PTO.S_RET_MSG")
	// 驱逐必须先打一场（官方：有敌方可战斗护法时先与之战斗，胜利后方可）。
	rec, ok := all2["FIGHT_PTO.S_FIGHT_RECORD"]
	if !ok {
		t.Fatal("驱逐应先生成战报，不能直接把人挪走")
	}
	for _, f := range rec["team2"].([]xym.Msg) {
		if num(f, "pos") < 100 {
			t.Fatalf("驱逐战报里守方站位应 100+, got %v", f["pos"])
		}
	}
	b := terrArenaBattle(t, p1.UID)
	b.mu.Lock()
	gate := ""
	if pl := b.players[p1.UID]; pl != nil {
		gate = pl.Gate
	}
	b.mu.Unlock()
	if gate != "X" {
		t.Fatalf("被驱逐后应在出生点 X，got %q", gate)
	}
}

// TestTerritoryEjectLose：驱逐打输就不能把对方赶走（对方仍留在原据点、仍是擂主）。
func TestTerritoryEjectLose(t *testing.T) {
	resetTerr()
	c1, p1, c2, p2, srv := world2(t, "ejl1", "ejl2")
	giveDongfu(t, srv, p1.UID, "洞府甲")
	df2 := giveDongfu(t, srv, p2.UID, "洞府乙")
	setPetGrade(t, srv, p2.UID, 1) // B 弱：既抢不下擂台，也驱逐不动 A

	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": int64(df2), "gold": int64(0)})
	terrOpenMsg(t, c1)
	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_OPEN_MAIN", nil)
	terrOpenMsg(t, c2)

	c1.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": "A"})
	terrOpenMsg(t, c1)
	for _, g := range []string{"D", "N", "A"} {
		c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": g})
		terrOpenMsg(t, c2)
	}

	c2.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_EJECT_USER", xym.Msg{"vs_usernum": int64(p1.UID)})
	c2.until(t, "ETC_PTO.S_RET_MSG")

	b := terrArenaBattle(t, p1.UID)
	b.mu.Lock()
	gate := ""
	if pl := b.players[p1.UID]; pl != nil {
		gate = pl.Gate
	}
	owner := int32(0)
	if o := b.owners["A"]; o != nil {
		owner = o.UID
	}
	b.mu.Unlock()
	if gate != "A" {
		t.Fatalf("驱逐失败不该把对方挪走，A 现在在 %q", gate)
	}
	if owner != p1.UID {
		t.Fatalf("驱逐失败不该清掉擂主身份，owner=%d", owner)
	}
}

// TestTerritoryArenaNoDongfu：没有洞府不能报名（领土战以洞府为单位）。
func TestTerritoryArenaNoDongfu(t *testing.T) {
	resetTerr()
	c, _, srv := actWorld(t, "nodf")
	giveDongfu(t, srv, 0, "别的洞府")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": int64(1), "gold": int64(0)})
	all := c.until(t, "ETC_PTO.S_RET_MSG")
	if _, ok := all["DONGFU_TERRITORY_PTO.S_TERRITORY_OPEN_MAIN"]; ok {
		t.Fatal("没有洞府不应进场")
	}
}

// TestTerritoryNextEventCountdown：next_event_left_time 是「距下次场景事件」的倒计时（≤300 秒）。
func TestTerritoryNextEventCountdown(t *testing.T) {
	resetTerr()
	c, _, _, foe := terrSetup(t, "evt1")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	m := terrOpenMsg(t, c)
	left := num(m, "next_event_left_time")
	if left <= 0 || left > terrEventInterval {
		t.Fatalf("next_event_left_time=%d, want (0,%d]", left, terrEventInterval)
	}
}

// TestTerritoryFireSceneEvent：场景事件把各据点妖物补满，并返回一条广播公告。
func TestTerritoryFireSceneEvent(t *testing.T) {
	resetTerr()
	c, p, _, foe := terrSetup(t, "evt2")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	b := terrArenaBattle(t, p.UID)

	b.mu.Lock()
	b.mobs["G"].left = 0
	msg := b.fireEventLocked()
	left, want := b.mobs["G"].left, b.mobs["G"].max
	b.mu.Unlock()
	if left != want {
		t.Fatalf("场景事件补怪 %d, want %d", left, want)
	}
	if msg == nil || msg["msg"] == "" {
		t.Fatalf("场景事件应广播公告, got %v", msg)
	}
}

// TestTerritorySayExchange：战场频道「兑换胜利点」无论成功还是仙气不足都必须有提示
// （用户反馈过"输了没提示"——之前成功时只重推主界面、一个字都没有）。
func TestTerritorySayExchange(t *testing.T) {
	resetTerr()
	c, p, _, foe := terrSetup(t, "say1")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	b := terrArenaBattle(t, p.UID)
	b.mu.Lock()
	b.players[p.UID].Xianqi = 60
	b.mu.Unlock()

	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SAY", xym.Msg{"msg": "兑换胜利点", "kind": int64(0)})
	all := c.until(t, "ETC_PTO.S_RET_MSG")
	if m, ok := all["ETC_PTO.S_RET_MSG"]; !ok || m["msg"] == "" {
		t.Fatal("兑换成功必须回一条提示")
	}
	// 战场界面下 retMsg 不显示，提示还要走战场频道（客户端 DongFuChatChannelView 才渲染）。
	chat, ok := all["DONGFU_TERRITORY_PTO.S_TERRITORY_BROADCAST_MSG"]
	if !ok {
		t.Fatal("兑换成功必须在战场频道也给一条")
	}
	if msg, _ := chat["msg"].(string); msg == "" {
		t.Fatalf("频道提示内容为空: %v", chat)
	}
	b.mu.Lock()
	score, xianqi := b.players[p.UID].Score, b.players[p.UID].Xianqi
	b.mu.Unlock()
	if score != 30 || xianqi != 10 {
		t.Fatalf("兑换后 胜利点=%d 仙气=%d, want 30/10", score, xianqi)
	}

	// 仙气不足：也要有提示，且状态不变。
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SAY", xym.Msg{"msg": "兑换胜利点", "kind": int64(0)})
	all = c.until(t, "ETC_PTO.S_RET_MSG")
	if m, ok := all["ETC_PTO.S_RET_MSG"]; !ok || m["msg"] == "" {
		t.Fatal("仙气不足必须回一条提示")
	}
	b.mu.Lock()
	score2 := b.players[p.UID].Score
	b.mu.Unlock()
	if score2 != 30 {
		t.Fatalf("仙气不足不应改动胜利点, got %d", score2)
	}
}

// TestTerritorySayOutsideBattle：不在战场时发频道命令也要有反馈，不能静默。
func TestTerritorySayOutsideBattle(t *testing.T) {
	resetTerr()
	c, _, _ := actWorld(t, "say2")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SAY", xym.Msg{"msg": "兑换胜利点", "kind": int64(0)})
	all := c.until(t, "ETC_PTO.S_RET_MSG")
	if m, ok := all["ETC_PTO.S_RET_MSG"]; !ok || m["msg"] == "" {
		t.Fatal("不在战场也要提示")
	}
}

// TestTerritoryBossHpInherit：官方「妖王血量将被继承…当前血量恢复上限的20%，降低初始值的8%」。
// 打死妖王后血量归零；场景事件时复活，上限 -8%、血量回到 20%。
func TestTerritoryBossHpInherit(t *testing.T) {
	old := dyParamFor
	dyParamFor = func(int, int, int) int { return 1 } // 妖王压到最弱，保证打赢
	t.Cleanup(func() { dyParamFor = old })
	resetTerr()
	c, p, _, foe := terrSetup(t, "boss1")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	// X → C（地界擂A）→ W（妖王）
	for _, g := range []string{"C", "W"} {
		c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": g})
		terrOpenMsg(t, c)
	}
	b := terrArenaBattle(t, p.UID)

	b.mu.Lock()
	hpBefore, capBefore := b.mobs["W"].hpRate, b.mobs["W"].capRate
	b.mu.Unlock()
	if hpBefore != 100 || capBefore != 100 {
		t.Fatalf("妖王初始 hp=%d cap=%d, want 100/100", hpBefore, capBefore)
	}

	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_KILL_BOSS", nil)
	terrOpenMsg(t, c)
	b.mu.Lock()
	left, hp := b.mobs["W"].left, b.mobs["W"].hpRate
	b.mu.Unlock()
	if left != 0 || hp != 0 {
		t.Fatalf("打死妖王后 left=%d hpRate=%d, want 0/0", left, hp)
	}

	// 场景事件：复活，上限 -8%（100→92），血量回到 20%
	b.mu.Lock()
	msg := b.fireEventLocked()
	left, hp, cap := b.mobs["W"].left, b.mobs["W"].hpRate, b.mobs["W"].capRate
	b.mu.Unlock()
	if left != 1 || hp != 20 || cap != 92 {
		t.Fatalf("事件复活后 left=%d hp=%d cap=%d, want 1/20/92", left, hp, cap)
	}
	if msg == nil || msg["msg"] == "" {
		t.Fatal("场景事件应有公告")
	}
}

// TestTerritoryBossLoseKeepsHp：没打死时血量不该被清零（要继承下来）。
func TestTerritoryBossLoseKeepsHp(t *testing.T) {
	old := dyParamFor
	dyParamFor = func(int, int, int) int { return 1 << 30 } // 妖王极强，必败
	t.Cleanup(func() { dyParamFor = old })
	resetTerr()
	c, p, _, foe := terrSetup(t, "boss2")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	for _, g := range []string{"C", "W"} {
		c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", xym.Msg{"gate_key": g})
		terrOpenMsg(t, c)
	}
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_KILL_BOSS", nil)
	c.until(t, "ETC_PTO.S_RET_MSG")

	b := terrArenaBattle(t, p.UID)
	b.mu.Lock()
	left, hp := b.mobs["W"].left, b.mobs["W"].hpRate
	b.mu.Unlock()
	if left != 1 {
		t.Fatalf("打输不应扣掉妖王, left=%d", left)
	}
	if hp <= 0 || hp > 100 {
		t.Fatalf("打输后继承血量异常: hpRate=%d", hp)
	}
}

// TestTerrEndBonus：战场结束奖励——胜 2 / 负 1，有胜场（前 20%）再 +1。
func TestTerrEndBonus(t *testing.T) {
	cases := []struct {
		wins int
		won  bool
		want int
	}{
		{0, false, 1},
		{0, true, 2},
		{3, false, 2},
		{3, true, 3},
	}
	for _, c := range cases {
		if got := terrEndBonus(&terrPlayer{Wins: c.wins}, c.won); got != c.want {
			t.Fatalf("wins=%d won=%v: bonus=%d, want %d", c.wins, c.won, got, c.want)
		}
	}
	if n := terrEndBonus(nil, true); n != 0 {
		t.Fatalf("nil player bonus=%d, want 0", n)
	}
}

// TestTerritoryEndSettle：战场关闭时按胜负把奖励发到洞府神魂（不依赖撤退协议）。
func TestTerritoryEndSettle(t *testing.T) {
	resetTerr()
	c, p, _, foe := terrSetup(t, "evt3")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	terrOpenMsg(t, c)
	b := terrArenaBattle(t, p.UID)
	b.mu.Lock()
	b.players[p.UID].Wins = 1
	b.players[p.UID].Score = 0
	b.scores[0] = 300 // 攻方大幅领先 → 胜
	b.mu.Unlock()

	b.closeAll()
	// 胜 2 + 前 20% 额外 1 = 3（胜利点 0，不走按分折算）。
	if n := p.df().GodSoul; n != 3 {
		t.Fatalf("结束时神魂=%d, want 3", n)
	}
}
