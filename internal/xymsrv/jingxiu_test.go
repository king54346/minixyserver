package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestJingxiu：火球术（火属性，jingxiu_cost 20）3 级书精修一次：扣 2 个火元素和 4000 两，jxgrade 1；
// 精修到等级后拒绝。
func TestJingxiu(t *testing.T) {
	c, p, srv := actWorld(t, "jingxiu")
	mi := gamedata.Magics[12101]
	p.mu.Lock()
	p.Books = append(p.Books, &Book{Pos: 50, Type: 12101, Grade: 2})
	p.addItem(10133, 100)
	p.mu.Unlock()
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Cash = 1000000; return true })
	elems, cash := jingxiuCost(mi, 1)
	c.sendExt("ITEM_PTO.C_JINGXIU_MAGIC", xym.Msg{"pos": 50})
	got := c.until(t, "ITEM_PTO.S_UPDATE_BOOK")
	if b := got["ITEM_PTO.S_UPDATE_BOOK"]["books"].([]xym.Msg)[0]; num(b, "jxgrade") != 1 {
		t.Fatalf("book %v", b)
	}
	if p.itemCount(10133) != 100-elems || srv.roles.Get(p.UID).Cash != 1000000-cash || elems != 2 || cash != 4000 {
		t.Fatalf("elems %d cash %d (cost %d %d)", p.itemCount(10133), srv.roles.Get(p.UID).Cash, elems, cash)
	}
	c.sendExt("ITEM_PTO.C_JINGXIU_MAGIC", xym.Msg{"pos": 50})
	c.until(t, "ITEM_PTO.S_UPDATE_BOOK")
	c.sendExt("ITEM_PTO.C_JINGXIU_MAGIC", xym.Msg{"pos": 50})
	c.expectMsg(t, "当前该法术书的等级已经无法精修出更好的附加效果了。")
	if p.bookByPos(50).JX != 2 {
		t.Fatalf("jx %d", p.bookByPos(50).JX)
	}
	// 精修吸纳石：退回两次精修的全部元素（2 + 9），精修次数清零
	e2, _ := jingxiuCost(mi, 2)
	p.mu.Lock()
	p.addItem(jingxiuPullItem, 1)
	p.mu.Unlock()
	before := p.itemCount(10133)
	c.sendExt("ITEM_PTO.C_PULL_OUT_MAGIC_JINGXIU", xym.Msg{"pos": 50, "type": 12101})
	c.until(t, "ITEM_PTO.S_UPDATE_BOOK")
	if p.bookByPos(50).JX != 0 || p.itemCount(10133) != before+elems+e2 || p.itemCount(jingxiuPullItem) != 0 {
		t.Fatalf("pull out: jx %d elems %d", p.bookByPos(50).JX, p.itemCount(10133)-before)
	}
}
