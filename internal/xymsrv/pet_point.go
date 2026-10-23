package xymsrv

import (
	"turnrpg/internal/xym"
)

// 护法培养（护法详情「培养」→ view/CPetView.lua CPetTrainView）与护法 / 装备 / 法术书加锁。
//
// 依据：
//   - CPetTrainView：战力丹 10126 加攻击、气血丹 10127 加生命，第 N 次培养消耗 N 颗（界面「需要 attadd+1」），
//     每项最多 200 次（maxCount，「少侠，护法被培养得够结实了」）；按钮发 C_ADD_PET_POINT{npcnum, type = 道具号}，
//     等 S_UPDATE_PET 飘字（攻击 / 生命 / 战力的增量），面板靠 S_OPEN_POINT_PANEL{hpadd, attadd, attitem, hpitem} 刷新。
//   - logic/pet_prop.lua：攻击 = floor((等级攻击 + attadd + 装备) × 缘分)，生命 = floor((等级生命 + hpadd × 4 + 装备) × 缘分)
//     ——每颗战力丹 +1 攻击、每颗气血丹 +4 生命（超级星战「丹药 30」的 +30 攻 / +120 血同口径）。
//     S_UPDATE_PET 带 hpadd / attadd。
//   - C_ADD_PET_POINT 的 type 1 / 2（普通 / 精心培养，回 S_CONFIRM_ADD_POINT 预览、C_REAL_ADD_POINT 确认）是旧版界面，
//     本版 ui/Pet_Train.lua 只有攻击、生命两个按钮；照样回预览（数值不变）解锁。
//   - 加锁：C_CHANGE_PET_LOCK{npcnum, is_lock} 等 S_UPDATE_PET（logic/pet_mgr.lua）；C_CHANGE_SUIT_LOCK{itemnum, is_lock}
//     等 S_UPDATE_SUIT（suit_mgr.lua）；C_CHANGE_MAGIC_LOCK{pos, is_lock} 等 S_UPDATE_BOOK（magic_mgr.lua）。
//     锁住的不能出售、吸纳、融魂、当材料（各玩法已检查 Locked）。

const (
	pointAttItem = 10126 // 战力丹
	pointHPItem  = 10127 // 气血丹
	pointMax     = 200
	pointHPStep  = 4 // 每颗气血丹的生命
)

func init() {
	Handle("PET_PTO.C_OPEN_POINT_PANEL", func(s *Session, p *Player, m xym.Msg) {
		if pet := p.petByNpc(int32(m["npcnum"].(int64))); pet != nil {
			s.pointPanel(p, pet)
		}
	})
	Handle("PET_PTO.C_ADD_PET_POINT", onAddPetPoint)
	Handle("PET_PTO.C_REAL_ADD_POINT", func(s *Session, p *Player, m xym.Msg) {
		pet := p.petByNpc(int32(m["npcnum"].(int64)))
		if pet == nil {
			s.SendExt("PET_PTO.S_OPEN_POINT_PANEL", xym.Msg{})
			return
		}
		s.pointPanel(p, pet)
	})
	Handle("PET_PTO.C_CHANGE_PET_LOCK", func(s *Session, p *Player, m xym.Msg) {
		pet := p.petByNpc(int32(m["npcnum"].(int64)))
		if pet == nil {
			s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0})
			return
		}
		pet.Locked = m["is_lock"].(int64) == 1
		p.saveLog(s)
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
	})
	Handle("ITEM_PTO.C_CHANGE_SUIT_LOCK", func(s *Session, p *Player, m xym.Msg) {
		l := []xym.Msg{}
		if su := p.suitByNum(int32(m["itemnum"].(int64))); su != nil {
			su.Locked = m["is_lock"].(int64) == 1
			p.saveLog(s)
			l = append(l, su.msg())
		}
		s.SendExt("ITEM_PTO.S_UPDATE_SUIT", xym.Msg{"is_all": 0, "suits": l})
	})
	Handle("ITEM_PTO.C_CHANGE_MAGIC_LOCK", func(s *Session, p *Player, m xym.Msg) {
		l := []xym.Msg{}
		if b := p.bookByPos(int(m["pos"].(int64))); b != nil {
			b.Locked = m["is_lock"].(int64) == 1
			p.saveLog(s)
			l = append(l, b.msg())
		}
		s.SendExt("ITEM_PTO.S_UPDATE_BOOK", xym.Msg{"is_all": 0, "books": l})
	})
}

func (s *Session) pointPanel(p *Player, pet *Pet) {
	s.SendExt("PET_PTO.S_OPEN_POINT_PANEL", xym.Msg{"hpadd": pet.HPAdd, "attadd": pet.AttAdd,
		"attitem": p.itemCount(pointAttItem), "hpitem": p.itemCount(pointHPItem)})
}

// onAddPetPoint：用 attadd+1 颗战力丹（或 hpadd+1 颗气血丹）培养一次，回背包变化 + S_UPDATE_PET + S_OPEN_POINT_PANEL。
func onAddPetPoint(s *Session, p *Player, m xym.Msg) {
	pet := p.petByNpc(int32(m["npcnum"].(int64)))
	typ := int(m["type"].(int64))
	if pet == nil {
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0})
		return
	}
	var cnt *int
	switch typ {
	case pointAttItem:
		cnt = &pet.AttAdd
	case pointHPItem:
		cnt = &pet.HPAdd
	default: // 旧版普通 / 精心培养：回一个不变的预览解锁
		att, hp := p.petStats(pet)
		s.SendExt("PET_PTO.S_CONFIRM_ADD_POINT", xym.Msg{"hpbase": hp, "hpnew": hp, "atbase": att, "atnew": att})
		return
	}
	need := int32(*cnt + 1)
	switch {
	case *cnt >= pointMax:
		s.sendMsg("少侠，护法被培养得够结实了，不能再培养了")
	case p.itemCount(typ) < need:
		if typ == pointAttItem {
			s.sendMsg("战力丹不足，参与荡妖、论道等玩法可获得战力丹。")
		} else {
			s.sendMsg("气血丹不足，参与荡妖、论道等玩法可获得气血丹。")
		}
	default:
		ch, rm := p.takeItems(typ, need)
		*cnt++
		p.saveLog(s)
		d := &bagDelta{items: ch, removed: rm}
		s.sendDelta(p, d)
	}
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pet)}})
	s.pointPanel(p, pet)
}
