package battle

import "sort"

// 站位变化（阵亡补位见 battle.go moveUp）：换位、驱逐，以及遁让 / 遁入选谁换上来。

// bench 是一方存活的后阵单位。
func (b *battle) bench(side int) []*unit {
	var out []*unit
	for _, x := range b.units[side] {
		if x.alive() && !x.gone && !x.front() {
			out = append(out, x)
		}
	}
	return out
}

// swapPool 把带【雷影庇护】的雷震子排到最后：有别的可选后阵时，被动换人不会把他换上前阵；
// 只有他是唯一可选时才会选他（2026-11-16 用户规则）。
func swapPool(bench []*unit) []*unit {
	var normal []*unit
	for _, x := range bench {
		if x.tals[famLeiYingBiHu] == nil {
			normal = append(normal, x)
		}
	}
	if len(normal) > 0 {
		return normal
	}
	return bench
}

// benchForSwap 是遁让 / 换位法术 / 避阵这类「从后阵随机挑一个换上前」的可选池。
func (b *battle) benchForSwap(side int) []*unit { return swapPool(b.bench(side)) }

// quzhuPick：[驱逐] t 时换上来的人——随机一名后阵护法，有可能是已阵亡的（被吞噬的除外，FAQ 驱逐）；没有返回 nil。
// 同样先避开雷震子，只有他是唯一可选时才选他。
func (b *battle) quzhuPick(t *unit) *unit {
	var bn []*unit
	for _, x := range b.units[t.side] {
		if x.slot() > frontSlots && (x.alive() && !x.gone || !x.alive() && !x.eaten) {
			bn = append(bn, x)
		}
	}
	bn = swapPool(bn)
	if len(bn) == 0 {
		return nil
	}
	return bn[b.rng.IntN(len(bn))]
}

// quzhu：[驱逐] t，换上 x（驱逐、神威、芭蕉扇及其激发、脱帽退敌共用）。换上来的死者留在前阵，回合开始由后阵补位；
// 换上来的活人被【捆缚】（用户规则：驱逐 / 换位上来的卡无法行动，下次行动后清除）。
func (b *battle) quzhu(t, x *unit) {
	b.swap(t, x)
	if !x.alive() {
		x.gone = false
	}
	b.bindEntered(x)
}

// bindEntered：被驱逐 / 换位法术从后阵换到前阵的护法【捆缚】（入阵神通照常发动；捆缚下次行动后清除）。
func (b *battle) bindEntered(x *unit) {
	if !x.alive() || !x.front() {
		return
	}
	b.setStatus(x, stBind, 0, 1, true, qkxBindBuff, slotDebuff)
	if s := x.status[stBind]; s != nil {
		s.foe = true
	}
}

// swap 交换两个单位的站位（客户端 CWar:moveTo 对 MOVE 就是交换）。
// 从后阵换到前阵的一方触发入阵神通（官方战报：遁让 / 驱逐换上来的羲和立即放蟠桃宴，与阵亡补位相同）。
// 入阵神通本身也可能换位（驱逐），enterDepth 防止无限连锁。
func (b *battle) swap(x, y *unit) {
	wasFront := map[*unit]bool{x: x.front(), y: y.front()}
	b.w.u8(CmdMove)
	b.w.u8(x.Pos)
	b.w.u8(y.Pos)
	x.Pos, y.Pos = y.Pos, x.Pos
	b.zfSync(x) // 阵法：固定加成跟着阵位走
	b.zfSync(y)
	for _, z := range []*unit{x, y} {
		if z.alive() && !z.front() {
			b.backToBench(z)
			if wasFront[z] && z.alive() && z.Race == raceHuman && z.charge > 0 {
				// MOVE 的退阵动画清空客户端的蓄力显示，恢复现有层数。
				b.chargeDelta(z, z.charge)
			}
		}
	}
	for _, z := range []*unit{x, y} {
		if z.alive() && z.front() && !wasFront[z] {
			b.rampage(z) // 换上前阵先判定暴走，再结算入阵及敌方入阵反应。
			if b.enterDepth < 3 {
				b.enterDepth++
				b.enter(z)
				b.enterDepth--
			}
		}
	}
}

// dunrangTo 在所有可用的存活后阵护法中等概率随机选择，不按技能或站位排序；
// 带【雷影庇护】的雷震子最后才考虑（有别的后阵就不选他）。
func (b *battle) dunrangTo(side int) *unit {
	bench := b.benchForSwap(side)
	if len(bench) == 0 {
		return nil
	}
	return bench[b.rng.IntN(len(bench))]
}

// leiyingSwapTo：雷影庇护触发遁让时换谁上来（2026-11-16 调整：不再随机）。
// 优先级：未[遁入]过的入阵护法 → 已[遁入]过的入阵护法 → 有[遁让]的护法 → 其它，同档按站位从左到右。
// 雷震子（protector）会让给任何其它可用后阵，只有没有别的存活后阵时才被换上前阵。
func (b *battle) leiyingSwapTo(side int, protector *unit) *unit {
	bn := b.bench(side)
	if len(bn) == 0 {
		return nil
	}
	sort.SliceStable(bn, func(i, j int) bool { return bn[i].slot() < bn[j].slot() })
	rank := func(x *unit) int {
		switch {
		case hasAny(x, enterFamilies) && !x.dunru:
			return 0
		case hasAny(x, enterFamilies):
			return 1
		case x.tals[famDunRang] != nil:
			return 2
		}
		return 3
	}
	var best *unit
	for _, x := range bn {
		if x == protector {
			continue
		}
		if best == nil || rank(x) < rank(best) {
			best = x
		}
	}
	if best == nil { // 只有雷震子自己可用
		for _, x := range bn {
			if x == protector {
				return x
			}
		}
	}
	return best
}

// dunruTarget 是[遁入]交换的后阵护法（图鉴 FAQ）：未[遁入]过的入阵护法 → 已[遁入]过的入阵护法 → 无入阵技护法，
// 同档从左到右。
func (b *battle) dunruTarget(side int) *unit {
	bn := swapPool(b.bench(side)) // 雷震子最后考虑
	sort.SliceStable(bn, func(i, j int) bool { return bn[i].slot() < bn[j].slot() })
	rank := func(x *unit) int {
		switch {
		case hasAny(x, enterFamilies) && !x.dunru:
			return 0
		case hasAny(x, enterFamilies):
			return 1
		}
		return 2
	}
	var best *unit
	for _, x := range bn {
		if best == nil || rank(x) < rank(best) {
			best = x
		}
	}
	return best
}
