package xymsrv

import (
	"math/rand/v2"
	"strconv"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 洞府仙灵铺（福利页「仙灵铺」→ view/PartyXianLingView.lua）：每天正午 12:00 上架 num + extra_num 件货（护法），
// 成员用个人灵气或元宝（灵气 ×6）竞拍，出价至少比当前高 ⌊底价/5⌋、不超过底价 3 倍；倒计时结束最高价者得。
// 未售出的货物清除，每件补给洞府 10×洞府等级 元宝；元宝成交时一部分汇入洞府元宝。管理员可花洞府元宝
// （每件 20×洞府等级）给下一次补货（C_ADD_EXTRA_DONGFU_SHOP_GOODS_NUM{full 0 一件 / 1 补满}）。
//
// 抓包（session-01）：S_SEND_DONGFU_SHOP_GOODS_LIST{num 4, extra_num 0, add_num 10, lingqi 个人灵气,
// list[type 1, goods_id 护法号, gold = 灵气 ×6, lingqi, bid_lingqi 0, bid_left_time -1, bid_member_num 0, is_invade 0]}；
// 底价按星级：3 星 20、4 星 60、5 星 150 灵气（24021 → 60、35037 → 150、43016 / 13020 → 20）。
//
// 估计：竞拍倒计时从第一次出价起 5 分钟；元宝成交价的一半汇入洞府元宝；被超过的出价退回（灵气记在洞府账上，
// 下次打开福利时到账；元宝直接退）；拍到的护法用邮件寄出；补货每天最多 10 件。

const (
	dfShopBase     = 4
	dfShopMaxExtra = 10
	dfBidSeconds   = 300
)

var dfShopPrice = map[int]int32{1: 10, 2: 15, 3: 20, 4: 60, 5: 150}

type DFShop struct {
	Day     string          `json:"day"` // 上一次正午刷新的日期
	Goods   []*DFGood       `json:"goods,omitempty"`
	Extra   int             `json:"extra,omitempty"`  // 下次刷新的额外件数
	Refund  map[int32]int32 `json:"refund,omitempty"` // 待退还的个人灵气
	History []DFBuy         `json:"history,omitempty"`
}

type DFGood struct {
	Goods   int32   `json:"goods"`
	Lingqi  int32   `json:"lingqi"`
	BidUID  int32   `json:"bid_uid,omitempty"`
	BidLQ   int32   `json:"bid_lq,omitempty"`   // 当前最高价（灵气计）
	BidGold bool    `json:"bid_gold,omitempty"` // 最高价是元宝出的
	BidEnd  int64   `json:"bid_end,omitempty"`
	Bidders []int32 `json:"bidders,omitempty"`
	Sold    bool    `json:"sold,omitempty"`
}

type DFBuy struct {
	UID    int32 `json:"uid"`
	Goods  int32 `json:"goods"`
	Lingqi int32 `json:"lingqi"`
	Gold   int32 `json:"gold"`
}

// shopDay 是当前所属的刷新日（正午前算前一天）。
func shopDay(now time.Time) string {
	if now.Hour() < 12 {
		now = now.AddDate(0, 0, -1)
	}
	return now.Format("2006-01-02")
}

// shop 结算到期竞拍、按需刷新货架。调用方持有洞府锁。
func (srv *Server) dfShop(df *Dongfu) *DFShop {
	if df.Shop == nil {
		df.Shop = &DFShop{}
	}
	sh := df.Shop
	if sh.Refund == nil {
		sh.Refund = map[int32]int32{}
	}
	srv.settleBids(df)
	if d := shopDay(time.Now()); sh.Day != d {
		for _, g := range sh.Goods {
			if !g.Sold {
				df.Gold += 10 * df.Lv
			}
		}
		sh.Day, sh.Goods = d, nil
		rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(df.ID)))
		for range dfShopBase + sh.Extra {
			star := 3 + rng.IntN(3)
			if l := gamedata.PetsByStar[star]; len(l) > 0 {
				t := l[rng.IntN(len(l))]
				sh.Goods = append(sh.Goods, &DFGood{Goods: t, Lingqi: dfShopPrice[star]})
			}
		}
		sh.Extra = 0
	}
	return sh
}

// settleBids：倒计时结束的竞拍成交，护法用邮件寄给得主。
func (srv *Server) settleBids(df *Dongfu) {
	now := nowUnix()
	for _, g := range df.Shop.Goods {
		if g.Sold || g.BidUID == 0 || g.BidEnd > now {
			continue
		}
		g.Sold = true
		gold := int32(0)
		if g.BidGold {
			gold = g.BidLQ * 6
			df.Gold += gold / 2
		}
		df.Shop.History = append(df.Shop.History, DFBuy{UID: g.BidUID, Goods: g.Goods, Lingqi: g.BidLQ, Gold: gold})
		if n := len(df.Shop.History); n > 20 {
			df.Shop.History = df.Shop.History[n-20:]
		}
		name := ""
		if pi := gamedata.Pets[g.Goods]; pi != nil {
			name = pi.Name
		}
		srv.SendMail(g.BidUID, MailSpec{Title: "仙灵铺竞拍", Text: "恭喜你在洞府仙灵铺拍得护法「" + name + "」。",
			Items: map[string]int32{"DZ" + strconv.Itoa(int(g.Goods)): 1}})
	}
}

func (s *Session) dfShopMsg(p *Player, df *Dongfu) xym.Msg {
	sh := s.srv.dfShop(df)
	p.dfRefund(df)
	now := nowUnix()
	list := []xym.Msg{}
	for _, g := range sh.Goods {
		if g.Sold {
			continue
		}
		left := int64(-1)
		bid := int32(0)
		if g.BidUID != 0 {
			left, bid = max(0, g.BidEnd-now), g.BidLQ
		}
		list = append(list, xym.Msg{"type": 1, "goods_id": g.Goods, "gold": g.Lingqi * 6, "lingqi": g.Lingqi, "bid_lingqi": bid,
			"bid_left_time": left, "bid_member_num": len(g.Bidders), "is_invade": 0})
	}
	hist := []xym.Msg{}
	for i := len(sh.History) - 1; i >= 0; i-- {
		h := sh.History[i]
		name, icon, _ := s.srv.roleBrief(h.UID)
		hist = append(hist, xym.Msg{"icon": icon, "name": name, "type": 1, "goods_id": h.Goods, "gold": h.Gold, "lingqi": h.Lingqi})
	}
	return xym.Msg{"num": dfShopBase, "extra_num": sh.Extra, "add_num": dfShopMaxExtra - sh.Extra, "lingqi": p.df().Lingqi,
		"list": list, "buy_history": hist}
}

// dfRefund 把被超过的灵气出价退回个人灵气。
func (p *Player) dfRefund(df *Dongfu) {
	if df.Shop == nil {
		return
	}
	if n := df.Shop.Refund[p.UID]; n > 0 {
		p.df().Lingqi += n
		delete(df.Shop.Refund, p.UID)
	}
}

// findGood 是第 pos 件（1 起，按未售出的顺序）且号码一致的货。
func (sh *DFShop) findGood(pos int, goods int32) *DFGood {
	i := 0
	for _, g := range sh.Goods {
		if g.Sold {
			continue
		}
		i++
		if i == pos && g.Goods == goods {
			return g
		}
	}
	for _, g := range sh.Goods { // 位置对不上时按号码找
		if !g.Sold && g.Goods == goods {
			return g
		}
	}
	return nil
}

func onBidDongfuGoods(s *Session, p *Player, m xym.Msg) {
	pos, goods := int(m["pos"].(int64)), int32(m["goods_id"].(int64))
	lq, gold := int32(m["lingqi"].(int64)), int32(m["gold"].(int64))
	var refundGold struct {
		uid int32
		n   int32
	}
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		sh := s.srv.dfShop(df)
		p.dfRefund(df)
		g := sh.findGood(pos, goods)
		price := lq
		byGold := gold > 0
		if byGold {
			price = gold / 6
		}
		ok := false
		switch {
		case g == nil:
			s.sendMsg("该货物已经售出。")
		case g.BidUID == s.role.UID:
			s.sendMsg("你已经是最高出价者了。")
		case price < g.Lingqi || g.BidUID != 0 && price < g.BidLQ+max(1, g.Lingqi/5) || price > g.Lingqi*3:
			s.sendMsg("出价不符合要求。")
		case !byGold && p.df().Lingqi < price:
			s.sendMsg("灵气不足。")
		case byGold && !s.spend(0, price*6):
		default:
			if !byGold {
				p.df().Lingqi -= price
			}
			if g.BidUID != 0 { // 退还上一位
				if g.BidGold {
					refundGold.uid, refundGold.n = g.BidUID, g.BidLQ*6
				} else {
					sh.Refund[g.BidUID] += g.BidLQ
				}
			} else {
				g.BidEnd = nowUnix() + dfBidSeconds
			}
			g.BidUID, g.BidLQ, g.BidGold = s.role.UID, price, byGold
			g.Bidders = addUniq(g.Bidders, s.role.UID)
			p.saveLog(s)
			ok = true
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_SHOP_GOODS_LIST", s.dfShopMsg(p, df))
		return ok
	})
	if refundGold.uid != 0 {
		ro, err := s.srv.roles.Update(refundGold.uid, func(ro *Role) bool { ro.Gold += refundGold.n; return true })
		if err == nil {
			s.srv.notify(refundGold.uid, func(t *Session) {
				t.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "gold", "val": ro.Gold}}})
				t.sendMsg("你在仙灵铺的出价被超过了，元宝已退回。")
			})
		}
	}
}

// onBuyDongfuGoods：没人出价的货按底价（灵气）直接买下。
func onBuyDongfuGoods(s *Session, p *Player, m xym.Msg) {
	pos, goods := int(m["pos"].(int64)), int32(m["goods_id"].(int64))
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		sh := s.srv.dfShop(df)
		p.dfRefund(df)
		g := sh.findGood(pos, goods)
		ok := false
		switch {
		case g == nil:
			s.sendMsg("该货物已经售出。")
		case g.BidUID != 0:
			s.sendMsg("该货物正在竞拍中。")
		case p.df().Lingqi < g.Lingqi:
			s.sendMsg("灵气不足。")
		default:
			p.df().Lingqi -= g.Lingqi
			g.BidUID, g.BidLQ, g.BidEnd = s.role.UID, g.Lingqi, nowUnix()
			s.srv.settleBids(df)
			p.saveLog(s)
			ok = true
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_SHOP_GOODS_LIST", s.dfShopMsg(p, df))
		return ok
	})
}

func onAddShopGoods(s *Session, p *Player, m xym.Msg) {
	full := m["full"].(int64) != 0
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		sh := s.srv.dfShop(df)
		n := 1
		if full {
			n = dfShopMaxExtra - sh.Extra
		}
		cost := int32(n) * 20 * df.Lv
		ok := false
		switch {
		case !isAdmin(me.Position):
			s.sendMsg("只有府主和洞主可以补货。")
		case n <= 0 || sh.Extra+n > dfShopMaxExtra:
			s.sendMsg("补货已达上限。")
		case df.Gold < cost:
			s.sendMsg("洞府元宝不足。")
		default:
			df.Gold -= cost
			sh.Extra += n
			ok = true
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_SHOP_GOODS_LIST", s.dfShopMsg(p, df))
		return ok
	})
}

func init() {
	Handle("DONGFU_PTO.C_GET_DONGFU_SHOP_GOODS_LIST", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			p.syncDF(df, me)
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_SHOP_GOODS_LIST", s.dfShopMsg(p, df))
			p.saveLog(s)
			return true
		})
	})
	Handle("DONGFU_PTO.C_BID_DONGFU_GOODS", onBidDongfuGoods)
	Handle("DONGFU_PTO.C_BUY_DONGFU_GOODS", onBuyDongfuGoods)
	Handle("DONGFU_PTO.C_ADD_EXTRA_DONGFU_SHOP_GOODS_NUM", onAddShopGoods)
}
