package xymsrv

import (
	"math/rand/v2"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 第四神通（护法详情第四个神通图标 → view/PetNewDetailViewExt.lua Disishentong）：五星护法三个神通都升满后可用
// 无字天书 11153 激活，激活后每用一本随机给三个候选（PetInfo.ExTalents，客户端 ex_talent1-3）里没满级的一个 +1 级，
// 再从中选一个当第四神通在战斗里生效。
//
// 依据：S_UPDATE_PET / S_OPEN_DISISHENTONG 的 extlgrade1-3（三个候选等级）、extlidx（选中的候选 1-3，0 未选）、
// extlstate（抓包：非五星 0；五星未满 1；三神通满 2 = 可激活；已激活 3）；PetNewDetailMgr：star > 4 且
// extlstate ≥ 2 才显示第四神通图标。抓包（session-01）：C_OPEN_TIANSHU_NEW{pet_id = 卡类型} →
// S_OPEN_DISISHENTONG{flag 0}；state 2 时 C_USE_TIANSHU{pet_type} 扣一本无字天书，随机一个候选 0→1，
// 回 S_UPDATE_PET 与 S_OPEN_DISISHENTONG{extlstate 3, lastUpIdx 被升的候选, beforeUpdateGrade 升前等级, flag 1}；
// C_SWITCH_EXTALENT{pet_type, extalent_idx} → S_UPDATE_PET、S_OPEN_DISISHENTONG{flag 0}。
//
// 估计：只有升过（等级 > 0）的候选能选；第四神通与原神通同族时按等级高的算（战斗里同族只留一个）。

const wuziTianshu = 11153

func (p *Player) extlState(pet *Pet) int {
	pi := gamedata.Pets[pet.Type]
	if pi == nil || pi.Star < 5 || len(pi.ExTalents[0]) == 0 {
		return 0
	}
	if pet.ExtOn {
		return 3
	}
	if !pet.talentsFull(pi) {
		return 1
	}
	return 2
}

// talentsFull：三个神通槽都升满了（战报 skillfull、第四神通开启条件）。
func (pet *Pet) talentsFull(pi *gamedata.PetInfo) bool {
	n := 0
	for i, l := range pi.Talents {
		if len(l) == 0 {
			continue
		}
		if pet.talGrade(i) < len(l) {
			return false
		}
		n++
	}
	return n > 0
}

// extTalent 是选中的第四神通（神通号），没有返回 0。
func (pet *Pet) extTalent(pi *gamedata.PetInfo) int32 {
	if !pet.ExtOn || pet.ExtIdx < 1 || pet.ExtIdx > 3 {
		return 0
	}
	l, g := pi.ExTalents[pet.ExtIdx-1], pet.ExtGrade[pet.ExtIdx-1]
	if g < 1 || g > len(l) {
		return 0
	}
	return l[g-1]
}

func (p *Player) disishentongMsg(pet *Pet, lastUp, before, flag int) xym.Msg {
	return xym.Msg{"extlgrade1": pet.ExtGrade[0], "extlgrade2": pet.ExtGrade[1], "extlgrade3": pet.ExtGrade[2],
		"extlidx": pet.ExtIdx, "type": pet.Type, "lastUpIdx": lastUp, "beforeUpdateGrade": before,
		"extlstate": p.extlState(pet), "flag": flag}
}

func init() {
	Handle("PET_PTO.C_OPEN_TIANSHU_NEW", func(s *Session, p *Player, m xym.Msg) {
		if pet := p.petByType(int32(m["pet_id"].(int64))); pet != nil {
			s.SendExt("ETC_PTO.S_OPEN_DISISHENTONG", p.disishentongMsg(pet, 0, 0, 0))
		}
	})
	Handle("PET_PTO.C_USE_TIANSHU", onUseTianshu)
	Handle("PET_PTO.C_SWITCH_EXTALENT", onSwitchExTalent)
}

func onUseTianshu(s *Session, p *Player, m xym.Msg) {
	pet := p.petByType(int32(m["pet_type"].(int64)))
	if pet == nil {
		return
	}
	pi := gamedata.Pets[pet.Type]
	st := p.extlState(pet)
	if st < 2 {
		s.retMsg("三个神通都升满后才能激活第四神通。")
		return
	}
	var open []int
	for i, l := range pi.ExTalents {
		if pet.ExtGrade[i] < len(l) {
			open = append(open, i)
		}
	}
	if len(open) == 0 {
		s.retMsg("第四神通已经全部满级了。")
		return
	}
	if p.itemCount(wuziTianshu) < 1 {
		s.retMsg("无字天书不足。")
		return
	}
	d := &bagDelta{}
	ch, rm := p.takeItems(wuziTianshu, 1)
	d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	i := open[rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(pet.NpcNum))).IntN(len(open))]
	before := pet.ExtGrade[i]
	pet.ExtGrade[i]++
	pet.ExtOn = true
	p.saveLog(s)
	s.sendDelta(p, d)
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
	s.SendExt("ETC_PTO.S_OPEN_DISISHENTONG", p.disishentongMsg(pet, i+1, before, 1))
}

func onSwitchExTalent(s *Session, p *Player, m xym.Msg) {
	pet := p.petByType(int32(m["pet_type"].(int64)))
	if pet == nil {
		return
	}
	idx := int(m["extalent_idx"].(int64))
	if idx < 1 || idx > 3 || !pet.ExtOn || pet.ExtGrade[idx-1] < 1 {
		s.retMsg("请先选择一个已经升级过的神通。")
		return
	}
	pet.ExtIdx = idx
	p.saveLog(s)
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
	s.SendExt("ETC_PTO.S_OPEN_DISISHENTONG", p.disishentongMsg(pet, 0, 0, 0))
}
