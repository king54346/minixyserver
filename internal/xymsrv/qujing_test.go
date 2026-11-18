package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// qjGoldWorld：测试号的护法换成不同的五星护法（取经只能用五星）。
func qjGoldWorld(t *testing.T, urs string) (*client, *Player, *Server) {
	c, p, srv := actWorld(t, urs)
	var golds []int32
	for id, pi := range gamedata.Pets {
		if pi.Star == 5 && len(pi.Talents[0]) > 0 {
			golds = append(golds, id)
		}
	}
	slices.Sort(golds)
	p.mu.Lock()
	for i, pet := range p.Pets {
		pet.Type = golds[i%len(golds)]
	}
	for i := range 12 { // 多几张：献祭要用不在阵上的满血护法
		p.addPet(golds[(len(p.Pets)+i)%len(golds)])
	}
	p.mu.Unlock()
	return c, p, srv
}

// qjWeaken 把当前这一难的敌人改成一碰就倒（测试用）。
func qjWeaken(p *Player) {
	p.mu.Lock()
	defer p.mu.Unlock()
	q := p.qujing()
	foe := q.foe(p.UID, nil)
	for _, f := range foe.Team {
		f.HP, f.MaxHP, f.Att, f.Talents = 1, 1, 1, nil
	}
}

func qjFightAll(c *client, sel []xym.Msg) {
	var pos []xym.Msg
	names := map[string]bool{}
	for _, x := range sel {
		name := gamedata.Pets[int32(num(x, "type"))].Name
		if len(pos) < 8 && num(x, "hp") > 0 && !names[name] { // 只上活着的（审死、十日圣母这类会让自己人阵亡）；同名只上一张
			names[name] = true
			pos = append(pos, xym.Msg{"use_type": x["use_type"], "bag_pos": x["bag_pos"], "fight_pos": int64(len(pos) + 1)})
		}
	}
	c.sendExt("QUJING_PTO.C_QUJING_START_FIGHT", xym.Msg{"pos_list": pos})
}

// TestQujingFlow：选难度 → 布阵界面 → 打第 1 难（赢）→ 第 2 难输一场（双方血量保留）→ 献祭跳过 → 复活（每天一次）
// → 打完一劫领宝箱 → 下一劫 → 商店兑换 → 援助 → 阵容格。
func TestQujingFlow(t *testing.T) {
	c, p, srv := qjGoldWorld(t, "qujing")
	c.sendExt("QUJING_PTO.C_QUJING_OPEN_MAIN", nil)
	if m := c.expectExt("QUJING_PTO.S_QUJING_OPEN_INIT"); num(m, "left") != 1 {
		t.Fatalf("init %v", m)
	}
	c.sendExt("QUJING_PTO.C_QUJING_SELECT_STAGE_TYPE", xym.Msg{"stage_type": int64(qjHard)})
	if m := c.expectExt("QUJING_PTO.S_QUJING_OPEN_STAGE_MAIN"); num(m, "stage") != 1 || num(m, "sub_stage") != 1 || num(m, "stage_state") != qjOpen {
		t.Fatalf("stage main %v", m)
	}
	c.sendExt("QUJING_PTO.C_QUJING_OPEN_SUB_STAGE_MAIN", nil)
	sub := c.expectExt("QUJING_PTO.S_QUJING_OPEN_SUB_STAGE_MAIN")
	sel := sub["select_list"].([]xym.Msg)
	if len(sub["vs_fight_list"].([]xym.Msg)) != 8 || len(sel) == 0 || num(sub, "stage_type") != qjHard {
		t.Fatalf("sub main %v", sub)
	}
	for _, x := range sel {
		if gamedata.Pets[int32(num(x, "type"))].Star != 5 || num(x, "grade") < 100 {
			t.Fatalf("取经只用五星、等级补到人物等级×2: %v", x)
		}
	}

	// 第 1 难：赢
	qjWeaken(p)
	qjFightAll(c, sel)
	got := c.untilSkipMsg(t, "QUJING_PTO.S_QUJING_OPEN_STAGE_MAIN")
	if got["FIGHT_PTO.S_FIGHT_RECORD"] == nil || num(got["ETC_PTO.S_REWARD_MSG"], "type") != 2 || num(got["QUJING_PTO.S_QUJING_OPEN_STAGE_MAIN"], "sub_stage") != 2 {
		t.Fatalf("win %v", got)
	}

	// 第 2 难（取经人）：把敌人改成打不死，输一场——我方阵亡带到下一场，敌人血量保留
	p.mu.Lock()
	foe := p.qujing().foe(p.UID, nil)
	if foe.Level != 50 || len(foe.Team) < 7 {
		p.mu.Unlock()
		t.Fatalf("取经人阵容 %+v", foe)
	}
	for _, f := range foe.Team {
		f.HP, f.MaxHP, f.Att = 1<<30, 1<<30, 1<<30
	}
	p.mu.Unlock()
	qjFightAll(c, sel)
	got = c.untilSkipMsg(t, "QUJING_PTO.S_QUJING_OPEN_SUB_STAGE_MAIN")
	if num(got["ETC_PTO.S_REWARD_MSG"], "type") != 3 {
		t.Fatalf("lose %v", got)
	}
	after := got["QUJING_PTO.S_QUJING_OPEN_SUB_STAGE_MAIN"]
	dead := 0
	for _, x := range after["select_list"].([]xym.Msg) {
		if num(x, "hp") == 0 {
			dead++
		}
	}
	if dead == 0 {
		t.Fatal("输了应有护法阵亡并保留")
	}
	// 阵亡的不能再上
	qjFightAll(c, sel)
	c.expectExtSkip("ETC_PTO.S_RET_MSG")

	// 献祭满血护法换点数，跳过第 2 难
	p.mu.Lock()
	var full []xym.Msg
	inTeam := map[int]bool{}
	for _, sl := range p.qujing().Lineups[0] {
		inTeam[sl.BagPos] = true
	}
	for _, x := range p.qjPets() {
		if x.useType == 1 && x.hp == int64(x.f.MaxHP) && !inTeam[x.bagPos] {
			full = append(full, xym.Msg{"bag_pos": int64(x.bagPos)})
		}
	}
	p.qujing().Xianji = 0
	p.mu.Unlock()
	if len(full) == 0 {
		t.Skip("测试号护法不够献祭")
	}
	c.sendExt("QUJING_PTO.C_QUJING_TRANSFORM_PETS_TO_XIANJI", xym.Msg{"bag_pos_list": full})
	x := c.expectExt("QUJING_PTO.S_QUJING_FINISH_TRANSFORM_PETS_TO_XIANJI")
	if num(x, "xianji") < 15*len(full) {
		t.Fatalf("xianji %v", x)
	}
	c.collect()
	p.mu.Lock()
	p.qujing().Xianji = 10000
	p.mu.Unlock()
	c.sendExt("QUJING_PTO.C_QUJING_SKIP_SUB_STAGE", nil)
	c.expectExt("QUJING_PTO.S_QUJING_FINISH_SKIP_SUB_STAGE")
	p.mu.Lock()
	if q := p.qujing(); q.Sub != 3 || q.Xianji != 10000-qjSkipCost[qjHard][0] {
		p.mu.Unlock()
		t.Fatalf("skip sub %d xianji %d", q.Sub, q.Xianji)
	}
	p.mu.Unlock()

	// 全体复活：每天一次
	c.sendExt("QUJING_PTO.C_QUJING_RELIVE_ALL_PETS", nil)
	got = c.untilSkipMsg(t, "QUJING_PTO.S_QUJING_OPEN_SUB_STAGE_MAIN")
	for _, x := range got["QUJING_PTO.S_QUJING_OPEN_SUB_STAGE_MAIN"]["select_list"].([]xym.Msg) {
		if num(x, "hp") != num(x, "max_hp") {
			t.Fatalf("复活后应满血: %v", x)
		}
	}
	c.sendExt("QUJING_PTO.C_QUJING_RELIVE_ALL_PETS", nil)
	c.expectMsgExt(t, "已用完")

	// 打到第 9 难（含特殊关、打魂关、首领），领宝箱，开下一劫
	for sub := 3; sub <= 9; sub++ {
		qjWeaken(p)
		c.sendExt("QUJING_PTO.C_QUJING_OPEN_SUB_STAGE_MAIN", nil)
		sel = c.expectExt("QUJING_PTO.S_QUJING_OPEN_SUB_STAGE_MAIN")["select_list"].([]xym.Msg)
		qjFightAll(c, sel)
		if rw := c.untilSkipMsg(t, "ETC_PTO.S_REWARD_MSG")["ETC_PTO.S_REWARD_MSG"]; num(rw, "type") != 2 {
			t.Fatalf("sub %d: %v", sub, rw)
		}
		got = c.untilSkipMsg(t, "QUJING_PTO.S_QUJING_OPEN_STAGE_MAIN")
	}
	if m := got["QUJING_PTO.S_QUJING_OPEN_STAGE_MAIN"]; num(m, "stage_state") != qjFinish {
		t.Fatalf("劫完成 %v", m)
	}
	p.mu.Lock()
	before := p.itemCount(qjJingshu)
	p.mu.Unlock()
	c.sendExt("QUJING_PTO.C_QUJING_CLICK_OPEN_BOX", nil)
	got = c.untilSkipMsg(t, "QUJING_PTO.S_QUJING_OPEN_STAGE_MAIN")
	p.mu.Lock()
	gotBooks := p.itemCount(qjJingshu) - before
	p.mu.Unlock()
	if gotBooks != int32(qjBoxBase(qjHard, 1)) || num(got["QUJING_PTO.S_QUJING_OPEN_STAGE_MAIN"], "stage_state") != qjGotBox {
		t.Fatalf("box +%d %v", gotBooks, got)
	}
	c.sendExt("QUJING_PTO.C_QUJING_OPEN_NEXT_STAGE", nil)
	if m := c.expectExt("QUJING_PTO.S_QUJING_OPEN_STAGE_MAIN"); num(m, "stage") != 2 || num(m, "sub_stage") != 1 {
		t.Fatalf("next stage %v", m)
	}

	// 商店：经书兑换三星卡
	p.mu.Lock()
	p.addItem(qjJingshu, 1000)
	p.mu.Unlock()
	c.sendExt("QUJING_PTO.C_QUJING_OPEN_GOODS_LIST", nil)
	gl := c.expectExt("QUJING_PTO.S_QUJING_OPEN_GOODS_LIST")
	if len(gl["list"].([]xym.Msg)) != len(gamedata.Qujing.Shop) {
		t.Fatalf("goods %v", gl)
	}
	g := gamedata.Qujing.Shop[0]
	c.sendExt("QUJING_PTO.C_QUJING_BUY_GOODS", xym.Msg{"pos": int64(g.Pos), "id": int64(g.ID), "type": int64(1), "price_key": "jingshu", "buy_num": int64(1)})
	got = c.untilSkipMsg(t, "QUJING_PTO.S_QUJING_OPEN_GOODS_LIST")
	if l := got["QUJING_PTO.S_QUJING_OPEN_GOODS_LIST"]["list"].([]xym.Msg); num(l[0], "limit") != g.Limit-1 {
		t.Fatalf("limit after buy %v", l[0])
	}
	shop := got["QUJING_PTO.S_QUJING_OPEN_GOODS_LIST"]
	books := num(shop, "jingshu")
	var sand xym.Msg
	for _, good := range shop["list"].([]xym.Msg) {
		if num(good, "id") == cuiYuanSha {
			sand = good
			break
		}
	}
	if sand == nil || num(sand, "type") != rtItem || num(sand, "jingshu") != 100 ||
		num(sand, "gold") != -1 || num(sand, "limit") != books/100 {
		t.Fatalf("sand shop entry %v with %d books", sand, books)
	}
	buySand := xym.Msg{"pos": sand["pos"], "id": sand["id"], "type": sand["type"], "price_key": "jingshu", "buy_num": int64(2)}
	c.sendExt("QUJING_PTO.C_QUJING_BUY_GOODS", buySand)
	got = c.untilSkipMsg(t, "QUJING_PTO.S_QUJING_OPEN_GOODS_LIST")
	if balance := num(got["QUJING_PTO.S_QUJING_OPEN_GOODS_LIST"], "jingshu"); balance != books-200 {
		t.Fatalf("sand exchange left %d books, want %d", balance, books-200)
	}
	buySand["buy_num"] = int64(books/100 + 1)
	c.sendExt("QUJING_PTO.C_QUJING_BUY_GOODS", buySand)
	c.expectMsgExt(t, "经书不足")
	p.mu.Lock()
	sandCount, bookCount := p.itemCount(cuiYuanSha), p.itemCount(qjJingshu)
	p.mu.Unlock()
	if sandCount != 10 || int(bookCount) != books-200 {
		t.Fatalf("sand exchange gave %d sand and left %d books", sandCount, bookCount)
	}

	// 援助：花 10 元宝招一名五星护法（use_type 2）
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 100; return true })
	c.sendExt("QUJING_PTO.C_QUJING_USE_HELP_DATA", nil)
	ask := c.expectExt("ETC_PTO.S_ASK_BOX")
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": ask["session"], "opt": int64(0)})
	got = c.untilSkipMsg(t, "QUJING_PTO.S_QUJING_OPEN_SUB_STAGE_MAIN")
	helpers := 0
	for _, x := range got["QUJING_PTO.S_QUJING_OPEN_SUB_STAGE_MAIN"]["select_list"].([]xym.Msg) {
		if num(x, "use_type") == 2 && num(x, "bag_pos") > qjHelperBase {
			helpers++
		}
	}
	if helpers != 1 {
		t.Fatalf("helpers %d", helpers)
	}

	// 阵容格
	c.sendExt("QUJING_PTO.C_QUJING_GET_LINEUP_LIST_INFO", nil)
	if m := c.expectExt("QUJING_PTO.S_QUJING_LINEUP_LIST_INFO"); num(m, "max_no") != 1 || len(m["list"].([]xym.Msg)) != 1 {
		t.Fatalf("lineups %v", m)
	}
	c.sendExt("QUJING_PTO.C_QUJING_ALL_RANK", nil)
	if m := c.expectExt("QUJING_PTO.S_QUJING_OPEN_ALL_RANK"); num(m, "stage_hard") != 1 || num(m, "sub_stage_hard") != 9 {
		t.Fatalf("rank %v", m)
	}
}

// TestQujingFoes：九劫九难都能生成敌人；NPC / 取经人的属性按劫数增长。
func TestQujingFoes(t *testing.T) {
	prev := map[int]int64{}
	for stage := 1; stage <= qjStages; stage++ {
		for sub := 1; sub <= 9; sub++ {
			foe := qjFoeFor(20001, 1000, qjHard, stage, sub, nil)
			if len(foe.Team) == 0 || foe.Name == "" {
				t.Fatalf("劫%d难%d 没有敌人: %+v", stage, sub, foe)
			}
			if sub == 1 {
				var hp int64
				for _, f := range foe.Team {
					hp += int64(f.MaxHP)
				}
				if hp <= prev[sub] {
					t.Fatalf("劫%d 第1难敌人没有比上一劫强: %d <= %d", stage, hp, prev[sub])
				}
				prev[sub] = hp
			}
		}
	}
}

// expectMsgExt 读到第一条 S_RET_MSG，要求包含 want。
func (c *client) expectMsgExt(t *testing.T, want string) {
	t.Helper()
	got := c.untilSkipMsg(t, "ETC_PTO.S_RET_MSG")
	if msg, _ := got["ETC_PTO.S_RET_MSG"]["msg"].(string); !strings.Contains(msg, want) {
		t.Fatalf("want %q, got %q", want, msg)
	}
}

// TestQujingPool：取胜阵容进池；别人的真实阵容不够 30 套时和抓包阵容一起抽，够了只抽真实阵容；不会抽到自己。
func TestQujingPool(t *testing.T) {
	_, p, srv := qjGoldWorld(t, "qjpool")
	if pool := srv.qjPoolFor(p.UID); len(pool) != len(gamedata.Qujing.Pvp) || pool[0].UID != 0 {
		t.Fatalf("起初是抓包阵容: %d", len(pool))
	}
	team := func(n int) []*battle.Fighter {
		var out []*battle.Fighter
		for i := range n {
			out = append(out, &battle.Fighter{Pos: 101 + i, Icon: gamedata.Qujing.Pvp[0].Members[i].Type, Speed: 9})
		}
		return out
	}
	srv.qjPoolAdd(p.UID, "自己", 50, 1, team(6))
	srv.qjPoolAdd(900001, "张三", 48, 1, team(6))
	srv.qjPoolAdd(900001, "张三", 49, 1, team(7)) // 同一人只留最新
	pool := srv.qjPoolFor(p.UID)
	last := pool[len(pool)-1]
	if len(pool) != len(gamedata.Qujing.Pvp)+1 || last.Name != "张三" || last.Grade != 49 || len(last.Members) != 7 || last.Members[0].Pos != 1 {
		t.Fatalf("pool %d %+v", len(pool), last)
	}
	for i := range qjPoolMax {
		srv.qjPoolAdd(int32(910000+i), "路人", 50, 1, team(6))
	}
	pool = srv.qjPoolFor(p.UID)
	if len(pool) != qjPoolMax || slices.ContainsFunc(pool, func(e qjPoolEntry) bool { return e.UID == 0 || e.Name == "张三" }) {
		t.Fatalf("满 %d 套后挤掉最早的: %d", qjPoolMax, len(pool))
	}
	foe := qjFoeFor(p.UID, 1000, qjHard, 1, 2, pool)
	if foe.Name != "路人" || len(foe.Team) != 6 || !strings.Contains(foe.Desc, "取经人") {
		t.Fatalf("foe %+v", foe)
	}
}

// TestQujingOfficial：九劫的 1/4/7/8/9 难都是官方阵型；属性与抓包一致（困难第 1 劫第 1 难孔子 3175 / 446，
// 第 1 劫首领蝴蝶 9306 / 806，第 5 劫第 1 难尉迟恭在抓包的布阵界面里）。
func TestQujingOfficial(t *testing.T) {
	for stage := 1; stage <= qjStages; stage++ {
		for _, sub := range []int{1, 4, 7, 8, 9} {
			if st := qjTemplate(stage, sub); st == nil || len(st.Members) == 0 || st.Param == 0 {
				t.Fatalf("劫%d难%d 没有官方阵型", stage, sub)
			}
		}
	}
	for _, c := range []struct {
		stage, sub int
		name       string
		hp, att    int32
	}{{1, 1, "孔子", 3175, 446}, {1, 9, "蝴蝶", 9306, 806}, {4, 9, "母蜘蛛", 25301, 1655}} {
		foe := qjFoeFor(1, 1, qjHard, c.stage, c.sub, nil)
		ok := false
		for _, f := range foe.Team {
			if f.Name == c.name {
				ok = f.MaxHP == c.hp && f.Att == c.att
				if !ok {
					t.Fatalf("%s: %d/%d want %d/%d", c.name, f.MaxHP, f.Att, c.hp, c.att)
				}
			}
		}
		if !ok {
			t.Fatalf("劫%d难%d 没有 %s", c.stage, c.sub, c.name)
		}
	}
}

// TestQujingAllStagesReplay：九劫的 NPC / 特殊 / 打魂 / 首领关各打一场（我方 8 名五星护法），战报都要能通过回放校验，
// 首领变身、分裂、自爆、复活等演出不能让客户端出错。
func TestQujingAllStagesReplay(t *testing.T) {
	var team []*battle.Fighter
	for _, m := range gamedata.Qujing.Pvp[0].Members {
		if f := qjMemberFighter(m, 3); f != nil {
			f.Pos -= 100
			f.ID = int32(f.Pos)
			team = append(team, f)
		}
	}
	for stage := 1; stage <= qjStages; stage++ {
		for _, sub := range []int{1, 4, 7, 8, 9} {
			foe := qjFoeFor(1, 1, qjHard, stage, sub, nil)
			t1, t2 := battle.CloneTeam(team), battle.CloneTeam(foe.Team)
			r1, r2 := battle.CloneTeam(t1), battle.CloneTeam(t2)
			opts := battle.Options{QjNPCs: qjTransformNPCs(qjHard, stage, sub)}
			if sub == 7 {
				opts = qjSpecialOpts(stage)
				opts.QjNPCs = qjTransformNPCs(qjHard, stage, sub)
			}
			for seed := uint64(0); seed < 3; seed++ {
				a, b := battle.CloneTeam(t1), battle.CloneTeam(t2)
				res := battle.SimulateWith(a, b, opts, rand.New(rand.NewPCG(seed, uint64(stage*10+sub))))
				if _, err := battle.Replay(r1, r2, res.Record); err != nil {
					t.Fatalf("劫%d难%d seed %d: %v", stage, sub, seed, err)
				}
			}
		}
	}
}

// TestQujingTrial：打魂关落宝进本周前三就记录像；榜和录像能取回；同一人只留最好的一场。
func TestQujingTrial(t *testing.T) {
	c, p, srv := qjGoldWorld(t, "qjtrial")
	s := &Session{srv: srv, role: &Role{UID: p.UID, Name: "甲", Grade: 50, Icon: 1}, player: p}
	rec := func(n string) *gateRecord {
		return &gateRecord{Name: n, T1: []*battle.Fighter{{Pos: 1, Icon: 25001, HP: 1, MaxHP: 1}}, Record: []byte{byte(battle.CmdWarStart), 0}}
	}
	s.qjTrialOffer(p, 3, 50, rec("甲50"))
	s.qjTrialOffer(p, 3, 40, rec("甲40")) // 比自己原来的差：不记
	for i, cnt := range []int{60, 30, 70} {
		o := &Session{srv: srv, role: &Role{UID: int32(800 + i), Name: fmt.Sprint("乙", i), Grade: 50}}
		o.qjTrialOffer(&Player{UID: int32(800 + i)}, 3, cnt, rec(fmt.Sprint("乙", cnt)))
	}
	list := srv.qjTrialList(3)
	if len(list) != 3 || list[0].Cnt != 70 || list[1].Cnt != 60 || list[2].Cnt != 50 || list[2].UID != p.UID {
		t.Fatalf("榜 %+v", list)
	}
	for _, x := range list {
		r := loadRecord(srv.players.st, store.RecordQujingTrial, qjTrialKey(3, x.Slot))
		if r == nil || r.Name != fmt.Sprint("乙", x.Cnt) && r.Name != "甲50" {
			t.Fatalf("录像 %+v -> %+v", x, r)
		}
	}
	c.sendExt("QUJING_PTO.C_QUJING_TRIAL_STAGE_RANK", xym.Msg{"stage": int64(3)})
	if m := c.expectExtSkip("QUJING_PTO.S_QUJING_OPEN_TRIAL_STAGE_RANK", "ETC_PTO.S_RET_MSG"); len(m["rank"].([]xym.Msg)) != 3 {
		t.Fatalf("rank %+v", m)
	}
	c.sendExt("QUJING_PTO.C_QUJING_TRIAL_STAGE_RANK_PLAY_RECORD", xym.Msg{"stage": int64(3), "pos": int64(3)})
	if m := c.expectExt("FIGHT_PTO.S_FIGHT_RECORD"); m["name1"] != "甲50" {
		t.Fatalf("record %+v", m["name1"])
	}
	c.sendExt("TREE_PTO.C_EVENT_LIST", nil)
	if m := c.expectExt("TREE_PTO.S_EVENT_LIST"); len(m["event_list"].([]xym.Msg)) != 1 {
		t.Fatalf("事件 %+v", m["event_list"])
	}
}

// TestQujingSameName：同名护法不能同时上阵（开始战斗时校验）。
func TestQujingSameName(t *testing.T) {
	c, p, _ := qjGoldWorld(t, "qjsame")
	p.mu.Lock()
	typ := p.Pets[0].Type
	p.addPet(typ)
	p.mu.Unlock()
	c.sendExt("QUJING_PTO.C_QUJING_SELECT_STAGE_TYPE", xym.Msg{"stage_type": int64(qjHard)})
	c.expectExt("QUJING_PTO.S_QUJING_OPEN_STAGE_MAIN")
	c.sendExt("QUJING_PTO.C_QUJING_OPEN_SUB_STAGE_MAIN", nil)
	sel := c.expectExt("QUJING_PTO.S_QUJING_OPEN_SUB_STAGE_MAIN")["select_list"].([]xym.Msg)
	var pos []xym.Msg
	for _, x := range sel {
		if int32(num(x, "type")) == typ {
			pos = append(pos, xym.Msg{"use_type": x["use_type"], "bag_pos": x["bag_pos"], "fight_pos": int64(len(pos) + 1)})
		}
	}
	if len(pos) < 2 {
		t.Fatalf("同名卡 %d", len(pos))
	}
	c.sendExt("QUJING_PTO.C_QUJING_START_FIGHT", xym.Msg{"pos_list": pos})
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "相同的护法") {
		t.Fatalf("msg %v", m)
	}
}

// TestWeekResetTuesdayMidnight：一周从「周二 00:00」换（= 客户端文案的「周一24:00截止」）。
// 早先按 lxEpoch 2026-10-20 09:00 滑动 7 天，等于周二 9 点才换周，周一整天都不重置。
func TestWeekResetTuesdayMidnight(t *testing.T) {
	loc := time.Local
	mondayLate := time.Date(2026, 11, 16, 23, 59, 0, 0, loc) // 周一 23:59
	mondayEarly := time.Date(2026, 11, 16, 0, 0, 0, 0, loc)  // 周一 00:00
	tuesdayZero := time.Date(2026, 11, 17, 0, 0, 0, 0, loc)  // 周二 00:00
	tuesdayNine := time.Date(2026, 11, 17, 9, 0, 0, 0, loc)  // 旧的换周点

	if a, b := lxWeekOf(mondayEarly), lxWeekOf(mondayLate); a != b {
		t.Fatalf("同一个周一的 00:00 和 23:59 应该同周: %d vs %d", a, b)
	}
	if w := lxWeekOf(tuesdayZero); w != lxWeekOf(mondayLate)+1 {
		t.Fatalf("周二 00:00 应该换周: 周一=%d 周二00:00=%d", lxWeekOf(mondayLate), w)
	}
	if lxWeekOf(tuesdayNine) != lxWeekOf(tuesdayZero) {
		t.Fatalf("周二 09:00 不该再换一次周: %d vs %d", lxWeekOf(tuesdayZero), lxWeekOf(tuesdayNine))
	}
}

// TestQujingWeeklyReset：上周用完次数的玩家，跨过「周二 00:00」后次数恢复；
// 周一 23:59 还算上周，不给重置。
func TestQujingWeeklyReset(t *testing.T) {
	oldNow := lxNow
	cur := time.Date(2026, 11, 16, 23, 59, 0, 0, time.Local) // 周一 23:59
	lxNow = func() time.Time { return cur }
	t.Cleanup(func() { lxNow = oldNow })

	srv, addr, _ := startServerSrv(t, DefaultConfig())
	enterWorld(t, addr, "qjweek").conn.Close()
	acc, _ := srv.accounts.Login("qjweek", "pw", "")
	p, _ := srv.players.Get(acc.UID)

	// 造一份上周的存档：次数用光、正在取经。
	p.Qujing = &QujingState{Week: lxWeekOf(cur), Left: 0, Active: true}

	if q := p.qujing(); q.Left != 0 || !q.Active {
		t.Fatalf("周一 23:59 不该重置: left=%d active=%v", q.Left, q.Active)
	}

	cur = time.Date(2026, 11, 17, 0, 1, 0, 0, time.Local) // 周二 00:01
	q := p.qujing()
	if q.Left != qjWeeklyTimes {
		t.Fatalf("周二 00:01 应重置为 %d 次, got %d", qjWeeklyTimes, q.Left)
	}
	if q.Active || q.Stage != 0 || len(q.PetHP) != 0 {
		t.Fatalf("跨周应清空进行中的取经: %+v", q)
	}
	if q.Week != lxWeekOf(cur) {
		t.Fatalf("周序号应更新为 %d, got %d", lxWeekOf(cur), q.Week)
	}
}
