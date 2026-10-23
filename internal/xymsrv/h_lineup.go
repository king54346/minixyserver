package xymsrv

import (
	"strconv"

	"turnrpg/internal/xym"
)

// 布阵（LINEUP_PTO.C_SET_LINEUPS，格式同 S_SEND_LINEUP）与换队长（PET_PTO.C_SET_LEADER）。
// 回包照抓包：S_UPDATE_PET（出战位变化的卡）、S_WEAR_SUIT、S_WEAR_BOOK、S_SET_LEADER_SUC、S_SEND_LINEUP。

func init() {
	Handle("LINEUP_PTO.C_SET_LINEUPS", onSetLineups)
	Handle("PET_PTO.C_SET_LEADER", func(s *Session, p *Player, m xym.Msg) {
		fp := int(m["fight_pos"].(int64))
		lu := p.lineup(p.FightNo)
		if lu == nil || p.petAtFightPos(fp) == nil {
			s.sendMsg("该位置没有护法。")
			return
		}
		lu.Leader = fp
		p.saveLog(s)
		s.SendExt("PET_PTO.S_SET_LEADER_SUC", xym.Msg{"leader": fp, "skill": p.leaderSkill()})
	})
}

func onSetLineups(s *Session, p *Player, m xym.Msg) {
	owned := map[int]bool{}
	for _, pet := range p.Pets {
		owned[pet.BagPos] = true
	}
	var lineups []*Lineup
	reqs, _ := m["lineups"].([]xym.Msg)
	for _, r := range reqs {
		lu := &Lineup{No: int(r["lineup_no"].(int64)), Leader: int(r["leader"].(int64))}
		for i := range lu.Slots {
			lu.Slots[i] = &Slot{}
		}
		used := map[int]bool{}
		info, _ := r["info"].([]xym.Msg)
		for _, x := range info {
			pos, pet := int(x["pos"].(int64)), int(x["pet"].(int64))
			if pos < 1 || pos > lineupSlots || (pet != 0 && (!owned[pet] || used[pet])) {
				s.sendMsg("阵容数据有误。")
				s.SendExt("LINEUP_PTO.S_SEND_LINEUP", p.lineupsMsg()) // 解开 LOCK，客户端回到原阵容
				return
			}
			used[pet] = pet != 0
			sl := lu.Slots[pos-1]
			sl.Pet = pet
			sl.Suit = [3]int{int(x["spos1"].(int64)), int(x["spos2"].(int64)), int(x["spos3"].(int64))}
			sl.Book = [2]int{int(x["mpos1"].(int64)), int(x["mpos2"].(int64))}
			p.fixSlotGear(sl) // 按部位归位（客户端本地数据过期时可能把装备填错栏）
		}
		if lu.Leader < 1 || lu.Leader > lineupSlots || lu.Slots[lu.Leader-1].Pet == 0 {
			// 阵容必须指定队长（用户要求）：有护法却没有队长（或队长位是空的）时不保存
			empty := true
			for _, sl := range lu.Slots {
				empty = empty && sl.Pet == 0
			}
			if !empty {
				s.sendMsg("请先为阵容" + strconv.Itoa(lu.No) + "设置队长。")
				s.SendExt("LINEUP_PTO.S_SEND_LINEUP", p.lineupsMsg()) // 解开 LOCK，客户端回到原阵容
				return
			}
			lu.Leader = 1
		}
		lineups = append(lineups, lu)
	}
	fno := int(m["fno"].(int64))
	before := map[int32]int{}
	for _, pet := range p.Pets {
		before[pet.NpcNum] = p.fightPos(pet.BagPos)
	}
	if len(lineups) > 0 {
		p.Lineups = lineups
	}
	if p.lineup(fno) != nil {
		p.FightNo = fno
	}
	var changed []xym.Msg
	for _, pet := range p.sortedPets() {
		if fp := p.fightPos(pet.BagPos); fp != before[pet.NpcNum] || fp > 0 { // 出战卡都回：缘分随阵容 / 装备变化
			changed = append(changed, p.petFull(pet))
		}
	}
	p.saveLog(s)
	if len(changed) > 0 {
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": changed})
	}
	// 换出战阵容后穿戴跟着换：发新出战阵容的整套穿戴（之前发空表，客户端一直显示上一套的装备）
	s.SendExt("ITEM_PTO.S_WEAR_SUIT", p.wearSuitMsg())
	s.SendExt("ITEM_PTO.S_WEAR_BOOK", p.wearBookMsg())
	if lu := p.lineup(p.FightNo); lu != nil {
		s.SendExt("PET_PTO.S_SET_LEADER_SUC", xym.Msg{"leader": lu.Leader, "skill": p.leaderSkill()})
	}
	s.SendExt("LINEUP_PTO.S_SEND_LINEUP", p.lineupsMsg())
}

// 主阵容换人（PET_PTO.C_PET_CHANGE_POS：把卡放到出战位 fight_pos，0 = 卸下）与对调两个出战位
// （C_PET_REPLACE）。客户端 pet_mgr 发这两条时都在本地挂了等 S_UPDATE_PET 的锁。
func init() {
	Handle("PET_PTO.C_PET_CHANGE_POS", func(s *Session, p *Player, m xym.Msg) {
		pet := p.petByNpc(int32(m["npcnum"].(int64)))
		fp := int(m["fight_pos"].(int64))
		lu := p.lineup(p.FightNo)
		if pet == nil || lu == nil || fp < 0 || fp > lineupSlots {
			s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0}) // 解锁
			return
		}
		s.changeSlots(p, func() {
			from := p.fightPos(pet.BagPos)
			switch {
			case fp == 0 || fp == from: // 卸下：客户端「卸下当前护法」发的是该卡自己所在的位置（CPetSelectListView.onClickBtnUnload）
				if from > 0 {
					lu.Slots[from-1].Pet = 0
				}
			case from > 0: // 已在阵上：与目标位对调——装备、法术书跟着护法走（整个槽位交换），队长也跟着护法
				lu.Slots[from-1], lu.Slots[fp-1] = lu.Slots[fp-1], lu.Slots[from-1]
				switch lu.Leader {
				case from:
					lu.Leader = fp
				case fp:
					lu.Leader = from
				}
			default: // 新上阵：顶掉目标位原来的卡
				lu.Slots[fp-1].Pet = pet.BagPos
			}
		})
	})
	Handle("PET_PTO.C_PET_REPLACE", func(s *Session, p *Player, m xym.Msg) {
		a, b := int(m["frompos"].(int64)), int(m["topos"].(int64))
		lu := p.lineup(p.FightNo)
		if lu == nil || a < 1 || b < 1 || a > lineupSlots || b > lineupSlots {
			s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0})
			return
		}
		s.changeSlots(p, func() {
			lu.Slots[a-1], lu.Slots[b-1] = lu.Slots[b-1], lu.Slots[a-1]
			switch lu.Leader {
			case a:
				lu.Leader = b
			case b:
				lu.Leader = a
			}
		})
	})
}

// changeSlots 改出战阵容后回 S_UPDATE_PET（出战位变化的卡，总会回一条以解开客户端的锁），
// 队长位空了就落到第一个有卡的位置。
func (s *Session) changeSlots(p *Player, change func()) {
	before := map[int32]int{}
	for _, pet := range p.Pets {
		before[pet.NpcNum] = p.fightPos(pet.BagPos)
	}
	change()
	if lu := p.lineup(p.FightNo); lu != nil && (lu.Leader < 1 || lu.Slots[lu.Leader-1].Pet == 0) {
		for i, sl := range lu.Slots {
			if sl.Pet != 0 {
				lu.Leader = i + 1
				break
			}
		}
	}
	changed := []xym.Msg{}
	for _, pet := range p.sortedPets() {
		if fp := p.fightPos(pet.BagPos); fp != before[pet.NpcNum] || fp > 0 { // 出战卡都回：缘分随阵容 / 装备变化
			changed = append(changed, p.petFull(pet))
		}
	}
	p.saveLog(s)
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": changed})
	// 官方抓包（C_PET_CHANGE_POS / C_PET_REPLACE）：随后发穿戴、队长与阵容。客户端的穿戴按出战位存，
	// 不重发的话换位后装备还显示在原来的位置上。
	s.SendExt("ITEM_PTO.S_WEAR_SUIT", p.wearSuitMsg())
	s.SendExt("ITEM_PTO.S_WEAR_BOOK", p.wearBookMsg())
	if lu := p.lineup(p.FightNo); lu != nil {
		s.SendExt("PET_PTO.S_SET_LEADER_SUC", xym.Msg{"leader": lu.Leader, "skill": p.leaderSkill()})
	}
	s.SendExt("LINEUP_PTO.S_SEND_LINEUP", p.lineupsMsg())
}
