package xymsrv

import (
	"encoding/json"
	"math/rand/v2"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func TestShopRestoresMissingOutOfPoolCard(t *testing.T) {
	var typ int32
	for id, info := range gamedata.Pets {
		if info.Star == 5 && len(info.Pools) == 0 && !xlExcluded(id) && (typ == 0 || id < typ) {
			typ = id
		}
	}
	if typ == 0 {
		t.Fatal("no out-of-pool test card")
	}
	c, p, srv := actWorld(t, "shoprestore")
	p.mu.Lock()
	p.markOwned(typ)
	x := p.xianling(rand.New(rand.NewPCG(1, 2)))
	x.Contribute = 10000
	x.YunPeriod = xlPeriodNow()
	x.Yun = []yyGoods{{RType: rtCard, Pet: typ, Num: 1, Price: yyCardPrice[petStar(typ)]}}
	p.mu.Unlock()
	before := srv.roles.Get(p.UID).SoulExp
	c.sendExt("ETC_PTO.C_YUNYOU_SHOP_BUY_GOODS", xym.Msg{"pos": 0, "type": rtCard, "goods_id": typ})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	p.mu.Lock()
	pet := p.petByType(typ)
	if pet == nil || x.Spent != yyCardPrice[petStar(typ)] || !x.Yun[0].Sold {
		t.Fatal("paid card did not reach inventory")
	}
	p.mu.Unlock()
	if got["PET_PTO.S_UPDATE_PET"] == nil || got["PET_PTO.S_PET_LIST"] == nil || srv.roles.Get(p.UID).SoulExp != before {
		t.Fatal("restored card was converted or not sent to the client")
	}
	data, err := p.st.LoadPlayer(p.UID)
	var saved Player
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.petByType(typ) == nil {
		t.Fatal("restored card was not persisted")
	}
}

func TestIntentionalCardOverflowCanBeClaimed(t *testing.T) {
	c, p, srv := actWorld(t, "restoremails")
	const typ int32 = 15031
	p.mu.Lock()
	p.markOwned(typ)
	p.BagLimit[bagPet] = len(p.Pets)
	p.addSoul(typ, 20, &bagDelta{})
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_SOUL_TO_PET", xym.Msg{"type": typ, "no_update": 0})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	p.mu.Lock()
	if p.petByType(typ) != nil || p.Souls[typ] != 0 {
		t.Fatal("unexpected full-bag synthesis state")
	}
	p.BagLimit[bagPet]++
	p.mu.Unlock()
	srv.social.mu.Lock()
	if len(srv.social.Mails[p.UID]) != 1 {
		t.Fatal("card overflow was not mailed")
	}
	srv.social.mu.Unlock()
	c.sendExt("MAIL_PTO.C_CLICK_NEW_MAIL_REWARD", xym.Msg{"idx": 0})
	c.until(t, "MAIL_PTO.S_CLICK_NEW_MAIL_REWARD_SUCC")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.petByType(typ) == nil {
		t.Fatal("claimed historical card disappeared again")
	}
}

func TestRecruitRestoresHistoricalBase(t *testing.T) {
	p := newPlayer(1)
	p.markOwned(15031)
	if p.dupSoulGain(15031) != 0 {
		t.Fatal("missing base must not also award conversion soul")
	}
	pet := p.gainPet(15031)
	if pet == nil || pet.TLAdd != 0 || p.OwnedCopies[15031] != 2 {
		t.Fatal("historical base and duplicate count were not restored")
	}
}
