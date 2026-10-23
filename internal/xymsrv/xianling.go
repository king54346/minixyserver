package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 仙灵店铺（ETC_PTO.C_OPEN_XIANLING_SHOP …）：用多余的装备 / 法术书换护法魂魄，换一次得贡献值，贡献值决定称号；
// 「热点」护法的魂魄出现得多，可以花 20 元宝变更热点（从随机 3 个稀有护法里挑一个）。
//
// 依据：view/XianLingMainView.lua（列表 pos 为 0 起的下标；需要的装备按 itemnum、法术按 pos 交上来，只收没穿、
// 没锁、1 级的；缺材料时「兑换」按钮上显示元宝；单个材料可花 50 元宝忽略 C_XIANLING_SHOP_IGNORE_STUFF；
// 碎片够时先 C_ON_MIX_XIANLING_ITEMS 合成，回 S_XIANLING_MIX_ITEMS_COMPLETE{status 1}；每天 left_refresh 次
// 免费刷新；称号 1–5 心不在焉 / 三心二意 / 心猿意马 / 心驰神往 / 一心一意，门槛 150 / 500 / 1000 / 3000）；
// view/XianlingPrayMainView.lua（C_CHANGE_HOT_ITEM → S_UPDATE_RANDOM_PET 弹出 3 选 1，选中 C_ON_REFRESH_XIANLING_HOT{id}，
// 再点「换一批」又是 C_CHANGE_HOT_ITEM）。
// 抓包：10 条，3 星魂魄要 1 件材料、贡献 18，4 星 2 件、60，5 星 3 件、100；材料是 3–4 星武器 / 衣服 / 法术；
// 10 条里 4 条是热点护法（recommend 1）；兑换后该条从列表里去掉，回 S_UPDATE_BOOK{is_all 1}、店铺信息、
// S_REWARD_MSG{rtype 2 魂魄 1 个}；refresh_gold 20、left_refresh 10。
//
// 缺材料时点「兑换」（客户端按钮上显示元宝图标）：弹框确认后按缺的每件材料补元宝直接换（用户要求）；
// 忽略单件材料的价格照 CXianLingGetBuyView：3 星材料 50、4 星以上 300 元宝。
// 估计：每 3 小时（0/3/6… 点）自动换一批；免费次数用完后每次 20 元宝；
// 非热点条目 3/4/5 星各 3/3/4 成；热点候选是 4、5 星护法。

const (
	xlEntries     = 10
	xlHotEntries  = 4
	xlFreeRefresh = 10
	xlRefreshGold = 20
	xlHotGold     = 20
	xlIgnoreGold  = 50  // 忽略一件 3 星材料
	xlIgnoreGold4 = 300 // 忽略一件 4 星以上材料
	xlPeriod      = 3 * time.Hour
)

var (
	xlTitleNums  = []int32{150, 500, 1000, 3000}
	xlContribute = map[int]int32{3: 18, 4: 60, 5: 100}
)

type xlNeed struct {
	Type int    `json:"type"`
	Kind string `json:"kind"` // suit / magic
}

type xlGoods struct {
	Pet  int32    `json:"pet"`
	Need []xlNeed `json:"need"`
	Hot  bool     `json:"hot,omitempty"`
}

// XianlingState 是仙灵店铺存档（Player.Xianling）。
type XianlingState struct {
	Contribute int32     `json:"contribute"`
	Hot        int32     `json:"hot"`
	Goods      []xlGoods `json:"goods"`
	Period     int64     `json:"period"` // 当前这批货的时段（unix 秒 / 3 小时）
	Day        string    `json:"day"`
	Refreshed  int       `json:"refreshed"` // 今天刷新次数
	Choices    []int32   `json:"choices,omitempty"`

	// 云游商人（yunyou.go）
	Spent        int32     `json:"spent,omitempty"` // 在云游商人花掉的贡献（称号仍按累计贡献算）
	Yun          []yyGoods `json:"yun,omitempty"`
	YunPeriod    int64     `json:"yun_period,omitempty"`
	YunRefreshed int       `json:"yun_refreshed,omitempty"` // 今天刷新次数（随 Day 清零）
}

func xlPeriodNow() int64 {
	_, off := time.Now().Zone()
	return (time.Now().Unix() + int64(off)) / int64(xlPeriod/time.Second)
}

func xlLeftTime() int64 {
	_, off := time.Now().Zone()
	sec := int64(xlPeriod / time.Second)
	return sec - (time.Now().Unix()+int64(off))%sec
}

func (p *Player) xianling(rng *rand.Rand) *XianlingState {
	if p.Xianling == nil {
		p.Xianling = &XianlingState{}
	}
	x := p.Xianling
	if x.Hot == 0 || gamedata.Pets[x.Hot] == nil {
		x.Hot = xlRarePets(rng, 1)[0]
	}
	if d := today(); x.Day != d {
		x.Day, x.Refreshed, x.YunRefreshed = d, 0, 0
	}
	if now := xlPeriodNow(); x.Period != now {
		x.Period = now
		x.Goods = xlNewGoods(rng, x.Hot)
	}
	return x
}

// xlRarePets 随机 n 个不同的 4、5 星护法。
func xlRarePets(rng *rand.Rand, n int) []int32 {
	pool := slices.Concat(gamedata.PetsByStar[4], gamedata.PetsByStar[5])
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	return pool[:min(n, len(pool))]
}

// xlMaterials 是可当材料的 3–4 星武器 / 衣服与法术书。
func xlMaterial(rng *rand.Rand) xlNeed {
	star := 3 + rng.IntN(2)
	if rng.IntN(2) == 0 {
		var l []int
		for _, id := range gamedata.SuitsByStar[star] {
			if k := gamedata.Suits[id].Kind; k == "ITK_WEAPON" || k == "ITK_SHIRT" {
				l = append(l, id)
			}
		}
		if len(l) > 0 {
			return xlNeed{Type: l[rng.IntN(len(l))], Kind: "suit"}
		}
	}
	l := gamedata.BooksByStar[star]
	return xlNeed{Type: int(l[rng.IntN(len(l))]), Kind: "magic"}
}

func xlEntry(rng *rand.Rand, pet int32, hot bool) xlGoods {
	g := xlGoods{Pet: pet, Hot: hot}
	for range max(1, petStar(pet)-2) {
		g.Need = append(g.Need, xlMaterial(rng))
	}
	return g
}

func petStar(typ int32) int {
	if pi := gamedata.Pets[typ]; pi != nil {
		return pi.Star
	}
	return 3
}

func xlNewGoods(rng *rand.Rand, hot int32) []xlGoods {
	var l []xlGoods
	for range xlHotEntries {
		l = append(l, xlEntry(rng, hot, true))
	}
	for len(l) < xlEntries {
		star := 5
		switch r := rng.IntN(10); {
		case r < 3:
			star = 3
		case r < 6:
			star = 4
		}
		pool := gamedata.PetsByStar[star]
		pet := pool[rng.IntN(len(pool))]
		l = append(l, xlEntry(rng, pet, pet == hot))
	}
	rng.Shuffle(len(l), func(i, j int) { l[i], l[j] = l[j], l[i] })
	return l
}

func (x *XianlingState) title() int {
	for i, n := range xlTitleNums {
		if x.Contribute < n {
			return i + 1
		}
	}
	return len(xlTitleNums) + 1
}

func (x *XianlingState) msg() xym.Msg {
	list := []xym.Msg{}
	for _, g := range x.Goods {
		need := []xym.Msg{}
		for _, n := range g.Need {
			need = append(need, xym.Msg{"type": n.Type, "kind": n.Kind})
		}
		list = append(list, xym.Msg{"goods": g.Pet, "type": 2, "kind": "pet", "need": need,
			"contribute": xlContribute[petStar(g.Pet)], "recommend": b2i(g.Hot)})
	}
	rl := []xym.Msg{}
	for _, id := range x.Choices {
		rl = append(rl, xym.Msg{"id": id})
	}
	return xym.Msg{"left_time": xlLeftTime(), "refresh_gold": xlRefreshGold, "left_refresh": max(0, xlFreeRefresh-x.Refreshed),
		"title": x.title(), "contribute": x.Contribute, "list": list, "recommend": x.Hot, "recommend_list": rl}
}

func init() {
	Handle("ETC_PTO.C_OPEN_XIANLING_SHOP", func(s *Session, p *Player, m xym.Msg) {
		x := p.xianling(s.actRng())
		p.saveLog(s)
		s.SendExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO", x.msg())
	})
	Handle("ETC_PTO.C_XIANLING_SHOP_EXCHANGE_GOODS", onXLExchange)
	Handle("ETC_PTO.C_XIANLING_SHOP_REFRESH_GOODS_LIST", onXLRefresh)
	Handle("ETC_PTO.C_XIANLING_SHOP_BUY_REFRESH", onXLRefresh)
	Handle("ETC_PTO.C_XIANLING_SHOP_IGNORE_STUFF", onXLIgnore)
	Handle("ETC_PTO.C_CHANGE_HOT_ITEM", onXLChangeHot)
	Handle("ETC_PTO.C_ON_REFRESH_XIANLING_HOT", onXLSetHot)
	Handle("ETC_PTO.C_ON_MIX_XIANLING_ITEMS", onXLMix)
}

// xlIgnorePrice 是忽略（用元宝代替）一件材料的价格。
func xlIgnorePrice(n xlNeed) int32 {
	star := 0
	if n.Kind == "suit" {
		star = suitStar(n.Type)
	} else {
		star = magicStar(int32(n.Type))
	}
	if star <= 3 {
		return xlIgnoreGold
	}
	return xlIgnoreGold4
}

// onXLExchange：按顺序核对交上来的材料，扣材料，得 1 个魂魄和贡献值，这条从列表去掉。
// 缺的材料弹框确认后按件补元宝（xlIgnorePrice）直接换。
func onXLExchange(s *Session, p *Player, m xym.Msg) {
	x := p.xianling(s.actRng())
	pos := int(m["pos"].(int64))
	if pos < 0 || pos >= len(x.Goods) {
		s.SendExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO", x.msg())
		return
	}
	g := x.Goods[pos]
	given, _ := m["need"].([]xym.Msg)
	mt := &materials{}
	usedSuit, usedBook := map[*Suit]bool{}, map[*Book]bool{}
	var gold int32
	missing := 0
	for _, n := range g.Need {
		ok := false
		for _, r := range given {
			if r["kind"] != n.Kind {
				continue
			}
			id := r["item"].(int64)
			switch n.Kind {
			case "suit":
				if su := p.suitByNum(int32(id)); su != nil && !usedSuit[su] && su.Type == n.Type && su.Grade <= 1 && !su.Locked && !p.suitWorn(su.Pos) {
					usedSuit[su], ok = true, true
					mt.suits = append(mt.suits, su)
				}
			case "magic":
				if b := p.bookByPos(int(id)); b != nil && !usedBook[b] && int(b.Type) == n.Type && b.Grade <= 1 && !b.Locked && !p.bookWorn(b.Pos) {
					usedBook[b], ok = true, true
					mt.books = append(mt.books, b)
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			missing++
			gold += xlIgnorePrice(n)
		}
	}
	if missing > 0 {
		text := fmt.Sprintf("缺少%d件材料，花费#G%d元宝#n代替缺少的材料直接兑换？", missing, gold)
		s.askBox(text, []string{"兑换", "取消"}, func(opt int) {
			if opt != 0 {
				return
			}
			if x.Goods == nil || pos >= len(x.Goods) || !slices.EqualFunc(x.Goods[pos].Need, g.Need, func(a, b xlNeed) bool { return a == b }) {
				s.SendExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO", x.msg()) // 期间列表变了
				return
			}
			for _, su := range mt.suits { // 交上来的材料期间可能被用掉了
				if p.suitByNum(su.ItemNum) != su {
					s.SendExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO", x.msg())
					return
				}
			}
			for _, b := range mt.books {
				if p.bookByPos(b.Pos) != b {
					s.SendExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO", x.msg())
					return
				}
			}
			if !s.spend(0, gold) {
				s.SendExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO", x.msg())
				return
			}
			s.xlFinish(p, x, pos, g, mt)
		})
		return
	}
	s.xlFinish(p, x, pos, g, mt)
}

// xlFinish 扣掉交上来的材料，给魂魄和贡献值，这条从列表去掉。
func (s *Session) xlFinish(p *Player, x *XianlingState, pos int, g xlGoods, mt *materials) {
	s.consume(p, mt)
	x.Contribute += xlContribute[petStar(g.Pet)]
	x.Goods = slices.Delete(x.Goods, pos, pos+1)
	d := &bagDelta{}
	p.addSoul(g.Pet, 1, d)
	p.saveLog(s)
	list := s.sendDelta(p, d)
	s.SendExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO", x.msg())
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, list))
}

func onXLRefresh(s *Session, p *Player, m xym.Msg) {
	rng := s.actRng()
	x := p.xianling(rng)
	if x.Refreshed >= xlFreeRefresh && !s.spend(0, xlRefreshGold) {
		s.SendExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO", x.msg())
		return
	}
	x.Refreshed++
	x.Goods = xlNewGoods(rng, x.Hot)
	p.saveLog(s)
	s.SendExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO", x.msg())
}

// onXLIgnore：花元宝（3 星材料 50、4 星以上 300）去掉某条里的一件材料。
func onXLIgnore(s *Session, p *Player, m xym.Msg) {
	x := p.xianling(s.actRng())
	pos, typ, kind := int(m["pos"].(int64)), int(m["type"].(int64)), m["stuff_kind"]
	if pos >= 0 && pos < len(x.Goods) {
		g := &x.Goods[pos]
		if i := slices.IndexFunc(g.Need, func(n xlNeed) bool { return n.Type == typ && n.Kind == kind }); i >= 0 && s.spend(0, xlIgnorePrice(g.Need[i])) {
			g.Need = slices.Delete(g.Need, i, i+1)
			p.saveLog(s)
		}
	}
	s.SendExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO", x.msg())
}

// onXLChangeHot：花 20 元宝随机 3 个稀有护法，弹出选择框。
func onXLChangeHot(s *Session, p *Player, m xym.Msg) {
	rng := s.actRng()
	x := p.xianling(rng)
	if !s.spend(0, xlHotGold) {
		return
	}
	x.Choices = xlRarePets(rng, 3)
	p.saveLog(s)
	l := []xym.Msg{}
	for _, id := range x.Choices {
		l = append(l, xym.Msg{"id": id})
	}
	s.SendExt("ETC_PTO.S_UPDATE_RANDOM_PET", xym.Msg{"force_open_view": 1, "recommend": x.Hot, "random_pets": l})
}

// onXLSetHot：从刚随机出的 3 个里选一个当热点，当前列表里的热点条目换成新热点。
func onXLSetHot(s *Session, p *Player, m xym.Msg) {
	rng := s.actRng()
	x := p.xianling(rng)
	id := int32(m["id"].(int64))
	if slices.Contains(x.Choices, id) {
		x.Hot, x.Choices = id, nil
		for i, g := range x.Goods {
			if g.Hot {
				x.Goods[i] = xlEntry(rng, id, true)
			}
		}
		p.saveLog(s)
	}
	s.SendExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO", x.msg())
}

// onXLMix：兑换前先用碎片合成缺的装备 / 法术（同 C_MIX_SUIT / C_MIX_BOOK_PART）。
func onXLMix(s *Session, p *Player, m xym.Msg) {
	d := &bagDelta{}
	ok := true
	suits, _ := m["suits"].([]xym.Msg)
	for _, r := range suits {
		typ := int(r["type"].(int64))
		star := suitStar(typ)
		if gamedata.Suits[typ] == nil || len(p.Suits) >= p.BagLimit[bagSuit] ||
			!p.takeParts(star, p.Irons[typ], suitPartItem[star], func(n int32) { p.addIron(typ, -n, d) }, d) {
			ok = false
			continue
		}
		p.giveSuit(typ, d)
	}
	magics, _ := m["magics"].([]xym.Msg)
	for _, r := range magics {
		typ := int32(r["type"].(int64))
		star := magicStar(typ)
		if gamedata.Magics[typ] == nil || len(p.Books) >= p.BagLimit[bagBook] ||
			!p.takeParts(star, p.BookParts[typ], magicPartItem[star], func(n int32) { p.addBookPart(typ, -n, d) }, d) {
			ok = false
			continue
		}
		p.giveBook(typ, d)
	}
	p.saveLog(s)
	s.sendDelta(p, d)
	s.SendExt("ETC_PTO.S_XIANLING_MIX_ITEMS_COMPLETE", xym.Msg{"status": b2i(ok)})
}
