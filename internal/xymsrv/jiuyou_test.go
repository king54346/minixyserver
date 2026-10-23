package xymsrv

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

// TestJiuyou：主界面 → 选阵 → 战斗（wartype 1、对首领的伤害）→ 结算（排名 1、挑战奖励九幽币 50）；
// 免费次数用完后花元宝买次数；排行与阵容；商店兑换；跨天按名次发排名奖励邮件。
func TestJiuyou(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	old := jyNow
	jyNow = func() time.Time { return now }
	t.Cleanup(func() { jyNow = old })
	c, p, srv := actWorld(t, "jiuyouer")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 1000; return true })

	c.sendExt("JIUYOU_PTO.C_OPEN_MAIN", nil)
	if m := c.expectExt("JIUYOU_PTO.S_MAINVIEW_INFO"); num(m, "remain_challenges_today") != jyFree || num(m, "boss_id") == 0 ||
		num(m, "remain_time") != 12*3600 || num(m, "reward_id") != jyChallenge {
		t.Fatalf("main %v", m)
	}
	fight := func() map[string]xym.Msg {
		t.Helper()
		c.sendExt("JIUYOU_PTO.C_START_FIGHT", nil)
		tt := c.until(t, "QUEST_PTO.S_OPEN_TMP_TEAM")["QUEST_PTO.S_OPEN_TMP_TEAM"]
		team := tt["tmp_team"].([]xym.Msg)
		if tt["key"] != jyKey || len(team) == 0 || num(tt, "fight_ca") == 0 || num(tt, "leader_num") == 0 {
			t.Fatalf("tmp team %v", tt)
		}
		c.sendExt("QUEST_PTO.C_PREPARE_FINISH", xym.Msg{"key": jyKey, "id": tt["id"], "leader_num": tt["leader_num"], "tmp_team": team})
		return c.until(t, "JIUYOU_PTO.S_MAINVIEW_INFO")
	}
	got := fight()
	rec := got["FIGHT_PTO.S_FIGHT_RECORD"]["record"].([]byte)
	if rec[0] != battle.CmdWarStart || rec[1] != 1 {
		t.Fatalf("war start %v", rec[:2])
	}
	w := got["JIUYOU_PTO.S_WIN_BATTLE_INFO"]
	if num(w, "cur_hurt") <= 0 || num(w, "rank") != 1 || num(w, "challenge_reward_id") != jyChallenge ||
		num(w, "rank_reward_id") != int(jyRankTiers[0].reward) || p.itemCount(jyCoin) != jyChallengeN {
		t.Fatalf("win %v coins %d", w, p.itemCount(jyCoin))
	}
	for range jyFree - 1 {
		fight()
	}
	c.sendExt("JIUYOU_PTO.C_START_FIGHT", nil) // 免费次数用完：弹确认框，点「购买」花元宝
	ask := c.until(t, "ETC_PTO.S_ASK_BOX")["ETC_PTO.S_ASK_BOX"]
	if !strings.Contains(ask["text"].(string), fmt.Sprint(jyBuyGold)) {
		t.Fatalf("ask %v", ask)
	}
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": ask["session"], "opt": 0})
	got = fight()
	if num(got["JIUYOU_PTO.S_WIN_BATTLE_INFO"], "challenge_reward_id") != 0 || srv.roles.Get(p.UID).Gold != 1000-jyBuyGold ||
		p.itemCount(jyCoin) != jyFree*jyChallengeN {
		t.Fatalf("bought fight %v gold %d", got["JIUYOU_PTO.S_WIN_BATTLE_INFO"], srv.roles.Get(p.UID).Gold)
	}

	c.sendExt("JIUYOU_PTO.C_OPEN_RANK", nil)
	if m := c.expectExt("JIUYOU_PTO.S_RANK_VIEW_INFO"); len(m["ranking_list"].([]xym.Msg)) != 1 || num(m, "my_reward_id") != int(jyRankTiers[0].reward) {
		t.Fatalf("rank %v", m)
	}
	c.sendExt("JIUYOU_PTO.C_OPEN_RANK_LINEUP", xym.Msg{"rank": 1})
	if m := c.expectExt("JIUYOU_PTO.S_OPENING_BATTLE_TEAM_VIEW"); len(m["tmp_team"].([]xym.Msg)) == 0 {
		t.Fatalf("lineup %v", m)
	}

	c.sendExt("JIUYOU_PTO.C_BUY_ITEM", xym.Msg{"id": int64(10201), "cnt": int64(2)}) // 10 魂值 ×2，20 币一个
	got = c.until(t, "JIUYOU_PTO.S_SHOP_VIEW_INFO")
	if got["ETC_PTO.S_REWARD_MSG"] == nil || p.itemCount(jyCoin) != jyFree*jyChallengeN-40 || p.itemCount(10201) != 2 {
		t.Fatalf("buy %v coins %d", got, p.itemCount(jyCoin))
	}

	now = now.Add(24 * time.Hour) // 第二天：结算昨天的排名
	srv.jyDo(nil)
	mails := 0
	srv.social.do(func() bool {
		for _, ml := range srv.social.Mails[p.UID] {
			if strings.Contains(ml.Title, "九幽") {
				mails++
			}
		}
		return false
	})
	if mails != 1 {
		t.Fatalf("rank mails %d", mails)
	}
	c.sendExt("JIUYOU_PTO.C_OPEN_MAIN", nil)
	if m := c.until(t, "JIUYOU_PTO.S_MAINVIEW_INFO")["JIUYOU_PTO.S_MAINVIEW_INFO"]; num(m, "remain_challenges_today") != jyFree || num(m, "max_damage_value_today") != 0 {
		t.Fatalf("next day %v", m)
	}
}
