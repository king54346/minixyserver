package xymsrv

import (
	"cmp"
	"slices"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 一键装备（阵容界面「一键装备」→ ITEM_PTO.C_ON_ONE_CLICK_EQUIP{}，客户端等 S_WEAR_BOOK 解锁）：给出战阵容
// 重新分配所有装备和法术书。
//
// 依据：view/CTeamView.lua（提示「自动计算最优法术装备搭配」）；抓包回包顺序 S_UPDATE_PET（出战卡）、S_WEAR_SUIT、
// S_UPDATE_SUIT、S_WEAR_BOOK、S_SET_LEADER_SUC、S_SEND_LINEUP。
//
// 分配规则（按需求）：先满足缘分，再战力最大化。
//  1. 缘分：把每张出战卡每条缘分里的有缘装备 / 法术书（第 1 档、第 2 档）都列成候选，增益 = 该档百分比折成的战力
//     （攻击 × 4 + 生命，同 petPower），按增益从高到低分配；某条缘分已满足、部位已占、背包里没有空闲的那件就跳过。
//  2. 战力：剩下的武器 / 衣服位按装备加成折战力（攻击 × 4，生命 × 1，含天将加成）从高到低补；鞋（只有速度）
//     按星级、强化等级；法术书按星级、等级；同一张卡不穿两本同名法术，也不穿和天生法术同名的书。
//     卡按基础战力从高到低先挑。

type ocPet struct {
	fp    int
	pet   *Pet
	pi    *gamedata.PetInfo
	suits [3]*Suit
	books [2]*Book
}

func init() {
	Handle("ITEM_PTO.C_ON_ONE_CLICK_EQUIP", onOneClickEquip)
}

func (p *Player) gearValue(s *Suit) int {
	si := gamedata.Suits[s.Type]
	if si == nil {
		return 0
	}
	b := si.Bonus(s.Grade)
	b += p.tjBonus(s.Type, b)
	switch si.Prop {
	case "att":
		return int(b) * 4
	case "hp":
		return int(b)
	}
	return si.Star*1000 + s.Grade // 鞋：星级优先
}

func bookValue(b *Book) int { return magicStar(b.Type)*1000 + b.Grade }

func onOneClickEquip(s *Session, p *Player, m xym.Msg) {
	lu := p.lineup(p.FightNo)
	if lu == nil {
		s.SendExt("ITEM_PTO.S_WEAR_BOOK", p.wearBookMsg())
		return
	}
	team := p.planEquip(lu.Slots)
	p.applyPlan(lu, team)
	var pets, brief []xym.Msg
	for _, op := range team {
		pets = append(pets, p.petFull(op.pet))
		brief = append(brief, p.petBrief(op.pet))
	}
	p.saveLog(s)
	// 先穿戴再更新卡：阵容界面的缘分颜色、总战力是客户端按穿戴现算的，只在 S_PET_LIST（setData → showDetail）
	// 时整体刷新，所以最后补一条 S_PET_LIST。
	s.SendExt("ITEM_PTO.S_WEAR_SUIT", p.wearSuitMsg())
	s.SendExt("ITEM_PTO.S_WEAR_BOOK", p.wearBookMsg())
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": pets})
	s.SendExt("LINEUP_PTO.S_SEND_LINEUP", p.lineupsMsg())
	s.SendExt("PET_PTO.S_PET_LIST", xym.Msg{"is_all": 0, "pets": brief})
}

// applyPlan 把分配结果写回阵容 lu。
func (p *Player) applyPlan(lu *Lineup, team []*ocPet) {
	for _, op := range team {
		sl := lu.Slots[op.fp-1]
		for k, x := range op.suits {
			sl.Suit[k] = 0
			if x != nil {
				sl.Suit[k] = x.Pos
			}
		}
		for k, x := range op.books {
			sl.Book[k] = 0
			if x != nil {
				sl.Book[k] = x.Pos
			}
		}
	}
}

// planEquip 给阵容 slots 上的卡分配装备和法术书（不改存档）。各阵容独立分配，同一件装备可以出现在不同阵容里。
func (p *Player) planEquip(slots [8]*Slot) []*ocPet {
	var team []*ocPet
	for i, sl := range slots {
		if sl == nil || sl.Pet == 0 {
			continue
		}
		if pet := p.petAtBag(sl.Pet); pet != nil && gamedata.Pets[pet.Type] != nil {
			team = append(team, &ocPet{fp: i + 1, pet: pet, pi: gamedata.Pets[pet.Type]})
		}
	}
	slices.SortStableFunc(team, func(a, b *ocPet) int { return cmp.Compare(petPower(b.pet), petPower(a.pet)) })
	usedSuit, usedBook := map[*Suit]bool{}, map[*Book]bool{}
	suits := slices.Clone(p.Suits)
	slices.SortStableFunc(suits, func(a, b *Suit) int { return cmp.Compare(p.gearValue(b), p.gearValue(a)) })
	books := slices.Clone(p.Books)
	slices.SortStableFunc(books, func(a, b *Book) int { return cmp.Compare(bookValue(b), bookValue(a)) })
	freeSuit := func(typ int) *Suit {
		for _, x := range suits {
			if x.Type == typ && !usedSuit[x] {
				return x
			}
		}
		return nil
	}
	canBook := func(op *ocPet, typ int32) bool {
		if typ == op.pi.Magic {
			return false
		}
		for _, b := range op.books {
			if b != nil && b.Type == typ {
				return false
			}
		}
		return true
	}
	freeBook := func(op *ocPet, typ int32) *Book {
		if !canBook(op, typ) {
			return nil
		}
		for _, x := range books {
			if x.Type == typ && !usedBook[x] {
				return x
			}
		}
		return nil
	}
	putSuit := func(op *ocPet, x *Suit) bool {
		k, ok := suitSlot[gamedata.Suits[x.Type].Kind]
		if !ok || op.suits[k] != nil || usedSuit[x] {
			return false
		}
		op.suits[k], usedSuit[x] = x, true
		return true
	}
	putBook := func(op *ocPet, x *Book) bool {
		if usedBook[x] || !canBook(op, x.Type) {
			return false
		}
		for i := range op.books {
			if op.books[i] == nil {
				op.books[i], usedBook[x] = x, true
				return true
			}
		}
		return false
	}

	// 1. 缘分
	type cand struct {
		op    *ocPet
		rel   int32
		equip bool
		typ   int32
		gain  float64
	}
	var cands []cand
	for _, op := range team {
		att, hp := float64(op.pi.Att(op.pet.Grade))*4, float64(op.pi.HP(op.pet.Grade))
		for _, rid := range op.pi.Relate {
			r := gamedata.Relates[rid]
			if r == nil {
				continue
			}
			tierGain := func(t int) float64 {
				g := 0.0
				if t < len(r.Att) {
					g += att * r.Att[t] / 100
				}
				if t < len(r.HP) {
					g += hp * r.HP[t] / 100
				}
				return g
			}
			for t, e := range r.NEquip {
				cands = append(cands, cand{op, rid, true, e, tierGain(t)})
			}
			for t, mg := range r.NMagic {
				cands = append(cands, cand{op, rid, false, mg, tierGain(t)})
			}
		}
	}
	slices.SortStableFunc(cands, func(a, b cand) int { return cmp.Compare(b.gain, a.gain) })
	done := map[[2]any]bool{}
	for _, c := range cands {
		key := [2]any{c.op, c.rel}
		if done[key] || c.gain <= 0 {
			continue
		}
		if c.equip {
			if x := freeSuit(int(c.typ)); x != nil && gamedata.Suits[x.Type] != nil && putSuit(c.op, x) {
				done[key] = true
			}
		} else if x := freeBook(c.op, c.typ); x != nil && putBook(c.op, x) {
			done[key] = true
		}
	}

	// 2. 战力：按价值从高到低补空位
	for _, op := range team {
		for _, x := range suits {
			if !usedSuit[x] && gamedata.Suits[x.Type] != nil {
				putSuit(op, x)
			}
		}
		for _, x := range books {
			if op.books[0] != nil && op.books[1] != nil {
				break
			}
			putBook(op, x)
		}
	}

	return team
}

// petAtBag 按背包位找卡。
func (p *Player) petAtBag(bagPos int) *Pet {
	for _, pet := range p.Pets {
		if pet.BagPos == bagPos {
			return pet
		}
	}
	return nil
}
