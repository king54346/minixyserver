package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// strongPlayer 把 p 换成完美配置（打得过野外守卫）。
func strongPlayer(srv *Server, p *Player) {
	p.mu.Lock()
	p.role = srv.roles.Get(p.UID)
	p.fillPerfect()
	p.mu.Unlock()
}

// TestDongfuGank：争夺野外洞府，三个界门援军打光后进攻方胜、夺得灵气；第二次争夺要花洞府元宝。
func TestDongfuGank(t *testing.T) {
	srv, a, _, pa, _ := dfWorld(t)
	strongPlayer(srv, pa)
	a.sendExt("DONGFU_PTO.C_GET_GANK_LIST", nil)
	l := a.expectExt("DONGFU_PTO.S_SEND_GANK_LIST")
	if len(l["list"].([]xym.Msg)) < gankNPCCount || num(l, "gank_times") != 0 {
		t.Fatalf("gank list %v", l)
	}
	a.sendExt("DONGFU_PTO.C_GANK_DONGFU", xym.Msg{"dongfu_id": gankNPCBase})
	info := a.expectExt("DONGFU_PTO.S_SEND_GANK_FIGHT_INFO")
	if num(info, "status") != 1 || num(info, "side") != 1 || len(info["guard"].([]xym.Msg)) != 3 {
		t.Fatalf("gank info %v", info)
	}
	df := dfOf(srv, pa.UID)
	lingqi := df.Lingqi
	won := false
	for range 12 {
		for _, gt := range dfGuardTypes {
			a.sendExt("DONGFU_PTO.C_START_GANK_FIGHT", xym.Msg{"type": gt})
			got := a.untilSkipMsg(t, "ETC_PTO.S_REWARD_MSG")
			if g := got["DONGFU_PTO.S_SEND_GANK_FIGHT_INFO"]; g != nil && num(g, "status") == 2 {
				won = true
			}
			if won {
				break
			}
			// 阵亡就复活
			a.sendExt("DONGFU_PTO.C_GANK_RELIVE_ALL_FIGHTER", nil)
			a.untilSkipMsg(t, "DONGFU_PTO.S_SEND_GANK_FIGHT_INFO")
		}
		if won {
			break
		}
	}
	if !won || df.Lingqi <= lingqi {
		t.Fatalf("gank won %v lingqi %d -> %d", won, lingqi, df.Lingqi)
	}
	a.sendExt("DONGFU_PTO.C_GET_DONGFU_JOB_INFO", nil)
	if j := a.expectExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO"); num(j, "gank_status") != 2 || num(j, "gank_side") != 1 {
		t.Fatalf("job %v", j)
	}
	gold := df.Gold
	a.sendExt("DONGFU_PTO.C_GANK_DONGFU", xym.Msg{"dongfu_id": gankNPCBase + 1})
	a.expectExt("DONGFU_PTO.S_SEND_GANK_FIGHT_INFO")
	if df.Gold != gold-100 {
		t.Fatalf("second gank cost: %d -> %d", gold, df.Gold)
	}
}

// TestDongfuCave：迁徙到洞天 1，打开灵穴；抢 5 号灵穴（目标灵穴）1 号守位成功，洞府积分 +3。
func TestDongfuCave(t *testing.T) {
	srv, a, _, pa, _ := dfWorld(t)
	strongPlayer(srv, pa)
	df := dfOf(srv, pa.UID)
	srv.dongfu.do(func() bool { df.Lv = 2; return true })
	a.sendExt("DONGFU_PTO.C_GET_AVAIL_STATION_LIST", nil)
	if l := a.expectExt("DONGFU_PTO.S_SEND_AVAIL_STATION_LIST")["list"].([]xym.Msg); len(l) != 8 {
		t.Fatalf("stations %v", l)
	}
	a.sendExt("DONGFU_PTO.C_MOVE_DONGFU", xym.Msg{"station_id": 1})
	if info := a.expectExt("DONGFU_PTO.S_SEND_DONGFU_INFO"); num(info, "station_id") != 1 {
		t.Fatalf("move %v", info)
	}
	a.sendExt("DONGFU_PTO.C_GET_CAVE_GAME_INFO", nil)
	a.expectExt("DONGFU_PTO.S_SEND_CAVE_GAME_OPEN")
	a.sendExt("DONGFU_PTO.C_GET_CAVE_GAME_STATION_INFO", nil)
	if g := a.expectExt("DONGFU_PTO.S_SEND_CAVE_GAME_STATION_INFO")["gate_list"].([]xym.Msg); len(g) != caveGates {
		t.Fatalf("station %v", g)
	}
	won := false
	for range 5 {
		a.sendExt("DONGFU_PTO.C_START_CAVE_FIGHT", xym.Msg{"gate": 5, "gate_pos": 1})
		got := a.untilSkipMsg(t, "ETC_PTO.S_REWARD_MSG")
		if num(got["ETC_PTO.S_REWARD_MSG"], "type") == 2 {
			won = true
			break
		}
	}
	if !won {
		t.Fatal("never won cave fight")
	}
	srv.dongfu.do(func() bool {
		c := srv.dongfu.cave(1)
		if c.Gates[5][0].UID != pa.UID || c.Scores[df.ID] != 3 {
			t.Fatalf("cave pos %+v score %d", c.Gates[5][0], c.Scores[df.ID])
		}
		return false
	})
	a.sendExt("DONGFU_PTO.C_GET_CAVE_GAME_GATE_INFO", xym.Msg{"gate": 5})
	if g := a.expectExt("DONGFU_PTO.S_SEND_CAVE_GAME_GATE_INFO"); g["gate_pos_list"].([]xym.Msg)[0]["dongfu_name"] != "测试府" {
		t.Fatalf("gate %v", g)
	}
}
