package xymsrv

import (
	"slices"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 元神化形（护法 → 图鉴 / 化形 → view/HuaxingView.lua、view/HuaxingAutoView.lua）：
//
//   - 开启：金卡（五星护法，含同名卡）累计达到 30 张（CardSetView.NEED_GOLD_CARD_COUNT；登录 S_ATTR_UPDATE gold_card）。
//   - 转为元神：在登峰造极 / 每日推荐里抽到已有的五星护法（同名卡）时，奖励界面有「转为元神」→
//     CARD_SET_PTO.C_ON_TRANSFER_TO_YUANSHEN{type}（带锁等 S_SEND_YUANSHEN_OK{type, ok}），这张同名卡变成 40 元神。
//     奖励界面只在 S_REWARD_MSG.type 为 YUANBAO_SHOWCARD 16 / YUANBAO_PET_TEN 17 时给这个按钮（h_mall.go）。
//   - 自动转化：C_OPEN_AUTO_TRANSFORM_YUANSHEN → S_OPEN_AUTO_HUAXING{pet[type]}，点一个护法发
//     C_SET_AUTO_TRANSFORM_YUANSHEN{type} 切换勾选（不回包）；之后在上述两个池子抽到勾选护法的同名卡直接转 40 元神，
//     列在 S_REWARD_MSG.huaxing（界面「自动转为 N 点元神」）。
//   - 化形：C_GET_HUAXING_INFO → S_SEND_HUAXING_INFO{yuanshen, soul 魂值, list[type, left_time, cost, soul_cost, times]}
//     列出已有的五星护法；C_ON_HUAXING{type} 花元神和魂值得一张同名卡，客户端等 S_SEND_HUAXING_INFO。
//     价格照帮助文字：稀有 5 星第 1–5 次 150/300/400/500/500 元神，普通 100/200/300/400/500；魂值 1000–5000；
//     5 次以后按第 5 次算。
//
// 本服同名卡并入已有护法（TLAdd，pet_talent.go），所以「转为元神」是把刚并入的一张拆出来：TLAdd −1、扣回它带来的神通点。

const (
	yuanshenPerCard  = 40
	huaxingGoldCards = 30
)

var (
	huaxingCostRare   = []int{150, 300, 400, 500, 500}
	huaxingCostNormal = []int{100, 200, 300, 400, 500}
	huaxingSoulCost   = []int32{1000, 2000, 3000, 4000, 5000}
)

func huaxingCost(typ int32, times int) (yuanshen int, soul int32) {
	i := min(times, len(huaxingSoulCost)-1)
	if pi := gamedata.Pets[typ]; pi != nil && pi.Special {
		return huaxingCostRare[i], huaxingSoulCost[i]
	}
	return huaxingCostNormal[i], huaxingSoulCost[i]
}

func (p *Player) huaxingOpen() bool { return p.goldPets() >= huaxingGoldCards }

func (s *Session) huaxingInfo(p *Player) xym.Msg {
	list := []xym.Msg{}
	for _, pet := range p.sortedPets() {
		if pi := gamedata.Pets[pet.Type]; pi == nil || pi.Star != 5 {
			continue
		}
		times := p.HuaxingTimes[pet.Type]
		cost, soul := huaxingCost(pet.Type, times)
		list = append(list, xym.Msg{"type": pet.Type, "left_time": 0, "cost": cost, "soul_cost": soul, "times": times})
	}
	return xym.Msg{"yuanshen": p.Yuanshen, "soul": s.role.SoulExp, "list": list}
}

func init() {
	Handle("CARD_SET_PTO.C_GET_HUAXING_INFO", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("CARD_SET_PTO.S_SEND_HUAXING_INFO", s.huaxingInfo(p))
	})
	Handle("CARD_SET_PTO.C_ON_HUAXING", onHuaxing)
	Handle("CARD_SET_PTO.C_ON_TRANSFER_TO_YUANSHEN", onTransferYuanshen)
	Handle("CARD_SET_PTO.C_OPEN_AUTO_TRANSFORM_YUANSHEN", func(s *Session, p *Player, m xym.Msg) {
		l := []xym.Msg{}
		for _, t := range p.AutoYuanshen {
			l = append(l, xym.Msg{"type": t})
		}
		s.SendExt("CARD_SET_PTO.S_OPEN_AUTO_HUAXING", xym.Msg{"pet": l})
	})
	Handle("CARD_SET_PTO.C_SET_AUTO_TRANSFORM_YUANSHEN", func(s *Session, p *Player, m xym.Msg) {
		t := int32(m["type"].(int64))
		if i := slices.Index(p.AutoYuanshen, t); i >= 0 {
			p.AutoYuanshen = slices.Delete(p.AutoYuanshen, i, i+1)
		} else if pi := gamedata.Pets[t]; pi != nil && pi.Star == 5 {
			p.AutoYuanshen = append(p.AutoYuanshen, t)
		}
		p.saveLog(s)
	})
}

func onHuaxing(s *Session, p *Player, m xym.Msg) {
	typ := int32(m["type"].(int64))
	reply := func() { s.SendExt("CARD_SET_PTO.S_SEND_HUAXING_INFO", s.huaxingInfo(p)) }
	pet := p.petByType(typ)
	switch {
	case !p.huaxingOpen():
		s.retMsg("元神化形尚未开启，快去努力收集金卡开启元神化形吧！")
		reply()
		return
	case pet == nil || gamedata.Pets[typ] == nil || gamedata.Pets[typ].Star != 5:
		s.retMsg("只能化形已有的五星护法。")
		reply()
		return
	}
	cost, soul := huaxingCost(typ, p.HuaxingTimes[typ])
	if p.Yuanshen < cost {
		s.retMsg("元神不足，无法化形")
		reply()
		return
	}
	if !s.roleUpdate(func(ro *Role) bool {
		if ro.SoulExp < soul {
			return false
		}
		ro.SoulExp -= soul
		return true
	}, "soulexp") {
		s.retMsg("魂值不足，无法化形")
		reply()
		return
	}
	p.Yuanshen -= cost
	if p.HuaxingTimes == nil {
		p.HuaxingTimes = map[int32]int{}
	}
	p.HuaxingTimes[typ]++
	d := &bagDelta{}
	p.giveCard(typ, d)
	p.saveLog(s)
	list := s.sendDelta(p, d)
	reply()
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(6, list)) // BOX_SHOWCARD
}

// onTransferYuanshen：把刚抽到并入的一张五星同名卡转成 40 元神。
func onTransferYuanshen(s *Session, p *Player, m xym.Msg) {
	typ := int32(m["type"].(int64))
	pet := p.petByType(typ)
	ok := p.huaxingOpen() && pet != nil && pet.TLAdd > 0 && gamedata.Pets[typ] != nil && gamedata.Pets[typ].Star == 5
	if ok {
		pet.TLAdd--
		pet.TLPoint = max(pet.TLPoint-shenTongPoint[5], 0)
		p.Yuanshen += yuanshenPerCard
		p.saveLog(s)
		s.petUpdate(p, pet)
	}
	s.SendExt("CARD_SET_PTO.S_SEND_YUANSHEN_OK", xym.Msg{"type": typ, "ok": b2i(ok)})
}

// autoYuanshen：抽卡时抽到勾选了自动转化的五星护法的同名卡，直接转成元神（返回 true 表示已转，不再发卡）。
func (p *Player) autoYuanshen(typ int32) bool {
	pi := gamedata.Pets[typ]
	if pi == nil || pi.Star != 5 || !p.huaxingOpen() || !slices.Contains(p.AutoYuanshen, typ) || p.petByType(typ) == nil {
		return false
	}
	p.Yuanshen += yuanshenPerCard
	return true
}
