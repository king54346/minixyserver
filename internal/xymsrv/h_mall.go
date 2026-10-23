package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 招募（MALL_PTO C_OPEN_PET_MALL / C_BUY_PET）。客户端界面 view/ShopZhaomuViewNew.lua：
//
//	type 4 友情点   10 级开放   单抽 100 / 十连 1000 友情点
//	type 3 银两     10 级开放   单抽 10000 / 十连 100000 银两；每天 5 次免费（有冷却）、付费 99 次
//	type 2 请神符   2 级开放    道具 10114 ×1 / ×10；到点免费一次
//	type 1 超级请神符 6 级开放  道具 10317 ×1 / ×10；到点免费一次，十连必出 5 星，10 抽保底 5 星
//	type 5 灵仙请神符 15 级开放 道具 11461 ×1 / ×10
//
// 十连时 type + 10。结果：S_UPDATE_PET（新卡）+ S_REWARD_MSG（type 6 单抽亮卡 / 9 十连，
// list 里 rtype 1 = 卡牌）+ S_OPEN_PET_MALL 刷新界面。各池可出的卡取卡牌表 from 字段
// 「【银两、友情点…】招募」。
//
// 抽卡概率、免费冷却时长、保底规则在原服服务端，客户端和抓包里都没有，下面 recruitPools
// 的数值是本服定的，可以直接改。

type recruitPool struct {
	name      string      // 卡牌表 from 里的池名
	grade     int         // 开放等级（客户端界面）
	rates     map[int]int // 星级 -> 权重
	item      int         // 消耗道具（0 = 不用道具）
	cash      int32       // 单抽银两
	friend    int32       // 单抽友情点
	freeEvery time.Duration
	tenMin    int // 十连至少出一张该星级（0 = 不保证）
	pity      int // 连续多少抽未出 5 星则下一抽必出（0 = 无）
}

var recruitPools = map[int]*recruitPool{
	4: {name: "友情点", grade: 10, friend: 100, rates: map[int]int{1: 45, 2: 35, 3: 17, 4: 3}},
	3: {name: "银两", grade: 10, cash: 10000, rates: map[int]int{1: 50, 2: 35, 3: 15}, freeEvery: 10 * time.Minute},
	2: {name: "请神符", grade: 2, item: 10114, rates: map[int]int{2: 30, 3: 50, 4: 17, 5: 3}, freeEvery: 24 * time.Hour, tenMin: 4},
	1: {name: "超级请神符", grade: 6, item: 10317, rates: map[int]int{3: 50, 4: 40, 5: 10}, freeEvery: 72 * time.Hour, tenMin: 5, pity: 10}, // 登峰造极（shop_mgr space_time 259200）
	5: {name: "灵仙请神符", grade: 15, item: 11461, rates: map[int]int{3: 60, 4: 32, 5: 8}, pity: 10},                                       // 每日推荐：只出所选种族（race_choose_today）
}

const (
	cashFreePerDay = 5
	cashPaidPerDay = 99
)

// poolPets[池名][星级] = 可出的卡牌类型
var poolPets = map[string]map[int][]int32{}

// rarePets[种族] 是每日推荐能出的稀有卡（客户端 is_special）：同名卡取最高星级的版本。原来稀有卡的高星版本不在任何
// 卡池里，每日推荐永远抽不到（用户报：人族抽 1600 次没有稀有卡）。
var rarePets = map[int][]int32{}

const rarePct = 2

func init() {
	for _, pi := range gamedata.Pets {
		for _, name := range pi.Pools {
			if poolPets[name] == nil {
				poolPets[name] = map[int][]int32{}
			}
			poolPets[name][pi.Star] = append(poolPets[name][pi.Star], pi.Type)
		}
	}
	best := map[string]*gamedata.PetInfo{}
	for _, pi := range gamedata.Pets {
		if pi.Special && (best[pi.Name] == nil || pi.Star > best[pi.Name].Star) {
			best[pi.Name] = pi
		}
	}
	for _, pi := range best {
		rarePets[pi.Race] = append(rarePets[pi.Race], pi.Type)
	}
	for _, l := range rarePets {
		sortInt32(l)
	}
	for _, byStar := range poolPets { // map 遍历无序，排序保证同种子抽到同样的卡
		for _, l := range byStar {
			sortInt32(l)
		}
	}
	Handle("MALL_PTO.C_OPEN_PET_MALL", func(s *Session, p *Player, _ xym.Msg) {
		s.SendExt("MALL_PTO.S_OPEN_PET_MALL", p.mallMsg(time.Now()))
	})
	Handle("MALL_PTO.C_BUY_PET", onBuyPet)
}

func sortInt32(l []int32) {
	for i := range l {
		for j := i + 1; j < len(l); j++ {
			if l[j] < l[i] {
				l[i], l[j] = l[j], l[i]
			}
		}
	}
}

// Mall 是玩家的招募状态（存在 Player 里）。
type Mall struct {
	Day       string        `json:"day"` // 计数所属日期，跨天重置
	Free3Left int           `json:"free3_left"`
	Cost3Left int           `json:"cost3_left"`
	NextFree  map[int]int64 `json:"next_free"`           // 池 -> 下次免费的时间戳
	Pity      map[int]int   `json:"pity"`                // 池 -> 连续未出 5 星的抽数
	Race      int           `json:"race,omitempty"`      // 每日推荐今天切换到的种族（0 = 当天默认）
	WishDfzj  []int32       `json:"wish_dfzj,omitempty"` // 登峰造极寻英帖
	WishMrtj  [5][]int32    `json:"wish_mrtj,omitempty"` // 每日推荐寻英帖，下标 = 种族 1–4
	Bought    map[int]int   `json:"bought,omitempty"`    // 道具商城今日已买（限购商品）
	GiftWeek  [2]string     `json:"gift_week,omitempty"` // 特权礼包本周已买（[0]，ISO 周；每周只能买一个周礼包）
}

func (p *Player) mall(now time.Time) *Mall {
	if p.Mall == nil {
		p.Mall = &Mall{}
	}
	m := p.Mall
	if m.NextFree == nil {
		m.NextFree = map[int]int64{}
	}
	if m.Pity == nil {
		m.Pity = map[int]int{}
	}
	if day := now.Format("2006-01-02"); m.Day != day {
		m.Day, m.Free3Left, m.Cost3Left, m.Race, m.Bought = day, cashFreePerDay, cashPaidPerDay, 0, nil
	}
	return m
}

// featured 是当天「今日护法」展示用的卡：按日期从超级请神符池的 4、5 星里轮换。
func featured(now time.Time, n int) []int32 {
	l := append(append([]int32(nil), poolPets["超级请神符"][5]...), poolPets["超级请神符"][4]...)
	out := make([]int32, n)
	if len(l) == 0 {
		return out
	}
	day := int(now.Unix() / 86400)
	for i := range out {
		out[i] = l[(day*7+i*13)%len(l)]
	}
	return out
}

func (p *Player) mallMsg(now time.Time) xym.Msg {
	m := p.mall(now)
	f := featuredRace(now, m.race(now)) // 每日推荐展示所选种族的卡
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	race := raceToday(now)
	return xym.Msg{
		"free_high_pet_count": b2i(now.Unix() >= m.NextFree[1]), // 登峰造极免费次数（客户端据此决定要不要请神符）
		"list1next":           m.NextFree[1], "list2next": m.NextFree[2], "list3next": m.NextFree[3],
		"max": 1, "freepet3count": m.Free3Left, "costpet3count": m.Cost3Left,
		"daily_left_2_baodi": recruitPools[5].pity - m.Pity[5], "dfzj_left_2_baodi": recruitPools[1].pity - m.Pity[1], // 界面「再招募N次必得金卡」
		// 登峰造极页的「N次必得金卡」新版界面读的是 highaddon（view/ShopZhaomuViewNew.lua refreshDfzj：rate = highaddon），
		// 不发就显示 0（用户报：显示 0 次必得金卡）
		"highaddon":  recruitPools[1].pity - m.Pity[1],
		"race_today": race, "race_choose_today": m.race(now), "race_rest_time_today": int(midnight.Sub(now).Seconds()),
		"pet1_today": f[0], "pet2_today": f[1], "pet3_today": f[2], "pet4_today": f[3], "mystery_pet_today": f[4],
	}
}

// itemCount / takeItem：背包道具（itemCount 是所有格子的合计）。
func (p *Player) itemCount(typ int) int32 {
	n := int32(0)
	for _, it := range p.Items {
		if it.Type == typ {
			n += it.Amount
		}
	}
	return n
}

func (p *Player) takeItem(typ int, n int32) *Item {
	for _, it := range p.Items {
		if it.Type == typ && it.Amount >= n {
			it.Amount -= n
			return it
		}
	}
	return nil
}

func onBuyPet(s *Session, p *Player, m xym.Msg) {
	typ := int(m["type"].(int64))
	n := 1
	if typ > 10 {
		typ, n = typ-10, 10
	}
	pool := recruitPools[typ]
	switch {
	case pool == nil:
		s.sendMsg("没有这个招募。")
		return
	case s.role.Grade < pool.grade:
		s.sendMsg(fmt.Sprintf("%d 级开放。", pool.grade))
		return
	case len(p.Pets) >= p.BagLimit[bagPet]: // 同名卡并入不占格，放不下的改寄邮件（giveCard），所以只拦已经满的
		s.sendMsg("护法背包已满，请先整理。")
		return
	}
	now := time.Now()
	mall := p.mall(now)

	// 付费：先看免费，再看货币 / 道具
	free := false
	if n == 1 && pool.freeEvery > 0 && now.Unix() >= mall.NextFree[typ] && (typ != 3 || mall.Free3Left > 0) {
		free = true
	}
	var cash, friend int32
	var item *Item
	if !free {
		switch {
		case pool.cash > 0:
			cash = pool.cash * int32(n)
			if mall.Cost3Left < n {
				s.sendMsg("今日次数已用尽，无法进行招募")
				return
			}
			if s.role.Cash < cash {
				s.sendMsg("银两不足。")
				return
			}
		case pool.friend > 0:
			// 友情点可能被别人借用援助护法时加过（helper.go，不改本会话的 s.role），按存档里的算
			if friend = pool.friend * int32(n); s.srv.roles.Get(s.role.UID).FriendPoint < friend {
				s.sendMsg("友情点不足。")
				return
			}
		case pool.item > 0:
			if item = p.takeItem(pool.item, int32(n)); item == nil {
				s.sendMsg("请神符不足。")
				return
			}
		}
	}
	if free {
		mall.NextFree[typ] = now.Add(pool.freeEvery).Unix()
		if typ == 3 {
			mall.Free3Left--
		}
	} else if typ == 3 {
		mall.Cost3Left -= n
	}
	if cash > 0 || friend > 0 {
		role, err := s.srv.roles.Update(s.role.UID, func(ro *Role) bool {
			ro.Cash -= cash
			ro.FriendPoint -= friend
			return true
		})
		if err != nil {
			s.logErr("save role", err)
			s.sendMsg("服务器存档出错，请稍后再试。")
			return
		}
		s.role = role
		s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{
			{"name": "cash", "val": role.Cash}, {"name": "friend_point", "val": role.FriendPoint}}})
	}
	if item != nil {
		s.SendExt("ITEM_PTO.S_UPDATE_ITEM", xym.Msg{"is_all": 0, "items": []xym.Msg{
			{"pos": item.Pos, "type": item.Type, "amount": item.Amount, "expire": item.Expire}}})
	}

	types := drawPets(pool, mall, n, rand.New(rand.NewPCG(uint64(now.UnixNano()), uint64(p.UID))))
	full := make([]xym.Msg, 0, n)
	brief := make([]xym.Msg, 0, n)
	list := make([]xym.Msg, 0, n)
	p.track(evRecruit, n)
	yuanbao := typ == 1 || typ == 5 // 登峰造极 / 每日推荐：奖励界面可「转为元神」（huaxing.go）
	huaxing := []xym.Msg{}
	for _, t := range types {
		list = append(list, xym.Msg{"rtype": 1, "type": t, "count": 1}) // REWARD_TYPE_CARD
		if yuanbao && p.autoYuanshen(t) {
			huaxing = append(huaxing, xym.Msg{"type": t})
			continue
		}
		pet := p.gainPet(t) // 已有同类护法就并进去，得神通点（pet_talent.go）
		full = append(full, p.petFull(pet))
		brief = append(brief, p.petBrief(pet))
	}
	p.saveLog(s)
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": full})
	s.SendExt("PET_PTO.S_PET_LIST", xym.Msg{"is_all": 0, "pets": brief})
	show := 6 // BOX_SHOWCARD
	switch {
	case yuanbao && n == 10:
		show = 17 // YUANBAO_PET_TEN
	case yuanbao:
		show = 16 // YUANBAO_SHOWCARD
	case n == 10:
		show = 9 // PET_TEN
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": show, "list": list, "huaxing": huaxing})
	s.SendExt("MALL_PTO.S_OPEN_PET_MALL", p.mallMsg(now))
}

// drawPets 按池的星级权重抽 n 张；处理保底（pity）和十连至少一张 tenMin 星。
func drawPets(pool *recruitPool, mall *Mall, n int, rng *rand.Rand) []int32 {
	byStar := poolPets[pool.name]
	typ := poolType(pool)
	var wish, rare []int32
	switch typ {
	case 1:
		wish = mall.WishDfzj
	case 5: // 每日推荐只出所选种族
		race := mall.race(time.Now())
		byStar = raceOnly(byStar, race)
		wish = mall.WishMrtj[race]
		rare = rarePets[race]
	}
	stars := make([]int, 0, len(pool.rates))
	total := 0
	for st, w := range pool.rates {
		if len(byStar[st]) > 0 {
			stars = append(stars, st)
			total += w
		}
	}
	sortInts(stars)
	roll := func() int {
		r := rng.IntN(max(total, 1))
		for _, st := range stars {
			if r -= pool.rates[st]; r < 0 {
				return st
			}
		}
		return stars[len(stars)-1]
	}
	out := make([]int32, 0, n)
	best := 0
	for i := range n {
		if len(rare) > 0 && rng.IntN(100) < rarePct { // 每日推荐：2% 出该种族的稀有卡（用户要求）
			mall.Pity[typ] = 0
			best = max(best, 5)
			out = append(out, rare[rng.IntN(len(rare))])
			continue
		}
		st := roll()
		if pool.pity > 0 && mall.Pity[typ]+1 >= pool.pity {
			st = 5
		}
		if pool.tenMin > 0 && n == 10 && i == n-1 && best < pool.tenMin {
			st = max(st, pool.tenMin)
		}
		if len(byStar[st]) == 0 { // 池里没有该星级就退回最高的
			st = stars[len(stars)-1]
		}
		if st == 5 {
			mall.Pity[typ] = 0
		} else {
			mall.Pity[typ]++
		}
		best = max(best, st)
		l := byStar[st]
		if st == 5 && len(wish) > 0 && rng.IntN(100) < wishPct { // 寻英帖：出 5 星时更可能是所选的护法
			l = wish
		}
		out = append(out, l[rng.IntN(len(l))])
	}
	return out
}

func poolType(pool *recruitPool) int {
	for t, p := range recruitPools {
		if p == pool {
			return t
		}
	}
	return 0
}

func sortInts(l []int) {
	for i := range l {
		for j := i + 1; j < len(l); j++ {
			if l[j] < l[i] {
				l[i], l[j] = l[j], l[i]
			}
		}
	}
}
