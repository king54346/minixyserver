package xymsrv

import (
	"slices"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 缘分：出战卡满足缘分条件时，攻击 / 生命按百分比提升，多条缘分连乘。
//
// 依据 client logic/pet_prop.lua、logic/relate_mgr.lua、view/PetDetailView.lua：
//   - 属性 = floor((等级公式 + 装备加成) × 缘分倍率)，倍率 = ∏(1 + 生效档的百分比)；
//   - 有缘装备（nequip，如 5 星金武器、4 星紫武器）：出战位穿着第 1 件 → 第 1 档，否则穿着第 2 件 → 第 2 档；
//   - 有缘法术书（nmagic）：同装备，按出战位所穿法术书判断；
//   - 有缘护法（npet，逗号分隔多名；护法名可按 relate_rep 互相替换，如 孙悟空 / 斗战胜佛）：
//     - 3/4 星：全部在出战阵容里 → 取第 1 档；
//     - 5 星未同修（getPetRelateInfo，含援护）：npet 里每名在出战阵容 +10，不在阵上但拥有 5 星的那名按其同名卡数
//       （TLAdd，至多 10）计，加成 = min(合计, att / hp 第 1 档, 15)%；
//     - 5 星已同修（Pet.Together，getExtendPetRelateInfo，代替普通人物缘与援护）：npet 与 together_npet 的第一名，
//       每名在出战阵容 +10，不在阵上但拥有 5 星的那名按其同名卡数（至多 10）计，加成 = min(合计, together_att / hp, 15)%。

// relateRatio 是出战位 fp 上的卡 pi 的缘分倍率（hp 为 true 时算生命，否则攻击）；不在出战阵容时为 1。
func (p *Player) relateRatio(pi *gamedata.PetInfo, fp int, hp bool) float64 {
	sl := p.fightSlot(fp)
	if pi == nil || sl == nil || fp <= 0 {
		return 1
	}
	pet := p.petAtFightPos(fp)
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
		if len(r.NPet) > 0 {
			ratio *= 1 + p.personRelatePct(pet, pi, id, r, names, hp)/100
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

// personRelatePct 计算一条人物缘 r（npet 非空）的加成百分比（0–15）。
// 5 星护法：若该缘分已开启同修（Pet.Together），按 getExtendPetRelateInfo 算并代替普通缘分 / 援护；
// 否则按 getPetRelateInfo 算（含场下 5 星同名卡援护加成）。非 5 星护法仅在 npet 全部上阵时生效。
func (p *Player) personRelatePct(pet *Pet, pi *gamedata.PetInfo, id int32, r *gamedata.RelateInfo, names map[string]bool, hp bool) float64 {
	if pi == nil || len(r.NPet) == 0 {
		return 0
	}
	if pi.Star < 5 {
		vals := r.Att
		if hp {
			vals = r.HP
		}
		if len(vals) > 0 && relateActive(r, names, nil, nil)[0] {
			return vals[0]
		}
		return 0
	}
	list := r.NPet
	vals := r.Att
	if hp {
		vals = r.HP
	}
	if pet != nil && slices.Contains(pet.Together, id) && len(r.TogetherNPet) > 0 {
		list = append(slices.Clone(r.NPet), r.TogetherNPet[0])
		vals = r.TogetherAtt
		if hp {
			vals = r.TogetherHP
		}
	}
	if len(vals) == 0 {
		return 0
	}
	effect := 0.0
	for _, n := range list {
		if names[n] || names[gamedata.RelateReps[n]] {
			effect += 10
		} else if b := p.bestBench5(n); b > 0 {
			effect += float64(min(b, 10))
		}
	}
	return min(effect, vals[0], 15)
}

// extendRatio 是修行结缘得到的人物缘（Pet.Combine）带来的倍率（出战时）。
func (p *Player) extendRatio(pet *Pet, hp bool) float64 {
	if len(pet.Combine) == 0 || p.fightPos(pet.BagPos) <= 0 {
		return 1
	}
	pi := gamedata.Pets[pet.Type]
	names := p.fightNames()
	ratio := 1.0
	for _, id := range pet.Combine {
		if pi != nil && slices.Contains(pi.Relate, id) {
			continue
		}
		if r := gamedata.Relates[id]; r != nil && len(r.NPet) > 0 {
			ratio *= 1 + p.personRelatePct(pet, pi, id, r, names, hp)/100
		}
	}
	return ratio
}

// fightRelatePets 返回当前出战阵容里与 changed 中的 5 星护法有缘（含同修）的出战 5 星护法（不含已在 changed 里的），
// 供场下 5 星同名卡数量（TLAdd）变动时一并下发 S_UPDATE_PET 刷新客户端显示的血量 / 攻击。
func (p *Player) fightRelatePets(changed []*Pet) []*Pet {
	touched := map[string]bool{}
	seen := map[*Pet]bool{}
	for _, pet := range changed {
		if pet == nil {
			continue
		}
		seen[pet] = true
		if pi := gamedata.Pets[pet.Type]; pi != nil && pi.Star == 5 {
			touched[pi.Name] = true
			if rep := gamedata.RelateReps[pi.Name]; rep != "" {
				touched[rep] = true
			}
		}
	}
	if len(touched) == 0 {
		return nil
	}
	lu := p.lineup(p.FightNo)
	if lu == nil {
		return nil
	}
	var out []*Pet
	for _, sl := range lu.Slots {
		if sl == nil || sl.Pet == 0 {
			continue
		}
		for _, pet := range p.Pets {
			if pet.BagPos != sl.Pet || seen[pet] {
				continue
			}
			pi := gamedata.Pets[pet.Type]
			if pi == nil || pi.Star != 5 {
				continue
			}
			hit := false
			for _, id := range append(slices.Clone(pi.Relate), pet.Combine...) {
				r := gamedata.Relates[id]
				if r == nil {
					continue
				}
				for _, n := range append(slices.Clone(r.NPet), r.TogetherNPet...) {
					if touched[n] {
						hit = true
						break
					}
				}
				if hit {
					break
				}
			}
			if hit {
				seen[pet] = true
				out = append(out, pet)
			}
		}
	}
	return out
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

// fightNames 是当前出战阵容里所有卡的名字（含本场借来的援护）。
func (p *Player) fightNames() map[string]bool {
	names := map[string]bool{}
	for _, n := range p.helperNames { // 援护：借来的护法也算上阵（武则天「吾皇万岁」借李世民也生效）
		names[n] = true
	}
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
