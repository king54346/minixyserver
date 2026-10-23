package xymsrv

import (
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 法术书精修（法术详情「精修」按钮，view/MagicDetailView.lua CMagicDetailJingxiu）：
//   - ITEM_PTO.C_JINGXIU_MAGIC{pos}，回 S_UPDATE_BOOK（书的 jxgrade = 精修次数）；客户端不加锁。
//   - 条件：人物 ≥31 级（magic_mgr.JingxiuNeedGrade）、法术表 jingxiu_buff 非 0、精修次数 < 法术书等级
//     （「当前该法术书的等级已经无法精修出更好的附加效果了」）。
//   - 第 n 次消耗 ⌊n² × jingxiu_cost / 10⌋ 个本属性元素（YuanSuList[kind]：道 10136 土 10132 水 10134
//     火 10133 雷 10131 圣 10135）和 星级² × n² × 4000 银两（getCostNum / getCostMoney）。
//   - 效果见 battle/jingxiu.go；生效次数 = min(精修次数, 法术书等级)（详情里「(等级/次数)」），人物不到 31 级不生效。

const jingxiuGrade = 31

var jingxiuElement = map[int]int{0: 10136, 1: 10132, 2: 10134, 3: 10133, 4: 10131, 5: 10135}

func init() {
	Handle("ITEM_PTO.C_JINGXIU_MAGIC", onJingxiuMagic)
}

// jingxiuCost 是第 n 次精修的元素数与银两。
func jingxiuCost(m *gamedata.MagicInfo, n int) (elems, cash int32) {
	return int32(n * n * m.JXCost / 10), int32(m.Star * m.Star * n * n * 4000)
}

func onJingxiuMagic(s *Session, p *Player, m xym.Msg) {
	b := p.bookByPos(int(m["pos"].(int64)))
	var mi *gamedata.MagicInfo
	if b != nil {
		mi = gamedata.Magics[b.Type]
	}
	switch {
	case mi == nil || mi.JXBuff == 0:
		s.sendMsg("该法术书不能精修。")
		return
	case s.role.Grade < jingxiuGrade:
		s.sendMsg("人物达到31级后开放精修。")
		return
	case b.JX >= b.Grade:
		s.sendMsg("当前该法术书的等级已经无法精修出更好的附加效果了。")
		return
	}
	elem := jingxiuElement[mi.Kind]
	need, cash := jingxiuCost(mi, b.JX+1)
	if p.itemCount(elem) < need {
		s.sendMsg("元素不足。")
		return
	}
	if !s.spend(cash, 0) {
		return
	}
	d := &bagDelta{}
	d.items, d.removed = p.takeItems(elem, need)
	b.JX++
	p.saveLog(s)
	s.sendDelta(p, d)
	s.SendExt("ITEM_PTO.S_UPDATE_BOOK", xym.Msg{"is_all": 0, "books": []xym.Msg{b.msg()}})
}

// wornBookJX 与 wornBooks 按下标对应：每本书生效的精修次数。
func (p *Player) wornBookJX(fp int) []int {
	var out []int
	if sl := p.fightSlot(fp); sl != nil {
		for _, pos := range sl.Book {
			if b := p.bookByPos(pos); b != nil {
				jx := min(b.JX, b.Grade)
				if p.grade() < jingxiuGrade {
					jx = 0
				}
				out = append(out, jx)
			}
		}
	}
	return out
}

// 精修吸纳石（道具 10311，ItemListView 对「未加锁且有精修等级」的法术书使用）：
// ITEM_PTO.C_PULL_OUT_MAGIC_JINGXIU{pos, type}，消耗 1 个吸纳石，退回该书历次精修用掉的全部元素（银两不退），
// 精修次数清零。
const jingxiuPullItem = 10311

func init() {
	Handle("ITEM_PTO.C_PULL_OUT_MAGIC_JINGXIU", onPullOutJingxiu)
}

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
	case p.itemCount(jingxiuPullItem) < 1:
		s.sendMsg("精修吸纳石不足。")
		return
	}
	var back int32
	for n := 1; n <= b.JX; n++ {
		e, _ := jingxiuCost(mi, n)
		back += e
	}
	d := &bagDelta{}
	d.items, d.removed = p.takeItems(jingxiuPullItem, 1)
	if back > 0 {
		d.items = append(d.items, p.addItem(jingxiuElement[mi.Kind], back)...)
	}
	b.JX = 0
	p.saveLog(s)
	s.sendDelta(p, d)
	s.SendExt("ITEM_PTO.S_UPDATE_BOOK", xym.Msg{"is_all": 0, "books": []xym.Msg{b.msg()}})
}
