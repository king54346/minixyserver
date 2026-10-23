package xymsrv

import (
	"math/rand/v2"
	"strings"
	"testing"

	"turnrpg/internal/xym"
)

// TestShilianEnemy：敌方属性公式对官方抓包 session-02（战报 team2 与 S_SEND_NPC_TEAM）。
func TestShilianEnemy(t *testing.T) {
	for _, c := range []struct {
		stage   int
		typ     int32
		att, hp int32
		speed   int
	}{
		{1, 11002, 22, 81, 2},
		{1, 31003, 23, 85, 2},
		{21, 14013, 1237, 3302, 7},
		{50, 45005, 4541, 27271, 9},
		{50, 35004, 5948, 25785, 7},
		{201, 25001, 44115, 117657, 9},
		{201, 25005, 30804, 184892, 7},
		{201, 12003, 10064, 43656, 4},
		{495, 35019, 100165, 267107, 9},
		{495, 14019, 66858, 267432, 8},
	} {
		f := slEnemy(c.stage, 1, c.typ)
		if f == nil || f.Att != c.att || f.HP != c.hp || f.Speed != c.speed {
			t.Errorf("stage %d pet %d: got %+v, want att %d hp %d speed %d", c.stage, c.typ, f, c.att, c.hp, c.speed)
		}
	}
	// 人数：抓包 fightNcount2
	for _, c := range []struct{ stage, n1, n2, n3 int }{{1, 5, 4, 3}, {6, 6, 5, 4}, {11, 7, 6, 5}, {16, 9, 8, 7}, {21, 9, 9, 8}, {26, 9, 9, 9}} {
		if a, b, d := slCount(c.stage, 1), slCount(c.stage, 2), slCount(c.stage, 3); a != c.n1 || b != c.n2 || d != c.n3 {
			t.Errorf("stage %d count %d/%d/%d", c.stage, a, b, d)
		}
	}
	if f := slEnemy(201, 1, 25001); len(f.Talents) != 3 || f.Talents[0] != 20059 || f.Talents[1] != 20067 || f.Talents[2] != 21156 || f.MagicLv != 10 {
		t.Errorf("talents %v magic lv %d", f.Talents, f.MagicLv)
	}
}

// slWorld 进游戏：出战卡升到 100 级；scale 是敌方属性倍率（百分比）。
func slWorld(t *testing.T, urs string, scale int64) (*client, *Player) {
	t.Helper()
	setSlScale(t, scale)
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	enterWorld(t, addr, urs).conn.Close()
	acc, _ := srv.accounts.Login(urs, "pw", "")
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
	return c, p
}

func setSlScale(t *testing.T, scale int64) {
	old := slScale
	slScale = func(int) int64 { return scale }
	t.Cleanup(func() { slScale = old })
}

func num(m xym.Msg, k string) int { return int(m[k].(int64)) }

// TestShilianRun：首页、开始（三个对手 5/4/3 人）、死战 5 关（每关 9 星、300×关数银两、财宝 +1）、
// 第 5 关结算财宝并选加成、看对手阵容、战斗难度得 3 星、输掉结束（汇总银两、今日最佳、次数 −1）、
// 排行、从上次关数接着打。
func TestShilianRun(t *testing.T) {
	c, p := slWorld(t, "slrun", 1)
	c.sendExt("ENDLESS_FIGHT_PTO.C_OPEN_FIGHT", nil)
	if m := c.expectExt("ENDLESS_FIGHT_PTO.S_SEND_STATE"); num(m, "state") != slStateMain || num(m, "times") != 3 || num(m, "last_stage") != 0 {
		t.Fatalf("state %v", m)
	}
	c.sendExt("ENDLESS_FIGHT_PTO.C_START_FIGHT", nil)
	m := c.expectExt("ENDLESS_FIGHT_PTO.S_OPEN_FIGHT")
	if num(m, "stage") != 0 || m["name"] != "第1关" || num(m, "fight1count2") != 5 || num(m, "fight2count2") != 4 ||
		num(m, "fight3count2") != 3 || num(m, "fight1reward") != 1 || num(m, "fight2reward") != 0 || num(m, "is_con") != 1 { // 连续死战对所有人开放
		t.Fatalf("open %v", m)
	}
	cash := 0
	for st := 1; st <= 5; st++ {
		c.sendExt("ENDLESS_FIGHT_PTO.C_DO_FIGHT", xym.Msg{"idx": 1})
		if r := c.expectExt("FIGHT_PTO.S_FIGHT_RECORD"); r["rid"] != "shilian-1-"+itoa(st) || num(r, "level2") != 2*st-1 {
			t.Fatalf("record %v", r["rid"])
		}
		a := c.expectExt("ETC_PTO.S_ATTR_UPDATE")["attr"].([]xym.Msg)[0]
		if cash != 0 && num(a, "val")-cash != 300*st {
			t.Fatalf("stage %d cash %v (was %d)", st, a, cash)
		}
		cash = num(a, "val")
		if st < 5 {
			m = c.expectExt("ENDLESS_FIGHT_PTO.S_OPEN_FIGHT")
			if num(m, "stage") != st || num(m, "allstar") != 9*st || num(m, "currstar") != 9*st || num(m, "baoxiang") != st {
				t.Fatalf("after stage %d: %v", st, m)
			}
		}
	}
	got := c.until(t, "ENDLESS_FIGHT_PTO.S_SEND_ADD")
	if l := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg); num(got["ETC_PTO.S_REWARD_MSG"], "type") != 1 || len(l) != 1 ||
		num(l[0], "type") != slBoxItem || num(l[0], "count") != 5 {
		t.Fatalf("settle %v", got["ETC_PTO.S_REWARD_MSG"])
	}
	add := got["ENDLESS_FIGHT_PTO.S_SEND_ADD"]
	if num(add, "stage") != 5 || num(add, "allstar") != 45 || num(add, "add1") != 6 || num(add, "add2") != 30 || num(add, "add3") != 60 {
		t.Fatalf("add %v", add)
	}
	typ := num(add, "type3")
	if p.itemCount(slBoxItem) != 5 {
		t.Fatalf("box items %d", p.itemCount(slBoxItem))
	}
	c.sendExt("ENDLESS_FIGHT_PTO.C_OPEN_FIGHT", nil) // 重新进入：还在选加成
	c.expectExt("ENDLESS_FIGHT_PTO.S_SEND_ADD")
	c.sendExt("ENDLESS_FIGHT_PTO.C_SELECT_ADDON", xym.Msg{"idx": 3})
	m = c.expectExt("ENDLESS_FIGHT_PTO.S_OPEN_FIGHT")
	key := map[int]string{1: "attadd", 2: "hpadd", 3: "magicadd", 4: "healadd"}[typ]
	if num(m, "currstar") != 15 || num(m, key) != 60 || m["name"] != "第6关" || num(m, "baoxiang") != 0 {
		t.Fatalf("after addon %v", m)
	}

	c.sendExt("ENDLESS_FIGHT_PTO.C_OPEN_NPC_TEAM", xym.Msg{"idx": 2})
	if l := c.expectExt("ENDLESS_FIGHT_PTO.S_SEND_NPC_TEAM")["list"].([]xym.Msg); len(l) != slCount(6, 2) || num(l[0], "pos") != 1 || num(l[0], "grade") != 11 {
		t.Fatalf("npc team %v", l)
	}
	c.sendExt("ENDLESS_FIGHT_PTO.C_DO_FIGHT", xym.Msg{"idx": 3}) // 战斗：1 倍星、1/3 银两、没有财宝
	c.expectExt("FIGHT_PTO.S_FIGHT_RECORD")
	if a := c.expectExt("ETC_PTO.S_ATTR_UPDATE")["attr"].([]xym.Msg)[0]; num(a, "val")-cash != 100*6 {
		t.Fatalf("idx 3 cash %v", a)
	}
	if m = c.expectExt("ENDLESS_FIGHT_PTO.S_OPEN_FIGHT"); num(m, "allstar") != 48 || num(m, "currstar") != 18 || num(m, "baoxiang") != 0 {
		t.Fatalf("idx 3 %v", m)
	}

	setSlScale(t, 1_000_000) // 必败
	c.sendExt("ENDLESS_FIGHT_PTO.C_DO_FIGHT", xym.Msg{"idx": 1})
	c.expectExt("FIGHT_PTO.S_FIGHT_RECORD")
	if r := c.expectExt("ETC_PTO.S_REWARD_MSG"); num(r, "type") != 13 || num(r, "cash") != 300*15+600 {
		t.Fatalf("end reward %v", r)
	}
	if sh := c.expectExt("ENDLESS_FIGHT_PTO.S_SEND_SHARE_INFO"); num(sh, "maxstage") != 6 || num(sh, "rankpos") != 1 {
		t.Fatalf("share %v", sh)
	}
	if m = c.expectExt("ENDLESS_FIGHT_PTO.S_SEND_STATE"); num(m, "times") != 2 || num(m, "last_stage") != 6 || num(m, "maxstar") != 48 || num(m, "maxstage") != 6 {
		t.Fatalf("state %v", m)
	}

	c.sendExt("ENDLESS_FIGHT_PTO.C_OPEN_RANK", xym.Msg{"type": slGroup(1)})
	if r := c.expectExt("ENDLESS_FIGHT_PTO.S_OPEN_RANK"); num(r, "userpos") != 1 || len(r["rank"].([]xym.Msg)) != 1 || num(r["rank"].([]xym.Msg)[0], "star") != 48 {
		t.Fatalf("rank %v", r)
	}
	c.sendExt("ENDLESS_FIGHT_PTO.C_OPEN_PLAYER_TEAM", xym.Msg{"type": slGroup(1), "pos": 1})
	if l := c.expectExt("ENDLESS_FIGHT_PTO.S_SEND_PLAYER_TEAM")["list"].([]xym.Msg); len(l) == 0 {
		t.Fatal("player team empty")
	}

	c.sendExt("ENDLESS_FIGHT_PTO.C_RESTART_LAST", nil)
	if m = c.expectExt("ENDLESS_FIGHT_PTO.S_OPEN_FIGHT"); num(m, "stage") != 6 || num(m, "allstar") != 48 || num(m, key) != 60 || m["name"] != "第7关" {
		t.Fatalf("restart %v", m)
	}
	if p.Shilian.Times != 1 || p.Shilian.Runs != 2 {
		t.Fatalf("times %d runs %d", p.Shilian.Times, p.Shilian.Runs)
	}
}

// TestShilianRules：次数用完、星数不够选加成、连续死战的开放条件与结算、魔王关只有一个对手。
func TestShilianRules(t *testing.T) {
	c, p := slWorld(t, "slrules", 1)
	p.mu.Lock()
	p.sl().Times = 0
	p.mu.Unlock()
	c.sendExt("ENDLESS_FIGHT_PTO.C_START_FIGHT", nil)
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "次数") {
		t.Fatalf("times %v", m)
	}
	c.expectExt("ENDLESS_FIGHT_PTO.S_SEND_STATE")

	p.mu.Lock()
	p.sl().Times = 1
	p.mu.Unlock()
	c.sendExt("ENDLESS_FIGHT_PTO.C_START_FIGHT", nil)
	c.expectExt("ENDLESS_FIGHT_PTO.S_OPEN_FIGHT")
	c.sendExt("ENDLESS_FIGHT_PTO.C_CONTINUOUS_FIGHT", nil)
	if a := c.expectExt("ETC_PTO.S_ATTR_UPDATE")["attr"].([]xym.Msg)[0]; a["name"] != "cash" {
		t.Fatalf("continuous attr %v", a)
	}
	got := c.until(t, "ENDLESS_FIGHT_PTO.S_SEND_ADD")
	if add := got["ENDLESS_FIGHT_PTO.S_SEND_ADD"]; num(add, "stage") != 5 || num(add, "allstar") != 45 {
		t.Fatalf("continuous add %v", add)
	}
	if l := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg); num(l[0], "count") != 5 {
		t.Fatalf("continuous settle %v", l)
	}
	c.sendExt("ENDLESS_FIGHT_PTO.C_SELECT_ADDON", xym.Msg{"idx": 1})
	if m := c.expectExt("ENDLESS_FIGHT_PTO.S_OPEN_FIGHT"); num(m, "currstar") != 42 || num(m, "is_con") != 1 {
		t.Fatalf("addon 1 %v", m)
	}

	c.sendExt("ENDLESS_FIGHT_PTO.C_CONTINUOUS_FIGHT", nil) // 6–10 关，第 10 关是魔王关
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	c.until(t, "ENDLESS_FIGHT_PTO.S_SEND_ADD")
	p.mu.Lock()
	p.sl().Run.CurStar = 10
	p.mu.Unlock()
	c.sendExt("ENDLESS_FIGHT_PTO.C_SELECT_ADDON", xym.Msg{"idx": 2}) // 要 15 星
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "星数不足") {
		t.Fatalf("need star %v", m)
	}
	c.expectExt("ENDLESS_FIGHT_PTO.S_SEND_ADD")
	c.sendExt("ENDLESS_FIGHT_PTO.C_SELECT_ADDON", xym.Msg{"idx": 1})
	c.expectExt("ENDLESS_FIGHT_PTO.S_OPEN_FIGHT")

	p.mu.Lock()
	p.sl().Run.Stage = 19
	p.sl().Run.nextFoes(rand.New(rand.NewPCG(1, 2)))
	p.mu.Unlock()
	c.sendExt("ENDLESS_FIGHT_PTO.C_OPEN_FIGHT", nil)
	if m := c.expectExt("ENDLESS_FIGHT_PTO.S_OPEN_FIGHT"); num(m, "fight2face") != 0 || num(m, "fight3face") != 0 || num(m, "fight1face") == 0 {
		t.Fatalf("boss %v", m)
	}
	c.sendExt("ENDLESS_FIGHT_PTO.C_DO_FIGHT", xym.Msg{"idx": 2}) // 魔王关没有第 2 个对手
	c.expectExt("ETC_PTO.S_RET_MSG")
}
