package xymsrv

import (
	"strings"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestZhenfa：学阵法要 100 本阵法书（学会自动使用）；培养扣阵魂符篆 + 魂值，一键培养给投入最少的阵位；
// 五阵位满 10 级后用 300 本阵魂图册升阶、阵位回到 1 级；重置本阶全额退回；打关卡时战报带阵法参数（otherparam 11）。
func TestZhenfa(t *testing.T) {
	c, p, srv := actWorld(t, "zhenfa")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.SoulExp = 100000; return true })
	open := func() xym.Msg {
		t.Helper()
		return c.until(t, "ZHENFA_PTO.S_OPEN_ALL_ZHENFA")["ZHENFA_PTO.S_OPEN_ALL_ZHENFA"]
	}
	c.sendExt("ETC_PTO.C_ON_QUEST_SWITCH", xym.Msg{"key": "zhenfa"})
	if m := c.expectExt("ETC_PTO.S_SEND_REPLY_QUEST_SWITCH"); num(m, "state") != 1 {
		t.Fatalf("switch %v", m)
	}

	p.mu.Lock()
	p.addItem(10433, 99) // 地载阵书 99 本：不够
	p.mu.Unlock()
	c.sendExt("ZHENFA_PTO.C_LEARN_ZHENFA", xym.Msg{"type": 2})
	if m := c.until(t, "ETC_PTO.S_RET_MSG")["ETC_PTO.S_RET_MSG"]; !strings.Contains(m["msg"].(string), "×100") {
		t.Fatalf("learn short %v", m)
	}
	open()
	p.mu.Lock()
	p.addItem(10433, 1)
	p.addItem(zfFuzhuan, 100000)
	p.mu.Unlock()
	c.sendExt("ZHENFA_PTO.C_LEARN_ZHENFA", xym.Msg{"type": 2})
	if m := open(); num(m, "cur_zhenfa") != 2 || len(m["open_list"].([]xym.Msg)) != 1 || p.itemCount(10433) != 0 {
		t.Fatalf("learn %v", m)
	}

	need := gamedata.ZhenfaOf(1, 1).Enhance[1] // 1 → 2 级
	c.sendExt("ZHENFA_PTO.C_ENHANCE_ZHENFA", xym.Msg{"pos": 3, "count": need})
	m := open()
	if pl := m["pos_lvl"].([]xym.Msg); num(pl[2], "lvl") != 2 || num(pl[0], "lvl") != 1 {
		t.Fatalf("enhance %v", pl)
	}
	if p.itemCount(zfFuzhuan) != int32(100000-need) || srv.roles.Get(p.UID).SoulExp != int32(100000-need) {
		t.Fatalf("cost fu %d soul %d", p.itemCount(zfFuzhuan), srv.roles.Get(p.UID).SoulExp)
	}
	c.sendExt("ZHENFA_PTO.C_QUICK_ENHANCE", xym.Msg{"count": 4 * need}) // 补给投入最少的四个阵位
	if pl := open()["pos_lvl"].([]xym.Msg); num(pl[0], "lvl") != 2 || num(pl[4], "lvl") != 2 {
		t.Fatalf("quick %v", pl)
	}

	// 打一关：战报带阵法参数
	c.sendExt("TASK_PTO.C_START_FIGHT", xym.Msg{"gid": 10101, "type": 0, "pos1": 1, "pos2": 2, "pos3": 3, "pos4": 4, "pos5": 5})
	rec := c.until(t, "FIGHT_PTO.S_FIGHT_RECORD")["FIGHT_PTO.S_FIGHT_RECORD"]
	found := false
	for _, op := range rec["otherparam"].([]xym.Msg) {
		if num(op, "idx") == 11 && op["value"] == "1:2:1:2:2:2:2:2|2:0:1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("otherparam %v", rec["otherparam"])
	}

	// 升满、升阶
	p.mu.Lock()
	z := p.zhenfa()
	z.Lv, z.Exp = [5]int{10, 10, 10, 10, 10}, [5]int{}
	p.addItem(zfAtlas, zfAtlasNeed)
	p.mu.Unlock()
	c.sendExt("ZHENFA_PTO.C_UPGRADE_ZHENFA", nil)
	if m := open(); num(m, "big_lvl") != 2 || num(m["pos_lvl"].([]xym.Msg)[0], "lvl") != 1 || p.itemCount(zfAtlas) != 0 {
		t.Fatalf("upgrade %v", m)
	}

	// 重置本阶：全额退回
	c.sendExt("ZHENFA_PTO.C_ENHANCE_ZHENFA", xym.Msg{"pos": 1, "count": 5})
	open()
	fu, soul := p.itemCount(zfFuzhuan), srv.roles.Get(p.UID).SoulExp
	c.sendExt("ZHENFA_PTO.C_RESET_ZHENFA", nil)
	if m := open(); num(m["pos_lvl"].([]xym.Msg)[0], "lvl") != 1 || p.itemCount(zfFuzhuan) != fu+5 || srv.roles.Get(p.UID).SoulExp != soul+5 {
		t.Fatalf("reset %v fu %d soul %d", m, p.itemCount(zfFuzhuan), srv.roles.Get(p.UID).SoulExp)
	}
}

// TestDangyaoZhenfaBook：荡妖功德商店有「随机阵法书」，奖励开出八本阵法书之一。
func TestDangyaoZhenfaBook(t *testing.T) {
	rw := gamedata.Rewards[gamedata.RewardRandomZhenfaBook]
	if rw == nil || len(rw.Drops) != 2 || strings.Count(rw.Drops[1], "WP104") != 8 {
		t.Fatalf("reward %+v", rw)
	}
	found := false
	for _, it := range gamedata.Dangyao.Shop {
		if it.CA == gamedata.RewardRandomZhenfaBook && it.Price == 200 {
			found = true
		}
	}
	if !found {
		t.Fatal("not in dangyao shop")
	}
}
