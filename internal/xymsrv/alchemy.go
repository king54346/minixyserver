package xymsrv

import (
	"math/rand/v2"
	"slices"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 炼化（背包「炼化」→ view/AlchemyFrameView.lua 的三个页签）：
//
//   - 合成（view/AlchemyJiaGongMainView.lua，logic/stone.lua）：3 颗同种同级宝石合成 1 颗高一级的
//     （C_REFINE_STONE{type, num 1}），花 等级³×10000 银两；宝石号 101x1–101x9，9 级不能再合。
//   - 洗炼（view/AlchemyXiLianMainView.lua）：1 个淬元沙 10130 + 当天单价元宝 → 1 个粒子（道 10136、土 10132、
//     火 10133、水 10134、生 10135、雷 10131），单价按星期几查 PriceTable（客户端自己显示，服务端照收）；
//     C_REFRESH_ELEMENT{type, count} / C_REFRESH_ELEMENT_ALL{type}（全部淬元沙）；「还原」C_ELEMENT_REFRESH_BACK
//     {itemid, count} 粒子 1:1 换回淬元沙，回 S_REFRESH_BACK_OK。洗沙券本服没有，恒为 0。
//     抓包：S_SEND_ELEMENT_PRICE_INFO 的价格与客户端星期一那列一致。
//   - 烘炉（view/AlchemyHongLuMainView.lua 与帮助页）：多余的 5 星装备（鞋除外）/ 5 星法术回炉，随机换成六格里
//     还亮着的一件（那格变灰），六格都换完自动换一批；装备、法术各一页（type 1 / 2）。每次消耗浴火丹 11462：
//     当天第 1–5 次起 4/5/7/8/10 颗，不够的每颗折 60 元宝；每天 6 点刷新。
//     抓包：S_SEND_HONGLU_INFO{left_time, refresh_gold 20, left_refresh 4, buy_refresh_need_gold 20,
//     huilu_gold 300, list[equip_type, available], today_time, dan_cnt}；免费刷新次数两页共用，刷新后该页
//     冷却 1800 秒（冷却中「强制刷新」花 refresh_gold），次数用完「购买次数」花 buy_refresh_need_gold 直接刷新。
//
// 估计：今日剩余次数 4；强制刷新也算一次免费次数；换来的是 1 级新件（本服装备没有宝石 / 精修 / 套装可转移）。

const (
	stoneMaxGrade = 9
	cuiYuanSha    = 10130
	yuhuoDan      = 11462
	hlFreeRefresh = 4
	hlRefreshGold = 20
	hlCooldown    = 1800
	hlDanGold     = 60
	hlResetHour   = 6
	hlSlots       = 6
	hlTypeSuit    = 1
	hlTypeMagic   = 2
)

var (
	// 粒子道具，下标同客户端 LianType / PriceTable
	elementItems = []int{10136, 10132, 10133, 10134, 10135, 10131}
	// PriceTable[i][星期一..日]
	elementPrice = [][7]int32{
		{15, 10, 10, 10, 10, 10, 5},
		{15, 5, 10, 5, 10, 15, 5},
		{15, 5, 15, 10, 10, 5, 5},
		{5, 10, 5, 15, 5, 10, 15},
		{5, 10, 5, 10, 15, 5, 15},
		{5, 15, 10, 5, 5, 10, 15},
	}
	hlDanCost = []int32{4, 5, 7, 8, 10}
)

func init() {
	Handle("ITEM_PTO.C_REFINE_STONE", onRefineStone)
	Handle("ETC_PTO.C_ON_GET_ELEMENT_PRICE", onElementPrice)
	Handle("ITEM_PTO.C_REFRESH_ELEMENT", func(s *Session, p *Player, m xym.Msg) {
		s.refreshElement(p, int(m["type"].(int64)), int32(m["count"].(int64)))
	})
	Handle("ITEM_PTO.C_REFRESH_ELEMENT_ALL", func(s *Session, p *Player, m xym.Msg) {
		s.refreshElement(p, int(m["type"].(int64)), p.itemCount(cuiYuanSha))
	})
	Handle("ITEM_PTO.C_ELEMENT_REFRESH_BACK", onElementBack)
	Handle("ETC_PTO.C_OPEN_HONGLU", func(s *Session, p *Player, m xym.Msg) {
		h := p.honglu(s.actRng())
		p.saveLog(s)
		s.SendExt("ETC_PTO.S_SEND_HONGLU_INFO", p.hongluMsg(h, int(m["type"].(int64))))
	})
	Handle("ETC_PTO.C_REFRESH_HONGLU", func(s *Session, p *Player, m xym.Msg) { s.hongluRefresh(p, int(m["type"].(int64)), false) })
	Handle("ETC_PTO.C_HONGLU_BUY_REFRESH", func(s *Session, p *Player, m xym.Msg) { s.hongluRefresh(p, int(m["type"].(int64)), true) })
	Handle("ETC_PTO.C_HONGLU_HUILU", onHuilu)
}

// ---- 宝石合成 ----

func onRefineStone(s *Session, p *Player, m xym.Msg) {
	typ := int(m["type"].(int64))
	n := max(int32(m["num"].(int64)), 1)
	grade := typ % 10
	base := typ - grade
	if !slices.Contains(gemBase, base) || grade < 1 || grade >= stoneMaxGrade || gamedata.Items[typ+1] == nil {
		s.sendMsg("这颗宝石不能再合成了。")
		return
	}
	n = min(n, p.itemCount(typ)/3)
	if n < 1 {
		s.sendMsg("宝石数量不足，需要 3 颗同级宝石。")
		return
	}
	if !s.spend(int32(grade*grade*grade*10000)*n, 0) {
		return
	}
	d := &bagDelta{}
	ch, rm := p.takeItems(typ, 3*n)
	d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	d.items = append(d.items, p.addItem(typ+1, n)...)
	d.gains = append(d.gains, gain{rtItem, typ + 1, n})
	p.saveLog(s)
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, s.sendDelta(p, d)))
}

// ---- 洗炼 ----

// weekdayIdx 是 PriceTable 的列：星期一 0 … 星期日 6。
func weekdayIdx(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }

func onElementPrice(s *Session, p *Player, m xym.Msg) {
	day := weekdayIdx(time.Now())
	price := func(item int) int32 { return elementPrice[slices.Index(elementItems, item)][day] }
	s.SendExt("ETC_PTO.S_SEND_ELEMENT_PRICE_INFO", xym.Msg{"list": []xym.Msg{{
		"tu": price(10132), "huo": price(10133), "dao": price(10136), "shui": price(10134), "lei": price(10131), "sheng": price(10135),
	}}, "xishaquan": 0})
}

func (s *Session) refreshElement(p *Player, typ int, n int32) {
	i := slices.Index(elementItems, typ)
	n = min(n, p.itemCount(cuiYuanSha))
	if i < 0 || gamedata.Items[typ] == nil || n < 1 {
		s.sendMsg("淬元沙不足。")
		return
	}
	if !s.spend(0, elementPrice[i][weekdayIdx(time.Now())]*n) {
		return
	}
	d := &bagDelta{}
	ch, rm := p.takeItems(cuiYuanSha, n)
	d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	d.items = append(d.items, p.addItem(typ, n)...)
	d.gains = append(d.gains, gain{rtItem, typ, n})
	p.saveLog(s)
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, s.sendDelta(p, d)))
}

func onElementBack(s *Session, p *Player, m xym.Msg) {
	typ, n := int(m["itemid"].(int64)), int32(m["count"].(int64))
	if !slices.Contains(elementItems, typ) || n < 1 || p.itemCount(typ) < n {
		s.sendMsg("粒子数量不足。")
		return
	}
	d := &bagDelta{}
	ch, rm := p.takeItems(typ, n)
	d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	d.items = append(d.items, p.addItem(cuiYuanSha, n)...)
	p.saveLog(s)
	s.sendDelta(p, d)
	s.SendExt("ITEM_PTO.S_REFRESH_BACK_OK", xym.Msg{})
}

// ---- 烘炉 ----

type hlPage struct {
	List     []int  `json:"list"`
	Used     []bool `json:"used"`
	Cooldown int64  `json:"cooldown,omitempty"` // 免费刷新冷却到的 unix 秒
}

// HongluState 是烘炉存档（Player.Honglu）。
type HongluState struct {
	Day       string     `json:"day"`
	Times     int        `json:"times"`     // 今天回炉次数
	Refreshed int        `json:"refreshed"` // 今天用掉的免费刷新
	Pages     [2]*hlPage `json:"pages"`
}

func hlDay() string { return time.Now().Add(-hlResetHour * time.Hour).Format("2006-01-02") }

func hlPool(typ int) []int {
	var out []int
	if typ == hlTypeSuit {
		for _, id := range gamedata.SuitsByStar[5] {
			if gamedata.Suits[id].Kind != "ITK_SHOE" {
				out = append(out, id)
			}
		}
		return out
	}
	for _, id := range gamedata.BooksByStar[5] {
		out = append(out, int(id))
	}
	return out
}

func newHlPage(rng *rand.Rand, typ int) *hlPage {
	pool := hlPool(typ)
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	l := slices.Clone(pool[:min(hlSlots, len(pool))])
	slices.Sort(l)
	return &hlPage{List: l, Used: make([]bool, len(l))}
}

func (p *Player) honglu(rng *rand.Rand) *HongluState {
	if p.Honglu == nil {
		p.Honglu = &HongluState{}
	}
	h := p.Honglu
	if d := hlDay(); h.Day != d {
		h.Day, h.Times, h.Refreshed = d, 0, 0
		h.Pages = [2]*hlPage{newHlPage(rng, hlTypeSuit), newHlPage(rng, hlTypeMagic)}
	}
	return h
}

func hlPageIdx(typ int) int {
	if typ == hlTypeMagic {
		return 1
	}
	return 0
}

func (p *Player) hongluMsg(h *HongluState, typ int) xym.Msg {
	pg := h.Pages[hlPageIdx(typ)]
	list := []xym.Msg{}
	for i, id := range pg.List {
		list = append(list, xym.Msg{"equip_type": id, "available": b2i(!pg.Used[i])})
	}
	return xym.Msg{"left_time": max(0, pg.Cooldown-time.Now().Unix()), "refresh_gold": hlRefreshGold,
		"left_refresh": max(0, hlFreeRefresh-h.Refreshed), "buy_refresh_need_gold": hlRefreshGold, "huilu_gold": hlDanGold * 5,
		"list": list, "today_time": h.Times, "dan_cnt": p.itemCount(yuhuoDan)}
}

// hongluRefresh：buy 为「购买次数」（次数用完后花元宝直接刷新）；否则用一次免费次数，冷却中要花元宝强制刷新。
func (s *Session) hongluRefresh(p *Player, typ int, buy bool) {
	rng := s.actRng()
	h := p.honglu(rng)
	pg := h.Pages[hlPageIdx(typ)]
	reply := func() { s.SendExt("ETC_PTO.S_SEND_HONGLU_INFO", p.hongluMsg(h, typ)) }
	switch {
	case buy:
		if !s.spend(0, hlRefreshGold) {
			reply()
			return
		}
	case h.Refreshed >= hlFreeRefresh:
		s.sendMsg("今日免费刷新次数已用完。")
		reply()
		return
	default:
		if pg.Cooldown > time.Now().Unix() && !s.spend(0, hlRefreshGold) {
			reply()
			return
		}
		h.Refreshed++
	}
	np := newHlPage(rng, typ)
	np.Cooldown = time.Now().Unix() + hlCooldown
	h.Pages[hlPageIdx(typ)] = np
	p.saveLog(s)
	reply()
}

// onHuilu：回炉一件 5 星装备 / 法术，随机换成还亮着的一格。
func onHuilu(s *Session, p *Player, m xym.Msg) {
	rng := s.actRng()
	h := p.honglu(rng)
	typ := int(m["type"].(int64))
	pg := h.Pages[hlPageIdx(typ)]
	reply := func() { s.SendExt("ETC_PTO.S_SEND_HONGLU_INFO", p.hongluMsg(h, typ)) }
	mt := &materials{}
	if typ == hlTypeSuit {
		su := p.suitByNum(int32(m["suit_num"].(int64)))
		if su == nil || su.Locked || p.suitWorn(su.Pos) || gamedata.Suits[su.Type] == nil ||
			gamedata.Suits[su.Type].Star != 5 || gamedata.Suits[su.Type].Kind == "ITK_SHOE" {
			s.sendMsg("只能放入没穿戴、没锁定的五星装备（鞋子除外）。")
			reply()
			return
		}
		mt.suits = []*Suit{su}
	} else {
		b := p.bookByPos(int(m["magic_pos"].(int64)))
		if b == nil || b.Locked || p.bookWorn(b.Pos) || magicStar(b.Type) != 5 {
			s.sendMsg("只能放入没穿戴、没锁定的五星法术。")
			reply()
			return
		}
		mt.books = []*Book{b}
	}
	var avail []int
	for i := range pg.List {
		if !pg.Used[i] {
			avail = append(avail, i)
		}
	}
	if len(avail) == 0 {
		*pg = *newHlPage(rng, typ)
		avail = []int{0, 1, 2, 3, 4, 5}[:len(pg.List)]
	}
	need := hlDanCost[min(h.Times, len(hlDanCost)-1)]
	have := min(p.itemCount(yuhuoDan), need)
	if !s.spend(0, (need-have)*hlDanGold) {
		reply()
		return
	}
	d := &bagDelta{}
	if have > 0 {
		ch, rm := p.takeItems(yuhuoDan, have)
		d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	}
	s.consume(p, mt)
	slot := avail[rng.IntN(len(avail))]
	pg.Used[slot] = true
	if typ == hlTypeSuit {
		p.giveSuit(pg.List[slot], d)
	} else {
		p.giveBook(int32(pg.List[slot]), d)
	}
	h.Times++
	if !slices.Contains(pg.Used, false) {
		*pg = *newHlPage(rng, typ)
	}
	p.saveLog(s)
	list := s.sendDelta(p, d)
	reply()
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, list))
}
