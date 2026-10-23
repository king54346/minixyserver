package xymsrv

import (
	"fmt"

	"turnrpg/internal/xym"
)

// 服务端发起的确认框：ETC_PTO.S_ASK_BOX{text, session, opt[word], type} → 客户端 view/AskBoxNewStyleView.lua；
// 两个选项时右边按钮是 opt[1]（点了回 C_ASK_BOX{session, opt 0}），左边是 opt[2]（回 opt 1）；有 session 且有
// 第二个选项时还显示关闭叉（关掉不回包）。

// askBox 弹确认框，玩家点了按钮后在处理该回包的请求里调 fn(opt)（0 = 第一个选项）。
func (s *Session) askBox(text string, words []string, fn func(opt int)) {
	if s.asks == nil {
		s.asks = map[int]func(int){}
	}
	s.askSeq = s.askSeq%120 + 1 // session 是 int8
	s.asks[s.askSeq] = fn
	opts := make([]xym.Msg, 0, len(words))
	for _, w := range words {
		opts = append(opts, xym.Msg{"word": w})
	}
	s.SendExt("ETC_PTO.S_ASK_BOX", xym.Msg{"text": text, "session": s.askSeq, "opt": opts, "type": 0})
}

// 背包扩充（护法 / 装备 / 法术列表「+」→ TASK_PTO.C_BUY_BAG{type 1/2/3}）：先弹确认框，购买后上限 +5，
// 回 S_SEND_BAG_COUNT。护法照官方确认框「花费300元宝扩充5个护法位置？」；装备、法术按用户要求：第一次 100 元宝，
// 之后每次 ×3，上限 600 格。
const (
	bagExpandStep = 5
	bagExpandGold = 300
	gearBagGold   = 100
	gearBagMax    = 600
)

// bagExpandPrice 是第 t 种背包下一次扩充的元宝价（0 = 已到上限）。
func (p *Player) bagExpandPrice(t int) int32 {
	if t == bagPet {
		return bagExpandGold
	}
	if p.BagLimit[t] >= gearBagMax {
		return 0
	}
	price := int64(gearBagGold)
	for range p.BagBuys[t] {
		if price *= 3; price > 2_000_000_000 {
			return 2_000_000_000
		}
	}
	return int32(price)
}

var bagNames = map[int]string{bagPet: "护法", bagSuit: "装备", bagBook: "法术"}

func init() {
	Handle("ETC_PTO.C_ASK_BOX", func(s *Session, p *Player, m xym.Msg) {
		id := int(m["session"].(int64))
		if fn := s.asks[id]; fn != nil {
			delete(s.asks, id)
			fn(int(m["opt"].(int64)))
		}
	})
	Handle("TASK_PTO.C_BUY_BAG", func(s *Session, p *Player, m xym.Msg) {
		t := int(m["type"].(int64))
		name, ok := bagNames[t]
		if !ok {
			return
		}
		cur := p.BagLimit[t]
		price := p.bagExpandPrice(t)
		if price == 0 {
			s.sendMsg(fmt.Sprintf("%s位置已达上限%d。", name, gearBagMax))
			return
		}
		next := cur + bagExpandStep
		if t != bagPet {
			next = min(next, gearBagMax)
		}
		text := fmt.Sprintf("花费#G%d元宝#n扩充%d个%s位置？#r当前%s上限%d，扩充后为%d。", price, next-cur, name, name, cur, next)
		s.askBox(text, []string{"购买", "关闭"}, func(opt int) {
			if opt != 0 || p.bagExpandPrice(t) != price || p.BagLimit[t] != cur || !s.spend(0, price) {
				return
			}
			p.BagLimit[t] = next
			p.BagBuys[t]++
			p.saveLog(s)
			s.SendExt("ETC_PTO.S_SEND_BAG_COUNT", xym.Msg{"type": t, "count": p.BagLimit[t]})
		})
	})
}
