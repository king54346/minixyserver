package xymsrv

import (
	"slices"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 缘分：出战卡满足缘分条件时，攻击 / 生命按百分比提升，多条缘分连乘。
//
// 依据 client logic/pet_prop.lua：属性 = floor((等级公式 + 装备加成) × 缘分倍率)，
// 倍率 = ∏(1 + 生效档的百分比)；relate_mgr.getActiveExt 给出每条缘分的条件是否满足：
//   - 有缘护法（npet，逗号分隔多名）：全部在出战阵容里 → 取第 1 档；
//   - 有缘装备（nequip，如 5 星金武器、4 星紫武器）：出战位穿着第 1 件 → 第 1 档，否则穿着第 2 件 → 第 2 档；
//   - 有缘法术书（nmagic）：同装备，按出战位所穿法术书判断；
//   - 护法名可按 relate_rep 互相替换（如 孙悟空 / 斗战胜佛）。
// 例：东华帝君 缘分 2407「神农尺,晓风残月」hp 15;8——穿金武器神农尺 +15% 生命，只穿紫武器晓风残月 +8%。
// 大缘（together_npet / together_att / together_hp）不在这个公式里，未实现。

// relateRatio 是出战位 fp 上的卡 pi 的缘分倍率（hp 为 true 时算生命，否则攻击）；不在出战阵容时为 1。
func (p *Player) relateRatio(pi *gamedata.PetInfo, fp int, hp bool) float64 {
	sl := p.fightSlot(fp)
	if pi == nil || sl == nil || fp <= 0 {
		return 1
	}
	names := p.fightNames()
	suits := map[int32]bool{}
	for _, pos := range sl.Suit {
		if s := p.suitByPos(pos); s != nil {
			suits[int32(s.Type)] = true
		}
	}
	books := map[int32]bool{}
	for _, pos := range sl.Book {
		if b := p.bookByPos(pos); b != nil {
			books[b.Type] = true
		}
	}
	ratio := 1.0
	for _, id := range pi.Relate {
		r := gamedata.Relates[id]
		if r == nil {
			continue
		}
		vals := r.Att
		if hp {
			vals = r.HP
		}
		if len(vals) == 0 {
			continue
		}
		act := relateActive(r, names, suits, books)
		switch {
		case len(act) > 0 && act[0]:
			ratio *= 1 + vals[0]/100
		case len(act) > 1 && act[1] && len(vals) > 1:
			ratio *= 1 + vals[1]/100
		}
	}
	return ratio
}

// relateActive 按 getActiveExt 的顺序给出各条件是否满足：[护法]、每件装备、每本法术书（法术书只认第一本穿着的）。
func relateActive(r *gamedata.RelateInfo, names map[string]bool, suits, books map[int32]bool) []bool {
	var act []bool
	if len(r.NPet) > 0 {
		all := true
		for _, n := range r.NPet {
			if !names[n] && !names[gamedata.RelateReps[n]] {
				all = false
				break
			}
		}
		act = append(act, all)
	}
	for _, e := range r.NEquip {
		act = append(act, suits[e])
	}
	got := false
	for _, m := range r.NMagic {
		f := !got && books[m]
		got = got || f
		act = append(act, f)
	}
	return act
}

// ridList 是 S_UPDATE_PET 的 combine_relate / together_relate：[{rid}]。
func ridList(ids []int32) []xym.Msg {
	out := make([]xym.Msg, 0, len(ids))
	for _, id := range ids {
		out = append(out, xym.Msg{"rid": id})
	}
	return out
}

// extendRatio 是修行结缘得到的人物缘（Pet.Combine）带来的倍率（出战时）。照 relate_mgr：
//   - 只结缘：和普通人物缘一样，npet 都在出战阵容 → 第 1 档（如「礼仪之邦」与孔子同上阵攻击 +10%）；
//   - 已同修（Pet.Together）：getExtendPetRelateInfo——npet 与 together_npet 的第一名，每名在出战阵容 +10，
//     不在阵上但拥有 5 星的那名按其同名卡数（至多 10）计，加成 = min(合计, together_att / hp, 15)%。
func (p *Player) extendRatio(pet *Pet, hp bool) float64 {
	if len(pet.Combine) == 0 || p.fightPos(pet.BagPos) <= 0 {
		return 1
	}
	names := p.fightNames()
	ratio := 1.0
	for _, id := range pet.Combine {
		r := gamedata.Relates[id]
		if r == nil {
			continue
		}
		if slices.Contains(pet.Together, id) && len(r.TogetherNPet) > 0 {
			limit := r.TogetherAtt
			if hp {
				limit = r.TogetherHP
			}
			if len(limit) == 0 {
				continue
			}
			effect := 0.0
			for _, n := range append(slices.Clone(r.NPet), r.TogetherNPet[0]) {
				if names[n] || names[gamedata.RelateReps[n]] {
					effect += 10
				} else if b := p.bestBench5(n); b > 0 {
					effect += float64(min(b, 10))
				}
			}
			ratio *= 1 + min(effect, limit[0], 15)/100
			continue
		}
		vals := r.Att
		if hp {
			vals = r.HP
		}
		if len(vals) > 0 && len(r.NPet) > 0 && relateActive(r, names, nil, nil)[0] {
			ratio *= 1 + vals[0]/100
		}
	}
	return ratio
}

// bestBench5 是名为 name（或可替换名）、没在出战阵容的 5 星卡里最多的同名卡数（同修的后援加成）。
func (p *Player) bestBench5(name string) int {
	best := 0
	for _, pet := range p.Pets {
		pi := gamedata.Pets[pet.Type]
		if pi == nil || pi.Star != 5 || p.fightPos(pet.BagPos) > 0 || (pi.Name != name && gamedata.RelateReps[name] != pi.Name) {
			continue
		}
		best = max(best, pet.TLAdd)
	}
	return best
}

// fightNames 是当前出战阵容里所有卡的名字。
func (p *Player) fightNames() map[string]bool {
	names := map[string]bool{}
	lu := p.lineup(p.FightNo)
	if lu == nil {
		return names
	}
	for _, sl := range lu.Slots {
		if sl == nil || sl.Pet == 0 {
			continue
		}
		for _, pet := range p.Pets {
			if pet.BagPos == sl.Pet {
				if pi := gamedata.Pets[pet.Type]; pi != nil {
					names[pi.Name] = true
				}
			}
		}
	}
	return names
}
