package xymsrv

import (
	"strings"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// dyWorld 进游戏并准备好荡妖：精英闯关打到第 8 张图（特殊妖窟 108 开放）、元宝 / 体力足够。
func dyWorld(t *testing.T, urs string, param int) (*client, *Player, *Server) {
	t.Helper()
	old := dyParamFor
	dyParamFor = func(int, int, int) int { return param }
	t.Cleanup(func() { dyParamFor = old })
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	enterWorld(t, addr, urs).conn.Close()
	acc, _ := srv.accounts.Login(urs, "pw", "")
	srv.roles.Update(acc.UID, func(ro *Role) bool { ro.Gold, ro.TPoint = 200, 200; return true })
	p, _ := srv.players.Get(acc.UID)
	p.mu.Lock()
	p.Prog[1].Last = 801
	if param < 100 { // 必胜用例：出战卡升到 100 级
		for _, pet := range p.Pets {
			pet.Grade = 100
		}
	}
	p.mu.Unlock()
	c := dial(t, addr)
	c.sendRaw(xym.CLogin, loginBody(urs, "pw"))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
	c.collect()
	return c, p, srv
}

func gridOf(list []xym.Msg, typ int) int {
	for _, g := range list {
		if g["type"] == int64(typ) {
			return int(g["grid"].(int64))
		}
	}
	return -1
}

// TestDangyaoRun：打开（没有进行中的一轮 → S_OPEN_FAIL）、重置特殊妖窟（扣 50 元宝、妖气 +25）、
// 每层一键荡妖（扣 15 体力）+ 打头目 + 下楼，最后一层头目打完结算功德。回包顺序照抓包 session-01。
func TestDangyaoRun(t *testing.T) {
	c, p, _ := dyWorld(t, "dyrun", 1) // param 1：怪物极弱，必胜
	c.sendExt("DANGYAO_PTO.C_OPEN_MAP", xym.Msg{"mapid": 801})
	if m := c.expectExt("DANGYAO_PTO.S_OPEN_FAIL"); m["stage"] != int64(8) {
		t.Fatalf("open fail %v", m)
	}
	c.sendExt("DANGYAO_PTO.C_REFRESH_MAP", xym.Msg{"stage": 109}) // 精英只打到第 8 图
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "没有开放") {
		t.Fatalf("locked %v", m)
	}

	c.sendExt("DANGYAO_PTO.C_REFRESH_MAP", xym.Msg{"stage": 108})
	if m := c.expectExt("ETC_PTO.S_ATTR_UPDATE"); m["attr"].([]xym.Msg)[0]["val"] != int64(150) {
		t.Fatalf("gold %v", m)
	}
	if m := c.expectExt("DANGYAO_PTO.S_OPEN_DANGYAO"); m["mapid"] != int64(10801) || len(m["list"].([]xym.Msg)) != 1 {
		t.Fatalf("open %v", m)
	}
	c.expectExt("DANGYAO_PTO.S_SEND_GONGDE")
	if m := c.expectExt("DANGYAO_PTO.S_SEND_BAODI_RATIO"); m["base_ratio"] != int64(25) {
		t.Fatalf("ratio %v", m)
	}

	// 怪物法术附带的混乱会让我方互相攻击，偶尔有护法阵亡（多出一条 S_DIE_LIST），流程里跳过它
	total := 0
	floors := dyFloors(108)
	for floor := 1; floor <= floors; floor++ {
		mapid := int64(dyMapID(108, floor))
		c.sendExt("DANGYAO_PTO.C_ONE_KEY_DANGYAO", xym.Msg{"mapid": mapid})
		if m := c.expectExtSkip("ETC_PTO.S_ATTR_UPDATE", "DANGYAO_PTO.S_DIE_LIST"); m["attr"].([]xym.Msg)[0]["name"] != "tpoint" {
			t.Fatalf("one key cost %v", m)
		}
		ok := c.expectExtSkip("DANGYAO_PTO.S_SEND_ONE_KEY_DANGYAO", "DANGYAO_PTO.S_DIE_LIST")
		list := ok["list"].([]xym.Msg)
		if len(list) != dyGrids || gridOf(list, dyMonster) >= 0 || gridOf(list, dyBox) >= 0 {
			t.Fatalf("one key list %v", list)
		}
		ling := int(c.expectExtSkip("DANGYAO_PTO.S_SEND_REWARD", "DANGYAO_PTO.S_DIE_LIST")["ling"].(int64))
		if gained := ling - total; gained%16 != 0 || gained < 3*16+2*32 {
			t.Fatalf("floor %d one key ling %d", floor, gained)
		}
		total = ling

		c.sendExt("DANGYAO_PTO.C_CLICK_GRID", xym.Msg{"mapid": mapid, "grid": gridOf(list, dyLeader)})
		if m := c.expectExtSkip("FIGHT_PTO.S_FIGHT_RECORD", "DANGYAO_PTO.S_DIE_LIST"); !strings.HasPrefix(m["rid"].(string), "dangyao-") {
			t.Fatalf("rid %v", m["rid"])
		}
		if m := c.expectExtSkip("ETC_PTO.S_REWARD_MSG", "DANGYAO_PTO.S_DIE_LIST"); m["type"] != int64(2) || m["ling"] != int64(48) {
			t.Fatalf("fight reward %v", m)
		}
		c.expectExtSkip("DANGYAO_PTO.S_OPEN_GRID", "DANGYAO_PTO.S_DIE_LIST")
		total = int(c.expectExtSkip("DANGYAO_PTO.S_SEND_REWARD", "DANGYAO_PTO.S_DIE_LIST")["ling"].(int64))
		if floor == floors {
			if m := c.expectExtSkip("DANGYAO_PTO.S_DANGYAO_END", "DANGYAO_PTO.S_DIE_LIST"); m["state"] != int64(dyStateSuccess) || m["stage"] != int64(108) {
				t.Fatalf("end %v", m)
			}
			if m := c.expectExtSkip("DANGYAO_PTO.S_SEND_GONGDE", "DANGYAO_PTO.S_DIE_LIST"); m["gongde"] != int64(total) {
				t.Fatalf("gongde %v, want %d", m, total)
			}
			if m := c.expectExtSkip("ETC_PTO.S_REWARD_MSG", "DANGYAO_PTO.S_DIE_LIST"); m["type"] != int64(4) || m["ling"] != int64(total) {
				t.Fatalf("end reward %v", m)
			}
			break
		}
		c.sendExt("DANGYAO_PTO.C_CLICK_GRID", xym.Msg{"mapid": mapid, "grid": gridOf(list, dyDown)})
		if m := c.expectExtSkip("DANGYAO_PTO.S_OPEN_DANGYAO", "DANGYAO_PTO.S_DIE_LIST"); m["mapid"] != mapid+1 {
			t.Fatalf("down %v", m)
		}
		c.expectExtSkip("ETC_PTO.S_RET_MSG", "DANGYAO_PTO.S_DIE_LIST")
	}
	c.sendExt("DANGYAO_PTO.C_OPEN_MAP", xym.Msg{"mapid": 10805})
	c.expectExt("DANGYAO_PTO.S_OPEN_FAIL")
	if p.Dangyao.Run != nil {
		t.Fatal("run not cleared")
	}
}

// TestDangyaoLose：打不过头目——阵亡护法进 S_DIE_LIST，全军覆没回 S_DANGYAO_END{state 1}；
// 全员复活先用 3 次免费，其余每名 2 元宝；之后 C_OPEN_MAP 回到当前层。
func TestDangyaoLose(t *testing.T) {
	c, p, srv := dyWorld(t, "dylose", 20000) // param 20000：怪物极强，必败
	c.sendExt("DANGYAO_PTO.C_REFRESH_MAP", xym.Msg{"stage": 108})
	c.until(t, "DANGYAO_PTO.S_SEND_BAODI_RATIO")
	c.sendExt("DANGYAO_PTO.C_ONE_KEY_DANGYAO", xym.Msg{"mapid": 10801})
	list := c.until(t, "DANGYAO_PTO.S_SEND_ONE_KEY_DANGYAO")["DANGYAO_PTO.S_SEND_ONE_KEY_DANGYAO"]["list"].([]xym.Msg)
	c.expectExt("DANGYAO_PTO.S_SEND_REWARD")
	c.sendExt("DANGYAO_PTO.C_CLICK_GRID", xym.Msg{"mapid": 10801, "grid": gridOf(list, dyLeader)})
	c.expectExt("FIGHT_PTO.S_FIGHT_RECORD")
	if m := c.expectExt("ETC_PTO.S_REWARD_MSG"); m["ling"] != int64(0) || m["type"] != int64(boxFightLose) { // 输了是 FIGHT_LOSE（之前误回胜利）
		t.Fatalf("lose reward %v", m)
	}
	dead := c.expectExt("DANGYAO_PTO.S_DIE_LIST")["list"].([]xym.Msg)
	if len(dead) != len(starterPets) {
		t.Fatalf("dead %v", dead)
	}
	if m := c.expectExt("DANGYAO_PTO.S_DANGYAO_END"); m["state"] != int64(dyStateFail) {
		t.Fatalf("end %v", m)
	}
	gold := srv.roles.Get(p.UID).Gold
	c.sendExt("DANGYAO_PTO.C_RELIVE_ALL", nil)
	if m := c.expectExt("ETC_PTO.S_ATTR_UPDATE"); m["attr"].([]xym.Msg)[0]["val"] != int64(gold)-int64((len(dead)-dyFreeRelive)*dyReliveGold) {
		t.Fatalf("relive cost %v (gold %d, dead %d)", m, gold, len(dead))
	}
	if m := c.expectExt("DANGYAO_PTO.S_RELIVE_SUC"); m["free"] != int64(0) || len(m["list"].([]xym.Msg)) != 0 {
		t.Fatalf("relive %v", m)
	}
	c.expectExt("ETC_PTO.S_RET_MSG")
	c.sendExt("DANGYAO_PTO.C_OPEN_MAP", xym.Msg{"mapid": 801})
	if m := c.expectExt("DANGYAO_PTO.S_OPEN_DANGYAO"); m["mapid"] != int64(10801) {
		t.Fatalf("reopen %v", m)
	}
	// 携宝潜逃：没有遁地符，扣 50 元宝，带着一键荡妖的功结算
	c.until(t, "DANGYAO_PTO.S_SEND_REWARD")
	ling := p.Dangyao.Run.Ling
	c.sendExt("DANGYAO_PTO.C_END_MAP", nil)
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	if m := c.expectExt("DANGYAO_PTO.S_DANGYAO_END"); m["state"] != int64(dyStateSuccess) {
		t.Fatalf("escape %v", m)
	}
	if m := c.expectExt("DANGYAO_PTO.S_SEND_GONGDE"); m["gongde"] != int64(ling) {
		t.Fatalf("escape gongde %v want %d", m, ling)
	}
}

// TestDangyaoShop：功德兑换——成功（扣功德、累计消耗、限购减一、背包到账）、超出限购、功德不足、本周固定魂、魂值丹。
func TestDangyaoShop(t *testing.T) {
	c, p, srv := dyWorld(t, "dyshop", 1)
	p.mu.Lock()
	d := p.dy()
	d.Gongde = 500
	d.Shop = map[int][]*dyShopSlot{1: {{Pos: 1, Left: 2}}, 2: {{Pos: 21, Left: 2}}, 3: {{Pos: 32, Left: 2}}}
	p.mu.Unlock()

	c.sendExt("DANGYAO_PTO.C_OPEN_SHOP", xym.Msg{"type": 2})
	if m := c.expectExt("DANGYAO_PTO.S_OPEN_SHOP"); m["gongde"] != int64(500) || len(m["list"].([]xym.Msg)) != 1 {
		t.Fatalf("shop %v", m)
	}
	c.sendExt("DANGYAO_PTO.C_BUY_ITEM", xym.Msg{"pos": 21, "type": 2, "count": 1}) // 高级法术册 200
	got := c.untilSkipMsg(t, "ETC_PTO.S_REWARD_MSG")
	if got["DANGYAO_PTO.S_SEND_GONGDE"]["gongde"] != int64(300) {
		t.Fatalf("gongde %v", got["DANGYAO_PTO.S_SEND_GONGDE"])
	}
	shop := got["DANGYAO_PTO.S_OPEN_SHOP"]
	if shop["max_gongde"] != int64(200) || shop["list"].([]xym.Msg)[0]["left"] != int64(1) {
		t.Fatalf("shop after buy %v", shop)
	}
	if got["ITEM_PTO.S_UPDATE_BOOK"] == nil {
		t.Fatal("no book")
	}
	if l := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg); len(l) != 1 || l[0]["rtype"] != int64(rtMagic) {
		t.Fatalf("reward %v", got["ETC_PTO.S_REWARD_MSG"])
	}

	c.sendExt("DANGYAO_PTO.C_BUY_ITEM", xym.Msg{"pos": 21, "type": 2, "count": 2}) // 只剩 1 次
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "限购") {
		t.Fatalf("limit %v", m)
	}
	c.expectExt("DANGYAO_PTO.S_OPEN_SHOP")
	c.sendExt("DANGYAO_PTO.C_BUY_ITEM", xym.Msg{"pos": 21, "type": 2, "count": 1}) // 功德 300 ≥ 200，还能买
	c.untilSkipMsg(t, "ETC_PTO.S_REWARD_MSG")
	c.sendExt("DANGYAO_PTO.C_BUY_ITEM", xym.Msg{"pos": 32, "type": 3, "count": 1}) // 小魂值丹 125 > 100
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "功德不足") {
		t.Fatalf("gongde short %v", m)
	}
	c.expectExt("DANGYAO_PTO.S_OPEN_SHOP")

	p.mu.Lock()
	p.Dangyao.Gongde = 1000
	p.mu.Unlock()
	soul := srv.roles.Get(p.UID).SoulExp
	c.sendExt("DANGYAO_PTO.C_BUY_ITEM", xym.Msg{"pos": 32, "type": 3, "count": 2}) // 2 × 50 魂值
	got = c.untilSkipMsg(t, "ETC_PTO.S_REWARD_MSG")
	if got["ETC_PTO.S_REWARD_MSG"]["soul"] != int64(100) || srv.roles.Get(p.UID).SoulExp != soul+100 {
		t.Fatalf("soul %v", got["ETC_PTO.S_REWARD_MSG"])
	}
	c.sendExt("DANGYAO_PTO.C_BUY_ITEM", xym.Msg{"pos": 1, "type": 1, "count": 1}) // 本周固定 5 星魂
	got = c.untilSkipMsg(t, "ETC_PTO.S_REWARD_MSG")
	if l := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg); len(l) != 1 || l[0]["rtype"] != int64(rtSoul) {
		t.Fatalf("weekly soul %v", got["ETC_PTO.S_REWARD_MSG"])
	}
}

// TestDangyaoRules：功德称号等级（累计消耗 500 / 3000 / 8000 / 15000）、上限、每层格子构成、商店按等级上架。
func TestDangyaoRules(t *testing.T) {
	d := &DangyaoState{}
	for spent, want := range map[int64][2]int{0: {1, 100}, 499: {1, 100}, 500: {2, 200}, 4905: {3, 300}, 8000: {4, 400}, 20000: {5, 500}} {
		d.Spent = spent
		if lv, mx := d.gongdeLevel(), d.gongdeMax(); lv != want[0] || mx != want[1] {
			t.Errorf("spent %d: level %d max %d, want %v", spent, lv, mx, want)
		}
	}
	rng := (&Session{role: &Role{UID: 1}}).dyRng()
	for floor := 1; floor <= 5; floor++ {
		f := newFloor(floor, 5, floor == 5, rng)
		n := map[int]int{}
		open := 0
		for _, g := range f.Grids {
			n[g.Type]++
			if g.Open {
				open++
			}
		}
		down := 1
		if floor == 5 {
			down = 0
		}
		if open != 1 || n[dyDown] != down || n[dyBox] != 2 || n[dyMonster] < 3 || n[dyMonster] > 5 {
			t.Errorf("floor %d: %v open %d", floor, n, open)
		}
		if floor == 5 && n[dyBoss] != 1 || floor < 5 && n[dyLeader] != 1 {
			t.Errorf("floor %d leader/boss %v", floor, n)
		}
	}
	d.Spent = 0
	d.rollShop(false, rng)
	for _, l := range d.Shop {
		for _, sl := range l {
			if dyShopItem(sl.Pos).Need > 1 {
				t.Errorf("level-1 shop has %v", dyShopItem(sl.Pos))
			}
		}
	}
	if len(gamedata.Dangyao.Shop) < 30 || len(gamedata.Dangyao.Formations) != 73 {
		t.Fatalf("config shop %d formations %d", len(gamedata.Dangyao.Shop), len(gamedata.Dangyao.Formations))
	}
}

// TestDangyaoOpenGrid：逐格翻开（每格 1 体力，已翻开的不再扣），点宝箱得 32 功并变空地。
func TestDangyaoOpenGrid(t *testing.T) {
	c, p, srv := dyWorld(t, "dygrid", 1)
	c.sendExt("DANGYAO_PTO.C_REFRESH_MAP", xym.Msg{"stage": 108})
	c.until(t, "DANGYAO_PTO.S_SEND_BAODI_RATIO")
	tp := srv.roles.Get(p.UID).TPoint
	box := -1
	for i := range dyGrids {
		c.sendExt("DANGYAO_PTO.C_OPEN_GRID", xym.Msg{"mapid": 10801, "grid": i})
		got := c.until(t, "DANGYAO_PTO.S_OPEN_GRID")
		if got["DANGYAO_PTO.S_OPEN_GRID"]["type"] == int64(dyBox) && box < 0 {
			box = i
		}
	}
	if got := srv.roles.Get(p.UID).TPoint; got != tp-(dyGrids-1) {
		t.Fatalf("tpoint %d → %d, want −15", tp, got)
	}
	c.sendExt("DANGYAO_PTO.C_CLICK_GRID", xym.Msg{"mapid": 10801, "grid": box})
	if m := c.expectExt("DANGYAO_PTO.S_OPEN_GRID"); m["type"] != int64(dyBlank) {
		t.Fatalf("box grid %v", m)
	}
	if m := c.expectExt("DANGYAO_PTO.S_SEND_REWARD"); m["ling"] != int64(32) {
		t.Fatalf("box ling %v", m)
	}
	c.expectExt("ETC_PTO.S_RET_MSG")
}
