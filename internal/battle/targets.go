package battle

// 选目标与常用查询：前阵 / 敌我、对位、相邻、伤重 / 易死、概率判定。

func (b *battle) foes(u *unit) []*unit { return b.frontOf(1 - u.side) }

func (b *battle) allies(u *unit) []*unit { return b.frontOf(u.side) }

// team 是 side 一方所有存活且在场的护法（前阵 + 后阵）。「护法处于后阵也可触发」的神通找载体用它，不要用 allies。
func (b *battle) team(side int) []*unit {
	var out []*unit
	for _, x := range b.units[side] {
		if x.alive() && !x.gone {
			out = append(out, x)
		}
	}
	return out
}

func (b *battle) frontOf(side int) []*unit {
	var out []*unit
	for _, x := range b.units[side] {
		if x.alive() && x.front() {
			out = append(out, x)
		}
	}
	return out
}

// attackable 是 u 能物理攻击的敌方前阵护法（隐身的不能成为物理攻击目标）。
func (b *battle) attackable(u *unit) []*unit {
	var out []*unit
	for _, t := range b.foes(u) {
		if t.has(stHidden) == nil {
			out = append(out, t)
		}
	}
	return out
}

// opposite 是 u 对位（同槽位）的敌方前阵存活护法，没有返回 nil（对位锁定：已死 / 为空不改选）。
func (b *battle) opposite(u *unit) *unit {
	for _, o := range b.foes(u) {
		if o.slot() == u.slot() {
			return o
		}
	}
	return nil
}

// pickTarget：对位的敌人，对位已死就打槽位最靠前的。
func (b *battle) pickTarget(u *unit) *unit {
	var first *unit
	for _, t := range b.foes(u) {
		if t.has(stHidden) != nil { // 隐身：无法成为物理攻击目标
			continue
		}
		if t.slot() == u.slot() {
			return t
		}
		if first == nil || t.slot() < first.slot() {
			first = t
		}
	}
	return first
}

// facing3 是 u 的「对面三位」/「对位三体」：敌方前阵站在 u 对位槽位及其左右两槽的存活护法（对位在前）。位置是锁定的：
// 对位已死或为空也不改选别人——1 号位只打对面 1、2 号，5 号位只打 4、5 号（用户规则）。
func (b *battle) facing3(u *unit) []*unit {
	s := u.slot()
	var out []*unit
	for _, want := range []int{s, s - 1, s + 1} {
		for _, x := range b.frontOf(1 - u.side) {
			if x.slot() == want {
				out = append(out, x)
			}
		}
	}
	return out
}

// adjacent 返回 t 左右相邻（槽位 ±1）的存活前阵单位。
func (b *battle) adjacent(t *unit) []*unit {
	var out []*unit
	for _, x := range b.frontOf(t.side) {
		if d := x.slot() - t.slot(); d == 1 || d == -1 {
			out = append(out, x)
		}
	}
	return out
}

// mostHurt 返回一方失血比例最高的存活前阵单位（「伤重护法」）。
func (b *battle) mostHurt(side int) *unit {
	var best *unit
	for _, x := range b.frontOf(side) {
		if best == nil || x.hp*best.maxHP < best.hp*x.maxHP {
			best = x
		}
	}
	return best
}

// deadAllies 是 side 一方可被复活的阵亡护法（前阵或已换到后阵；未被吞噬、未锢魂）。
func (b *battle) deadAllies(side int) []*unit {
	var out []*unit
	for _, x := range b.units[side] {
		if !x.alive() && !x.eaten && x.has(stNoSoul) == nil {
			out = append(out, x)
		}
	}
	return out
}

// invincible：无敌 / 暴走 / 诈死后本回合（伤害免疫）。
func (b *battle) invincible(x *unit) bool {
	// 诈死的免疫来自神通：被僵化（如踏浪）就失效
	return x.has(stImmune) != nil || x.has(stZhasi) != nil && x.has(stStiff) == nil
}

// vulnerable 是 l 中不处于无敌 / 暴走的护法；全都无敌时原样返回 l（「优先攻击不处于无敌和暴走的敌方护法」）。
func (b *battle) vulnerable(l []*unit) []*unit {
	var out []*unit
	for _, x := range l {
		if !b.invincible(x) {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		return l
	}
	return out
}

// easyDie 是 side 一方的「易死目标」：前阵生命值最少且不具备无敌（伤害免疫）的护法（FAQ：冲锋、乾坤破）。
func (b *battle) easyDie(side int) *unit {
	var low *unit
	for _, x := range b.frontOf(side) {
		if !b.invincible(x) && (low == nil || x.hp < low.hp) {
			low = x
		}
	}
	return low
}

// mostHurtExcept 是 self 一方前阵除 self 外生命比例最低的护法。
func (b *battle) mostHurtExcept(self *unit) *unit {
	var best *unit
	for _, x := range b.frontOf(self.side) {
		if x != self && (best == nil || x.hp*best.maxHP < best.hp*x.maxHP) {
			best = x
		}
	}
	return best
}

// sortByAtt 把 l 按攻击力从高到低排（选择交换，同攻击的先后关系不保证稳定——改成稳定排序会改变战报）。
func sortByAtt(l []*unit) {
	for i := range l {
		for j := i + 1; j < len(l); j++ {
			if l[j].att > l[i].att {
				l[i], l[j] = l[j], l[i]
			}
		}
	}
}

// pickN 从 l 里随机取至多 n 个（不重复）。
func (b *battle) pickN(l []*unit, n int) []*unit {
	l = append([]*unit(nil), l...)
	b.rng.Shuffle(len(l), func(i, j int) { l[i], l[j] = l[j], l[i] })
	return l[:min(n, len(l))]
}

func (b *battle) chance(pct int) bool { return pct > 0 && b.rng.IntN(100) < pct }

// chanceF 是带小数的概率（%），如激发后的 58.5%。
func (b *battle) chanceF(pct float64) bool { return pct > 0 && b.rng.Float64()*100 < pct }

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
