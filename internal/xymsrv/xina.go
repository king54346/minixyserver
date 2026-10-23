package xymsrv

import (
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 吸纳（护法详情「吸纳」→ view/CPetView.lua CPetXinaView）：
//
//   - 吸纳护法 PET_PTO.C_TRANS_PET_EXP{from_npcnum, to_npcnum}：目标得到被吸纳护法的全部经验
//     （pet_mgr.total_exp = 各级所需之和 + 本级经验），被吸纳的护法变回 1 级；花 被吸纳护法等级²×10 银两
//     （pet_mgr.getPetXinaCash）。超过上限（人物等级×2）的经验按 5000 一颗折成中级经验丹 10014
//     （界面提示「多余的经验将自动转换为经验丹」）。
//   - 吸纳丹药 ITEM_PTO.C_USE_DRUG{type, npcnum, count}：经验丹 10013 / 10014 / 10015 / 10113 各加
//     5000 / 5000 / 10000 / 100000 经验（客户端 DrugEffect）；道具描述：低级的不能超过人物等级，中级不能超过
//     人物等级×2（超出的经验消失），高级、特级不受人物等级限制（只到满级 100）。
//   - 选卡时客户端会发 PET_PTO.C_APPLY_PET{bag_pos, npcnum} 等该卡的 S_UPDATE_PET（带锁）。
//
// 估计：出战 / 上阵 / 锁定的护法不能被吸纳；不能吸纳自己。

var drugExp = map[int]int32{10013: 5000, 10014: 5000, 10015: 10000, 10113: 100000}

const transDrug = 10014

func init() {
	Handle("PET_PTO.C_APPLY_PET", func(s *Session, p *Player, m xym.Msg) {
		pet := p.petByNpc(int32(m["npcnum"].(int64)))
		l := []xym.Msg{}
		if pet != nil {
			l = append(l, p.petFull(pet))
		}
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": l})
	})
	Handle("PET_PTO.C_TRANS_PET_EXP", onTransPetExp)
	Handle("ITEM_PTO.C_USE_DRUG", onUseDrug)
}

// totalExp 是卡牌累计的全部经验。
func (pet *Pet) totalExp() int32 {
	pi := gamedata.Pets[pet.Type]
	if pi == nil {
		return pet.Exp
	}
	n := pet.Exp
	for g := 1; g < pet.Grade; g++ {
		n += gamedata.CardNeed(pi.Star, g)
	}
	return n
}

// capExcess：到了 limit 级后本级经验最多留到差 1 点升级，返回截下来的多余经验。
func (pet *Pet) capExcess(limit int) int32 {
	pi := gamedata.Pets[pet.Type]
	if pi == nil || pet.Grade < limit {
		return 0
	}
	keep := max(gamedata.CardNeed(pi.Star, pet.Grade)-1, 0)
	if pet.Exp <= keep {
		return 0
	}
	ex := pet.Exp - keep
	pet.Exp = keep
	return ex
}

func onTransPetExp(s *Session, p *Player, m xym.Msg) {
	from, to := p.petByNpc(int32(m["from_npcnum"].(int64))), p.petByNpc(int32(m["to_npcnum"].(int64)))
	switch {
	case from == nil || to == nil || from == to:
		s.retMsg("请选择要被吸纳的护法。")
		return
	case from.Locked || p.inLineup(from.BagPos):
		s.retMsg("已上阵或已锁定的护法不能被吸纳。")
		return
	}
	exp := from.totalExp()
	if exp <= 0 {
		s.retMsg("这个护法没有可以吸纳的经验。")
		return
	}
	if !s.spend(int32(from.Grade*from.Grade*10), 0) {
		return
	}
	limit := min(s.role.Grade*2, gamedata.MaxPetGrade)
	to.addExp(exp, limit)
	from.Grade, from.Exp = 1, 0
	d := &bagDelta{}
	if n := to.capExcess(limit) / drugExp[transDrug]; n > 0 {
		d.items = append(d.items, p.addItem(transDrug, n)...)
		d.gains = append(d.gains, gain{rtItem, transDrug, n})
	}
	p.saveLog(s)
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(to), p.petFull(from)}})
	if list := s.sendDelta(p, d); len(list) > 0 {
		s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, list))
	}
}

func onUseDrug(s *Session, p *Player, m xym.Msg) {
	typ, n := int(m["type"].(int64)), int32(m["count"].(int64))
	pet := p.petByNpc(int32(m["npcnum"].(int64)))
	per, ok := drugExp[typ]
	n = min(max(n, 1), p.itemCount(typ))
	if !ok || pet == nil || n < 1 {
		s.retMsg("经验丹不足。")
		return
	}
	limit := gamedata.MaxPetGrade
	switch typ {
	case 10013:
		limit = s.role.Grade
	case 10014:
		limit = s.role.Grade * 2
	}
	limit = min(limit, gamedata.MaxPetGrade)
	if pet.Grade >= limit {
		s.retMsg("护法已经达到这种经验丹的等级上限。")
		return
	}
	d := &bagDelta{}
	ch, rm := p.takeItems(typ, n)
	d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	pet.addExp(per*n, limit)
	pet.capExcess(limit) // 超出上限的经验消失
	p.saveLog(s)
	s.sendDelta(p, d)
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
}
