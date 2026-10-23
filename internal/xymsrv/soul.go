package xymsrv

import (
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 魂魄（logic/pet_mgr.lua，apk-src 里的明文脚本）：
//   - 聚魂 PET_PTO.C_SOUL_TO_PET{type, no_update}：用 PetSoulCount[星级]（1–5 星 2 / 4 / 6 / 15 / 20）个该护法的
//     魂魄合成一张卡（护法详情「聚魂」、升星材料里的「合成」）；4 / 5 星魂魄够 12 / 16 个时可以用通用魂魄道具
//     10116 / 10117 补足（canMergeSoul）。回 S_UPDATE_CARD（客户端等它）、护法更新、S_REWARD_MSG；
//     no_update = 1（升星界面合成材料）不弹奖励框。
//   - 融魂 PET_PTO.C_DROP_PET_SOUL{pets[npcnum], souls[type, amount]}：把护法 / 魂魄融成魂值。护法给
//     total_soul_exp = 已花在境界上的魂值 + PetSoulPoint[星级]（2 / 8 / 18 / 60 / 100），魂魄每个
//     SoulSoulPoint[星级]（1–5）。回 S_UPDATE_CARD（客户端等它）、S_REMOVE_PET、S_ATTR_UPDATE soulexp。
//
// 估计：上阵 / 加锁的护法不收；本服按客户端算的数给魂值。
var (
	petSoulCount  = [6]int32{0, 2, 4, 6, 15, 20}
	petSoulPoint  = [6]int32{0, 2, 8, 18, 60, 100}
	soulSoulPoint = [6]int32{0, 1, 2, 3, 4, 5}
	soulWildItem  = map[int]int{4: 10116, 5: 10117} // 通用魂魄
	soulWildMin   = map[int]int32{4: 12, 5: 16}     // 用通用魂魄补足前至少要有的本魂魄数
)

func init() {
	Handle("PET_PTO.C_SOUL_TO_PET", onSoulToPet)
	Handle("PET_PTO.C_DROP_PET_SOUL", onDropPetSoul)
}

func onSoulToPet(s *Session, p *Player, m xym.Msg) {
	typ := int32(m["type"].(int64))
	noUpdate, _ := m["no_update"].(int64)
	pi := gamedata.Pets[typ]
	if pi == nil || pi.Star < 1 || pi.Star > 5 {
		s.retMsg("没有这个护法。")
		s.SendExt("PET_PTO.S_UPDATE_CARD", p.soulsMsg([]int32{typ}))
		return
	}
	need, have := petSoulCount[pi.Star], p.Souls[typ]
	wild := int32(0) // 要用的通用魂魄数
	if have < need {
		item, ok := soulWildItem[pi.Star]
		if !ok || have < soulWildMin[pi.Star] || have+p.itemCount(item) < need {
			s.retMsg("魂魄数量不足，无法聚魂。")
			s.SendExt("PET_PTO.S_UPDATE_CARD", p.soulsMsg([]int32{typ}))
			return
		}
		wild = need - have
	}
	d := &bagDelta{}
	if wild > 0 {
		items, removed := p.takeItems(soulWildItem[pi.Star], wild)
		d.items, d.removed = append(d.items, items...), append(d.removed, removed...)
	}
	p.addSoul(typ, -(need - wild), d)
	p.giveCard(typ, d)
	p.saveLog(s)
	list := s.sendDelta(p, d) // 含 S_UPDATE_CARD（魂魄变化，客户端等这条）、护法更新
	if noUpdate == 0 {
		s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, list))
	}
}

func onDropPetSoul(s *Session, p *Player, m xym.Msg) {
	var gain int32
	d := &bagDelta{}
	removed := []xym.Msg{}
	pets, _ := m["pets"].([]xym.Msg)
	for _, x := range pets {
		pet := p.petByNpc(int32(x["npcnum"].(int64)))
		if pet == nil || pet.Locked || p.inLineup(pet.BagPos) {
			continue
		}
		pi := gamedata.Pets[pet.Type]
		if pi == nil || pi.Star < 1 || pi.Star > 5 {
			continue
		}
		g := petSoulPoint[pi.Star]
		for i := 0; i < pet.SoulGrade && i < len(tishenSoulCost); i++ {
			g += tishenSoulCost[i][pi.Star-1]
		}
		gain += g
		p.removePet(pet)
		removed = append(removed, xym.Msg{"npcnum": pet.NpcNum})
	}
	souls, _ := m["souls"].([]xym.Msg)
	var types []int32
	for _, x := range souls {
		typ := int32(x["type"].(int64))
		n := min(int32(x["amount"].(int64)), p.Souls[typ])
		pi := gamedata.Pets[typ]
		if n <= 0 || pi == nil || pi.Star < 1 || pi.Star > 5 {
			continue
		}
		gain += soulSoulPoint[pi.Star] * n
		p.addSoul(typ, -n, d)
		types = append(types, typ)
	}
	if gain > 0 {
		s.roleUpdate(func(ro *Role) bool { ro.SoulExp += gain; return true }, "soulexp")
	}
	p.saveLog(s)
	if len(removed) > 0 {
		s.SendExt("PET_PTO.S_REMOVE_PET", xym.Msg{"pets": removed})
	}
	s.SendExt("PET_PTO.S_UPDATE_CARD", p.soulsMsg(types)) // 客户端等这条刷新界面
}
