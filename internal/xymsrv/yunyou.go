package xymsrv

import (
	"math/rand/v2"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 云游商人（仙灵店铺第二页，ETC_PTO.C_YUNYOU_SHOP_OPEN / C_YUNYOU_SHOP_BUY_GOODS / C_YUNYOU_SHOP_REFRESH_GOODS_LIST）：
// 用仙灵店铺攒下的贡献兑换稀有护法魂魄或护法。
//
// 依据：view/XianLingMainView.lua CXianLingYunView / CXianLingYunItemView（S_SEND_YUNYOU_SHOP_INFO{refresh_gold,
// left_refresh, money 仙灵贡献, left_time, list[type rtype, state 0 可买 / 1 售罄, goods_id, cash 银子价, money 贡献价, num]}；
// 列表 pos 为 0 起的下标；cash > 0 时显示银子价，否则显示「贡献N」；确认后 C_YUNYOU_SHOP_BUY_GOODS{pos, type, goods_id}）。
//
// 估计（无抓包）：9 件货——1 张 5 星护法、2 张 4 星护法、3 份 5 星魂魄（其一是当前热点护法）、3 份 4 星魂魄 ×2；
// 价格 5 星护法 1500、4 星护法 500、5 星魂魄 120、4 星魂魄 50 / 个；花贡献不降称号（称号按累计贡献）；
// 和仙灵店铺同一个 3 小时周期自动换货；每天免费刷新 5 次，之后每次 20 元宝。

const (
	yyFreeRefresh = 5
	yyRefreshGold = 20
)

var (
	yyCardPrice = map[int]int32{4: 500, 5: 1500}
	yySoulPrice = map[int]int32{4: 50, 5: 120}
)

type yyGoods struct {
	RType int   `json:"rtype"` // rtCard / rtSoul
	Pet   int32 `json:"pet"`
	Num   int32 `json:"num"`
	Price int32 `json:"price"`
	Sold  bool  `json:"sold,omitempty"`
}

func yyRandPet(rng *rand.Rand, star int) int32 {
	l := gamedata.PetsByStar[star]
	return l[rng.IntN(len(l))]
}

func yyNewGoods(rng *rand.Rand, hot int32) []yyGoods {
	card := func(star int) yyGoods {
		return yyGoods{RType: rtCard, Pet: yyRandPet(rng, star), Num: 1, Price: yyCardPrice[star]}
	}
	soul := func(pet int32, n int32) yyGoods {
		return yyGoods{RType: rtSoul, Pet: pet, Num: n, Price: yySoulPrice[petStar(pet)] * n}
	}
	return []yyGoods{
		card(5), card(4), card(4),
		soul(hot, 1), soul(yyRandPet(rng, 5), 1), soul(yyRandPet(rng, 5), 1),
		soul(yyRandPet(rng, 4), 2), soul(yyRandPet(rng, 4), 2), soul(yyRandPet(rng, 4), 2),
	}
}

func (p *Player) yunyou(rng *rand.Rand) *XianlingState {
	x := p.xianling(rng)
	if now := xlPeriodNow(); x.YunPeriod != now || len(x.Yun) == 0 {
		x.YunPeriod = now
		x.Yun = yyNewGoods(rng, x.Hot)
	}
	return x
}

func (x *XianlingState) money() int32 { return x.Contribute - x.Spent }

func (x *XianlingState) yunMsg() xym.Msg {
	list := []xym.Msg{}
	for _, g := range x.Yun {
		list = append(list, xym.Msg{"type": g.RType, "state": b2i(g.Sold), "goods_id": g.Pet, "cash": 0, "money": g.Price, "num": g.Num})
	}
	return xym.Msg{"refresh_gold": yyRefreshGold, "left_refresh": max(0, yyFreeRefresh-x.YunRefreshed),
		"money": x.money(), "left_time": xlLeftTime(), "list": list}
}

func init() {
	Handle("ETC_PTO.C_YUNYOU_SHOP_OPEN", func(s *Session, p *Player, m xym.Msg) {
		x := p.yunyou(s.actRng())
		p.saveLog(s)
		s.SendExt("ETC_PTO.S_SEND_YUNYOU_SHOP_INFO", x.yunMsg())
	})
	Handle("ETC_PTO.C_YUNYOU_SHOP_REFRESH_GOODS_LIST", onYYRefresh)
	Handle("ETC_PTO.C_YUNYOU_SHOP_BUY_GOODS", onYYBuy)
}

func onYYRefresh(s *Session, p *Player, m xym.Msg) {
	rng := s.actRng()
	x := p.yunyou(rng)
	if x.YunRefreshed >= yyFreeRefresh && !s.spend(0, yyRefreshGold) {
		s.SendExt("ETC_PTO.S_SEND_YUNYOU_SHOP_INFO", x.yunMsg())
		return
	}
	x.YunRefreshed++
	x.Yun = yyNewGoods(rng, x.Hot)
	p.saveLog(s)
	s.SendExt("ETC_PTO.S_SEND_YUNYOU_SHOP_INFO", x.yunMsg())
}

// onYYBuy：扣贡献，发护法 / 魂魄，该格标为售罄。
func onYYBuy(s *Session, p *Player, m xym.Msg) {
	x := p.yunyou(s.actRng())
	pos := int(m["pos"].(int64))
	reply := func() { s.SendExt("ETC_PTO.S_SEND_YUNYOU_SHOP_INFO", x.yunMsg()) }
	if pos < 0 || pos >= len(x.Yun) || x.Yun[pos].Sold || int32(m["goods_id"].(int64)) != x.Yun[pos].Pet {
		s.sendMsg("这件货已经卖完了。")
		reply()
		return
	}
	g := &x.Yun[pos]
	if x.money() < g.Price {
		s.sendMsg("仙灵贡献不足。")
		reply()
		return
	}
	if g.RType == rtCard && len(p.Pets) >= p.BagLimit[bagPet] && p.petByType(g.Pet) == nil {
		s.sendMsg("护法背包已满。")
		reply()
		return
	}
	d := &bagDelta{}
	if g.RType == rtCard {
		p.giveCard(g.Pet, d)
	} else {
		p.addSoul(g.Pet, g.Num, d)
	}
	x.Spent += g.Price
	g.Sold = true
	p.saveLog(s)
	list := s.sendDelta(p, d)
	reply()
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, list))
}
