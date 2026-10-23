package xymsrv

import (
	"slices"
	"strings"
	"testing"
	"time"

	"turnrpg/internal/xym"
)

// TestXiuxing：派护法修行（扣 5 体力、次数 −1）；每 15 分钟出一个奇遇、累积银两；领取 / 挑战 / 商人 / 放弃
// 各类奇遇；结束发累计银两并回到未开始。
func TestXiuxing(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.Local)
	old := xxNow
	xxNow = func() time.Time { return now }
	t.Cleanup(func() { xxNow = old })
	c, p, srv := actWorld(t, "xiuxinger")
	strongPlayer(srv, p)
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold, ro.TPoint = 10000, 60; return true })

	c.sendExt("XIUXING_PTO.C_OPEN_XIUXING", nil)
	if m := c.expectExt("XIUXING_PTO.S_OPEN_XIUXING"); num(m, "state") != 0 || num(m, "left_count") != xxDaily ||
		len(m["week_pet_list"].([]xym.Msg)) != xxWeekPets {
		t.Fatalf("open %v", m)
	}
	pets := p.sortedPets()
	var fs []xym.Msg
	for _, pet := range pets[:3] {
		fs = append(fs, xym.Msg{"type": int64(pet.Type)})
	}
	c.sendExt("XIUXING_PTO.C_START_XIUXING", xym.Msg{"fighter": fs})
	got := c.until(t, "XIUXING_PTO.S_OPEN_XIUXING")
	if m := got["XIUXING_PTO.S_OPEN_XIUXING"]; num(m, "state") != 1 || num(m, "left_count") != xxDaily-1 ||
		len(m["fighter"].([]xym.Msg)) != 3 || srv.roles.Get(p.UID).TPoint != 60-xxCost {
		t.Fatalf("start %v", m)
	}

	now = now.Add(61 * time.Minute) // 修行结束：4 个奇遇、4 次银两
	c.sendExt("XIUXING_PTO.C_OPEN_XIUXING", nil)
	m := c.expectExt("XIUXING_PTO.S_OPEN_XIUXING")
	if num(m, "state") != 2 || len(m["qiyu_list"].([]xym.Msg)) != xxTicks || num(m, "money") != int(4*p.Xiuxing.PerPay) {
		t.Fatalf("after hour %v", m)
	}
	for i, q := range p.Xiuxing.Qiyu {
		idx := int64(i + 1)
		c.sendExt("XIUXING_PTO.C_OPEN_QIYU", xym.Msg{"index": idx})
		om := c.expectExt("XIUXING_PTO.S_OPEN_QIYU")
		keys := om["keys"].([]xym.Msg)
		if len(keys) == 0 || om["reward_icon1"] == "" || num(om, "is_finished") != 0 {
			t.Fatalf("qiyu %d %v", i, om)
		}
		switch q.Kind {
		case "gift", "week", "merchant":
			c.sendExt("XIUXING_PTO.C_QIYU_KEY_PRESS", xym.Msg{"index": idx, "key": keys[0]["key"], "param": int64(0)})
			got := c.until(t, "XIUXING_PTO.S_OPEN_XIUXING")
			if got["ETC_PTO.S_REWARD_MSG"] == nil || !q.Done {
				t.Fatalf("qiyu %s %v", q.Kind, got)
			}
		case "fight":
			c.sendExt("XIUXING_PTO.C_QIYU_KEY_PRESS", xym.Msg{"index": idx, "key": "挑战", "param": int64(0)})
			if ca := c.expectExt("XIUXING_PTO.S_FIGHT_CA"); num(ca, "ca") == 0 {
				t.Fatalf("fight ca %v", ca)
			}
			var team []xym.Msg
			for k, pet := range pets[:5] {
				team = append(team, xym.Msg{"fight_pos": int64(k + 1), "npcnum": int64(pet.NpcNum), "wear_pos": int64(k + 1)})
			}
			c.sendExt("XIUXING_PTO.C_FIGHT_WITH_TMP", xym.Msg{"index": idx, "leader_num": int64(pets[0].NpcNum), "tmp_team": team})
			got := c.until(t, "ETC_PTO.S_REWARD_MSG")
			if got["FIGHT_PTO.S_FIGHT_RECORD"] == nil || (num(got["ETC_PTO.S_REWARD_MSG"], "type") == 2) != q.Done {
				t.Fatalf("fight %v done %v", got, q.Done)
			}
		}
	}
	cash := srv.roles.Get(p.UID).Cash
	c.sendExt("XIUXING_PTO.C_END_XIUXING", nil)
	got = c.until(t, "XIUXING_PTO.S_OPEN_XIUXING")
	if m := got["XIUXING_PTO.S_OPEN_XIUXING"]; num(m, "state") != 0 || srv.roles.Get(p.UID).Cash != cash+4*p.Xiuxing.PerPay {
		t.Fatalf("end %v cash %d→%d", m, cash, srv.roles.Get(p.UID).Cash)
	}
}

// TestXiuxingQiyuKinds：派出本周星阵容的卡时，四种奇遇（领取、挑战、商人、周奇遇）都会出现；同一种子结果不变。
func TestXiuxingQiyuKinds(t *testing.T) {
	week, _ := xxWeek(time.Date(2026, 10, 19, 10, 0, 0, 0, time.Local))
	seen := map[string]bool{}
	for seed := uint64(1); seed < 40; seed++ {
		x := &XiuxingState{Seed: seed, Pets: week[:1]}
		x.rollQiyu(xxTicks, week)
		y := &XiuxingState{Seed: seed, Pets: week[:1]}
		y.rollQiyu(2, week)
		y.rollQiyu(xxTicks, week)
		for i, q := range x.Qiyu {
			seen[q.Kind] = true
			if *q != *y.Qiyu[i] {
				t.Fatalf("seed %d qiyu %d not stable: %+v vs %+v", seed, i, q, y.Qiyu[i])
			}
		}
	}
	for _, k := range []string{"gift", "fight", "merchant", "week"} {
		if !seen[k] {
			t.Fatalf("kind %s never rolled: %v", k, seen)
		}
	}
}

// TestXiuxingJieyuanTongxiu：结缘值满点按钮 → 派出可结缘的护法第一个奇遇必是结缘，完成后护法获得那条人物缘；
// 再同修一次进阶。每次修行结缘值 +20、同修值 +15。
func TestXiuxingJieyuanTongxiu(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.Local)
	old := xxNow
	xxNow = func() time.Time { return now }
	t.Cleanup(func() { xxNow = old })
	c, p, srv := actWorld(t, "tongxiu")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.TPoint = 60; return true })
	const typ, rid = 15001, 631 // 火神祝融：统领氏族
	p.mu.Lock()
	pet := p.petByType(typ)
	if pet == nil {
		pet = p.addPet(typ)
	}
	p.xiuxing().Jieyuan = 100
	p.addItem(jieyuanStone, 1)
	p.addItem(tongxiuStone, 1)
	p.mu.Unlock()

	run := func(baodi int64, key string) {
		c.sendExt("XIUXING_PTO.C_OPEN_BAODI", xym.Msg{"type": baodi})
		c.until(t, "XIUXING_PTO.S_OPEN_XIUXING")
		c.sendExt("XIUXING_PTO.C_START_XIUXING", xym.Msg{"fighter": []xym.Msg{{"type": int64(typ)}}})
		c.until(t, "XIUXING_PTO.S_OPEN_XIUXING")
		now = now.Add(16 * time.Minute)
		c.sendExt("XIUXING_PTO.C_OPEN_QIYU", xym.Msg{"index": int64(1)})
		if om := c.expectExt("XIUXING_PTO.S_OPEN_QIYU"); num(om, "type") != int(baodi) {
			t.Fatalf("first qiyu %v, want type %d", om, baodi)
		}
		c.sendExt("XIUXING_PTO.C_QIYU_KEY_PRESS", xym.Msg{"index": int64(1), "key": key, "param": int64(0)})
		c.until(t, "XIUXING_PTO.S_OPEN_XIUXING")
		c.sendExt("XIUXING_PTO.C_END_XIUXING", nil)
		c.until(t, "XIUXING_PTO.S_OPEN_XIUXING")
		now = now.Add(time.Hour)
	}
	run(qyJieyuan, "结缘")
	if !slices.Contains(pet.Combine, rid) || p.itemCount(jieyuanStone) != 0 || p.Xiuxing.Jieyuan != xxJieyuanStep || p.Xiuxing.Tongxiu != xxTongxiuStep {
		t.Fatalf("jieyuan: combine %v values %d %d", pet.Combine, p.Xiuxing.Jieyuan, p.Xiuxing.Tongxiu)
	}
	p.mu.Lock()
	p.Xiuxing.Tongxiu = 100
	p.mu.Unlock()
	run(qyTongxiu, "同修")
	if !slices.Contains(pet.Together, rid) || p.Xiuxing.Tongxiu != xxTongxiuStep || p.itemCount(tongxiuStone) != 0 {
		t.Fatalf("tongxiu: together %v value %d", pet.Together, p.Xiuxing.Tongxiu)
	}
}

// TestXiuxingNeedStone：没有结缘石时结缘奇遇不能完成（提示不足，奇遇保留）。
func TestXiuxingNeedStone(t *testing.T) {
	c, p, _ := actWorld(t, "nostone")
	const typ, rid = 15001, 631
	p.mu.Lock()
	if p.petByType(typ) == nil {
		p.addPet(typ)
	}
	x := p.xiuxing()
	x.Start = xxNow().Unix()
	x.Qiyu = []*XQiyu{{Type: qyJieyuan, Kind: "jieyuan", Pet: typ, Rid: rid}}
	p.mu.Unlock()
	c.sendExt("XIUXING_PTO.C_QIYU_KEY_PRESS", xym.Msg{"index": int64(1), "key": "结缘", "param": int64(0)})
	if m := c.until(t, "ETC_PTO.S_RET_MSG")["ETC_PTO.S_RET_MSG"]; !strings.Contains(m["msg"].(string), "结缘石不足") || x.Qiyu[0].Done {
		t.Fatalf("msg %v done %v", m, x.Qiyu[0].Done)
	}
}
