package xymsrv

import (
	"cmp"
	"slices"

	"turnrpg/internal/xym"
)

// 多套阵容（阵容界面「切换阵容」→ view/MultiTeamView.lua，logic/multiteam.lua）：
//
//   - S_SEND_LINEUP.count 是已开的阵容数（抓包：5 套阵容时 count 5、fno 4）。客户端 OpenGrade {25, 25, 35, 35}：
//     免费阵容数 = 达到的门槛数，最多 3（MAX_FREE_COUNT）；之后最多再买 4 套（MAX_BUY_COUNT），价格
//     BuyTeamPrice {500, 1000, 1000, 1000} 元宝，C_BUY_LINEUP{times = 第几次购买} → S_SEND_LINEUP（带锁）。
//     最多 7 套（MAX_TEAM_COUNT）。
//   - 计算战力 C_CALC_FIGHTPOINT{cno, lineups[1 套]} → S_SEND_FIGHTPOINT{cno, fightpoint}。
//   - 切换阵容里的一键装备 LINEUP_PTO.C_ONE_CLICK_EQUIP{cno, lineups[1 套]} →
//     S_SEND_ONE_CLICK_EQUIP_LINEUP{cno, lineups}，只算不存（玩家点「选择出战」时 C_SET_LINEUPS 才存）。
//
// 估计：新开的阵容是空的；不同阵容可以共用同一件装备 / 法术书（同一阵容里不能重复）。

const maxLineups = 7

var (
	lineupOpenGrade = []int{25, 25, 35, 35}
	lineupPrice     = []int32{500, 1000, 1000, 1000}
)

// lineupCount 是已开的阵容数（至少 1）。
func (p *Player) lineupCount() int {
	free := 0
	for _, g := range lineupOpenGrade {
		if p.grade() >= g {
			free++
		}
	}
	return min(max(1, min(free, 3))+p.LineupBuys, maxLineups)
}

// ensureLineups 补齐 1..lineupCount 号阵容（新的为空），并按编号排好。
func (p *Player) ensureLineups() {
	for no := 1; no <= p.lineupCount(); no++ {
		if p.lineup(no) == nil {
			lu := &Lineup{No: no}
			for i := range lu.Slots {
				lu.Slots[i] = &Slot{}
			}
			p.Lineups = append(p.Lineups, lu)
		}
	}
	slices.SortFunc(p.Lineups, func(a, b *Lineup) int { return cmp.Compare(a.No, b.No) })
}

func init() {
	Handle("LINEUP_PTO.C_BUY_LINEUP", onBuyLineup)
	Handle("LINEUP_PTO.C_CALC_FIGHTPOINT", onCalcFightPoint)
	Handle("LINEUP_PTO.C_ONE_CLICK_EQUIP", onLineupOneClick)
}

func onBuyLineup(s *Session, p *Player, m xym.Msg) {
	times := int(m["times"].(int64))
	switch {
	case p.grade() < lineupOpenGrade[0]:
		s.retMsg("25 级开放多套阵容。")
	case times != p.LineupBuys+1 || times > len(lineupPrice) || p.lineupCount() >= maxLineups:
		s.retMsg("已经不能再购买阵容了。")
	case s.spend(0, lineupPrice[times-1]):
		p.LineupBuys++
		p.ensureLineups()
		p.saveLog(s)
	}
	s.SendExt("LINEUP_PTO.S_SEND_LINEUP", p.lineupsMsg())
}

// reqSlots 把请求里的一套阵容（info[pos, pet, spos1-3, mpos1-2]）转成 Slot（只认自己的卡）。
func (p *Player) reqSlots(m xym.Msg) (no, leader int, slots [8]*Slot) {
	for i := range slots {
		slots[i] = &Slot{}
	}
	l, _ := m["lineups"].([]xym.Msg)
	if len(l) == 0 {
		return 0, 0, slots
	}
	no, leader = int(l[0]["lineup_no"].(int64)), int(l[0]["leader"].(int64))
	info, _ := l[0]["info"].([]xym.Msg)
	for _, x := range info {
		pos, pet := int(x["pos"].(int64)), int(x["pet"].(int64))
		if pos < 1 || pos > lineupSlots || p.petAtBag(pet) == nil {
			continue
		}
		slots[pos-1] = &Slot{Pet: pet,
			Suit: [3]int{int(x["spos1"].(int64)), int(x["spos2"].(int64)), int(x["spos3"].(int64))},
			Book: [2]int{int(x["mpos1"].(int64)), int(x["mpos2"].(int64))}}
		p.fixSlotGear(slots[pos-1]) // 装备按部位归位
	}
	return no, leader, slots
}

func onCalcFightPoint(s *Session, p *Player, m xym.Msg) {
	_, _, slots := p.reqSlots(m)
	// 临时把出战阵容换成请求里的这套来算（装备、缘分都按这套），算完换回
	lu := p.lineup(p.FightNo)
	if lu == nil {
		s.SendExt("LINEUP_PTO.S_SEND_FIGHTPOINT", xym.Msg{"cno": m["cno"], "fightpoint": 0})
		return
	}
	saved := lu.Slots
	lu.Slots = slots
	var pets []*Pet
	for _, sl := range slots {
		if pet := p.petAtBag(sl.Pet); pet != nil {
			pets = append(pets, pet)
		}
	}
	total := p.teamCap(pets)
	lu.Slots = saved
	s.SendExt("LINEUP_PTO.S_SEND_FIGHTPOINT", xym.Msg{"cno": m["cno"], "fightpoint": total})
}

func onLineupOneClick(s *Session, p *Player, m xym.Msg) {
	no, leader, slots := p.reqSlots(m)
	if no == 0 {
		no = int(m["cno"].(int64))
	}
	lu := &Lineup{No: no, Leader: leader, Slots: slots}
	p.applyPlan(lu, p.planEquip(slots))
	info := []xym.Msg{}
	for i, sl := range lu.Slots {
		info = append(info, xym.Msg{"pos": i + 1, "pet": sl.Pet, "spos1": sl.Suit[0], "spos2": sl.Suit[1], "spos3": sl.Suit[2],
			"mpos1": sl.Book[0], "mpos2": sl.Book[1]})
	}
	s.SendExt("LINEUP_PTO.S_SEND_ONE_CLICK_EQUIP_LINEUP", xym.Msg{"cno": m["cno"],
		"lineups": []xym.Msg{{"lineup_no": no, "leader": leader, "info": info}}})
}
