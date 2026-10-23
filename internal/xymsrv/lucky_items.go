package xymsrv

import "math/rand/v2"

// 随机道具（背包「使用」，ITEM_PTO.C_USE_ITEM）：奥义秘录、神兵宝鼎、法术秘录、装备宝鼎，以及 20 个合成一件的
// 奥义残片 / 神兵精铁。概率照道具描述：
//
//   - 奥义秘录 11106：5 星法术碎片 85% / 5 星法术 5% / 奥义残片 11128 10%
//   - 神兵宝鼎 11107：5 星装备碎片 85% / 5 星装备 5% / 神兵精铁 11129 10%
//   - 法术秘录 10122：3 星 48% / 4 星 50% / 5 星 2% 法术一本；装备宝鼎 10123：3 / 4 / 5 星装备 49% / 50% / 1%
//   - 奥义残片 11128 / 神兵精铁 11129：满 20 个合成稀有五星法术 / 装备一件（客户端神兵精铁按钮是「合成」，
//     一次发 use_num 20；奥义残片走普通「使用」）。
//
// 估计：碎片每次 1 个；「稀有五星」按随机 5 星（不含奇缘幻境专属）。

type luckyItem struct {
	drops []boxDrop
	per   int32 // 每次消耗几个
}

var luckyItems = map[int]luckyItem{
	11106: {drops: []boxDrop{{85, "GPSP5", 1}, {5, "GPFS5", 1}, {10, "WP11128", 1}}, per: 1},
	11107: {drops: []boxDrop{{85, "GPJT5", 1}, {5, "GPZB5", 1}, {10, "WP11129", 1}}, per: 1},
	10122: {drops: []boxDrop{{48, "GPFS3", 1}, {50, "GPFS4", 1}, {2, "GPFS5", 1}}, per: 1},
	10123: {drops: []boxDrop{{49, "GPZB3", 1}, {50, "GPZB4", 1}, {1, "GPZB5", 1}}, per: 1},
	11128: {drops: []boxDrop{{1, "GPFS5", 1}}, per: 20},
	11129: {drops: []boxDrop{{1, "GPZB5", 1}}, per: 20},
}

// useLucky 用 n 个随机道具（合成类按每 per 个一次），返回是否是这类道具；数量不够时 ok 但 used 为 0。
func (p *Player) useLucky(typ int, n int32, rng *rand.Rand, d *bagDelta) (ok bool, used int32) {
	li, ok := luckyItems[typ]
	if !ok {
		return false, 0
	}
	times := min(max(n, li.per), p.itemCount(typ)) / li.per // 合成类点一次「使用」也按凑满一份算
	if times < 1 {
		return true, 0
	}
	total := 0
	for _, x := range li.drops {
		total += x.w
	}
	for range times {
		r := rng.IntN(total)
		for _, x := range li.drops {
			if r -= x.w; r < 0 {
				p.giveCode(x.code, x.cnt, rng, d)
				break
			}
		}
	}
	return true, times * li.per
}
