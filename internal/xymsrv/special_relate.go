package xymsrv

import (
	"slices"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
)

// 特殊缘分（logic/relate_mgr.lua specilal / getSpecialDesc，非 trunk 分支）：
//   - 5 星项羽获得【挥扫 N】，N = 5 星虞姬的同名卡数（最高 10）；
//   - 牛魔王获得【分水 N】，N = 5 星避水金睛兽的同名卡数；
//   - 地藏菩萨获得【疯魔 N】，N = 5 星谛听的同名卡数；
//   - 5 星二郎神获得【背水 N】，N = 5 星天狗的同名卡数。
// 客户端在虞姬 / 项羽（3、4 星）等卡的缘分说明里显示这条，同名卡为 0 时灰色。
//
// 估计：和普通缘分一样要求两人同时在出战阵容（伙伴任意星级）；获得者本身已有同族神通时取等级高的。

type specialRelate struct {
	receivers []int32 // 获得神通的护法
	partners  []int32 // 需要同时上阵的伙伴（任意星级）
	source    int32   // 同名卡数取自这张 5 星卡
	base      int32   // 1 级神通号
}

var specialRelates = []specialRelate{
	{[]int32{15023}, []int32{13016, 14026, 15031}, 15031, 20251},               // 项羽 ← 虞姬：挥扫
	{[]int32{25005}, []int32{43015, 44025, 45030}, 45030, 20401},               // 牛魔王 ← 避水金睛兽：分水
	{[]int32{23013, 24023, 25029}, []int32{43016, 44026, 45031}, 45031, 20051}, // 地藏菩萨 ← 谛听：疯魔
	{[]int32{35019}, []int32{43017, 44027, 45032}, 45032, 20011},               // 二郎神 ← 天狗：背水
}

// applySpecialRelates 给出战阵容里满足条件的护法加上特殊缘分的神通（playerTeam 调用）。
func (p *Player) applySpecialRelates(team []*battle.Fighter) {
	on := map[int32]bool{}
	for _, f := range team {
		on[f.Icon] = true
	}
	for _, r := range specialRelates {
		partner := slices.ContainsFunc(r.partners, func(t int32) bool { return on[t] })
		src := p.petByType(r.source)
		if !partner || src == nil || src.TLAdd <= 0 {
			continue
		}
		id := r.base + int32(min(src.TLAdd, 10)) - 1
		fam := gamedata.Talents[r.base].Family
		for _, f := range team {
			if !slices.Contains(r.receivers, f.Icon) {
				continue
			}
			i := slices.IndexFunc(f.Talents, func(t int32) bool {
				ti := gamedata.Talents[t]
				return ti != nil && ti.Family == fam
			})
			switch {
			case i < 0:
				f.Talents = append(f.Talents, id)
			case f.Talents[i] < id:
				f.Talents[i] = id
			}
		}
	}
}
