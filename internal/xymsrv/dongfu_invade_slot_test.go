package xymsrv

import (
	"testing"
	"time"

	"turnrpg/internal/xym"
)

// TestInvadeSlotStart：魔潮的「下一次开启时间」换算——选了时段要排到那个时刻，不是点了就开。
// 客户端把三个选项写死成 12:02 / 18:02 / 21:02，所以分钟固定 invadeMinute。
func TestInvadeSlotStart(t *testing.T) {
	loc := time.Local
	// 上午 10 点选 12 点：排到今天 12:02。
	got, msg := invadeSlotStart(12, time.Date(2026, 11, 17, 10, 0, 0, 0, loc))
	if msg != "" {
		t.Fatalf("10:00 选 12 点不该被拒: %s", msg)
	}
	if want := time.Date(2026, 11, 17, 12, invadeMinute, 0, 0, loc).Unix(); got != want {
		t.Fatalf("start=%d, want %d", got, want)
	}

	// 下午 13 点选 12 点：这个时段已经过了，要拒绝并提示。
	if _, msg := invadeSlotStart(12, time.Date(2026, 11, 17, 13, 0, 0, 0, loc)); msg == "" {
		t.Fatal("已过的时段应被拒绝")
	}

	// 正好卡在时段那一分钟（12:02:00）也算已过，避免「排到过去」。
	if _, msg := invadeSlotStart(12, time.Date(2026, 11, 17, 12, invadeMinute, 0, 0, loc)); msg == "" {
		t.Fatal("时段当刻应视为已过")
	}

	// 时段前一分钟可以排。
	if _, msg := invadeSlotStart(12, time.Date(2026, 11, 17, 12, invadeMinute-1, 0, 0, loc)); msg != "" {
		t.Fatalf("时段前一分钟应可排: %s", msg)
	}
}

// TestInvadeLevelCap：可开重数 = 4×洞府等级，封顶 20（客户端管理页写死「魔潮最高为20层」）。
// 早先写死 6，5 级洞府永远开不出第 7 重。
func TestInvadeLevelCap(t *testing.T) {
	cases := []struct {
		lv   int32
		want int
	}{
		{1, 4}, {2, 8}, {3, 12}, {4, 16}, {5, 20}, {6, 20}, // 6 级不存在，防御性封顶
	}
	for _, c := range cases {
		if got := invadeLevelCap(c.lv); got != c.want {
			t.Errorf("洞府 %d 级可开 %d 重, want %d", c.lv, got, c.want)
		}
	}
	if invadeMaxLevel != 20 {
		t.Fatalf("invadeMaxLevel 应对齐客户端文案 20, got %d", invadeMaxLevel)
	}
}

// TestInvadeLevelListAndReject：5 级洞府列表给 1-20 重，超出上限的开启请求被拒且不扣元宝。
func TestInvadeLevelListAndReject(t *testing.T) {
	srv, a, _, pa, _ := dfWorld(t)
	df := dfOf(srv, pa.UID)
	srv.dongfu.do(func() bool { df.Lv = 5; df.Gold = 100000; return false })

	a.sendExt("DONGFU_PTO.C_OPEN_MONSTER_INVADE_LEVEL_INFO", nil)
	list := a.expectExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_LEVEL_INFO")["list"].([]xym.Msg)
	if len(list) != invadeMaxLevel {
		t.Fatalf("5 级洞府应给 %d 个可选重数, got %d", invadeMaxLevel, len(list))
	}
	if lv := num(list[len(list)-1], "level"); lv != invadeMaxLevel {
		t.Fatalf("最后一重应为 %d, got %d", invadeMaxLevel, lv)
	}

	// 21 重：拒绝（提示里带上限数字，顺便校验 itoa 修好后不再出乱码），且洞府元宝不变。
	a.sendExt("DONGFU_PTO.C_OPEN_MONSTER_INVADE", xym.Msg{"level": invadeMaxLevel + 1, "open_hour": 12})
	a.expectMsg(t, "最多 20 重")
	if g := dfOf(srv, pa.UID).Gold; g != 100000 {
		t.Fatalf("被拒时不该扣元宝, gold=%d", g)
	}
}

// TestInvadeMailSpec：魔潮奖励 = 官方道具（已去掉无字天书碎片）+ 元宝/银两：
//   - 50227 完成奖励：黄金宝箱×10 + 黄金钥匙×10（所有人）；
//   - 50212–50218 排行道具按名次，**不含 WP11154 无字天书碎片**；
//   - 元宝 200×重数、银两 50万×重数；前 3 名再 +(3-名次)×200×重数 元宝；
//   - 50226 BOSS击败：五星飞升护法×1，只给最后一击的人；不发神魂。
func TestInvadeMailSpec(t *testing.T) {
	// 贡献 0：只有完成奖励 + 基础元宝/银两，没有排行道具、没有额外元宝。
	base := invadeMailSpec(10, 200, 0, false, 0, false)
	if base.Soul != 0 {
		t.Fatalf("魔潮不该发神魂: %d", base.Soul)
	}
	if base.Gold != 2000 || base.Cash != 5000000 {
		t.Fatalf("10 重基础应为 元宝2000/银两500万, got %d/%d", base.Gold, base.Cash)
	}
	if base.Items["WP10002"] != 10 || base.Items["WP10003"] != 10 {
		t.Fatalf("完成奖励应为黄金宝箱×10 + 黄金钥匙×10, got %v", base.Items)
	}
	if len(base.Items) != 2 {
		t.Fatalf("贡献 0 不该有排行道具, got %v", base.Items)
	}

	// 排行 1：额外 3 份元宝（3×200×重数），且道具里不能出现无字天书碎片。
	first := invadeMailSpec(10, 0, 100, false, 0, false)
	if first.Gold != 2000+3*2000 {
		t.Fatalf("第 1 名元宝 %d, want %d", first.Gold, 2000+3*2000)
	}
	if _, ok := first.Items["WP11154"]; ok {
		t.Fatalf("不该再发无字天书碎片: %v", first.Items)
	}
	for id, want := range map[string]int32{"GPZFS2": 3, "WP10442": 5, "WP10440": 20, "WP10130": 10} {
		if first.Items[id] != want {
			t.Fatalf("排行1 %s=%d, want %d（%v）", id, first.Items[id], want, first.Items)
		}
	}

	// 各档排行：元宝递减、道具递减，且都没有 WP11154。
	cases := []struct {
		rank     int
		wantGold int32
		want     map[string]int32
	}{
		{1, 2000 + 2*2000, map[string]int32{"GPZFS2": 2, "WP10442": 3, "WP10440": 15, "WP10130": 10}},
		{2, 2000 + 1*2000, map[string]int32{"GPZFS2": 2, "WP10442": 3, "WP10440": 15, "WP10130": 10}},
		{3, 2000, map[string]int32{"GPZFS2": 2, "WP10442": 1, "WP10440": 15, "WP10130": 10}},
		{10, 2000, map[string]int32{"GPZFS2": 1, "WP10442": 1, "WP10440": 15, "WP10130": 5}},
		{20, 2000, map[string]int32{"GPZFS2": 1, "WP10442": 1, "WP10440": 10, "WP10130": 5}},
		{50, 2000, map[string]int32{"GPZFS2": 1, "WP10442": 1, "WP10440": 10, "WP10130": 5}},
		{100, 2000, map[string]int32{"GPZFS2": 1, "WP10442": 1, "WP10440": 10}},
	}
	for _, c := range cases {
		got := invadeMailSpec(10, c.rank, 10, false, 0, false)
		if got.Gold != c.wantGold {
			t.Fatalf("名次 %d 元宝 %d, want %d", c.rank, got.Gold, c.wantGold)
		}
		if _, ok := got.Items["WP11154"]; ok {
			t.Fatalf("名次 %d 不该有 WP11154: %v", c.rank, got.Items)
		}
		for id, want := range c.want {
			if got.Items[id] != want {
				t.Fatalf("名次 %d 的 %s=%d, want %d", c.rank, id, got.Items[id], want)
			}
		}
	}

	// 妖王最后一击：额外五星飞升护法。
	if got := invadeMailSpec(10, 5, 10, false, 0, true); got.Items["GPFSDZ5"] != 1 {
		t.Fatalf("最后一击应给 GPFSDZ5, got %v", got.Items)
	}
	if got := invadeMailSpec(10, 5, 10, false, 0, false); got.Items["GPFSDZ5"] != 0 {
		t.Fatalf("非最后一击不该给 GPFSDZ5, got %v", got.Items)
	}
}
