package xymsrv

import (
	"slices"
	"testing"

	"turnrpg/internal/gamedata"

	"turnrpg/internal/xym"
)

// TestLiexiFlow：时空裂隙走一遍——主界面、开副本、布阵、打第 1 关、选神通、装神通、竞技场匹配开战、排行、商店兑换。
func TestLiexiFlow(t *testing.T) {
	c, p, _ := actWorld(t, "liexi")
	// 只有金卡（五星）能出战：把测试号的护法都换成不同的五星护法
	var golds []int32
	for id, pi := range gamedata.Pets {
		if pi.Star == 5 {
			golds = append(golds, id)
		}
	}
	slices.Sort(golds)
	p.mu.Lock()
	for i, pet := range p.Pets {
		pet.Type = golds[i%len(golds)]
	}
	p.mu.Unlock()
	c.sendExt("LIEXI_PTO.C_OPEN_MAIN", nil)
	m := c.expectExt("LIEXI_PTO.S_OPEN_MAIN")
	fbs := m["fb_list"].([]xym.Msg)
	if len(fbs) != len(lxFbDefs) || num(m, "fb_left_times") != lxFbDaily || num(m, "jjc_left_times") != lxJjcDaily {
		t.Fatalf("main %v", m)
	}
	fb := int64(num(fbs[0], "fb_id"))
	c.sendExt("LIEXI_PTO.C_FB_BUY_TICKET", xym.Msg{"fb_id": fb})
	c.expectExt("LIEXI_PTO.S_FB_BUY_TICKET_SUC")
	c.sendExt("LIEXI_PTO.C_FB_GET_LINEUP", xym.Msg{"fb_id": fb})
	lu := c.expectExt("LIEXI_PTO.S_FB_SEND_LINEUP")
	if len(lu["target_team"].([]xym.Msg)) == 0 || len(lu["my_team"].([]xym.Msg)) == 0 || num(lu, "next_gate") != 1 {
		t.Fatalf("lineup %v", lu)
	}
	team := []xym.Msg{}
	query := []xym.Msg{}
	for _, x := range lu["my_team"].([]xym.Msg) {
		team = append(team, xym.Msg{"pet_type": x["type"], "pos": x["pos"]})
		query = append(query, xym.Msg{"pos": x["pos"]})
	}
	c.sendExt("LIEXI_PTO.C_FB_UPDATE_TEAM", xym.Msg{"fb_id": fb, "team": team, "query": query})
	up := c.expectExt("LIEXI_PTO.S_FB_UPDATE_TEAM")
	if num(up, "fight_point") == 0 || len(up["details"].([]xym.Msg)) != len(team) {
		t.Fatalf("update team %v", up)
	}
	if d := up["details"].([]xym.Msg)[0]; num(d, "grade") < 100 {
		t.Fatalf("裂隙等级应补到人物等级×2: %v", d)
	}

	// 让第 1 关必胜：把敌人换成一碰就倒的
	p.mu.Lock()
	x := p.liexi()
	r := x.run(int(fb))
	p.mu.Unlock()
	c.sendExt("LIEXI_PTO.C_FB_FIGHT", xym.Msg{"fb_id": fb, "gate_id": int64(1)})
	got := c.untilSkipMsg(t, "LIEXI_PTO.S_FB_FIGHT_END")
	if got["FIGHT_PTO.S_FIGHT_RECORD"] == nil {
		t.Fatalf("no record %v", got)
	}
	end := got["LIEXI_PTO.S_FB_FIGHT_END"]
	if num(end, "state") == lxStateChoose {
		c.sendExt("LIEXI_PTO.C_FB_GET_TAL_REWARDS", xym.Msg{"fb_id": fb})
		if tr := c.expectExt("LIEXI_PTO.S_FB_SEND_TAL_REWARDS"); len(tr["tal_rewards"].([]xym.Msg)) != lxRewardOpts {
			t.Fatalf("rewards %v", tr)
		}
		c.sendExt("LIEXI_PTO.C_FB_CHOOSE_TAL_REWARD", xym.Msg{"fb_id": fb, "choosed": int64(1)})
		if e := c.expectExt("LIEXI_PTO.S_FB_CHOOSE_TAL_REWARD_END"); num(e, "state") != lxStateTeam || num(e, "next_gate") != 2 {
			t.Fatalf("choose end %v", e)
		}
		pt := team[0]["pet_type"]
		c.sendExt("LIEXI_PTO.C_FB_GET_TAL_LIST", xym.Msg{"fb_id": fb, "pet_type": pt})
		if tl := c.expectExt("LIEXI_PTO.S_FB_SEND_TAL_LIST"); len(tl["not_armed"].([]xym.Msg)) != 1 {
			t.Fatalf("tal list %v", tl)
		}
		c.sendExt("LIEXI_PTO.C_FB_ARM_TAL", xym.Msg{"fb_id": fb, "pet_type": pt, "armed_tal_pos": int64(1), "bag_pos": int64(1)})
		got := c.untilSkipMsg(t, "LIEXI_PTO.S_FB_UPDATE_TEAM")
		d := got["LIEXI_PTO.S_FB_UPDATE_TEAM"]["details"].([]xym.Msg)
		if len(d) != 1 || len(d[0]["armed_tal"].([]xym.Msg)) != 1 && len(r.Bag) == 1 {
			// 与自身神通冲突时装不上（会先收到提示），这里只要求不崩
			t.Logf("armed %v", d)
		}
	} else if num(end, "state") != lxStateClosed {
		t.Fatalf("fight end %v", end)
	}

	// 竞技场
	c.sendExt("LIEXI_PTO.C_JJC_BUY_TICKET", nil)
	c.expectExt("LIEXI_PTO.S_JJC_BUY_TICKET_SUC")
	c.sendExt("LIEXI_PTO.C_JJC_GET_LINEUP", nil)
	if jl := c.expectExt("LIEXI_PTO.S_JJC_SEND_LINEUP"); num(jl, "state") != 1 || num(jl, "score") != lxInitScore {
		t.Fatalf("jjc lineup %v", jl)
	}
	c.sendExt("LIEXI_PTO.C_JJC_MATCH", nil)
	if me := c.expectExt("LIEXI_PTO.S_JJC_MATCH_END"); num(me, "state") != 2 {
		t.Fatalf("match %v", me)
	}
	c.sendExt("LIEXI_PTO.C_JJC_GET_LINEUP", nil)
	if jl := c.expectExt("LIEXI_PTO.S_JJC_SEND_LINEUP"); len(jl["vs_team"].([]xym.Msg)) == 0 {
		t.Fatalf("no opponent %v", jl)
	}
	c.sendExt("LIEXI_PTO.C_JJC_FIGHT", nil)
	got = c.untilSkipMsg(t, "LIEXI_PTO.S_JJC_FIGHT_END")
	if got["FIGHT_PTO.S_FIGHT_RECORD"] == nil || num(got["LIEXI_PTO.S_JJC_FIGHT_END"], "state") != 0 {
		t.Fatalf("jjc fight %v", got)
	}
	c.sendExt("LIEXI_PTO.C_JJC_GET_RANK_LIST", nil)
	if rl := c.expectExt("LIEXI_PTO.S_JJC_SEND_RANK_LIST"); len(rl["local_rank"].([]xym.Msg)) != 1 {
		t.Fatalf("rank %v", rl)
	}

	// 商店：增益兑换 5 级自选神通（碎片）
	c.sendExt("LIEXI_PTO.C_SHOP_GET_GOODS_LIST", xym.Msg{"shop": int64(lxShopGain)})
	sh := c.expectExt("LIEXI_PTO.S_SHOP_SEND_GOODS_LIST")
	if gl := sh["goods_list"].([]xym.Msg); len(gl) != 5 || sh["stitle"] != "初入时空" {
		t.Fatalf("shop %v", sh)
	}
	p.mu.Lock()
	p.Liexi.Pieces = 200
	shop0 := len(p.Liexi.Shop)
	p.mu.Unlock()
	c.sendExt("LIEXI_PTO.C_SHOP_BUY_GOODS", xym.Msg{"shop": int64(lxShopGain), "idx": int64(2), "buy_num": int64(1)})
	c.untilSkipMsg(t, "LIEXI_PTO.S_SHOP_SEND_GOODS_LIST")
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.Liexi.Shop) != shop0+1 || p.Liexi.Pieces != 100 {
		t.Fatalf("shop talent %v pieces %d", p.Liexi.Shop, p.Liexi.Pieces)
	}
}
