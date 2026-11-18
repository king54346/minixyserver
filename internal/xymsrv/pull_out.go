package xymsrv

import (
	"fmt"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 石头与经验提取类道具（ItemListView 点击使用）：
//
// 1. 培养吸纳石（10310）：
//    C_PULL_OUT_PET_POINT{npcnum}，消耗 1 个吸纳石，全额退回护法培养消耗的战力丹（10126）与气血丹（10127），
//    护法 attadd / hpadd 清零。
//
// 2. 炼魂石（10251）：
//    C_PULL_OUT_PET_SOUL{npcnum}，消耗 1 个炼魂石，重置护法神通境界与已学神通，全额返还升级境界消耗的魂值到魂值池，
//    神通点数重置（保留同名卡并入的点数）。
//
// 3. 炼器石（10217）：
//    C_PULL_OUT_SUIT_EXP{itemnum}，消耗 1 个炼器石，清空未穿戴装备的强化经验（等级重置为 1），
//    返还一个等价值强化经验的「强化晶魄」（道具 2003，4星材料，exp 为全部强化消耗及自身累计经验）。
//
// 4. 炼法石（10249）：
//    C_PULL_OUT_MAGIC_EXP{pos, type}，消耗 1 个炼法石，清空未装备法术书的强化经验（等级重置为 1），
//    返还一个等价值强化经验的「炼法秘要」（法术 17000，5星材料，exp 为全部强化消耗及自身累计经验）。
//
// 5. 悟法石（10250）：
//    C_PULL_OUT_TALENT_MAGIC_EXP{npcnum}，消耗 1 个悟法石，清空未上阵护法天赋法术的强化经验（等级重置为 1），
//    返还一个等价值强化经验的「炼法秘要」（法术 17000，5星材料）。
//
// 6. 精修吸纳石（10311）：
//    C_PULL_OUT_MAGIC_JINGXIU{pos, type}，消耗 1 个精修吸纳石，退回法术书历次精修用掉的全部元素（银两不退），
//    精修次数清零。
//
// 7. 凿晶石（10216）：
//    C_PULL_OUT_SUIT_STONE{itemnum} / C_PULL_OUT_ALL_SUIT_STONE{suits[itemnum]}，每件装备消耗 1 个凿晶石，
//    取下宝石并返还宝石道具（石数不足取到用完为止）。
//
// 所有「取出」类石头的使用入口都集中在本文件；精修本身（C_JINGXIU_MAGIC）在 jingxiu.go，
// 镶嵌（C_ADD_PROP_STONE）在 inlay.go。

const (
	itemPullPetPoint    = 10310 // 培养吸纳石
	itemPullPetSoul     = 10251 // 炼魂石
	itemPullSuitExp     = 10217 // 炼器石
	itemPullMagicExp    = 10249 // 炼法石
	itemPullTalentMagic = 10250 // 悟法石
	itemPullJingxiu     = 10311 // 精修吸纳石
	itemZaojingStone    = 10216 // 凿晶石

	suitMaterialCrystal = 2003  // 强化晶魄
	magicMaterialSecret = 17000 // 炼法秘要
)

func init() {
	Handle("ITEM_PTO.C_PULL_OUT_PET_POINT", onPullOutPetPoint)
	Handle("ITEM_PTO.C_PULL_OUT_PET_SOUL", onPullOutPetSoul)
	Handle("ITEM_PTO.C_PULL_OUT_SUIT_EXP", onPullOutSuitExp)
	Handle("ITEM_PTO.C_PULL_OUT_MAGIC_EXP", onPullOutMagicExp)
	Handle("ITEM_PTO.C_PULL_OUT_TALENT_MAGIC_EXP", onPullOutTalentMagicExp)
	Handle("ITEM_PTO.C_PULL_OUT_MAGIC_JINGXIU", onPullOutJingxiu)
	Handle("ITEM_PTO.C_PULL_OUT_SUIT_STONE", func(s *Session, p *Player, m xym.Msg) {
		s.pullStones(p, []int32{int32(m["itemnum"].(int64))})
	})
	Handle("ITEM_PTO.C_PULL_OUT_ALL_SUIT_STONE", func(s *Session, p *Player, m xym.Msg) {
		var nums []int32
		l, _ := m["suits"].([]xym.Msg)
		for _, x := range l {
			nums = append(nums, int32(x["itemnum"].(int64)))
		}
		s.pullStones(p, nums)
	})
}

// onPullOutPetPoint：培养吸纳石
func onPullOutPetPoint(s *Session, p *Player, m xym.Msg) {
	pet := p.petByNpc(int32(m["npcnum"].(int64)))
	switch {
	case pet == nil:
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0})
		return
	case pet.Locked:
		s.sendMsg("护法已加锁。")
		return
	case pet.AttAdd == 0 && pet.HPAdd == 0:
		s.sendMsg("该护法没有培养材料可以取出。")
		return
	case p.itemCount(itemPullPetPoint) < 1:
		s.sendMsg("培养吸纳石不足。")
		return
	}

	attRefund := int32(pet.AttAdd * (pet.AttAdd + 1) / 2)
	hpRefund := int32(pet.HPAdd * (pet.HPAdd + 1) / 2)

	d := &bagDelta{}
	d.items, d.removed = p.takeItems(itemPullPetPoint, 1)
	if attRefund > 0 {
		d.items = append(d.items, p.addItem(pointAttItem, attRefund)...)
	}
	if hpRefund > 0 {
		d.items = append(d.items, p.addItem(pointHPItem, hpRefund)...)
	}

	pet.AttAdd, pet.HPAdd = 0, 0
	p.saveLog(s)

	s.sendDelta(p, d)
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
	s.pointPanel(p, pet)
	s.retMsg(fmt.Sprintf("成功取出战力丹×%d、气血丹×%d。", attRefund, hpRefund))
}

// onPullOutPetSoul：炼魂石
func onPullOutPetSoul(s *Session, p *Player, m xym.Msg) {
	pet := p.petByNpc(int32(m["npcnum"].(int64)))
	switch {
	case pet == nil:
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0})
		return
	case pet.Locked:
		s.sendMsg("护法已加锁。")
		return
	case p.inLineup(pet.BagPos):
		s.sendMsg("上阵护法不能使用炼魂石。")
		return
	case pet.SoulGrade == 0 && pet.TalGrade[0] == 0 && pet.TalGrade[1] == 0 && pet.TalGrade[2] == 0:
		s.sendMsg("该护法尚未提升境界或神通。")
		return
	case p.itemCount(itemPullPetSoul) < 1:
		s.sendMsg("炼魂石不足。")
		return
	}

	star := petStar(pet.Type)
	var totalSoul int32
	for g := 0; g < pet.SoulGrade && g < len(tishenSoulCost); g++ {
		totalSoul += tishenSoulCost[g][star-1]
	}

	d := &bagDelta{}
	d.items, d.removed = p.takeItems(itemPullPetSoul, 1)

	pet.SoulGrade = 0
	pet.TalGrade = [3]int{0, 0, 0}
	pet.TLPoint = pet.TLAdd * shenTongPoint[star]

	p.saveLog(s)
	s.roleUpdate(func(ro *Role) bool { ro.SoulExp += totalSoul; return true }, "soulexp")

	s.sendDelta(p, d)
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
	s.retMsg(fmt.Sprintf("重置神通境界成功，返还魂值×%d。", totalSoul))
}

// onPullOutSuitExp：炼器石
func onPullOutSuitExp(s *Session, p *Player, m xym.Msg) {
	su := p.suitByNum(int32(m["itemnum"].(int64)))
	switch {
	case su == nil:
		s.sendMsg("装备不存在。")
		return
	case su.Locked:
		s.sendMsg("装备已加锁。")
		return
	case p.suitWorn(su.Pos):
		s.sendMsg("已穿戴装备不能使用炼器石。")
		return
	case su.Grade <= 1 && su.Exp == 0:
		s.sendMsg("该装备没有强化经验可以取出。")
		return
	case p.itemCount(itemPullSuitExp) < 1:
		s.sendMsg("炼器石不足。")
		return
	}

	st := suitStar(su.Type)
	spent := upTable(suitUpExp).spent(st, su.Grade) + su.Exp

	d := &bagDelta{}
	d.items, d.removed = p.takeItems(itemPullSuitExp, 1)

	su.Grade, su.Exp = 1, 0
	crystal := p.addSuit(suitMaterialCrystal)
	crystal.Exp = spent

	p.saveLog(s)
	s.sendDelta(p, d)
	s.SendExt("ITEM_PTO.S_UPDATE_SUIT", xym.Msg{"is_all": 0, "suits": []xym.Msg{su.msg(), crystal.msg()}})
	s.retMsg("清空装备强化经验成功，获得强化晶魄。")
}

// onPullOutMagicExp：炼法石
func onPullOutMagicExp(s *Session, p *Player, m xym.Msg) {
	b := p.bookByPos(int(m["pos"].(int64)))
	switch {
	case b == nil:
		s.sendMsg("法术不存在。")
		return
	case b.Locked:
		s.sendMsg("法术已加锁。")
		return
	case p.bookWorn(b.Pos):
		s.sendMsg("已装备法术不能使用炼法石。")
		return
	case b.Grade <= 1 && b.Exp == 0:
		s.sendMsg("该法术没有强化经验可以取出。")
		return
	case p.itemCount(itemPullMagicExp) < 1:
		s.sendMsg("炼法石不足。")
		return
	}

	st := magicStar(b.Type)
	spent := upTable(magicUpExp).spent(st, b.Grade) + b.Exp

	d := &bagDelta{}
	d.items, d.removed = p.takeItems(itemPullMagicExp, 1)

	b.Grade, b.Exp = 1, 0
	secret := p.addBook(magicMaterialSecret)
	secret.Exp = spent

	p.saveLog(s)
	s.sendDelta(p, d)
	s.SendExt("ITEM_PTO.S_UPDATE_BOOK", xym.Msg{"is_all": 0, "books": []xym.Msg{b.msg(), secret.msg()}})
	s.retMsg("清空法术强化经验成功，获得炼法秘要。")
}

// onPullOutTalentMagicExp：悟法石
func onPullOutTalentMagicExp(s *Session, p *Player, m xym.Msg) {
	pet := p.petByNpc(int32(m["npcnum"].(int64)))
	switch {
	case pet == nil:
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0})
		return
	case pet.Locked:
		s.sendMsg("护法已加锁。")
		return
	case p.inLineup(pet.BagPos):
		s.sendMsg("上阵护法不能使用悟法石。")
		return
	case pet.MagicLv <= 1 && pet.MagicExp == 0:
		s.sendMsg("该护法天赋法术没有强化经验可以取出。")
		return
	case p.itemCount(itemPullTalentMagic) < 1:
		s.sendMsg("悟法石不足。")
		return
	}

	pi := gamedata.Pets[pet.Type]
	if pi == nil {
		s.sendMsg("护法数据异常。")
		return
	}
	st := magicStar(pi.Magic)
	spent := upTable(magicUpExp).spent(st, max(pet.MagicLv, 1)) + pet.MagicExp

	d := &bagDelta{}
	d.items, d.removed = p.takeItems(itemPullTalentMagic, 1)

	pet.MagicLv, pet.MagicExp = 1, 0
	secret := p.addBook(magicMaterialSecret)
	secret.Exp = spent

	p.saveLog(s)
	s.sendDelta(p, d)
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
	s.SendExt("ITEM_PTO.S_UPDATE_BOOK", xym.Msg{"is_all": 0, "books": []xym.Msg{secret.msg()}})
	s.retMsg("清空天赋法术强化经验成功，获得炼法秘要。")
}

// onPullOutJingxiu：精修吸纳石
func onPullOutJingxiu(s *Session, p *Player, m xym.Msg) {
	b := p.bookByPos(int(m["pos"].(int64)))
	var mi *gamedata.MagicInfo
	if b != nil {
		mi = gamedata.Magics[b.Type]
	}
	switch {
	case mi == nil || b.JX == 0:
		s.sendMsg("该法术没有精修材料可以取出。")
		return
	case b.Locked:
		s.sendMsg("法术已加锁。")
		return
	case p.itemCount(itemPullJingxiu) < 1:
		s.sendMsg("精修吸纳石不足。")
		return
	}
	var back int32
	for n := 1; n <= b.JX; n++ {
		e, _ := jingxiuCost(mi, n)
		back += e
	}
	d := &bagDelta{}
	d.items, d.removed = p.takeItems(itemPullJingxiu, 1)
	if back > 0 {
		d.items = append(d.items, p.addItem(jingxiuElement[mi.Kind], back)...)
	}
	b.JX = 0
	p.saveLog(s)
	s.sendDelta(p, d)
	s.SendExt("ITEM_PTO.S_UPDATE_BOOK", xym.Msg{"is_all": 0, "books": []xym.Msg{b.msg()}})
}

// pullStones 用凿晶石取出装备上的宝石（每件一个凿晶石，不够就取到用完为止）。
func (s *Session) pullStones(p *Player, nums []int32) {
	d := &bagDelta{}
	var changed []xym.Msg
	for _, n := range nums {
		su := p.suitByNum(n)
		if su == nil || su.Stone == 0 {
			continue
		}
		if p.itemCount(itemZaojingStone) < 1 {
			s.retMsg("凿晶石不足。")
			break
		}
		ch, rm := p.takeItems(itemZaojingStone, 1)
		d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
		d.items = append(d.items, p.addItem(su.Stone, 1)...)
		su.Stone = 0
		changed = append(changed, su.msg())
	}
	p.saveLog(s)
	s.sendDelta(p, d)
	if changed == nil {
		changed = []xym.Msg{}
	}
	s.SendExt("ITEM_PTO.S_UPDATE_SUIT", xym.Msg{"is_all": 0, "suits": changed})
}
