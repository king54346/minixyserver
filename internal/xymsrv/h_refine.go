package xymsrv

import (
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// Refinement handlers: validate, charge, consume, persist, then unlock the target UI.
func onRefineSuit(s *Session, p *Player, m xym.Msg) {
	target := p.suitByNum(int32(m["itemnum"].(int64)))
	unlock := func(msg string) {
		if msg != "" {
			s.sendMsg(msg)
		}
		var l []xym.Msg
		if target != nil {
			l = append(l, target.msg())
		}
		s.SendExt("ITEM_PTO.S_UPDATE_SUIT", xym.Msg{"is_all": 0, "suits": l})
	}
	switch {
	case target == nil || gamedata.Suits[target.Type] == nil || isSuitMaterial(target.Type):
		unlock("装备不存在。")
		return
	case m["from_xiuwei"] != int64(0):
		unlock("修为强化暂未开放。")
		return
	}
	star := suitStar(target.Type)
	mt := p.suitMaterials(m, target)
	if mt.exp <= 0 {
		unlock("您还未选择消耗的装备，无法强化！")
		return
	}
	grade, exp := upTable(suitUpExp).add(star, target.Grade, target.Exp, mt.exp)
	if grade > s.role.Grade {
		unlock("装备等级不能超过人物等级。")
		return
	}
	if !s.spend(mt.exp*100*int32(star), 0) {
		unlock("")
		return
	}
	s.consume(p, mt)
	target.Grade, target.Exp = grade, exp
	target.ensureSkill() // 升到 20 级开启炼神效果（inlay.go）
	p.saveLog(s)
	if fp := p.suitFightPos(target.Pos); fp > 0 {
		if pet := p.petAtFightPos(fp); pet != nil {
			s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
		}
	}
	s.SendExt("ITEM_PTO.S_UPDATE_SUIT", xym.Msg{"is_all": 0, "suits": []xym.Msg{target.msg()}})
}

// suitFightPos 是穿着背包位 pos 装备的出战位（当前阵容），没穿为 0。
func (p *Player) suitFightPos(pos int) int {
	lu := p.lineup(p.FightNo)
	if lu == nil {
		return 0
	}
	for i, sl := range lu.Slots {
		if sl != nil && sl.Pet != 0 {
			for _, x := range sl.Suit {
				if x == pos {
					return i + 1
				}
			}
		}
	}
	return 0
}

func onUpgradeBook(s *Session, p *Player, m xym.Msg) {
	target := p.bookByPos(int(m["target_pos"].(int64)))
	unlock := func(msg string) {
		if msg != "" {
			s.sendMsg(msg)
		}
		var l []xym.Msg
		if target != nil {
			l = append(l, target.msg())
		}
		s.SendExt("ITEM_PTO.S_UPDATE_BOOK", xym.Msg{"is_all": 0, "books": l})
	}
	switch {
	case target == nil || gamedata.Magics[target.Type] == nil || isBookMaterial(target.Type):
		unlock("法术不存在。")
		return
	case m["from_xiuwei"] != int64(0):
		unlock("修为强化暂未开放。")
		return
	case target.Grade >= maxMagicGrade:
		unlock("您的法术已经达到最高级！")
		return
	}
	star := magicStar(target.Type)
	mt := p.bookMaterials(m, target)
	if mt.exp <= 0 {
		unlock("请选择强化材料!")
		return
	}
	grade, exp := upTable(magicUpExp).add(star, target.Grade, target.Exp, mt.exp)
	if !s.spend(mt.exp*100*int32(star), 0) {
		unlock("")
		return
	}
	s.consume(p, mt)
	target.Grade, target.Exp = min(grade, maxMagicGrade), exp
	p.saveLog(s)
	s.SendExt("ITEM_PTO.S_UPDATE_BOOK", xym.Msg{"is_all": 0, "books": []xym.Msg{target.msg()}})
}

// onUpgradePetMagic 强化卡牌的天生法术（客户端 curr_magic_pos > 10000 时即 npcnum），等 S_UPDATE_PET。
func onUpgradePetMagic(s *Session, p *Player, m xym.Msg) {
	pet := p.petByNpc(int32(m["npcnum"].(int64)))
	unlock := func(msg string) {
		if msg != "" {
			s.sendMsg(msg)
		}
		var l []xym.Msg
		if pet != nil {
			l = append(l, p.petFull(pet))
		}
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": l})
	}
	if pet == nil || gamedata.Pets[pet.Type] == nil || gamedata.Magics[gamedata.Pets[pet.Type].Magic] == nil {
		unlock("法术不存在。")
		return
	}
	if m["from_xiuwei"] != int64(0) {
		unlock("修为强化暂未开放。")
		return
	}
	if pet.magicLv() >= maxMagicGrade {
		unlock("您的法术已经达到最高级！")
		return
	}
	star := magicStar(gamedata.Pets[pet.Type].Magic)
	mt := p.bookMaterials(m, nil)
	if mt.exp <= 0 {
		unlock("请选择强化材料!")
		return
	}
	grade, exp := upTable(magicUpExp).add(star, pet.magicLv(), pet.MagicExp, mt.exp)
	if !s.spend(mt.exp*100*int32(star), 0) {
		unlock("")
		return
	}
	s.consume(p, mt)
	pet.MagicLv, pet.MagicExp = min(grade, maxMagicGrade), exp
	p.saveLog(s)
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
}
