package xymsrv

import (
	"strings"
	"testing"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// fiveStars 给 p 加 n 张不同的五星护法（化形要金卡 ≥ 30），返回第一张的类型。
func fiveStars(p *Player, n int) int32 {
	var first int32
	for _, id := range gamedata.PetsByStar[5] {
		if n == 0 {
			break
		}
		if p.petByType(id) != nil {
			continue
		}
		p.gainPet(id)
		if first == 0 {
			first = id
		}
		n--
	}
	return first
}

// TestHuaxing：金卡不足不能化形；同名卡转元神 +40；化形扣元神和魂值得同名卡，第二次价格上涨；自动转化开关。
func TestHuaxing(t *testing.T) {
	c, p, srv := actWorld(t, "hx")
	p.mu.Lock()
	typ := fiveStars(p, 5)
	p.mu.Unlock()
	c.sendExt("CARD_SET_PTO.C_ON_HUAXING", xym.Msg{"type": typ})
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "尚未开启") {
		t.Fatalf("locked %v", m)
	}
	c.expectExt("CARD_SET_PTO.S_SEND_HUAXING_INFO")

	p.mu.Lock()
	fiveStars(p, 30)
	pet := p.petByType(typ)
	p.gainPet(typ) // 抽到同名卡 → 并入
	p.mu.Unlock()
	c.sendExt("CARD_SET_PTO.C_ON_TRANSFER_TO_YUANSHEN", xym.Msg{"type": typ})
	c.expectExt("PET_PTO.S_UPDATE_PET")
	if m := c.expectExt("CARD_SET_PTO.S_SEND_YUANSHEN_OK"); num(m, "ok") != 1 || p.Yuanshen != yuanshenPerCard || pet.TLAdd != 0 {
		t.Fatalf("transfer %v yuanshen %d tladd %d", m, p.Yuanshen, pet.TLAdd)
	}
	c.sendExt("CARD_SET_PTO.C_ON_TRANSFER_TO_YUANSHEN", xym.Msg{"type": typ}) // 没有同名卡了
	if m := c.expectExt("CARD_SET_PTO.S_SEND_YUANSHEN_OK"); num(m, "ok") != 0 {
		t.Fatalf("second transfer %v", m)
	}

	srv.roles.Update(p.UID, func(ro *Role) bool { ro.SoulExp = 10000; return true })
	p.mu.Lock()
	p.Yuanshen = 1000
	p.mu.Unlock()
	cost1, soul1 := huaxingCost(typ, 0)
	c.sendExt("CARD_SET_PTO.C_ON_HUAXING", xym.Msg{"type": typ})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	info := got["CARD_SET_PTO.S_SEND_HUAXING_INFO"]
	if p.Yuanshen != 1000-cost1 || srv.roles.Get(p.UID).SoulExp != 10000-soul1 || pet.TLAdd != 1 || num(info, "yuanshen") != 1000-cost1 {
		t.Fatalf("huaxing yuanshen %d soul %d tladd %d", p.Yuanshen, srv.roles.Get(p.UID).SoulExp, pet.TLAdd)
	}
	for _, x := range info["list"].([]xym.Msg) {
		if num(x, "type") == int(typ) {
			if c2, _ := huaxingCost(typ, 1); num(x, "cost") != c2 || num(x, "times") != 1 {
				t.Fatalf("next cost %v", x)
			}
		}
	}

	c.sendExt("CARD_SET_PTO.C_SET_AUTO_TRANSFORM_YUANSHEN", xym.Msg{"type": typ})
	c.sendExt("CARD_SET_PTO.C_OPEN_AUTO_TRANSFORM_YUANSHEN", nil)
	if l := c.expectExt("CARD_SET_PTO.S_OPEN_AUTO_HUAXING")["pet"].([]xym.Msg); len(l) != 1 || num(l[0], "type") != int(typ) {
		t.Fatalf("auto list %v", l)
	}
	p.mu.Lock()
	before := p.Yuanshen
	ok := p.autoYuanshen(typ)
	p.mu.Unlock()
	if !ok || p.Yuanshen != before+yuanshenPerCard {
		t.Fatalf("auto convert %v %d", ok, p.Yuanshen)
	}
}

// TestGateLoseAndReset：输了回 S_REWARD_MSG{type 3}、扣体力、记今日次数；次数用完弹重置框，重置扣 10 元宝后开打。
func TestGateLoseAndReset(t *testing.T) {
	c, p, srv := actWorld(t, "gl")
	old := enemyTeamFor
	enemyTeamFor = func(gamedata.GateMode) []*battle.Fighter { return strongTeam() }
	defer func() { enemyTeamFor = old }()
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.TPoint, ro.Gold = 60, 100; return true })
	fight := xym.Msg{"gid": 10101, "type": 0, "pos1": 1, "pos2": 2, "pos3": 3, "pos4": 4, "pos5": 5}
	c.sendExt("TASK_PTO.C_START_FIGHT", fight)
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	if got["ETC_PTO.S_REWARD_START"] != nil { // 失败不能包在 REWARD_START/END 里（客户端会显示胜利）
		t.Fatal("loss wrapped in S_REWARD_START")
	}
	if r := got["ETC_PTO.S_REWARD_MSG"]; r == nil || num(r, "type") != boxFightLose {
		t.Fatalf("lose reward %v", r)
	}
	if srv.roles.Get(p.UID).TPoint >= 60 || p.Prog[0].today(10101) != 1 {
		t.Fatalf("tpoint %d times %d", srv.roles.Get(p.UID).TPoint, p.Prog[0].today(10101))
	}

	p.mu.Lock()
	p.Prog[0].Daily[10101] = gamedata.Gates[10101].Mode[0].Limit
	p.mu.Unlock()
	c.sendExt("TASK_PTO.C_START_FIGHT", fight)
	c.expectExt("ETC_PTO.S_RET_MSG")
	ask := c.expectExt("ETC_PTO.S_ASK_BOX")
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": ask["session"], "opt": 0})
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	c.expectExt("FIGHT_PTO.S_FIGHT_RECORD")
	if g := srv.roles.Get(p.UID).Gold; g != 100-gateResetGold {
		t.Fatalf("reset gold %d", g)
	}
}

// TestDangyaoNewRunClearsDead：新一轮荡妖清掉上一轮的阵亡并通知客户端。
func TestDangyaoNewRunClearsDead(t *testing.T) {
	c, p, _ := dyWorld(t, "dyclear", 1)
	p.mu.Lock()
	p.dy().Dead = []int{1, 2}
	p.mu.Unlock()
	c.sendExt("DANGYAO_PTO.C_REFRESH_MAP", xym.Msg{"stage": 108})
	got := c.until(t, "DANGYAO_PTO.S_DIE_LIST")
	if l := got["DANGYAO_PTO.S_DIE_LIST"]["list"].([]xym.Msg); len(l) != 0 || len(p.dy().Dead) != 0 {
		t.Fatalf("dead not cleared %v", l)
	}
}
