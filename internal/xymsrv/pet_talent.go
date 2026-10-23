package xymsrv

import (
	"fmt"
	"sort"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 神通（护法的三个天赋槽）：境界解锁、神通点、升级、洗点，以及同名卡并入。
//
// 依据（客户端 client2 logic/pet_mgr.lua、logic/talent_mgr.lua、view/PetNewDetailView*.lua）：
//   - 境界 soul_grade 0–10；境界 ≥1 / 3 / 5 开第 1 / 2 / 3 个神通（talent_mgr.getTalentCount、getNeedJingjieGrade）；
//     另外人物要到 3 / 9 / 13 级（pet_mgr.SHENTONG_ACTIVE_GRADES）。没开的神通不生效（战斗详情里灰掉）。
//   - 提升境界扣魂值 TishenSoulCost[境界][星级]、银两 = 魂值 × 100 × 星级，得神通点：5 星升到 6–10 境界 2 点、其余 1 点
//     （getUpgradeJingjieGotTalentPoint）。
//   - 神通升一级扣「当前等级神通」的 need 点（talent.csv need 列；analyzePetTalentInfo needPoint = tl.need）。
//   - 洗点花 30 元宝（PetNewDetailView 提示「确定要花费30元宝重置护法神通点数吗？」），返还已花的神通点。
//   - 同名卡：分水 / 疯魔等级「由同名卡数量决定」（relate_mgr），tlpoint_add 即并入的同名卡张数；
//     每张给 ShenTongPoint[星级] = 1 / 1 / 1 / 2 / 2 点。
//
// 依据（官方抓包）：
//   - session-02 45033（5 星）境界 5→6→7→8→9：每次 +2 神通点；6→7 扣魂值 300、银两 150000；回包 S_ATTR_UPDATE{soulexp, cash, gold}
//     + S_UPDATE_PET；C_UPGRADE_SKILL 每次扣 2 点（该神通 need），回包同样是 S_ATTR_UPDATE + S_UPDATE_PET；
//   - 官方号背包 352 / 267 张卡没有一种重复：拿到已有护法的卡就并进原卡；境界 0 的卡 tlpoint 正好是 tlpoint_add × 每张点数。

var (
	shenTongPoint        = [6]int{0, 1, 1, 1, 2, 2}
	shentongActiveGrades = [3]int{3, 9, 13}
	tishenSoulCost       = [10][5]int32{
		{1, 2, 3, 8, 25}, {2, 4, 6, 16, 50}, {3, 6, 9, 24, 75}, {4, 8, 12, 32, 100}, {5, 10, 15, 40, 125},
		{8, 16, 24, 64, 200}, {12, 24, 36, 96, 300}, {18, 36, 54, 144, 450}, {25, 50, 75, 200, 625}, {33, 66, 99, 264, 825},
	}
)

const (
	maxSoulGrade   = 10
	resetPointGold = 30
)

// talentSlots 是境界 g 开放的神通数。
func talentSlots(g int) int {
	switch {
	case g <= 0:
		return 0
	case g < 3:
		return 1
	case g < 5:
		return 2
	}
	return 3
}

// jingjiePoint 是升到境界 g 得的神通点。
func jingjiePoint(g, star int) int {
	if star >= 5 && g > 5 {
		return 2
	}
	return 1
}

// talentNeed 是神通 tid 升下一级要的神通点（talent.csv need）。
func talentNeed(tid int32) int {
	if t := gamedata.Talents[tid]; t != nil {
		return t.Need
	}
	return 0
}

// talentOn：第 i 槽（0 起）神通是否生效。
func (p *Player) talentOn(pet *Pet, i int) bool {
	return i < talentSlots(pet.SoulGrade) && p.grade() >= shentongActiveGrades[i]
}

// activeTalents 是这张卡在战斗里生效的神通号（每槽取第 tlgrade 级）及各自的激发等级（下标对应）。
func (p *Player) activeTalents(pet *Pet, pi *gamedata.PetInfo) ([]int32, []int) {
	tj := p.petTJ(pet)
	var ids []int32
	var lv []int
	for i, l := range pi.Talents {
		if len(l) == 0 || !p.talentOn(pet, i) {
			continue
		}
		ids = append(ids, l[min(pet.talGrade(i), len(l))-1])
		lv = append(lv, tj[i])
	}
	if ext := pet.extTalent(pi); ext != 0 { // 第四神通（disishentong.go），不带激发等级
		ids = append(ids, ext)
	}
	return ids, lv
}

// spentPoints 是这张卡升神通已花的神通点（洗点返还）。
func spentPoints(pet *Pet, pi *gamedata.PetInfo) int {
	n := 0
	for i, l := range pi.Talents {
		for g := 1; g < pet.talGrade(i) && g <= len(l); g++ {
			n += talentNeed(l[g-1])
		}
	}
	return n
}

// mergeFrom 把另一张同类护法 x（连同它已并入的同名卡）并进 pet：每张同名卡各算一次 absorb。
func (pet *Pet) mergeFrom(x *Pet) {
	for range x.TLAdd + 1 {
		pet.absorb()
	}
}

// absorb 把一张同名卡并进 pet：神通点 + ShenTongPoint[星级]，同名卡数 +1。
func (pet *Pet) absorb() {
	if pi := gamedata.Pets[pet.Type]; pi != nil && pi.Star >= 1 && pi.Star <= 5 {
		pet.TLPoint += shenTongPoint[pi.Star]
	}
	pet.TLAdd++
}

// gainPet 是获得一张卡：已有同类护法就并进去（官方不留重复卡），否则新发一张。
func (p *Player) gainPet(typ int32) *Pet {
	if old := p.petByType(typ); old != nil {
		old.absorb()
		return old
	}
	return p.addPet(typ)
}

func (p *Player) petByType(typ int32) *Pet {
	var best *Pet
	for _, pet := range p.Pets {
		if pet.Type == typ && (best == nil || p.inLineup(pet.BagPos) && !p.inLineup(best.BagPos) || pet.Grade > best.Grade) {
			best = pet
		}
	}
	return best
}

func (p *Player) inLineup(bagPos int) bool {
	for _, lu := range p.Lineups {
		for _, sl := range lu.Slots {
			if sl != nil && sl.Pet == bagPos {
				return true
			}
		}
	}
	return false
}

// mergeDuplicates 把存档里不在阵容中的重复卡并进同类里保留的那张（旧存档迁移，返还它们的境界与已花神通点不计）。
func (p *Player) mergeDuplicates() bool {
	byType := map[int32][]*Pet{}
	for _, pet := range p.Pets {
		byType[pet.Type] = append(byType[pet.Type], pet)
	}
	drop := map[*Pet]bool{}
	for _, l := range byType {
		if len(l) < 2 {
			continue
		}
		sort.SliceStable(l, func(i, j int) bool {
			a, b := l[i], l[j]
			if ia, ib := p.inLineup(a.BagPos), p.inLineup(b.BagPos); ia != ib {
				return ia
			}
			return a.Grade > b.Grade
		})
		for _, x := range l[1:] {
			if !p.inLineup(x.BagPos) {
				l[0].mergeFrom(x)
				drop[x] = true
			}
		}
	}
	if len(drop) == 0 {
		return false
	}
	kept := p.Pets[:0]
	for _, pet := range p.Pets {
		if !drop[pet] {
			kept = append(kept, pet)
		}
	}
	p.Pets = kept
	return true
}

func init() {
	Handle("PET_PTO.C_PET_UPGRADE_SOUL", onUpgradeSoul)
	Handle("PET_PTO.C_UPGRADE_SKILL", onUpgradeSkill)
	Handle("PET_PTO.C_RESET_POINT", onResetPoint)
	Handle("PET_PTO.C_PET_TO_TLPOINT", onPetToPoint)
}

func (s *Session) petUpdate(p *Player, pets ...*Pet) {
	l := make([]xym.Msg, 0, len(pets))
	for _, pet := range pets {
		l = append(l, p.petFull(pet))
	}
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": l})
}

// onUpgradeSoul：提升境界（扣魂值、银两，得神通点）。客户端等 S_UPDATE_PET 的锁。
func onUpgradeSoul(s *Session, p *Player, m xym.Msg) {
	pet := p.petByNpc(int32(m["npcnum"].(int64)))
	if pet == nil {
		s.retMsg("护法不存在。")
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{}})
		return
	}
	pi := gamedata.Pets[pet.Type]
	if pi == nil || pet.SoulGrade >= maxSoulGrade || pi.Star < 1 || pi.Star > 5 {
		s.retMsg("境界已达最大等级。")
		s.petUpdate(p, pet)
		return
	}
	soul := tishenSoulCost[pet.SoulGrade][pi.Star-1]
	cash := soul * 100 * int32(pi.Star)
	if !s.roleUpdate(func(ro *Role) bool {
		if ro.SoulExp < soul || ro.Cash < cash {
			return false
		}
		ro.SoulExp -= soul
		ro.Cash -= cash
		return true
	}, "soulexp", "cash", "gold") {
		s.retMsg(fmt.Sprintf("魂值或银两不足：需要 %d 魂值、%d 银两。", soul, cash))
		s.petUpdate(p, pet)
		return
	}
	pet.SoulGrade++
	pet.TLPoint += jingjiePoint(pet.SoulGrade, pi.Star)
	p.saveLog(s)
	s.petUpdate(p, pet)
}

// onUpgradeSkill：神通升一级（tlskill 1–3），扣当前等级神通的 need 点。
func onUpgradeSkill(s *Session, p *Player, m xym.Msg) {
	pet := p.petByNpc(int32(m["npcnum"].(int64)))
	i := int(m["tlskill"].(int64)) - 1
	if pet == nil || gamedata.Pets[pet.Type] == nil || i < 0 || i > 2 {
		s.retMsg("护法不存在。")
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{}})
		return
	}
	l := gamedata.Pets[pet.Type].Talents[i]
	g := pet.talGrade(i)
	switch {
	case !p.talentOn(pet, i):
		s.retMsg("该神通尚未开启。")
	case g >= len(l):
		s.retMsg("神通已达最高等级。")
	case pet.TLPoint < talentNeed(l[g-1]):
		s.retMsg("神通点不足。")
	default:
		pet.TLPoint -= talentNeed(l[g-1])
		pet.TalGrade[i] = g + 1
		p.saveLog(s)
		s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "soulexp", "val": s.role.SoulExp},
			{"name": "cash", "val": s.role.Cash}, {"name": "gold", "val": s.role.Gold}}})
	}
	s.petUpdate(p, pet)
}

// onResetPoint：洗点——花 30 元宝，神通回到 1 级，返还已花的神通点。
func onResetPoint(s *Session, p *Player, m xym.Msg) {
	pet := p.petByNpc(int32(m["npcnum"].(int64)))
	if pet == nil || gamedata.Pets[pet.Type] == nil {
		return
	}
	back := spentPoints(pet, gamedata.Pets[pet.Type])
	if back == 0 {
		s.retMsg("这名护法还没有使用过神通点。")
		return
	}
	if !s.roleUpdate(func(ro *Role) bool {
		if ro.Gold < resetPointGold {
			return false
		}
		ro.Gold -= resetPointGold
		return true
	}, "gold") {
		s.retMsg("元宝不足：重置神通点需要 30 元宝。")
		return
	}
	pet.TLPoint += back
	pet.TalGrade = [3]int{}
	p.saveLog(s)
	s.petUpdate(p, pet)
}

// onPetToPoint：把 pets 里的同名卡并进 tonpc（不在阵容中、未加锁）。现版客户端没有界面发这条，保留兼容。
func onPetToPoint(s *Session, p *Player, m xym.Msg) {
	to := p.petByNpc(int32(m["tonpc"].(int64)))
	if to == nil {
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{}})
		return
	}
	pets, _ := m["pets"].([]xym.Msg)
	removed := []xym.Msg{}
	for _, x := range pets {
		pet := p.petByNpc(int32(x["npcnum"].(int64)))
		if pet == nil || pet == to || pet.Type != to.Type || pet.Locked || p.inLineup(pet.BagPos) {
			continue
		}
		to.absorb()
		p.removePet(pet)
		removed = append(removed, xym.Msg{"npcnum": pet.NpcNum})
	}
	p.saveLog(s)
	if len(removed) > 0 {
		s.SendExt("PET_PTO.S_REMOVE_PET", xym.Msg{"pets": removed})
	}
	s.petUpdate(p, to)
}

func (p *Player) removePet(pet *Pet) {
	for i, x := range p.Pets {
		if x == pet {
			p.Pets = append(p.Pets[:i], p.Pets[i+1:]...)
			return
		}
	}
}
