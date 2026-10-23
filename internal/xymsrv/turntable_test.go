package xymsrv

import (
	"math/rand/v2"
	"slices"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestGemBagAndSilver：五级宝石袋开出六种五级宝石之一；银块值 50000 银两；幻境专属不进随机池。
func TestGemBagAndSilver(t *testing.T) {
	if it := gamedata.Items[10452]; it == nil || gamedata.Rewards[it.Etc] == nil {
		t.Fatal("五级宝石袋 missing")
	}
	p := newPlayer(1)
	rng := rand.New(rand.NewPCG(1, 2))
	d := &bagDelta{}
	for range 20 {
		p.giveCode("GPXBS5", 1, rng, d)
	}
	if len(d.gains) != 20 {
		t.Fatalf("gains %v", d.gains)
	}
	for _, g := range d.gains {
		if g.typ%10 != 5 || !slices.Contains(gemBase, g.typ-5) {
			t.Fatalf("not a level-5 gem: %d", g.typ)
		}
	}
	if c := gamedata.Items[10257].Cash; c != 50000 {
		t.Fatalf("银块 cash %d", c)
	}
	for star, l := range gamedata.BooksByStar {
		for _, id := range l {
			if gamedata.HuanjingOnly[int(id)] {
				t.Fatalf("exclusive magic %d in %d-star pool", id, star)
			}
		}
	}
	for _, l := range gamedata.SuitsByStar {
		if slices.Contains(l, 1016) {
			t.Fatal("射日弓 in random pool")
		}
	}
}

// TestTurntable：打开（每日送灵力）、探宝扣 30 得物品、灵力不足弹购买、买灵力、重置、宝藏幻境带走一件。
func TestTurntable(t *testing.T) {
	c, p, srv := actWorld(t, "tt")
	c.sendExt("TURNTABLE_PTO.C_OPEN_TURNTABLE", nil)
	m := c.expectExt("TURNTABLE_PTO.S_OPEN_TURNTABLE")
	l := m["list"].([]xym.Msg)
	if len(l) != ttSlots || l[0]["name"] != "" {
		t.Fatalf("table %v", l)
	}
	ex := 0
	for _, x := range l[1:] {
		if x["name"] == "" {
			t.Fatalf("empty cell %v", x)
		}
		if gamedata.HuanjingOnly[int(num(x, "id"))] {
			ex++
		}
	}
	if ex != 2 {
		t.Fatalf("exclusives %d", ex)
	}
	if sc := c.expectExt("TURNTABLE_PTO.S_SEND_SCORE"); num(sc, "score") != ttDailyScore || num(sc, "base_guarantee") != ttBaseGuar {
		t.Fatalf("score %v", sc)
	}

	c.sendExt("TURNTABLE_PTO.C_START_TURN", nil)
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	if pos := num(got["TURNTABLE_PTO.S_MOVE"], "pos"); pos < 1 || pos >= ttSlots {
		t.Fatalf("move %v", got["TURNTABLE_PTO.S_MOVE"])
	}
	if sc := got["TURNTABLE_PTO.S_SEND_SCORE"]; num(sc, "score") != ttDailyScore-ttCost || num(sc, "cur_guarantee") != 1 {
		t.Fatalf("after turn %v", sc)
	}
	if len(got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg)) != 1 {
		t.Fatalf("reward %v", got["ETC_PTO.S_REWARD_MSG"])
	}

	p.mu.Lock()
	p.Turntable.Score = 0
	p.mu.Unlock()
	c.sendExt("TURNTABLE_PTO.C_START_TURN", nil)
	c.expectExt("TURNTABLE_PTO.S_OPEN_BUY_SCORE")
	c.expectExt("TURNTABLE_PTO.S_MOVE")

	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 1000; return true })
	c.sendExt("TURNTABLE_PTO.C_BUY_SCORE", xym.Msg{"score": 500})
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	if sc := c.expectExt("TURNTABLE_PTO.S_SEND_SCORE"); num(sc, "score") != 500 || srv.roles.Get(p.UID).Gold != 500 {
		t.Fatalf("buy %v", sc)
	}

	for _, want := range []int{500, 400} { // 每天第一次重置免费，之后扣 100
		c.sendExt("TURNTABLE_PTO.C_REFERSH_TURNTABLE", nil)
		c.expectExt("TURNTABLE_PTO.S_OPEN_TURNTABLE")
		if sc := c.expectExt("TURNTABLE_PTO.S_SEND_SCORE"); num(sc, "score") != want || num(sc, "free_refresh") != 0 {
			t.Fatalf("reset %v want %d", sc, want)
		}
	}

	// 宝藏幻境：第一次免费，第二次 100，之前那件作废；携宝离开得到最后一件、次数清零
	p.mu.Lock()
	p.Turntable.Guar = ttBaseGuar
	p.mu.Unlock()
	c.sendExt("TURNTABLE_PTO.C_OPEN_GUARANTEE_TABLE", nil)
	g := c.expectExt("TURNTABLE_PTO.S_OPEN_GUARANTEE_TABLE")
	if num(g, "cost") != 0 || len(g["list"].([]xym.Msg)) != ttSlots {
		t.Fatalf("guarantee %v", g)
	}
	c.expectExt("TURNTABLE_PTO.S_SEND_SCORE")
	c.sendExt("TURNTABLE_PTO.C_START_TURN", nil)
	first := num(c.expectExt("TURNTABLE_PTO.S_MOVE"), "pos")
	c.expectExt("TURNTABLE_PTO.S_OPEN_GUARANTEE_TABLE")
	c.expectExt("TURNTABLE_PTO.S_SEND_SCORE")
	c.sendExt("TURNTABLE_PTO.C_START_TURN", nil)
	second := num(c.expectExt("TURNTABLE_PTO.S_MOVE"), "pos")
	g = c.expectExt("TURNTABLE_PTO.S_OPEN_GUARANTEE_TABLE")
	if second == first || num(g["list"].([]xym.Msg)[first], "isvalid") != 0 {
		t.Fatalf("first %d second %d %v", first, second, g)
	}
	if sc := c.expectExt("TURNTABLE_PTO.S_SEND_SCORE"); num(sc, "score") != 300 {
		t.Fatalf("guarantee cost %v", sc)
	}
	want := g["list"].([]xym.Msg)[second]
	c.sendExt("TURNTABLE_PTO.C_GET_GUARANTEE_REWARD", nil)
	got = c.until(t, "ETC_PTO.S_REWARD_MSG")
	if sc := got["TURNTABLE_PTO.S_SEND_SCORE"]; num(sc, "cur_guarantee") != 0 {
		t.Fatalf("guarantee not reset %v", sc)
	}
	r := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg)
	if len(r) != 1 || num(r[0], "type") != num(want, "icon") {
		t.Fatalf("took %v want %v", r, want)
	}
}
