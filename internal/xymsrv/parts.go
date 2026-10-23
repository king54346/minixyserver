package xymsrv

import (
	"sort"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 碎片与强化的规则，全部照客户端：
//
//   - 装备碎片（suit_iron，按装备类型计数，奖励 rtype 9 / 编码 JT）、法术碎片（book_part，按法术号，
//     rtype 4 / SP）。合成一件要 1/4/6/15/20 个（suit_mgr / magic_mgr 的 NEED_IRON / NEED_PART，
//     按星级）；4、5 星自有碎片 ≥12/16 时可用同星通用碎片道具补足（CSuitView / CMagicView can_combine）。
//   - 通用碎片道具：三/四/五星装备碎片 10302/10083/10084、法术碎片 10301/10103/10104，
//     凑够 6/15/20 个「合成」（客户端发 C_USE_ITEM）出该星级随机一件。
//   - 强化：材料经验 = 基础值（1/4/18/60/100，_SUIT_ADD_EXP / MAGIC_PURE_VALUE；随装备下发在
//     extra_exp / extra_magic_exp）+ 材料已累计的升级经验；碎片每个 1/1/3/4/5（iron_exp_tbl /
//     part_exp_tbl），选中即整堆。花费 经验×100×星级 银两。装备满级 = 经验表长度+1（1~5 星
//     10/20/30/40/50），且不超过人物等级；法术满级 10（CFG.MAX_MAGIC_GRADE）。

var (
	baseExp  = [6]int32{0, 1, 4, 18, 60, 100}
	partExp  = [6]int32{0, 1, 1, 3, 4, 5}
	needPart = [6]int32{0, 1, 4, 6, 15, 20}
	// 4、5 星可用通用碎片补足时，自有碎片至少要有的数量
	partSubst = [6]int32{4: 12, 5: 16}
)

// 通用碎片道具：星级 -> 道具号
var (
	suitPartItem  = map[int]int{3: 10302, 4: 10083, 5: 10084}
	magicPartItem = map[int]int{3: 10301, 4: 10103, 5: 10104}
)

// suit_mgr._SUIT_UPGRADE_EXP：第 lv 级升下一级所需经验（下标 lv-1）
var suitUpExp = map[int][]int32{
	1: {1, 1, 1, 1, 1, 1, 1, 1, 15},
	2: {1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 24},
	3: {1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 24, 40, 40, 40, 40, 40, 40, 40, 40, 40, 80},
	4: {1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 24, 40, 40, 40, 40, 40, 40, 40, 40, 40, 80,
		80, 80, 80, 80, 80, 80, 80, 80, 80, 140},
	5: {1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 24, 40, 40, 40, 40, 40, 40, 40, 40, 40, 80,
		80, 80, 80, 80, 80, 80, 80, 80, 80, 140, 140, 140, 140, 140, 140, 140, 140, 140, 140, 240},
}

// magic_mgr.UPGRADE_MAGIC_NEED_EXP（满级 10，只用前 9 项）
var magicUpExp = map[int][]int32{
	1: {1, 5, 20, 40, 80, 150, 150, 150, 150},
	2: {2, 8, 30, 60, 120, 240, 240, 240, 240},
	3: {3, 10, 40, 80, 160, 300, 300, 300, 300},
	4: {4, 20, 50, 100, 200, 375, 375, 375, 375},
	5: {5, 30, 60, 150, 300, 600, 600, 600, 600},
}

const maxMagicGrade = 10

// upTable 是一种强化曲线：need(lv) 为 0 表示 lv 已满级。
type upTable map[int][]int32

func (t upTable) need(star, lv int) int32 {
	if l := t[star]; lv >= 1 && lv <= len(l) {
		return l[lv-1]
	}
	return 0
}

// spent 是从 1 级升到 grade 级累计花掉的经验。
func (t upTable) spent(star, grade int) int32 {
	var n int32
	for lv := 1; lv < grade; lv++ {
		n += t.need(star, lv)
	}
	return n
}

// add 在 (grade, exp) 上加 n 点经验，返回新的等级和本级经验；满级后经验清零。
func (t upTable) add(star, grade int, exp, n int32) (int, int32) {
	exp += n
	for need := t.need(star, grade); need > 0 && exp >= need; need = t.need(star, grade) {
		exp -= need
		grade++
	}
	if t.need(star, grade) == 0 {
		exp = 0
	}
	return grade, exp
}

func clampStar(st int) int { return min(max(st, 1), 5) }

func suitStar(typ int) int {
	if si := gamedata.Suits[typ]; si != nil {
		return clampStar(si.Star)
	}
	return 1
}

func magicStar(typ int32) int {
	if mi := gamedata.Magics[typ]; mi != nil {
		return clampStar(mi.Star)
	}
	return 1
}

// 作为强化材料时提供的经验（suit_mgr / magic_mgr 的 exp_value）
func (s *Suit) expValue() int32 {
	st := suitStar(s.Type)
	return baseExp[st] + upTable(suitUpExp).spent(st, s.Grade) + s.Exp
}

func (b *Book) expValue() int32 {
	st := magicStar(b.Type)
	return baseExp[st] + upTable(magicUpExp).spent(st, b.Grade) + b.Exp
}

// ---- 碎片存取 ----

func (p *Player) addIron(typ int, n int32, d *bagDelta) {
	if p.Irons == nil {
		p.Irons = map[int]int32{}
	}
	if p.Irons[typ] += n; p.Irons[typ] <= 0 {
		delete(p.Irons, typ)
	}
	d.irons = append(d.irons, typ)
	if n > 0 {
		d.gains = append(d.gains, gain{rtSuitPart, typ, n})
	}
}

func (p *Player) addBookPart(typ int32, n int32, d *bagDelta) {
	if p.BookParts == nil {
		p.BookParts = map[int32]int32{}
	}
	if p.BookParts[typ] += n; p.BookParts[typ] <= 0 {
		delete(p.BookParts, typ)
	}
	d.bookParts = append(d.bookParts, typ)
	if n > 0 {
		d.gains = append(d.gains, gain{rtMagicPart, int(typ), n})
	}
}

// ironsMsg：S_UPDATE_SUIT_IRON，客户端按 suit_type 合并，amount 0 为删除。types 为 nil 时发全部。
func (p *Player) ironsMsg(types []int) xym.Msg {
	if types == nil {
		for t := range p.Irons {
			types = append(types, t)
		}
		sort.Ints(types)
	}
	seen := map[int]bool{}
	l := []xym.Msg{}
	for _, t := range types {
		if !seen[t] {
			seen[t] = true
			l = append(l, xym.Msg{"suit_type": t, "amount": p.Irons[t]})
		}
	}
	return xym.Msg{"irons": l}
}

// bookPartsMsg：S_UPDATE_BOOK_PART，同上。
func (p *Player) bookPartsMsg(types []int32) xym.Msg {
	if types == nil {
		for t := range p.BookParts {
			types = append(types, t)
		}
		sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	}
	seen := map[int32]bool{}
	l := []xym.Msg{}
	for _, t := range types {
		if !seen[t] {
			seen[t] = true
			l = append(l, xym.Msg{"magic": t, "cnt": p.BookParts[t]})
		}
	}
	return xym.Msg{"parts": l}
}

// partItemsMsg：S_UPDATE_SUIT_PART_ITEMS / S_UPDATE_MAGIC_PART_ITEMS，客户端每次整表替换，
// 所以总是发背包里全部该类通用碎片。
func (p *Player) partItemsMsg(byStar map[int]int) xym.Msg {
	want := map[int]bool{}
	for _, t := range byStar {
		want[t] = true
	}
	l := []xym.Msg{}
	for _, it := range sortedItems(p.Items) {
		if want[it.Type] && it.Amount > 0 {
			l = append(l, xym.Msg{"pos": it.Pos, "type": it.Type, "amount": it.Amount})
		}
	}
	return xym.Msg{"list": l}
}

func isPartItem(typ int, byStar map[int]int) bool {
	for _, t := range byStar {
		if t == typ {
			return true
		}
	}
	return false
}

// partItemStar：typ 是通用碎片道具时返回 (星级, 是否装备)。
func partItemStar(typ int) (int, bool, bool) {
	for st, t := range suitPartItem {
		if t == typ {
			return st, true, true
		}
	}
	for st, t := range magicPartItem {
		if t == typ {
			return st, false, true
		}
	}
	return 0, false, false
}

// takeParts 扣合成一件所需的碎片：自有够就只扣自有；4、5 星自有 ≥ partSubst 时差额用通用碎片道具补。
// 返回是否成功（失败时不改任何东西）。
func (p *Player) takeParts(star int, have int32, item int, take func(n int32), d *bagDelta) bool {
	need := needPart[star]
	if have >= need {
		take(need)
		return true
	}
	if star < 4 || have < partSubst[star] || have+p.itemCount(item) < need {
		return false
	}
	take(have)
	ch, rm := p.takeItems(item, need-have)
	d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	return true
}
