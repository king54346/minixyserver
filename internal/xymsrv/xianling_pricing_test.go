package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func TestXianlingRefreshPriceProtocol(t *testing.T) {
	for _, yun := range []bool{false, true} {
		t.Run(fmt.Sprint(yun), func(t *testing.T) {
			c, p, srv := actWorld(t, "rollbackshop")
			srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 100; return true })
			p.mu.Lock()
			p.addItem(11460, 3)
			p.mu.Unlock()
			open, refresh, reply, free := "ETC_PTO.C_OPEN_XIANLING_SHOP", "ETC_PTO.C_XIANLING_SHOP_REFRESH_GOODS_LIST", "ETC_PTO.S_SEND_XIANLING_SHOP_INFO", xlFreeRefresh
			if yun {
				open, refresh, reply, free = "ETC_PTO.C_YUNYOU_SHOP_OPEN", "ETC_PTO.C_YUNYOU_SHOP_REFRESH_GOODS_LIST", "ETC_PTO.S_SEND_YUNYOU_SHOP_INFO", yyFreeRefresh
			}
			c.sendExt(open, nil)
			c.until(t, reply)
			for range free {
				c.sendExt(refresh, nil)
				c.until(t, reply)
			}
			if srv.roles.Get(p.UID).Gold != 100 {
				t.Fatal("free quota charged")
			}
			for i := range 3 {
				c.sendExt(refresh, nil)
				m := c.until(t, reply)[reply] // The historical action replies directly, without a new ask box.
				if num(m, "refresh_gold") != 20 || srv.roles.Get(p.UID).Gold != int32(80-i*20) {
					t.Fatal("historical fixed fee not restored", m)
				}
			}
			p.mu.Lock()
			tokens := p.itemCount(11460)
			p.mu.Unlock()
			if tokens != 3 {
				t.Fatal("rollback consumed refresh tokens")
			}
			srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 0; return true })
			c.sendExt(refresh, nil)
			c.expectMsg(t, "元宝不足")
			c.expectExt(reply)
		})
	}
}

func TestXianlingMaterialAndCardPrices(t *testing.T) {
	c, p, srv := actWorld(t, "rollbackmat")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 1000; return true })
	for _, tc := range []struct {
		star int
		cost int32
	}{{3, 50}, {4, 300}} {
		id := gamedata.BooksByStar[tc.star][0]
		p.mu.Lock()
		x := p.xianling(rand.New(rand.NewPCG(1, 2)))
		x.Goods[0].Need = []xlNeed{{Type: int(id), Kind: "magic"}}
		p.mu.Unlock()
		before := srv.roles.Get(p.UID).Gold
		c.sendExt("ETC_PTO.C_XIANLING_SHOP_IGNORE_STUFF", xym.Msg{"pos": 0, "type": id, "stuff_kind": "magic"})
		c.until(t, "ETC_PTO.S_SEND_XIANLING_SHOP_INFO")
		if srv.roles.Get(p.UID).Gold != before-tc.cost {
			t.Fatal("material price not restored", tc)
		}
	}
	p.mu.Lock()
	x := p.yunyou(rand.New(rand.NewPCG(1, 2)))
	x.Contribute = 10000
	x.Spent = 0
	p.mu.Unlock()
	if x.Yun[0].Price != 3000 || x.Yun[1].Price != 1000 || yySoulPrice[4] != 100 || yySoulPrice[5] != 240 {
		t.Fatal("fresh card prices not restored")
	}
	card := x.Yun[0]
	c.sendExt("ETC_PTO.C_YUNYOU_SHOP_BUY_GOODS", xym.Msg{"pos": 0, "type": rtCard, "goods_id": card.Pet})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	p.mu.Lock()
	spent, sold := x.Spent, x.Yun[0].Sold
	p.mu.Unlock()
	if spent != 3000 || !sold {
		t.Fatal("card purchase did not use historical contribution price", spent, sold)
	}
}
