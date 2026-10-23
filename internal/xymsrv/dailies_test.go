package xymsrv

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// actWorld 进游戏：人物 50 级、出战卡 100 级，返回重新登录后的连接。
func actWorld(t *testing.T, urs string) (*client, *Player, *Server) {
	t.Helper()
	srv, addr, _ := startServerSrv(t, DefaultConfig())
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
	return c, p, srv
}

// weakTeam 是一碰就倒的敌人。
func weakTeam() []*battle.Fighter {
	return []*battle.Fighter{{ID: 900001, Pos: 101, Name: "小妖", Att: 1, HP: 1, MaxHP: 1, Speed: 1, Icon: 11001, Race: 1, Grade: 1}}
}

func strongTeam() []*battle.Fighter {
	return []*battle.Fighter{{ID: 900001, Pos: 101, Name: "大妖", Att: 1 << 30, HP: 1 << 30, MaxHP: 1 << 30, Speed: 99, Icon: 11001, Race: 1, Grade: 200}}
}

// expectMsg 读一帧 S_MSG 提示，要求包含 want。
func (c *client) expectMsg(t *testing.T, want string) {
	t.Helper()
	main, body := c.recv()
	if main != xym.SMsg || !strings.Contains(string(body), want) {
		t.Fatalf("want message %q, got main=%d %q", want, main, body)
	}
}

// TestJoke：每天只能打一次招呼（点亮一颗心、finish 1、再点只回提示）；心跨天累计，第 4 颗必得一双鞋，之后重新累计。
func TestJoke(t *testing.T) {
	c, p, _ := actWorld(t, "joker")
	c.sendExt("ETC_PTO.C_OPEN_JOKE", nil)
	m := c.expectExt("ETC_PTO.S_OPEN_JOKE")
	if num(m, "max") != jokeMax || num(m, "times") != 0 || num(m, "finish") != 0 || m["role"] != int64(jokeRole(time.Now())) {
		t.Fatalf("open %v", m)
	}
	suits := len(p.Suits)
	for i := 1; i <= jokeMax+1; i++ {
		c.sendExt("ETC_PTO.C_MAKE_JOKE", nil)
		got := c.until(t, "ETC_PTO.S_OPEN_JOKE")
		want := (i-1)%jokeMax + 1 // 满 4 颗领大奖后，下一天重新从 1 颗开始
		if m := got["ETC_PTO.S_OPEN_JOKE"]; num(m, "times") != want || num(m, "finish") != 1 {
			t.Fatalf("day %d: %v", i, m)
		}
		if r := got["ETC_PTO.S_REWARD_MSG"]; want < jokeMax && num(r, "cash") != 2000*want {
			t.Fatalf("day %d reward %v", i, r)
		}
		if want == jokeMax && got["ITEM_PTO.S_UPDATE_SUIT"] == nil {
			t.Fatal("4th heart without shoes")
		}
		c.sendExt("ETC_PTO.C_MAKE_JOKE", nil) // 同一天再点：只回提示
		c.expectExt("ETC_PTO.S_RET_MSG")
		p.mu.Lock()
		p.joke().Day = "2000-01-01" // 过一天
		p.mu.Unlock()
	}
	if len(p.Suits) <= suits {
		t.Fatal("no shoes")
	}
}

// TestWish：选 4 星以上护法和有缘护法，8 张牌第一张免费、其余各 1 颗求缘珠，翻完正好 6 个有缘魂魄、4 个本身魂魄、100 魂值；
// 翻到一半不能重选，翻完可以。
func TestWish(t *testing.T) {
	c, p, srv := actWorld(t, "wisher")
	p.mu.Lock()
	low := p.addPet(11001) // 1 星
	pet := p.addPet(25001)
	p.addItem(wishItem, 7)
	p.mu.Unlock()
	to := gamedata.Wish[25001][0]

	c.sendExt("WISH_PTO.C_OPEN_WISH", xym.Msg{"from_npc": 0, "to_type": 0})
	if m := c.expectExt("WISH_PTO.S_OPEN_WISH"); num(m, "from_type") != 0 || num(m, "free_left") != 1 || num(m, "left") != 7 {
		t.Fatalf("query %v", m)
	}
	c.sendExt("WISH_PTO.C_OPEN_WISH", xym.Msg{"from_npc": low.NpcNum, "to_type": to})
	c.expectExt("ETC_PTO.S_RET_MSG")
	c.expectExt("WISH_PTO.S_OPEN_WISH")
	c.sendExt("WISH_PTO.C_OPEN_GRID", xym.Msg{"pos": 0}) // 还没选护法
	c.expectExt("ETC_PTO.S_RET_MSG")
	c.expectExt("WISH_PTO.S_OPEN_WISH")
	c.sendExt("WISH_PTO.C_OPEN_WISH", xym.Msg{"from_npc": pet.NpcNum, "to_type": to})
	if m := c.expectExt("WISH_PTO.S_OPEN_WISH"); num(m, "from_type") != 25001 || num(m, "to_type") != int(to) || num(m, "can_rechoose") != 1 {
		t.Fatalf("select %v", m)
	}
	soul0 := srv.roles.Get(p.UID).SoulExp
	for i := 0; i < wishGrids; i++ {
		c.sendExt("WISH_PTO.C_OPEN_GRID", xym.Msg{"pos": i})
		m := c.until(t, "WISH_PTO.S_OPEN_WISH")["WISH_PTO.S_OPEN_WISH"]
		if l := m["current"].([]xym.Msg); len(l) != i+1 || num(l[i], "pos") != i {
			t.Fatalf("grid %d: %v", i, m)
		}
		if i == 3 {
			c.sendExt("WISH_PTO.C_RECHOOSE_WISH", nil)
			c.expectExt("ETC_PTO.S_RET_MSG")
			c.expectExt("WISH_PTO.S_OPEN_WISH")
		}
	}
	if p.Souls[to] != 6 || p.Souls[25001] != 4 || srv.roles.Get(p.UID).SoulExp-soul0 != 100 || p.itemCount(wishItem) != 0 {
		t.Fatalf("souls to %d from %d soulexp +%d orbs %d", p.Souls[to], p.Souls[25001], srv.roles.Get(p.UID).SoulExp-soul0, p.itemCount(wishItem))
	}
	c.sendExt("WISH_PTO.C_OPEN_GRID", xym.Msg{"pos": 0})
	c.expectExt("ETC_PTO.S_RET_MSG")
	c.expectExt("WISH_PTO.S_OPEN_WISH")
	c.sendExt("WISH_PTO.C_RECHOOSE_WISH", nil)
	if m := c.expectExt("WISH_PTO.S_OPEN_WISH"); num(m, "from_type") != 0 {
		t.Fatalf("rechoose %v", m)
	}
}

// TestMijing：周三只开万刃山；每天 3 次，赢了扣体力、得强化晶；没开的副本、等级不够、次数用完都拒绝。
func TestMijing(t *testing.T) {
	oldNow, oldEnemy := mijingNow, mijingEnemyFor
	mijingNow = func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.Local) } // 周三
	mijingEnemyFor = func(int) []*battle.Fighter { return weakTeam() }
	t.Cleanup(func() { mijingNow, mijingEnemyFor = oldNow, oldEnemy })
	c, p, srv := actWorld(t, "mijing")

	c.sendExt("QUEST_PTO.C_OPEN_MIJING_MAIN", nil)
	levels := c.expectExt("QUEST_PTO.S_OPEN_MIJING_MAIN")["levels"].([]xym.Msg)
	for _, l := range levels {
		if open := num(l, "id") == 3; num(l, "status") != b2i(open) || (open && num(l, "count") != mijingTimes) {
			t.Fatalf("level %v", l)
		}
	}
	c.sendExt("QUEST_PTO.C_OPEN_MIJING_DETAIL", xym.Msg{"id": 3})
	m := c.expectExt("QUEST_PTO.S_OPEN_MIJING_DETAIL")
	diffs := m["difficulty"].([]xym.Msg)
	if len(diffs) != mijingDiffs || num(diffs[2], "num") != 303 || num(diffs[2], "recommend") != 1 || len(diffs[0]["items_main"].([]xym.Msg)) != 1 {
		t.Fatalf("detail %v", m)
	}

	c.sendExt("QUEST_PTO.C_START_QUEST_FIGHT", xym.Msg{"qid": 101}) // 金沙湾今天不开
	c.expectMsg(t, "没有开放")
	suits := len(p.Suits)
	for i := 1; i <= mijingTimes; i++ {
		c.sendExt("QUEST_PTO.C_START_QUEST_FIGHT", xym.Msg{"qid": 303})
		got := c.until(t, "ETC_PTO.S_REWARD_MSG")
		if got["FIGHT_PTO.S_FIGHT_RECORD"] == nil || num(got["ETC_PTO.S_REWARD_MSG"], "type") != 2 {
			t.Fatalf("fight %d: %v", i, got)
		}
		if tp := srv.roles.Get(p.UID).TPoint; tp != 60-14*i {
			t.Fatalf("tpoint %d", tp)
		}
	}
	if len(p.Suits) < suits+3*3 { // 难度 3（档位 5）：每次至少 3 个强化晶魄
		t.Fatalf("suits %d -> %d", suits, len(p.Suits))
	}
	c.sendExt("QUEST_PTO.C_START_QUEST_FIGHT", xym.Msg{"qid": 303})
	c.expectMsg(t, "次数已用完")
}

// TestTree：种树、浇灌升级、除妖、成熟收获元宝（回到 1 级未种）、一天只能种一棵、摇钱树林。
func TestTree(t *testing.T) {
	old := treeBugFor
	treeBugFor = func(int) []*battle.Fighter { return weakTeam() }
	t.Cleanup(func() { treeBugFor = old })
	c, p, srv := actWorld(t, "treer")
	c.sendExt("TREE_PTO.C_LOOK_TREE", nil)
	if m := c.expectExt("TREE_PTO.S_OPEN_PLANT"); num(m, "state") != treeNone || num(m, "level") != 1 {
		t.Fatalf("look %v", m)
	}
	c.sendExt("TREE_PTO.C_PLANT_TREE", nil)
	if m := c.expectExt("TREE_PTO.S_OPEN_PLANT"); num(m, "state") != treeGrow || num(m, "lefttime") < 8*3600-5 || num(m, "planted_nums") != 1 {
		t.Fatalf("plant %v", m)
	}
	p.mu.Lock()
	p.Tree.Bug = 1
	p.addItem(treeDew, 6)
	p.mu.Unlock()
	c.sendExt("TREE_PTO.C_WATERING_TREE", nil) // 有妖虱不能培养
	c.expectExt("ETC_PTO.S_RET_MSG")
	c.expectExt("TREE_PTO.S_OPEN_PLANT")
	c.sendExt("TREE_PTO.C_FIGHT_BUG", xym.Msg{"usernum": p.UID})
	c.expectExt("FIGHT_PTO.S_FIGHT_RECORD")
	c.expectExt("TREE_PTO.S_FIGHT_WIN")
	if m := c.expectExt("TREE_PTO.S_OPEN_PLANT"); num(m, "bug") != 0 {
		t.Fatalf("bug %v", m)
	}
	c.sendExt("TREE_PTO.C_WATERING_TREE", nil)
	if m := c.until(t, "TREE_PTO.S_OPEN_PLANT")["TREE_PTO.S_OPEN_PLANT"]; num(m, "level") != 2 || p.itemCount(treeDew) != 0 {
		t.Fatalf("water %v", m)
	}
	p.mu.Lock()
	p.Tree.Planted -= 9 * 3600
	p.mu.Unlock()
	c.sendExt("TREE_PTO.C_LOOK_TREE", nil)
	if m := c.expectExt("TREE_PTO.S_OPEN_PLANT"); num(m, "state") != treeRipe || num(m, "gold") != 44 {
		t.Fatalf("ripe %v", m)
	}
	gold := srv.roles.Get(p.UID).Gold
	c.sendExt("TREE_PTO.C_GET_GOLD", nil)
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	if r := c.expectExt("ETC_PTO.S_REWARD_MSG"); num(r, "gold") != 44 || num(r, "type") != 1 {
		t.Fatalf("harvest %v", r)
	}
	if m := c.expectExt("TREE_PTO.S_OPEN_PLANT"); num(m, "state") != treeNone || num(m, "level") != 1 || srv.roles.Get(p.UID).Gold != gold+44 {
		t.Fatalf("after harvest %v", m)
	}
	c.sendExt("TREE_PTO.C_PLANT_TREE", nil)
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "今天已经种过") {
		t.Fatalf("replant %v", m)
	}
	c.sendExt("TREE_PTO.C_TREE_RANK", nil)
	if m := c.expectExt("TREE_PTO.S_TREE_RANK"); num(m, "rich") != 44 || len(m["list"].([]xym.Msg)) == 0 {
		t.Fatalf("rank %v", m)
	}
}

// TestDuobao：3 个对手；胜利得残页（1 星要 3 片）、保底 +2，失败 +1；凑满得宝卷；活力不足拒绝；宝卷兑换装备 / 宝石。
func TestDuobao(t *testing.T) {
	c, p, srv := actWorld(t, "duobao")
	c.sendExt("DUOBAO_PTO.C_OPEN_DUOBAO", nil)
	if m := c.expectExt("DUOBAO_PTO.S_OPEN_DUOBAO"); num(m, "class") != 1 || len(m["fight_list"].([]xym.Msg)) != dbFoes {
		t.Fatalf("open %v", m)
	}
	setFoes := func(team []*battle.Fighter) {
		p.mu.Lock()
		for _, f := range p.Duobao.Foes {
			f.Team = team
		}
		p.mu.Unlock()
	}
	setFoes(weakTeam())
	for i := 1; i <= 3; i++ {
		c.sendExt("DUOBAO_PTO.C_FIGHT_DUOBAO", xym.Msg{"idx": i - 1})
		got := c.until(t, "ETC_PTO.S_REWARD_MSG")
		if r := got["ETC_PTO.S_REWARD_MSG"]; num(r, "type") != 2 || num(r, "cash") != dbWinCash || r["record"] != "duobao:"+itoa(i) {
			t.Fatalf("win %d: %v", i, r)
		}
		if b := got["DUOBAO_PTO.S_SEND_BAODI_COUNT"]; num(b, "baodi_count") != 2*i {
			t.Fatalf("baodi %v", b)
		}
		if i < 3 && num(got["DUOBAO_PTO.S_OPEN_DUOBAO"], "count") != i {
			t.Fatalf("count %v", got["DUOBAO_PTO.S_OPEN_DUOBAO"])
		}
	}
	if d := p.Duobao; d.Tickets[1] != 1 || d.Count != 0 || srv.roles.Get(p.UID).HPoint != 14 {
		t.Fatalf("tickets %v count %d hpoint %d", d.Tickets, d.Count, srv.roles.Get(p.UID).HPoint)
	}
	setFoes(strongTeam())
	c.sendExt("DUOBAO_PTO.C_FIGHT_DUOBAO", xym.Msg{"idx": 0})
	if got := c.until(t, "ETC_PTO.S_REWARD_MSG"); num(got["ETC_PTO.S_REWARD_MSG"], "type") != 3 || num(got["DUOBAO_PTO.S_SEND_BAODI_COUNT"], "baodi_count") != 7 {
		t.Fatalf("lose %v", got)
	}
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.HPoint = 1; return true })
	c.sendExt("DUOBAO_PTO.C_FIGHT_DUOBAO", xym.Msg{"idx": 0})
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "活力不足") {
		t.Fatalf("hpoint %v", m)
	}

	suits := len(p.Suits)
	c.sendExt("DUOBAO_PTO.C_USE_TICKET", xym.Msg{"type": 0, "level": 1, "count": 1})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	if l := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg); len(l) != 1 || num(l[0], "rtype") != rtEquip ||
		gamedata.Suits[num(l[0], "type")].Star != 1 || len(p.Suits) != suits+1 || p.Duobao.Tickets[1] != 0 {
		t.Fatalf("use ticket %v", got)
	}
	c.sendExt("DUOBAO_PTO.C_USE_TICKET", xym.Msg{"type": 0, "level": 1, "count": 1}) // 没有了
	c.expectExt("ETC_PTO.S_RET_MSG")
	c.expectExt("DUOBAO_PTO.S_OPEN_TICKET")
	p.mu.Lock()
	p.Duobao.Tickets[3], p.Duobao.Tickets[2] = 1, 1
	p.mu.Unlock()
	c.sendExt("DUOBAO_PTO.C_USE_TICKET", xym.Msg{"type": 2, "level": 2, "count": 1}) // 2 星不能换宝石
	c.expectExt("ETC_PTO.S_RET_MSG")
	c.expectExt("DUOBAO_PTO.S_OPEN_TICKET")
	c.sendExt("DUOBAO_PTO.C_USE_TICKET", xym.Msg{"type": 2, "level": 3, "count": 1})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	if p.itemCount(dbGem[3]) != 1 {
		t.Fatal("no gem")
	}
}

// TestDuobaoRules：凑满残页后重置星级，重置到 1 星多一张古老宝卷；保底满 1500 得五星宝卷。
func TestDuobaoRules(t *testing.T) {
	d := &DuobaoState{Class: 3, Count: 4, Baodi: 1499}
	rng := rand.New(rand.NewPCG(1, 2))
	d.win(rng)
	if d.Tickets[3] != 1 || d.Count != 0 || d.Tickets[5] != 1 || d.Baodi != 1 {
		t.Fatalf("%+v", d)
	}
	if d.Class == 1 && d.Tickets[0] != 1 || d.Class != 1 && d.Tickets[0] != 0 {
		t.Fatalf("ancient %+v", d)
	}
}

// TestTreeWoodPersist：种下的树（还没收获）也进摇钱树林，林子存成全服数据，重启后还在。
func TestTreeWoodPersist(t *testing.T) {
	c, p, srv := actWorld(t, "treewood")
	c.sendExt("TREE_PTO.C_PLANT_TREE", nil)
	c.expectExt("TREE_PTO.S_OPEN_PLANT")
	c.sendExt("TREE_PTO.C_TREE_RANK", nil)
	rank := c.until(t, "TREE_PTO.S_TREE_RANK")["TREE_PTO.S_TREE_RANK"]["list"].([]xym.Msg)
	found := false
	for _, r := range rank {
		found = found || num(r, "uid") == int(p.UID)
	}
	if !found {
		t.Fatal("planted tree not in the wood")
	}
	rows := map[int32]*treeRow{}
	loadGlobal(srv.players.st, treeUID, &rows, "tree wood")
	if r := rows[p.UID]; r == nil || !r.Planted {
		t.Fatalf("not persisted: %+v", rows)
	}
}
