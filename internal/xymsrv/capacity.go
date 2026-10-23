package xymsrv

import (
	"math"

	"turnrpg/internal/gamedata"
)

// 战力：照客户端 logic/capacity.lua getPetCapLikeServer——
// (攻击 × 4 + 生命) × 法术加成 × 神通加成 × 洞府修炼加成（本服无洞府，按 1），
// 攻击 / 生命用发给客户端的值（等级公式 + 装备 + 缘分，petStats）。阵容「总战力」客户端自己按这个算，
// 开战前的「战力」（C_GET_FIGHT_POINT）与切换阵容的「计算战力」由服务端算，所以要一致。
//
//   - 法术加成 = 1 + Σ ⌊(a + b × (等级 − 1)) × 位置系数 × 同名(天将)加成⌋₀.₀₁，a、b 按星级（magicAdd）；
//     位置系数：天生法术 1；第一本书 1 − min(35, 天生法术施放率)%；第二本书再乘 1 − min(35, 第一本书施放率)%。
//     同名加成 = 1 + ⌊2 × 天将阶 / 施放率⌋₀.₀₁；精修再乘 1 + 精修次数 × 目标数 × 0.1 × 施放率%
//     （目标数 magic_mgr.lua MAGIC_TARGET_COUNT）。
//   - 神通加成 = 1 + Σ 已开启的槽 talentAdd[该级 need] × 槽等级 × (1 + 三槽激发等级和 × 10 / 300)
//     + 第四神通 talentAdd[need] × 等级。

var (
	capTalentAdd = map[int]float64{1: 0.025, 2: 0.05, 3: 0.075}
	capMagicAdd  = map[int][2]float64{1: {0.171, 0.011}, 2: {0.159, 0.015}, 3: {0.182, 0.024}, 4: {0.212, 0.028}, 5: {0.278, 0.043}}
)

// magicTargetCount 是法术目标规则对应的目标数（magic_mgr.lua MAGIC_TARGET_COUNT）。
var magicTargetCount = map[int]int{1: 1, 2: 1, 3: 1, 4: 1, 5: 1, 6: 1, 7: 5, 8: 1, 9: 3, 10: 2, 11: 1, 12: 1, 13: 1,
	14: 5, 15: 3, 16: 3, 17: 5}

func floor2(v float64) float64 { return math.Floor(v*100) / 100 }

// petCap 是出战卡 pet 的战力（不在出战阵容时法术加成按 1）。
func (p *Player) petCap(pet *Pet) float64 {
	pi := gamedata.Pets[pet.Type]
	if pi == nil {
		return 0
	}
	att, hp := p.petStats(pet)
	return float64(att*4+hp) * floor2(p.capMagicRate(pet, pi)) * floor2(p.capTalentRate(pet, pi))
}

func (p *Player) capMagicRate(pet *Pet, pi *gamedata.PetInfo) float64 {
	fp := p.fightPos(pet.BagPos)
	if fp <= 0 {
		return 1
	}
	type worn struct {
		typ   int32
		grade int
		tj    int
		jx    int
	}
	var list [3]*worn // 下标 0 天生法术、1 第一本书、2 第二本书
	if pi.Magic != 0 {
		list[0] = &worn{pi.Magic, pet.magicLv(), 0, 0}
	}
	if sl := p.fightSlot(fp); sl != nil {
		for i, pos := range sl.Book {
			if b := p.bookByPos(pos); b != nil {
				list[i+1] = &worn{b.Type, b.Grade, p.magicTJ(b.Type), b.JX}
			}
		}
	}
	rate := func(w *worn) float64 {
		if m := gamedata.Magics[w.typ]; m != nil {
			return float64(m.Rate)
		}
		return 0
	}
	add := 0.0
	for i, w := range list {
		if w == nil || gamedata.Magics[w.typ] == nil {
			continue
		}
		m := gamedata.Magics[w.typ]
		star, ok := capMagicAdd[m.Star]
		if !ok {
			continue
		}
		pos := 1.0
		switch i {
		case 1:
			if list[0] != nil {
				pos = 1 - math.Min(35, rate(list[0]))*0.01
			}
		case 2:
			switch {
			case list[1] != nil && list[0] != nil:
				pos = (1 - math.Min(35, rate(list[0]))*0.01) * (1 - math.Min(35, rate(list[1]))*0.01)
			case list[0] != nil:
				pos = 1 - math.Min(35, rate(list[0]))*0.01
			}
		}
		same := 1.0
		if w.tj > 0 && m.Rate > 0 {
			same = 1 + floor2(2*float64(w.tj)/float64(m.Rate))
		}
		jx := 1 + float64(w.jx*magicTargetCount[m.Target])*0.1*float64(m.Rate)*0.01
		add += floor2((star[0] + star[1]*float64(w.grade-1)) * jx * pos * same)
	}
	return add + 1
}

func (p *Player) capTalentRate(pet *Pet, pi *gamedata.PetInfo) float64 {
	tj := p.petTJ(pet)
	jifa := 1 + float64((tj[0]+tj[1]+tj[2])*10)/300
	add := 0.0
	for i, l := range pi.Talents {
		g := pet.shownTalGrade(i)
		if len(l) == 0 || g <= 0 || i >= talentSlots(pet.SoulGrade) {
			continue
		}
		add += capTalentAdd[talentNeed(l[min(g, len(l))-1])] * float64(g) * jifa
	}
	if ext := pet.extTalent(pi); ext != 0 {
		add += capTalentAdd[talentNeed(ext)] * float64(pet.ExtGrade[pet.ExtIdx-1])
	}
	return add + 1
}

// teamCap 是一组卡的总战力（客户端 getAllFightPetCapLikeServer：各卡相加后取整）。
func (p *Player) teamCap(pets []*Pet) int {
	total := 0.0
	for _, pet := range pets {
		total += p.petCap(pet)
	}
	return int(math.Floor(total)) + p.zfFightPoint() // 阵法战力（zhenfa_mgr.getZhenFaAdd）
}
