package xymsrv

import (
	"strings"
	"testing"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func TestRegenStep(t *testing.T) {
	now := int64(100000)
	for _, c := range []struct {
		cur, maxV int
		at        int64
		want      int
		wantAt    int64
		name      string
	}{
		{10, 60, now - 25*60, 12, now - 5*60, "两次回复，余 5 分钟继续计时"},
		{59, 60, now - 30*60, 60, now, "到上限停止，计时停在现在"},
		{60, 60, now - 99*60, 60, now, "满的不累计"},
		{70, 60, now - 99*60, 70, now, "超上限（买的）不变"},
		{10, 60, now - 9*60, 10, now - 9*60, "不满 10 分钟不回"},
	} {
		got, at := regenStep(c.cur, c.maxV, c.at, now, tpRegen)
		if got != c.want || at != c.wantAt {
			t.Errorf("%s: got %d at %d, want %d at %d", c.name, got, at, c.want, c.wantAt)
		}
	}
}

// TestEnergy：自然回复随请求推送；体力不足开战斗回 S_RET_MSG + 获取体力面板；元宝购买；一键吃包子到满。
func TestEnergy(t *testing.T) {
	c, p, srv := actWorld(t, "tp")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.TPoint, ro.Gold = 0, 1000; return true })
	c.sendExt("TASK_PTO.C_START_FIGHT", xym.Msg{"gid": 10101, "type": 0, "pos1": 1, "pos2": 2, "pos3": 3, "pos4": 4, "pos5": 5})
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "体力不足") {
		t.Fatalf("no tpoint %v", m)
	}
	c.expectExt("ETC_PTO.S_OPEN_POINT_BUY")

	p.mu.Lock()
	p.energy().TPAt = time.Now().Unix() - 35*60 // 35 分钟前开始计时 → +3
	p.mu.Unlock()
	c.sendExt("ETC_PTO.C_OPEN_POINT_BUY", xym.Msg{"type": 1})
	a := c.expectExt("ETC_PTO.S_ATTR_UPDATE")["attr"].([]xym.Msg)
	if a[0]["name"] != "tpoint" || num(a[0], "val") != 3 {
		t.Fatalf("regen %v", a)
	}
	if m := c.expectExt("ETC_PTO.S_OPEN_POINT_BUY"); num(m, "goldBuyTimes") != pointBuys {
		t.Fatalf("open %v", m)
	}

	c.sendExt("ETC_PTO.C_BUY_POINT_CONFIRM", xym.Msg{"type": 1, "buy_type": 1})
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	c.expectExt("ETC_PTO.S_RET_MSG")
	if ro := srv.roles.Get(p.UID); ro.TPoint != 63 || ro.Gold != 1000-tpBuyBase || p.Energy.TBuys != 1 {
		t.Fatalf("buy tpoint %d gold %d", ro.TPoint, ro.Gold)
	}

	srv.roles.Update(p.UID, func(ro *Role) bool { ro.TPoint = 1; return true })
	p.mu.Lock()
	p.addItem(bunBig, 9)
	p.addItem(bunSmall, 10)
	p.mu.Unlock()
	c.sendExt("ETC_PTO.C_BUY_POINT_CONFIRM", xym.Msg{"type": 1, "buy_type": 3})
	got := c.until(t, "ETC_PTO.S_ATTR_UPDATE")
	msg := got["ETC_PTO.S_RET_MSG"]["msg"].(string)
	// 需要 119：大笼 ×9 = 90，再包子 ×6 = 30，浪费 1
	if !strings.Contains(msg, "大笼包子×9、包子×6") || !strings.Contains(msg, "浪费了1点") ||
		srv.roles.Get(p.UID).TPoint != maxTPoint || p.itemCount(bunSmall) != 4 || p.itemCount(bunBig) != 0 {
		t.Fatalf("eat %q tpoint %d small %d", msg, srv.roles.Get(p.UID).TPoint, p.itemCount(bunSmall))
	}
}

// TestXina：吸纳护法得全部经验、被吸纳的回 1 级、扣银两；吸纳经验丹。
func TestXina(t *testing.T) {
	c, p, srv := actWorld(t, "xina")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Cash = 1000000; return true })
	p.mu.Lock()
	to := p.petAtFightPos(1)
	to.Grade, to.Exp = 10, 0
	var other int32
	for _, id := range gamedata.PetsByStar[3] {
		if p.petByType(id) == nil {
			other = id
			break
		}
	}
	from := p.gainPet(other) // 背包里没有的一类，免得并入同名卡
	from.Grade, from.Exp = 20, 7
	want := from.totalExp()
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_TRANS_PET_EXP", xym.Msg{"from_npcnum": from.NpcNum, "to_npcnum": to.NpcNum, "use_gold": 0})
	got := c.until(t, "PET_PTO.S_UPDATE_PET")
	if from.Grade != 1 || from.Exp != 0 || to.totalExp() < want || srv.roles.Get(p.UID).Cash != 1000000-20*20*10 {
		t.Fatalf("trans to %d/%d from %d cash %d %v", to.Grade, to.totalExp(), from.Grade, srv.roles.Get(p.UID).Cash, got)
	}

	c.sendExt("PET_PTO.C_TRANS_PET_EXP", xym.Msg{"from_npcnum": p.petAtFightPos(2).NpcNum, "to_npcnum": to.NpcNum, "use_gold": 0})
	c.expectExt("ETC_PTO.S_RET_MSG") // 上阵的不能被吸纳

	p.mu.Lock()
	p.addItem(10015, 2)
	before := to.totalExp()
	p.mu.Unlock()
	c.sendExt("ITEM_PTO.C_USE_DRUG", xym.Msg{"type": 10015, "npcnum": to.NpcNum, "count": 2})
	c.until(t, "PET_PTO.S_UPDATE_PET")
	if to.totalExp() != before+20000 || p.itemCount(10015) != 0 {
		t.Fatalf("drug exp %d -> %d items %d", before, to.totalExp(), p.itemCount(10015))
	}
}

// TestBagFullMail：装备 / 法术背包满时开黄金十连，掉的装备 / 法术改寄邮件，奖励列表仍是 10 项。
func TestBagFullMail(t *testing.T) {
	c, p, srv := actWorld(t, "full")
	p.mu.Lock()
	for len(p.Suits) < p.BagLimit[bagSuit] {
		p.addSuit(1001)
	}
	for len(p.Books) < p.BagLimit[bagBook] {
		p.addBook(12101)
	}
	p.addItem(10002, 10)
	p.addItem(10003, 10)
	p.mu.Unlock()
	mailed := 0
	for range 5 {
		p.mu.Lock()
		p.addItem(10002, 10)
		p.addItem(10003, 10)
		p.mu.Unlock()
		c.sendExt("ETC_PTO.C_OPEN_TEN_BOX", xym.Msg{"type": 10003})
		got := c.until(t, "ETC_PTO.S_REWARD_MSG")
		if l := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg); len(l) < 10 {
			t.Fatalf("ten box list %d: %v", len(l), l)
		}
		c.collect()
		srv.social.do(func() bool { mailed = len(srv.social.Mails[p.UID]); return false })
		if mailed > 0 {
			break
		}
	}
	if mailed == 0 || len(p.Suits) != p.BagLimit[bagSuit] {
		t.Fatalf("no overflow mail (suits %d)", len(p.Suits))
	}
}
