package xymsrv

import (
	"fmt"
	"slices"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 招募页的周边：道具商城（元宝买请神符等）、请神符获取途径、每日推荐卡池切换、寻英帖。
//
// 依据：view/ShopZhaomuView.lua（没有请神符时发 C_OPEN_ZHAOMU_DAOJU_WAY，弹窗里「元宝购买」走 C_OPEN_ITEM_MALL →
// C_BUY_ITEM；登峰造极 300、每日推荐 350 元宝一张）；view/XuncaitieView.lua（卡池切换 st 2 使用中 / 0 花 300 元宝、
// 「X 后将自动切换到其他卡池」；寻英帖：登峰造极 5 个、每日推荐每个种族 5 个，「招募到对应的星级护法时，会有更高的
// 几率获得寻英帖中的护法」）；logic/shop_mgr.lua PetShopInfo（每日推荐「可获得指定护法」）。
// 抓包 session-01：S_OPEN_DAOJU_SHANGCHENG 21 件商品与价格；C_BUY_ITEM 回 S_UPDATE_ITEM、S_ATTR_UPDATE{gold}、
// S_REWARD_MSG{type 1}、S_OPEN_DAOJU_SHANGCHENG。
//
// 估计：寻英帖命中率 50%（出 5 星时一半概率从所选里出）；卡池切换当天有效，次日回到当天默认种族；
// 道具商城除抓包里标了每日限购 22 次的 10257 外不限购。

const (
	wishPct      = 50
	raceSwitch   = 300 // 元宝
	wishMax      = 5
	itemCostGold = 1 // S_OPEN_ITEM_MALL.costtype：元宝
)

// shopItem 是道具商城的一件商品（抓包 S_OPEN_DAOJU_SHANGCHENG）。
type shopItem struct {
	Type  int
	Price int32
	Limit int // 每日限购（0 = 不限）
}

var itemShop = []shopItem{
	{11153, 500, 0}, {11460, 20, 0}, {11461, 350, 0}, {11462, 60, 0}, {11464, 500, 0}, {10317, 300, 0},
	{10257, 20, 22},
	{10452, 1620, 0},                                                   // 五级宝石袋（hotfix 补的道具，gamedata/hotfix_items.go）
	{10216, 100, 0}, {10217, 100, 0}, {10249, 100, 0}, {10250, 100, 0}, // 凿晶石、炼器石、炼法石、悟法石
	{10417, 500, 0}, {10416, 500, 0}, // 同修石、结缘石（抓包商城里没有，价格估计）
	{10442, 15, 0}, // 阵魂图册（阵法升阶，zhenfa.go；用户 zhenfa.md 藏经阁 15 经书一本，本服没有藏经阁，按 15 元宝卖）
	{10251, 100, 0}, {10310, 100, 0}, {10311, 100, 0}, {10342, 30, 0}, {10140, 20, 0}, {10002, 20, 0},
	{10003, 10, 0}, {10020, 500, 0}, {11152, 500, 0},
}

func raceToday(now time.Time) int { return int(now.Unix()/86400)%4 + 1 }

// race 是每日推荐今天用的种族。
func (m *Mall) race(now time.Time) int {
	if m.Race >= 1 && m.Race <= 4 {
		return m.Race
	}
	return raceToday(now)
}

func raceOnly(byStar map[int][]int32, race int) map[int][]int32 {
	out := map[int][]int32{}
	for st, l := range byStar {
		for _, t := range l {
			if pi := gamedata.Pets[t]; pi != nil && pi.Race == race {
				out[st] = append(out[st], t)
			}
		}
	}
	return out
}

// featuredRace 是每日推荐展示的 5 张卡：所选种族灵仙池的 5、4 星按日期轮换。
func featuredRace(now time.Time, race int) []int32 {
	byStar := raceOnly(poolPets["灵仙请神符"], race)
	l := append(append([]int32(nil), byStar[5]...), byStar[4]...)
	if len(l) == 0 {
		return featured(now, 5)
	}
	out := make([]int32, 5)
	day := int(now.Unix() / 86400)
	for i := range out {
		out[i] = l[(day*7+i*13)%len(l)]
	}
	return out
}

func init() {
	// 现版客户端 shop_mgr.C_OPEN_ITEM_MALL 等的是 S_OPEN_DAOJU_SHANGCHENG（回旧的 S_OPEN_ITEM_MALL 会一直等到超时报错）
	Handle("MALL_PTO.C_OPEN_ITEM_MALL", func(s *Session, p *Player, m xym.Msg) { s.daojuShopMsg(p) })
	Handle("ETC_PTO.C_CLICK_ENTRANCE", onClickEntrance)
	Handle("MALL_PTO.C_OPEN_MALL_TAG", func(s *Session, p *Player, m xym.Msg) { s.mallTag(p, int(m["id"].(int64))) })
	Handle("MALL_PTO.C_BUY_GIFT", onBuyGift)
	Handle("MALL_PTO.C_BUY_ITEM", onBuyShopItem)
	Handle("MALL_PTO.C_OPEN_ZHAOMU_DAOJU_WAY", func(s *Session, p *Player, m xym.Msg) {
		// 礼包途径没有（ways 为空），客户端自己加一行「元宝购买」
		s.SendExt("MALL_PTO.S_OPEN_ZHAOMU_DAOJU_WAY", xym.Msg{"type": m["id"], "ways": []xym.Msg{}})
	})
	Handle("ETC_PTO.C_OPEN_MRTJ_KACHIQIEHUAN", func(s *Session, p *Player, m xym.Msg) { s.raceSwitchMsg(p) })
	Handle("ETC_PTO.C_MRTJ_KACHIQIEHUAN", onRaceSwitch)
	Handle("ETC_PTO.C_OPEN_DFZJ_XUNCAITIE", func(s *Session, p *Player, m xym.Msg) { s.dfzjWishMsg(p) })
	Handle("ETC_PTO.C_CONFIRM_DFZJ_XUNCAITIE", onConfirmDfzj)
	Handle("ETC_PTO.C_MRTJ_XUNCAITIE", func(s *Session, p *Player, m xym.Msg) { s.mrtjWishMsg(p) })
	Handle("ETC_PTO.C_CONFIRM_MRTJ_XUNCAITIE", onConfirmMrtj)
}

func (p *Player) itemShopList() []xym.Msg {
	m := p.mall(time.Now())
	out := make([]xym.Msg, 0, len(itemShop))
	for _, it := range itemShop {
		left, max := -1, 0
		if it.Limit > 0 {
			left, max = it.Limit-m.Bought[it.Type], it.Limit
		}
		out = append(out, xym.Msg{"type": it.Type, "price": it.Price, "costtype": itemCostGold, "avail_times": left,
			"limit_cur": m.Bought[it.Type], "limit_max": max})
	}
	return out
}

// onBuyShopItem：元宝买道具。回包顺序照抓包：背包、S_ATTR_UPDATE{gold}、S_REWARD_MSG{1}、S_OPEN_DAOJU_SHANGCHENG。
func onBuyShopItem(s *Session, p *Player, m xym.Msg) {
	typ, n := int(m["type"].(int64)), int(m["count"].(int64))
	idx := slices.IndexFunc(itemShop, func(x shopItem) bool { return x.Type == typ })
	mall := p.mall(time.Now())
	switch {
	case idx < 0 || n <= 0 || gamedata.Items[typ] == nil:
		s.retMsg("商城里没有这件商品。")
		return
	case itemShop[idx].Limit > 0 && mall.Bought[typ]+n > itemShop[idx].Limit:
		s.retMsg("超出今日限购数量。")
		return
	}
	cost := itemShop[idx].Price * int32(n)
	if !s.roleUpdate(func(ro *Role) bool {
		if ro.Gold < cost {
			return false
		}
		ro.Gold -= cost
		return true
	}) {
		s.retMsg(fmt.Sprintf("元宝不足：需要 %d 元宝。", cost))
		return
	}
	if mall.Bought == nil {
		mall.Bought = map[int]int{}
	}
	mall.Bought[typ] += n
	delta := &bagDelta{items: p.addItem(typ, int32(n))}
	delta.gains = append(delta.gains, gain{rtItem, typ, int32(n)})
	p.saveLog(s)
	list := s.sendDelta(p, delta)
	s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "gold", "val": s.role.Gold}}})
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, list))
	s.daojuShopMsg(p)
}

func (s *Session) daojuShopMsg(p *Player) {
	s.SendExt("MALL_PTO.S_OPEN_DAOJU_SHANGCHENG", xym.Msg{"libao_id": tagDaoju, "items": p.itemShopList()})
}

// raceSwitchMsg：每日推荐的 4 个种族卡池（st 2 使用中、0 花 300 元宝切换；time 是自动轮换的倒计时）。
func (s *Session) raceSwitchMsg(p *Player) {
	now := time.Now()
	cur := p.mall(now).race(now)
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	list := []xym.Msg{}
	for race := 1; race <= 4; race++ {
		st, left := 0, 0
		if race == cur {
			st, left = 2, int(midnight.Sub(now).Seconds())
		}
		list = append(list, xym.Msg{"race": race, "time": left, "st": st})
	}
	s.SendExt("ETC_PTO.S_OPEN_MRTJ_KACHIQIEHUAN", xym.Msg{"list": list})
}

func onRaceSwitch(s *Session, p *Player, m xym.Msg) {
	race := int(m["race"].(int64))
	now := time.Now()
	mall := p.mall(now)
	switch {
	case race < 1 || race > 4:
		return
	case race == mall.race(now):
		s.raceSwitchMsg(p)
		return
	}
	if !s.roleUpdate(func(ro *Role) bool {
		if ro.Gold < raceSwitch {
			return false
		}
		ro.Gold -= raceSwitch
		return true
	}, "gold") {
		s.retMsg(fmt.Sprintf("元宝不足：切换卡池需要 %d 元宝。", raceSwitch))
		return
	}
	mall.Race = race
	p.saveLog(s)
	s.raceSwitchMsg(p)
	s.SendExt("MALL_PTO.S_OPEN_PET_MALL", p.mallMsg(now))
}

func typeList(l []int32) []xym.Msg {
	out := []xym.Msg{}
	for _, t := range l {
		out = append(out, xym.Msg{"type": t})
	}
	return out
}

// wishPick 从客户端发来的列表里取最多 5 张、且在 allowed 里的卡。
func wishPick(l []xym.Msg, allowed []int32) []int32 {
	var out []int32
	for _, x := range l {
		t := int32(x["type"].(int64))
		if t != 0 && slices.Contains(allowed, t) && !slices.Contains(out, t) && len(out) < wishMax {
			out = append(out, t)
		}
	}
	return out
}

// dfzjWishMsg：登峰造极寻英帖，可选登峰造极池的 5 星。
func (s *Session) dfzjWishMsg(p *Player) {
	m := p.mall(time.Now())
	s.SendExt("ETC_PTO.S_OPEN_DFZJ_XUNCAITIE", xym.Msg{"list": typeList(m.WishDfzj),
		"shelf": typeList(poolPets["超级请神符"][5])})
}

func onConfirmDfzj(s *Session, p *Player, m xym.Msg) {
	l, _ := m["list"].([]xym.Msg)
	p.mall(time.Now()).WishDfzj = wishPick(l, poolPets["超级请神符"][5])
	p.saveLog(s)
}

// mrtjWishMsg：每日推荐寻英帖，每个种族各选 5 张该种族的灵仙池 5 星。
func (s *Session) mrtjWishMsg(p *Player) {
	m := p.mall(time.Now())
	msg := xym.Msg{"shelf": typeList(poolPets["灵仙请神符"][5])}
	for race := 1; race <= 4; race++ {
		msg[fmt.Sprintf("list%d", race)] = typeList(m.WishMrtj[race])
	}
	s.SendExt("ETC_PTO.S_OPEN_MRTJ_XUNCAITIE", msg)
}

func onConfirmMrtj(s *Session, p *Player, m xym.Msg) {
	mall := p.mall(time.Now())
	for race := 1; race <= 4; race++ {
		l, _ := m[fmt.Sprintf("list%d", race)].([]xym.Msg)
		mall.WishMrtj[race] = wishPick(l, raceOnly(poolPets["灵仙请神符"], race)[5])
	}
	p.saveLog(s)
}

// ---- 商城（主界面「商城」）----
//
// 依据：抓包 session-01——点商城发 C_CLICK_ENTRANCE{id 1005}，回 S_OPEN_MALL_BOTTOM_BTNS（道具商城 1000000、
// 特权礼包 3000003 两个页签）+ S_OPEN_DAOJU_SHANGCHENG；logic/shop_mgr.lua（切页签 C_OPEN_MALL_TAG{id}，
// 特权礼包 S_OPEN_TEQUANLIBAO → 第 5 页，VipGiftToGrade：11111–11125 VIP1–15 福利周礼包、11239–11253 阵法周礼包）；
// view/Mall_ItemView.lua（特权礼包用 C_BUY_GIFT{type}，「购买此礼包后，本周不能再购买其他档次每周礼包了」）。
//
// 估计：特权礼包价格（客户端和抓包里都没有）：现价 100 × VIP 级、原价 300 × VIP 级；要人物 VIP 达到礼包的档次；
// 每周只能买一个周礼包（礼品页文案「每周仅能购买一次周礼包」，福利、阵法合计）。

const (
	entranceMall = 1005
	tagDaoju     = 1000000
	tagTequan    = 3000003
)

var tequanGifts = func() []int {
	var out []int
	for t := 11111; t <= 11125; t++ {
		out = append(out, t)
	}
	for t := 11239; t <= 11253; t++ {
		out = append(out, t)
	}
	return out
}()

// tequanVip 是礼包对应的 VIP 档次。
func tequanVip(t int) int {
	if t >= 11239 {
		return t - 11238
	}
	return t - 11110
}

func onClickEntrance(s *Session, p *Player, m xym.Msg) {
	if int(m["id"].(int64)) != entranceMall {
		return
	}
	s.SendExt("MALL_PTO.S_OPEN_MALL_BOTTOM_BTNS", xym.Msg{"buttons": []xym.Msg{
		{"id": tagDaoju, "icon": "ui/entrance_icon/yeqian/btn_daojushangcheng_00.png", "name": "道具商城", "sub_buttons": []xym.Msg{}},
		{"id": tagTequan, "icon": "ui/entrance_icon/yeqian/btn_VIP_00.png", "name": "特权礼包", "sub_buttons": []xym.Msg{}},
	}, "selected_id": tagDaoju})
	s.daojuShopMsg(p)
}

func (s *Session) mallTag(p *Player, id int) {
	switch id {
	case tagTequan:
		s.tequanMsg(p)
	default:
		s.daojuShopMsg(p)
	}
}

// weekKey 是本周（ISO 周）的标识。
func weekKey(now time.Time) string {
	y, w := now.ISOWeek()
	return fmt.Sprintf("%d-%02d", y, w)
}

func (s *Session) tequanMsg(p *Player) {
	m := p.mall(time.Now())
	week := weekKey(time.Now())
	items := []xym.Msg{}
	for _, t := range tequanGifts {
		if gamedata.Items[t] == nil {
			continue
		}
		v := tequanVip(t)
		left := 1
		if m.GiftWeek[0] == week {
			left = 0
		}
		items = append(items, xym.Msg{"type": t, "base_price": 300 * v, "price": 100 * v, "discount_price": 100 * v,
			"left_amount": left})
	}
	s.SendExt("MALL_PTO.S_OPEN_TEQUANLIBAO", xym.Msg{"libao_id": tagTequan, "items": items})
}

// onBuyGift：买特权礼包（VIP 档次够、本周这一种还没买过、元宝够），礼包进背包。
func onBuyGift(s *Session, p *Player, m xym.Msg) {
	t := int(m["type"].(int64))
	if !slices.Contains(tequanGifts, t) || gamedata.Items[t] == nil {
		s.retMsg("没有这个礼包。")
		return
	}
	mall := p.mall(time.Now())
	week, v := weekKey(time.Now()), tequanVip(t)
	switch {
	case s.role.VIP < v:
		s.retMsg(fmt.Sprintf("需要 VIP%d 才能购买。", v))
		return
	case mall.GiftWeek[0] == week:
		s.retMsg("本周已经购买过周礼包了。")
		return
	}
	cost := int32(100 * v)
	if !s.roleUpdate(func(ro *Role) bool {
		if ro.Gold < cost {
			return false
		}
		ro.Gold -= cost
		return true
	}) {
		s.retMsg(fmt.Sprintf("元宝不足：需要 %d 元宝。", cost))
		return
	}
	mall.GiftWeek[0] = week
	delta := &bagDelta{items: p.addItem(t, 1)}
	delta.gains = append(delta.gains, gain{rtItem, t, 1})
	p.saveLog(s)
	list := s.sendDelta(p, delta)
	s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "gold", "val": s.role.Gold}}})
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, list))
	s.tequanMsg(p)
}
