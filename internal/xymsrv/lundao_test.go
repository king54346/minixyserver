package xymsrv

import (
	"slices"
	"strings"
	"testing"

	"turnrpg/internal/xym"
)

func TestLundaoFirstCanChallengeForTask(t *testing.T) {
	c, p, srv := actWorld(t, "lundao-first")
	c.sendExt("LUNDAO_PTO.C_OPEN_LUNDAO", nil)
	c.expectExt("LUNDAO_PTO.S_OPEN_LUNDAO")
	c.expectExt("LUNDAO_PTO.S_SEND_TIMES")

	g := ldGroup(p.grade())
	var before []int32
	srv.ladder.do(func() bool {
		ranks := srv.ladder.Groups[g]
		i := slices.Index(ranks, p.UID)
		ranks[0], ranks[i] = ranks[i], ranks[0]
		before = slices.Clone(ranks)
		return true
	})
	for _, rank := range []int{2, 10} {
		c.sendExt("LUNDAO_PTO.C_FIGHT_LUNDAO", xym.Msg{"rank": rank})
		got := c.until(t, "ETC_PTO.S_REWARD_MSG")
		if got["FIGHT_PTO.S_FIGHT_RECORD"] == nil {
			t.Fatalf("no battle against rank %d", rank)
		}
	}
	p.mu.Lock()
	times, taskCount := p.lundao().Times, p.tasks().Today[evLundao]
	p.mu.Unlock()
	if times != ldDailyTimes-2 || taskCount != 2 {
		t.Fatalf("after two challenges: times=%d, daily task=%d", times, taskCount)
	}
	srv.ladder.do(func() bool {
		if !slices.Equal(srv.ladder.Groups[g], before) {
			t.Error("first-place challenges must leave the standings intact")
		}
		return false
	})
}

// TestLundao：首次打开排在 1000 个假玩家之后（第 1001 名），列表是前 10 名 + 自己上 5 名；
// 挑战超过 5 名的被拒；挑战第 996 名胜利后互换名次、扣 1 次、弹 type 14；积分按小时结算；兑换扣积分。
func TestLundao(t *testing.T) {
	c, p, srv := actWorld(t, "lundaoer")
	c.sendExt("LUNDAO_PTO.C_OPEN_LUNDAO", nil)
	open := c.expectExt("LUNDAO_PTO.S_OPEN_LUNDAO")
	c.expectExt("LUNDAO_PTO.S_SEND_TIMES")
	l := open["list"].([]xym.Msg)
	if num(open, "times") != ldDailyTimes || len(l) != 16 || num(l[15], "rank") != 1001 || num(l[15], "uid") != int(p.UID) ||
		num(l[10], "rank") != 996 {
		t.Fatalf("open %v", open)
	}
	for _, x := range l[:10] {
		if x["name"] == "" || num(x, "fight_point") <= 0 || num(x, "petface_min") == 0 {
			t.Fatalf("fake entry %v", x)
		}
	}
	// 太远
	c.sendExt("LUNDAO_PTO.C_FIGHT_LUNDAO", xym.Msg{"rank": 1})
	c.expectMsg(t, "5 名以内")
	c.collect()
	// 挑战第 996 名（最弱的一档），赢了互换
	won := false
	for range 3 {
		c.sendExt("LUNDAO_PTO.C_FIGHT_LUNDAO", xym.Msg{"rank": 996})
		got := c.until(t, "LUNDAO_PTO.S_SEND_SCORE")
		if _, ok := got["FIGHT_PTO.S_FIGHT_RECORD"]; !ok {
			t.Fatalf("no record %v", got)
		}
		if share, ok := got["LUNDAO_PTO.S_SEND_SHARE_INFO"]; ok {
			if num(share, "rank") != 996 || num(share, "targetrank") != 1001 {
				t.Fatalf("share %v", share)
			}
			if r := c.expectExt("ETC_PTO.S_REWARD_MSG"); num(r, "type") != 14 {
				t.Fatalf("reward %v", r)
			}
			won = true
			break
		}
	}
	if !won {
		t.Fatal("never beat rank 996")
	}
	srv.ladder.do(func() bool {
		if srv.ladder.rankOf(p.UID, p.grade()) != 996 {
			t.Fatalf("rank %d", srv.ladder.rankOf(p.UID, p.grade()))
		}
		return false
	})
	if p.lundao().Times >= ldDailyTimes {
		t.Fatal("times not used")
	}
	// 积分：往前拨 3 小时，第 996 名（50 级组）每小时 68
	p.mu.Lock()
	p.lundao().ScoreAt -= 3 * 3600
	p.mu.Unlock()
	c.sendExt("LUNDAO_PTO.C_REFRESH_SCORE", nil)
	if m := c.expectExt("LUNDAO_PTO.S_SEND_SCORE"); num(m, "score") != 204 {
		t.Fatalf("score %v", m)
	}
	// 兑换 3 星筑基丹 ×5（700 分）：分不够被拒；补够后兑换
	c.sendExt("LUNDAO_PTO.C_BUY_ITEM", xym.Msg{"cid": 11118, "pos": 1, "count": 1})
	c.expectMsg(t, "积分不足")
	c.collect()
	p.mu.Lock()
	p.lundao().Score = 1000
	p.mu.Unlock()
	before := p.itemCount(10095)
	c.sendExt("LUNDAO_PTO.C_BUY_ITEM", xym.Msg{"cid": 11118, "pos": 1, "count": 1})
	c.until(t, "LUNDAO_PTO.S_SEND_ITEMLIST")
	if p.lundao().Score != 300 || p.itemCount(10095) != before+5 {
		t.Fatalf("buy: score %d items %d", p.lundao().Score, p.itemCount(10095))
	}
	// 次数用完
	p.mu.Lock()
	p.lundao().Times = 0
	p.mu.Unlock()
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 100; return true })
	c.sendExt("LUNDAO_PTO.C_FIGHT_LUNDAO", xym.Msg{"rank": 995})
	ask := c.until(t, "ETC_PTO.S_ASK_BOX")["ETC_PTO.S_ASK_BOX"]
	if !strings.Contains(ask["text"].(string), "次数已经用完") {
		t.Fatalf("ask %v", ask)
	}
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": ask["session"], "opt": 0})
	c.until(t, "LUNDAO_PTO.S_SEND_TIMES")
	if g := srv.roles.Get(p.UID).Gold; g != 100-ldBuyGold || p.lundao().Times != 0 {
		t.Fatalf("buy times: gold %d times %d", g, p.lundao().Times)
	}
	// 榜存档
	if l2 := openLadder(srv.players.st); l2.rankOf(p.UID, p.grade()) > 996 || len(l2.Groups[ldGroup(p.grade())]) != 1001 { // 买次数后打了第 995 名，赢了是 995
		t.Fatalf("persisted rank %d len %d", l2.rankOf(p.UID, p.grade()), len(l2.Groups[ldGroup(p.grade())]))
	}
}

// TestLundaoFakes：1000 个假玩家阵容固定且由强到弱（榜首战力明显高于榜尾）。
func TestLundaoFakes(t *testing.T) {
	a, z := fakeFoe(0), fakeFoe(999)
	if a.FP <= z.FP*2 || len(a.Team) != 8 || len(z.Team) != 6 || fakeFoe(0).Name != a.Name || fakeFoe(0).FP != a.FP {
		t.Fatalf("fakes top %d/%d bottom %d/%d", a.FP, len(a.Team), z.FP, len(z.Team))
	}
}
