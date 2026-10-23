package xymsrv

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestMallExtra：登峰造极免费次数下发、获取途径、元宝买超级请神符、每日推荐切换种族后只出该种族。
func TestMallExtra(t *testing.T) {
	c, p, srv := actWorld(t, "maller")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold = 1000; return true })
	c.sendExt("MALL_PTO.C_OPEN_PET_MALL", nil)
	if m := c.expectExt("MALL_PTO.S_OPEN_PET_MALL"); num(m, "free_high_pet_count") != 1 {
		t.Fatalf("free high %v", m["free_high_pet_count"])
	}
	c.sendExt("MALL_PTO.C_OPEN_ZHAOMU_DAOJU_WAY", xym.Msg{"id": 10317})
	if m := c.expectExt("MALL_PTO.S_OPEN_ZHAOMU_DAOJU_WAY"); num(m, "type") != 10317 {
		t.Fatalf("way %v", m)
	}
	c.sendExt("MALL_PTO.C_BUY_ITEM", xym.Msg{"type": 10317, "count": 2})
	got := c.until(t, "MALL_PTO.S_OPEN_DAOJU_SHANGCHENG")
	if p.itemCount(10317) != 2 || srv.roles.Get(p.UID).Gold != 400 || got["ETC_PTO.S_REWARD_MSG"] == nil {
		t.Fatalf("buy: items %d gold %d", p.itemCount(10317), srv.roles.Get(p.UID).Gold)
	}

	race := raceToday(time.Now())%4 + 1 // 换一个不是今天默认的种族
	c.sendExt("ETC_PTO.C_MRTJ_KACHIQIEHUAN", xym.Msg{"race": race})
	c.expectExt("ETC_PTO.S_ATTR_UPDATE")
	l := c.expectExt("ETC_PTO.S_OPEN_MRTJ_KACHIQIEHUAN")["list"].([]xym.Msg)
	if num(l[race-1], "st") != 2 || srv.roles.Get(p.UID).Gold != 400-raceSwitch {
		t.Fatalf("switch %v gold %d", l, srv.roles.Get(p.UID).Gold)
	}
	if m := c.expectExt("MALL_PTO.S_OPEN_PET_MALL"); num(m, "race_choose_today") != race {
		t.Fatalf("race_choose_today %v", m["race_choose_today"])
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	mall := p.mall(time.Now())
	for _, typ := range drawPets(recruitPools[5], mall, 30, rand.New(rand.NewPCG(1, 2))) {
		if gamedata.Pets[typ].Race != race {
			t.Fatalf("每日推荐 drew race %d, want %d", gamedata.Pets[typ].Race, race)
		}
	}
}

// TestWishBias：寻英帖里选了的 5 星，抽到 5 星时约一半是它。
func TestWishBias(t *testing.T) {
	five := poolPets["超级请神符"][5]
	if len(five) < 3 {
		t.Skip("pool too small")
	}
	mall := &Mall{Pity: map[int]int{}, WishDfzj: []int32{five[0]}}
	hit, all := 0, 0
	rng := rand.New(rand.NewPCG(3, 4))
	for range 400 {
		for _, typ := range drawPets(recruitPools[1], mall, 10, rng) {
			if gamedata.Pets[typ].Star == 5 {
				all++
				if typ == five[0] {
					hit++
				}
			}
		}
	}
	if r := float64(hit) / float64(all); r < 0.4 || r > 0.65 {
		t.Fatalf("wish share %.2f (%d/%d)", r, hit, all)
	}
}

// TestShop：商城入口（两个页签 + 道具商城）、C_OPEN_ITEM_MALL 回 S_OPEN_DAOJU_SHANGCHENG、特权礼包（VIP 不够拒绝、
// 够了扣元宝得礼包、每周只能买一个周礼包）。
func TestShop(t *testing.T) {
	c, p, srv := actWorld(t, "shopper")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Gold, ro.VIP = 1000, 0; return true }) // 默认 VIP15，先降到 0 测拒绝
	c.sendExt("ETC_PTO.C_CLICK_ENTRANCE", xym.Msg{"id": entranceMall})
	if l := c.expectExt("MALL_PTO.S_OPEN_MALL_BOTTOM_BTNS")["buttons"].([]xym.Msg); len(l) != 2 || num(l[1], "id") != tagTequan {
		t.Fatalf("tabs %v", l)
	}
	if m := c.expectExt("MALL_PTO.S_OPEN_DAOJU_SHANGCHENG"); num(m, "libao_id") != tagDaoju || len(m["items"].([]xym.Msg)) != len(itemShop) {
		t.Fatalf("daoju %v", m["libao_id"])
	}
	c.sendExt("MALL_PTO.C_OPEN_ITEM_MALL", nil)
	c.expectExt("MALL_PTO.S_OPEN_DAOJU_SHANGCHENG")
	c.sendExt("MALL_PTO.C_OPEN_MALL_TAG", xym.Msg{"id": tagTequan})
	l := c.expectExt("MALL_PTO.S_OPEN_TEQUANLIBAO")["items"].([]xym.Msg)
	if len(l) != len(tequanGifts) || num(l[0], "price") != 100 {
		t.Fatalf("tequan %v", l[:1])
	}
	c.sendExt("MALL_PTO.C_BUY_GIFT", xym.Msg{"type": 11112}) // VIP2，人物 VIP 0
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "VIP2") {
		t.Fatalf("vip check %v", m)
	}
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.VIP = 5; return true })
	c.sendExt("MALL_PTO.C_BUY_GIFT", xym.Msg{"type": 11112})
	got := c.until(t, "MALL_PTO.S_OPEN_TEQUANLIBAO")
	if p.itemCount(11112) != 1 || srv.roles.Get(p.UID).Gold != 800 || got["ETC_PTO.S_REWARD_MSG"] == nil {
		t.Fatalf("gift %d gold %d", p.itemCount(11112), srv.roles.Get(p.UID).Gold)
	}
	c.sendExt("MALL_PTO.C_BUY_GIFT", xym.Msg{"type": 11240}) // 本周已买过周礼包（阵法也算）
	if m := c.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "本周") {
		t.Fatalf("weekly %v", m)
	}
}

// TestItemShopStones：道具商城有各种石头（凿晶、炼器、炼法、悟法、培养 / 精修吸纳、同修、结缘）。
func TestItemShopStones(t *testing.T) {
	for _, id := range []int{10216, 10217, 10249, 10250, 10310, 10311, 10417, 10416} {
		if !slices.ContainsFunc(itemShop, func(x shopItem) bool { return x.Type == id }) || gamedata.Items[id] == nil {
			t.Errorf("shop missing %d", id)
		}
	}
}

// TestDailyRare：每日推荐每抽 2% 出所选种族的稀有卡（最高星级版本），1600 抽里能出十几到几十张。
func TestDailyRare(t *testing.T) {
	m := &Mall{Race: 1, Pity: map[int]int{}}
	got := 0
	for _, ty := range drawPets(recruitPools[5], m, 1600, rand.New(rand.NewPCG(1, 1))) {
		if pi := gamedata.Pets[ty]; pi.Special && slices.Contains(rarePets[1], ty) {
			got++
		}
	}
	if got < 10 || got > 70 {
		t.Fatalf("rare %d in 1600", got)
	}
	for _, ty := range rarePets[1] {
		if gamedata.Pets[ty].Race != 1 {
			t.Fatalf("wrong race %d", ty)
		}
	}
}
