package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 卡牌边框系统（CARD_FRAME_PTO）：碎片 10443-10449 各 50 个合成对应边框（金风玉露…真金火炼，
// 对应 card_frame.lua 边框 1-7）；边框背包 C_OPEN_FRAME_BAG，穿戴 C_USE_FRAME，卸下 C_UNUSE_FRAME，
// 销毁 C_DESTROY_FRAME。合成入口：道具「使用」（C_USE_ITEM，任意 usetype），客户端合成弹窗走的
// usetype=2 也走同一逻辑；扣 50 个碎片前弹确认框。

// CardFrame 玩家拥有的一个边框实例。
type CardFrame struct {
	Key   int32 `json:"key"`
	Type  int32 `json:"type"`            // 边框类型 1-7
	Begin int64 `json:"begin"`           // 获取时间（时效从这算）
	Using int32 `json:"using,omitempty"` // 已装备的护法 type（客户端 C_USE_FRAME.pet_type 就是护法类型），0 = 未装备
}

// 每合成一个边框需要的碎片数。
const frameComposeNeed = 50

// framePartMin / framePartMax 是七种边框碎片的道具号区间（10443 金风玉露 … 10449 真金火炼）。
const (
	framePartMin = 10443
	framePartMax = 10449
)

// framePartTypes 随机取 n 个边框碎片道具号（可能重复），
// 用于「魔潮入侵前三名」这种按个数发、不指定款的奖励。
func framePartTypes(rng *rand.Rand, n int) []int {
	out := make([]int, 0, n)
	for range n {
		out = append(out, framePartMin+rng.IntN(framePartMax-framePartMin+1))
	}
	return out
}

// framePartType 碎片道具号 -> 边框类型（10443 -> 1 … 10449 -> 7）。
func framePartType(itType int) (int, bool) {
	if itType < 10443 || itType > 10449 {
		return 0, false
	}
	return itType - 10442, true
}

func init() {
	Handle("CARD_FRAME_PTO.C_OPEN_FRAME_BAG", func(s *Session, p *Player, m xym.Msg) {
		s.sendFrameBag(p)
	})
	Handle("CARD_FRAME_PTO.C_USE_FRAME", func(s *Session, p *Player, m xym.Msg) {
		pet := int32(m["pet_type"].(int64)) // 护法 type（客户端传 pet_type）
		fid := int32(m["frame_id"].(int64))
		key := int32(m["key"].(int64))
		pe := p.petByType(pet)
		if pe == nil {
			s.sendMsg("护法不存在。")
			return
		}
		if pi := gamedata.Pets[pe.Type]; pi == nil || pi.Star < 5 { // 边框描述「适用于五星护法」：服务端同样只允许五星
			s.sendMsg("卡牌边框只能装在五星护法上。")
			return
		}
		var target *CardFrame
		for i := range p.CardFrames {
			if p.CardFrames[i].Key == key && p.CardFrames[i].Type == fid {
				target = &p.CardFrames[i]
				break
			}
		}
		if target == nil {
			s.sendMsg("边框不存在。")
			return
		}
		if !frameAlive(*target, time.Now().Unix()) {
			s.sendMsg("边框已到期，无法穿戴。")
			s.sendFrameBag(p)
			return
		}
		for i := range p.CardFrames {
			if p.CardFrames[i].Using == pet {
				p.CardFrames[i].Using = 0 // 同一护法只装备一个边框
			}
		}
		target.Using = pet
		if err := p.save(); err != nil {
			s.logErr("save frame", err)
		}
		if pe != nil { // 边框属性变化，刷新该护法
			s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pe)}})
		}
		s.sendFrameBag(p)
	})
	Handle("CARD_FRAME_PTO.C_UNUSE_FRAME", func(s *Session, p *Player, m xym.Msg) {
		key := int32(m["key"].(int64))
		for i := range p.CardFrames {
			if p.CardFrames[i].Key == key {
				pet := p.CardFrames[i].Using
				p.CardFrames[i].Using = 0
				if err := p.save(); err != nil {
					s.logErr("save frame", err)
				}
				if pet != 0 {
					if pe := p.petByType(pet); pe != nil {
						s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pe)}})
					}
				}
				s.sendFrameBag(p)
				return
			}
		}
		s.sendMsg("边框不存在。")
	})
	Handle("CARD_FRAME_PTO.C_DESTROY_FRAME", func(s *Session, p *Player, m xym.Msg) {
		key := int32(m["key"].(int64))
		for i := range p.CardFrames {
			if p.CardFrames[i].Key != key {
				continue
			}
			f := p.CardFrames[i]
			pet := f.Using
			p.CardFrames = append(p.CardFrames[:i], p.CardFrames[i+1:]...)
			// 销毁返还一半合成碎片（2026-11-16 用户要求）：50 个碎片合成一个，销毁退 25 个同款碎片。
			var refundDelta *bagDelta
			if cf := gamedata.CardFrames[int(f.Type)]; cf != nil && f.Type >= 1 && f.Type <= 7 {
				typ, refund := 10442+int(f.Type), frameComposeNeed/2
				d := &bagDelta{items: p.addItem(typ, int32(refund))}
				d.gains = append(d.gains, gain{rtItem, typ, int32(refund)})
				refundDelta = d
			}
			if err := p.save(); err != nil {
				s.logErr("save frame", err)
			}
			if pet != 0 {
				if pe := p.petByType(pet); pe != nil {
					s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pe)}})
				}
			}
			if refundDelta != nil {
				s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, s.sendDelta(p, refundDelta)))
			}
			s.sendFrameBag(p)
			return
		}
		s.sendMsg("边框不存在。")
	})
}

// frameAlive 边框是否仍有效（last_sec<=0 永久；last_sec>0 从 Begin 起算，到期时间点后失效）。
func frameAlive(f CardFrame, now int64) bool {
	cf := gamedata.CardFrames[int(f.Type)]
	if cf == nil {
		return false
	}
	if cf.LastSec <= 0 {
		return true
	}
	return f.Begin+cf.LastSec > now
}

// expireFrames 移除到期边框；返回被卸下的护法 type（数据有变更才非 nil）。调用方负责 save 与刷新。
func (p *Player) expireFrames(now int64) []int32 {
	if len(p.CardFrames) == 0 {
		return nil
	}
	unpet := make([]int32, 0) // Non-nil also records deletion of unworn expired frames.
	kept := p.CardFrames[:0]
	for _, f := range p.CardFrames {
		if frameAlive(f, now) {
			kept = append(kept, f)
			continue
		}
		if f.Using != 0 {
			unpet = append(unpet, f.Using)
		}
	}
	if len(kept) == len(p.CardFrames) {
		return nil
	}
	p.CardFrames = kept
	return unpet
}

// frameBonus 该护法装备的边框属性加成（攻击 / 生命），在缘分之前与培养一起加。到期边框不加成。
func (p *Player) frameBonus(pet *Pet) (att, hp int32) {
	now := time.Now().Unix()
	for _, f := range p.CardFrames {
		if f.Using != pet.Type || !frameAlive(f, now) {
			continue
		}
		if cf := gamedata.CardFrames[int(f.Type)]; cf != nil {
			att += cf.AttAdd
			hp += cf.HPAdd
		}
	}
	return att, hp
}

// sendFrameBag 回边框背包列表（S_OPEN_FRAME_BAG）。顺带清掉到期边框（属性已由 frameBonus 过滤）。
func (s *Session) sendFrameBag(p *Player) {
	if unpet := p.expireFrames(time.Now().Unix()); unpet != nil {
		if err := p.save(); err != nil {
			s.logErr("save frame", err)
		}
		for _, t := range unpet {
			if pe := p.petByType(t); pe != nil {
				s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": []xym.Msg{p.petFull(pe)}})
			}
		}
	}
	list := make([]xym.Msg, 0, len(p.CardFrames))
	for _, f := range p.CardFrames {
		list = append(list, xym.Msg{"frame_type": int64(f.Type), "begin_time": int64(f.Begin),
			"key": int64(f.Key), "using_pet_type": int64(f.Using)})
	}
	s.SendExt("CARD_FRAME_PTO.S_OPEN_FRAME_BAG", xym.Msg{"card_bag": list})
}

// composeFrame 用 50 个边框碎片合成边框；返回 true 表示该道具是边框碎片（已处理）。
// 凑够 50 个弹确认框（askBox），回调里重查数量再扣；不够则提示。
func (s *Session) composeFrame(p *Player, it *Item) bool {
	ft, ok := framePartType(it.Type)
	if !ok {
		return false
	}
	cf := gamedata.CardFrames[ft]
	if cf == nil {
		s.sendMsg("这个边框还没开放。")
		return true
	}
	if p.itemCount(it.Type) < frameComposeNeed {
		s.sendMsg(frameShortMsg(cf.Name))
		return true
	}
	s.askBox(fmt.Sprintf("确定消耗%d个%s合成%s吗？", frameComposeNeed, cf.Name+"碎片", cf.Name), []string{"合成", "取消"}, func(opt int) {
		if opt != 0 {
			return
		}
		if p.itemCount(it.Type) < frameComposeNeed {
			s.sendMsg("碎片不足。")
			return
		}
		d := &bagDelta{}
		ch, rm := p.takeItems(it.Type, frameComposeNeed)
		d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
		p.FrameSeq++
		p.CardFrames = append(p.CardFrames, CardFrame{Key: p.FrameSeq, Type: int32(ft), Begin: time.Now().Unix()})
		p.saveLog(s)
		list := s.sendDelta(p, d)
		s.sendFrameBag(p)
		s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxItem, "item": it.Type, "list": list})
		s.sendMsg("合成成功，获得「" + cf.Name + "」边框。")
	})
	return true
}

// frameShortMsg 是碎片不足时的提示。单独拆出来便于测试：h_parts.go 的 itoa 只处理个位数
// （'0'+50 = 'b'），这里必须用 strconv。
func frameShortMsg(name string) string {
	return "碎片不足，" + name + "需要 " + strconv.Itoa(frameComposeNeed) + " 个碎片才能合成。"
}
