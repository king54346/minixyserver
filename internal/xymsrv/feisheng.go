package xymsrv

import (
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 飞升（护法详情「飞升」→ view/PetUpGradeView.lua CPetUpGradeNewView）：3 星 → 4 星、4 星 → 5 星，卡类型换成
// 客户端 auto/pet_upgrade_relation.lua 里的升级后类型（PetInfo.Upgrade），等级、经验保留，境界和神通从头来。
//
// 请求 PET_PTO.C_UPGRADE_CARD_EX{pet_num, star3_pet, star3_soul, star4_pet, star4_soul, star5_soul, force_gold,
// stuff_list[3]{type "suit"/"magic", key itemnum/pos, stuff_type}, stuff_list_ext}，客户端带锁等 ETC_PTO.S_PET_UPGRADE{npcnum}。
// 之前没处理，界面一直「连接中」到超时重连。
//
// 消耗照客户端界面的算法（服务端按同样的式子算、校验）：
//   - 「元」进度：3 星要 30、4 星要 150；同名卡（本卡和并入的同名卡）每张 6 / 30，魂魄 3/4/5 星每个 1/2/6。
//   - 3 件同星级装备或法术书材料（没穿戴、没锁定）；不够时勾「用元宝补齐」每缺一件 20 / 100 元宝。
//   - 筑基丹 100 个（3 星 三星筑基丹 10095、4 星 四星筑基丹 10096），不够的每个 4 / 8 元宝。
//   - 银两 249900 / 855200 减去已经提升境界花掉的银两；魂值 = 补满境界 10 还要的魂值。
//   - 飞升只带走本卡：没用掉的低星同名卡留成一张原类型的护法（之前都并到了飞升后的卡上，4 星同名全变成 5 星同名）。
//   - 3 星飞升时 4、5 星魂魄额外每个 20 / 30 元宝；4 星飞升的扩展消耗（客户端 getSilverCostExt 等）这里不收。
//
// 估计：魂魄从本卡类型的魂魄里扣（客户端只发数量）；飞升后神通点清零、并入的同名卡用掉。

var (
	fsNeed      = map[int]int{3: 30, 4: 150}
	fsPetValue  = map[int]int{3: 6, 4: 30}
	fsSoulValue = map[int]int{3: 1, 4: 2, 5: 6}
	fsDan       = map[int]int{3: 10095, 4: 10096}
	fsDanPrice  = map[int]int32{3: 4, 4: 8}
	fsMatGold   = map[int]int32{3: 20, 4: 100}
	fsSilver    = map[int]int32{3: 249900, 4: 855200}
)

const fsDanNeed = 100

func init() {
	Handle("PET_PTO.C_OPEN_UPGRADE_CARD_EX", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("PET_PTO.S_OPEN_UPGRADE_CARD_EX", xym.Msg{"value": 0, "max_value": 0})
	})
	Handle("PET_PTO.C_UPGRADE_CARD_EX", onFeisheng)
}

func onFeisheng(s *Session, p *Player, m xym.Msg) {
	num := func(k string) int { v, _ := m[k].(int64); return int(v) }
	pet := p.petByNpc(int32(num("pet_num")))
	fail := func(msg string) {
		s.retMsg(msg) // S_RET_MSG 解开客户端等 S_PET_UPGRADE 的锁
	}
	if pet == nil {
		fail("护法不存在。")
		return
	}
	pi := gamedata.Pets[pet.Type]
	if pi == nil || pi.Upgrade == 0 || gamedata.Pets[pi.Upgrade] == nil || (pi.Star != 3 && pi.Star != 4) {
		fail("这个护法不能飞升。")
		return
	}
	star := pi.Star

	// 「元」进度
	cards := num("star3_pet")
	if star == 4 {
		cards = num("star4_pet")
	}
	cards = min(max(cards, 1), pet.TLAdd+1)
	soulN := num(map[int]string{3: "star3_soul", 4: "star4_soul"}[star])
	soulN = min(soulN, int(p.Souls[pet.Type]))
	value := cards*fsPetValue[star] + soulN*fsSoulValue[star]
	if value < fsNeed[star] {
		fail("少侠，同名卡和魂魄还不够飞升。")
		return
	}

	// 材料
	mt := &materials{}
	seenS, seenB := map[*Suit]bool{}, map[*Book]bool{}
	l, _ := m["stuff_list"].([]xym.Msg)
	for _, x := range l {
		key, _ := x["key"].(int64)
		switch x["type"] {
		case "suit":
			if su := p.suitByNum(int32(key)); su != nil && !seenS[su] && !su.Locked && !p.suitWorn(su.Pos) && suitStar(su.Type) == star {
				seenS[su] = true
				mt.suits = append(mt.suits, su)
			}
		case "magic":
			if b := p.bookByPos(int(key)); b != nil && !seenB[b] && !b.Locked && !p.bookWorn(b.Pos) && magicStar(b.Type) == star {
				seenB[b] = true
				mt.books = append(mt.books, b)
			}
		}
	}
	missing := 3 - len(mt.suits) - len(mt.books)
	if missing > 0 && num("force_gold") == 0 {
		fail("少侠，你还没有足够的材料")
		return
	}

	// 元宝、银两、魂值
	gold := int32(max(missing, 0)) * fsMatGold[star]
	dans := min(p.itemCount(fsDan[star]), fsDanNeed)
	gold += int32(fsDanNeed-dans) * fsDanPrice[star]
	if star == 3 {
		gold += int32(num("star4_soul"))*20 + int32(num("star5_soul"))*30
	}
	silver := fsSilver[star]
	var soul int32
	for g := 0; g < 10; g++ {
		c := tishenSoulCost[g][star-1]
		if g < pet.SoulGrade {
			silver -= c * 100 * int32(star)
		} else {
			soul += c
		}
	}
	silver = max(silver, 0)
	ok := s.roleUpdate(func(ro *Role) bool {
		if ro.Gold < gold || ro.Cash < silver || ro.SoulExp < soul {
			return false
		}
		ro.Gold -= gold
		ro.Cash -= silver
		ro.SoulExp -= soul
		return true
	}, "gold", "cash", "soulexp")
	if !ok {
		fail("元宝、银两或魂值不足，无法飞升。")
		return
	}

	// 扣材料、筑基丹、魂魄、同名卡，换卡
	s.consume(p, mt)
	d := &bagDelta{}
	if dans > 0 {
		ch, rm := p.takeItems(fsDan[star], dans)
		d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	}
	if soulN > 0 {
		p.addSoul(pet.Type, -int32(soulN), d)
	}
	changed := p.feishengSplit(pet, pi, cards)
	p.track(evFeisheng, 1)
	var removed []xym.Msg
	if keep, gone := p.feishengMerge(pet); gone != nil { // 已有飞升后类型的护法：并成一张（同名 +1）
		removed = append(removed, xym.Msg{"npcnum": gone.NpcNum})
		changed[0] = keep
	}
	p.saveLog(s)
	s.sendDelta(p, d)
	if len(removed) > 0 {
		s.SendExt("PET_PTO.S_REMOVE_PET", xym.Msg{"pets": removed})
	}
	full, brief := []xym.Msg{}, []xym.Msg{}
	for _, x := range changed {
		full, brief = append(full, p.petFull(x)), append(brief, p.petBrief(x))
	}
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": full})
	s.SendExt("PET_PTO.S_PET_LIST", xym.Msg{"is_all": 0, "pets": brief})
	s.SendExt("ETC_PTO.S_PET_UPGRADE", xym.Msg{"npcnum": changed[0].NpcNum}) // 并卡后 pet 已删，要指向留下的那张
}

// feishengMerge：飞升后若背包里已有同类型的护法，两张并成一张（留在阵上的那张，否则留原有的那张），
// 返回留下的和被并掉的；两张都在阵上时不并。
func (p *Player) feishengMerge(pet *Pet) (keep, gone *Pet) {
	var other *Pet
	for _, x := range p.Pets {
		if x != pet && x.Type == pet.Type {
			other = x
			break
		}
	}
	if other == nil {
		return pet, nil
	}
	keep, gone = other, pet
	if p.inLineup(pet.BagPos) && !p.inLineup(other.BagPos) {
		keep, gone = pet, other
	} else if p.inLineup(pet.BagPos) {
		return pet, nil
	}
	keep.mergeFrom(gone)
	p.removePet(gone)
	return keep, gone
}

// feishengSplit 把 pet 飞升成 pi.Upgrade：用掉 cards 张同名卡（含本卡）。没用掉的低星同名卡不能跟着升星，
// 留成一张原类型的护法（同名卡分开结算）。返回有变化的护法。
func (p *Player) feishengSplit(pet *Pet, pi *gamedata.PetInfo, cards int) []*Pet {
	oldType, left := pet.Type, pet.TLAdd-(cards-1) // 剩下的同名卡张数（不含本卡）
	pet.Type, pet.TLAdd = pi.Upgrade, 0
	pet.SoulGrade, pet.TalGrade, pet.TLPoint = 0, [3]int{}, 0
	pet.ExtOn, pet.ExtGrade, pet.ExtIdx = false, [3]int{}, 0
	changed := []*Pet{pet}
	if left > 0 {
		rest := p.addPet(oldType)
		rest.TLAdd = left - 1
		rest.TLPoint = rest.TLAdd * shenTongPoint[pi.Star]
		changed = append(changed, rest)
	}
	return changed
}
