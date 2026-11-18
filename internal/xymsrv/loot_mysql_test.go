package xymsrv

import (
	"encoding/json"
	"math/rand/v2"
	"net"
	"os"
	"testing"

	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// Explicit opt-in only; run against an empty, disposable schema.
func TestLootMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("XYM_LOOT_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set XYM_LOOT_TEST_MYSQL_DSN to an empty disposable schema")
	}
	st, err := store.OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := st.LoadAccounts()
	if err != nil || len(accounts) != 0 {
		st.Close()
		t.Fatal("loot test schema must be empty")
	}
	a, err := OpenAccounts(st)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	r, err := OpenRoles(st)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	srv := NewServer(DefaultConfig(), a, r, OpenPlayers(st))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Shutdown(); st.Close() })
	c := enterWorld(t, ln.Addr().String(), "lootcheck")
	acc, err := a.Login("lootcheck", "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := srv.players.Get(acc.UID)
	if err != nil {
		t.Fatal(err)
	}
	const typ int32 = 15031
	for round := 0; round < 2; round++ {
		p.mu.Lock()
		if old := p.petByType(typ); old != nil {
			p.removePet(old)
		}
		p.markOwned(typ)
		p.addSoul(typ, 20, &bagDelta{})
		if err := p.save(); err != nil {
			t.Fatal(err)
		}
		p.mu.Unlock()
		c.sendExt("PET_PTO.C_SOUL_TO_PET", xym.Msg{"type": typ, "no_update": 0})
		c.until(t, "ETC_PTO.S_REWARD_MSG")
		data, err := st.LoadPlayer(p.UID)
		var restored Player
		if err != nil || json.Unmarshal(data, &restored) != nil || restored.petByType(typ) == nil {
			t.Fatal("MySQL synthesis lost the restored card")
		}
	}
	p.mu.Lock()
	p.removePet(p.petByType(typ))
	x := p.xianling(rand.New(rand.NewPCG(1, 2)))
	x.Contribute = 10000
	x.YunPeriod = xlPeriodNow()
	x.Yun = []yyGoods{{RType: rtCard, Pet: typ, Num: 1, Price: yyCardPrice[petStar(typ)]}}
	if err := p.save(); err != nil {
		t.Fatal(err)
	}
	p.mu.Unlock()
	c.sendExt("ETC_PTO.C_YUNYOU_SHOP_BUY_GOODS", xym.Msg{"pos": 0, "type": rtCard, "goods_id": typ})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	p.mu.Lock()
	if p.petByType(typ) == nil {
		t.Fatal("MySQL shop purchase lost card")
	}
	p.addItem(10002, 1000)
	p.addItem(10003, 1000)
	p.BagLimit[bagSuit] = len(p.Suits)
	p.BagLimit[bagBook] = len(p.Books)
	var pos int
	for _, it := range p.Items {
		if it.Type == 10002 {
			pos = it.Pos
			break
		}
	}
	if err := p.save(); err != nil {
		t.Fatal(err)
	}
	p.mu.Unlock()
	c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": pos, "type": 10002, "use_num": 1000, "choose": 0, "usetype": 0})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	if num(got["ETC_PTO.S_REWARD_MSG"], "type") != boxNormal {
		t.Fatal("bulk box response not aggregated")
	}
	p.mu.Lock()
	if p.itemCount(10003) != 0 {
		t.Fatal("MySQL bulk box key accounting failed")
	}
	p.BagLimit[bagSuit] = 1000
	p.BagLimit[bagBook] = 1000
	p.mu.Unlock()
	c.sendExt("MAIL_PTO.C_CLICK_NEW_MAIL_REWARD", xym.Msg{"idx": 0})
	c.until(t, "MAIL_PTO.S_CLICK_NEW_MAIL_REWARD_SUCC")
	data, err := st.LoadPlayer(p.UID)
	var restored Player
	if err != nil || json.Unmarshal(data, &restored) != nil || restored.petByType(typ) == nil || len(restored.Suits)+len(restored.Books) == 0 {
		t.Fatal("MySQL loot/overflow claim did not persist")
	}
	t.Log("MySQL: historical-card synthesis twice, shop restore, 1000 boxes, overflow mail claim and reload passed")
}
