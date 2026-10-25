package battle

import "sort"

// 回合开始 / 回合结束触发的神通，以及暴走。

func init() {
	register(
		famHuZhen, famBiZhen, famJinHua, famFengMo, famYuHe, famYuanXing, famTunShi, famBuQu, famLuanZhan, famBoMing, famShenSu, famXueZhan, famBuJian, famMoLing, famJingYue, famYinRen,
		famYinYangLuan, famLieMing, famWuXiJiangGe, famWuZhiShan, famWuJiangZhiShang, famXiuLuoYinShen, famShiRiShengMu, famCangJianYuHua, famShuiZhiXing, famMoLong, famFengKuang, famMoYuRuTi,
		famLongWei, famShenSi, famMingShuYouWo, famDingTouQiJian, famFeiXian, famTianZhan,
	)
}

// roundReset：回合开始（补位之前），清每回合计数；十日圣母（在后阵也生效）。
func (b *battle) roundReset() {
	for side := range b.units {
		for _, u := range b.units[side] {
			u.perRound = [3]int{}
		}
	}
	for side := range b.units {
		for _, u := range b.units[side] {
			if k := u.tal(famShiRiShengMu); k != nil && u.sacrificed > 0 && u.alive() && !u.gone {
				// 前阵逆势护法每回合提升 献祭生命值×a% 的生命上限并回复同量。官方战报（献祭 12772）每回合
				// +4597 = 36%，是 24% 加上激发「增加生命效果提升」的部分，未激发按描述取 a%。
				var ts []*unit
				for _, x := range b.frontOf(side) {
					if x.tals[famNiShi] != nil {
						ts = append(ts, x)
					}
				}
				if len(ts) > 0 {
					b.show(u, k.id, u)
					grow := k.tjBoost(u.sacrificed * int64(k.param(0)) / 100)
					for _, x := range ts {
						b.changeMaxHP(x, k.id, grow)
						b.heal(x, k.id, grow)
					}
				}
			}
		}
	}
}

// roundOver：回合结束神通之后——后阵蓄力 / 剑意、还原本回合临时神通、弹丸减半、血池结算。
func (b *battle) roundOver() {
	for side := range b.units {
		for _, u := range b.units[side] {
			if u.alive() && !u.front() && !u.gone {
				b.chargeUp(u) // 回合结束时位于后阵蓄力 +1
				if u.tals[famZiQingJian] != nil {
					u.jianyi++
				}
			}
			for fam, prev := range u.roundTals {
				if prev == nil {
					delete(u.tals, fam)
				} else {
					u.tals[fam] = prev
				}
			}
			u.roundTals = nil
			if u.pellets > 0 { // 火炮之术：不稳定弹丸每回合减半（前后阵都减）
				u.pellets /= 2
				b.pelletCount(u)
			}
		}
	}
	for side := range b.units {
		b.bloodPool(side)
	}
	for side := range b.units {
		for _, u := range b.units[side] {
			b.pendingRevive(u)
		}
	}
}

// bloodPool：燃魂失去的生命蓄入本方血池，回合结束时消耗一半，对敌方易死护法造成等量火属性伤害；
// 天斩：以 a% 攻击力追击血池命中的目标，并对其相邻目标造成一半的物理伤害。
func (b *battle) bloodPool(side int) {
	half, by := b.pool[side]/2, b.poolBy[side]
	if half <= 0 || by == nil || b.over {
		return
	}
	b.pool[side] -= half
	t := b.mostHurt(1 - side)
	if t == nil {
		return
	}
	// 官方战报：ACT_DO(燃魂 → 目标) → BLOODPOOL_ATT{目标, 消耗量} → SUB_HP(10002 火属性伤害)
	b.show(by, b.poolID[side], t)
	b.w.u8(CmdBloodPoolAtt)
	b.w.u8(t.Pos)
	b.w.u32(half)
	b.propertyHurt(by, t, magicFireHurt, half, 3)
	for _, c := range b.frontOf(side) {
		k := c.tal(famTianZhan)
		if k == nil || !t.alive() {
			continue
		}
		adj := b.adjacent(t)
		if k.tjOn() {
			b.changeAtt(c, k.id, min(int64(float64(half)*k.tjX()/100), c.att/2))
		}
		b.show(c, k.id, append([]*unit{t}, adj...)...)
		n := c.att * int64(k.param(0)) / 100
		b.hurt(c, t, k.id, n, true)
		for _, x := range adj {
			b.hurt(c, x, k.id, n/2, true)
		}
	}
}

func (b *battle) roundStart(u *unit) {
	b.rampage(u)
	if u.dusha > 0 && u.alive() { // 毒砂（精修）：回合开始时损失 a% 攻击力
		b.changeAtt(u, u.dushaBuff, -u.att*int64(u.dusha)/100)
	}
	if k := u.tal(famShenSi); k != nil && k.tjOn() && b.round%2 == 1 && b.round > 1 && b.chance(k.tjPct()) {
		var dead []*unit
		for _, x := range b.units[u.side] {
			if !x.alive() && x.front() && x.has(stNoSoul) == nil {
				dead = append(dead, x)
			}
		}
		if len(dead) > 0 {
			x := dead[b.rng.IntN(len(dead))]
			b.show(u, k.id, x)
			b.relive(x, k.id, int64(float64(x.maxHP)*k.tjY()/100))
		}
	}
	if k := u.tal(famWuZhiShan); k != nil {
		// 敌方各 b% 概率随机获得一种：早夭10、僵化、麻痹、捆缚、锢魂
		for _, x := range b.foes(u) {
			if !b.chance(k.param(1)) {
				continue
			}
			b.show(u, k.id, x)
			var ok bool
			switch b.rng.IntN(5) {
			case 0:
				ok = b.addStatus(x, stZaoyao, x.maxHP*40/100, 1, false, 5260, slotDebuff)
			case 1:
				ok = b.addStatus(x, stStiff, 0, 1, true, 5001, slotDebuff)
			case 2:
				ok = b.addStatus(x, stSilence, 0, 1, true, 5011, slotDebuff)
			case 3:
				ok = b.addStatus(x, stBind, 0, 1, true, 5231, slotDebuff)
			case 4:
				ok = b.addStatus(x, stNoSoul, 0, maxRounds, false, 5141, slotDebuff)
			}
			if ok && k.tjOn() {
				b.propertyHurt(u, x, k.id, int64(float64(u.att)*k.tjX()/100), 1)
			}
		}
	}
	if k := u.tal(famWuJiangZhiShang); k != nil {
		// 扣除左右护法 (a%×剩余回合数 + b%) 的当前生命，给其该值 c% 的物理结界与战呼（buff 参数 4）
		if adj := b.adjacent(u); len(adj) > 0 {
			b.show(u, k.id, adj...)
			pct := int64(k.param(0)*(maxRounds-b.round) + k.param(1))
			for _, x := range adj {
				src := x
				if k.tjOn() && b.chance(k.tjPct()) {
					src = u
					b.chargeUp(x)
				}
				lost := b.hurt(src, x, k.id, min(x.hp*pct/100, x.hp-1), false)
				b.addShield(x, lost*int64(k.param(2))/100, false)
				b.zhanhu(x, int32(k.param(3)))
			}
		}
	}
	if k := u.tal(famXiuLuoYinShen); k != nil && u.xiuluo >= k.param(3) {
		// 消耗 d 点修罗点：战意 +5、隐身1、神之力（buff 参数 2：+3%×等级 攻击）、鬼斧（参数 3，只演出）
		b.setXiuluo(u, u.xiuluo-k.param(3))
		b.show(u, k.id, u)
		b.addZY(u.side, 5)
		b.setStatus(u, stHidden, 0, 1, false, int32(k.param(0)), slotBuff)
		b.shenli(u, int32(k.param(1)), int64(k.param(1)-5200)*3) // 神之力（参数 1）
		b.buff(u, int32(k.param(2)), 1)                          // 鬼斧（参数 2，只演出）
	}
	if k := u.tal(famCangJianYuHua); k != nil && b.chance(k.param(0)) {
		if allies := b.allies(u); len(allies) > 0 {
			x := allies[b.rng.IntN(len(allies))]
			b.show(u, k.id, x)
			b.giveSword(x)
		}
	}
	if k := u.tal(famShuiZhiXing); k != nil {
		// 对面三位里有攻击高于自己的：各吸取其 a% 攻击，本回合获得护盾 / 疯魔（参数 4、5）；
		// 全都低于自己：本回合获得恃强 / 暴击（参数 6、7）。
		opp := b.facing3(u) // 对面三位 = 对位及其左右（锁定站位，对位已死也不改选）
		var higher []*unit
		for _, x := range opp {
			if x.att > u.att && x.tals[famShuiZhiXing] == nil { // 另一个水之形不计入比较与吸取（FAQ）
				higher = append(higher, x)
			}
		}
		switch {
		case len(higher) > 0:
			b.show(u, k.id, higher...)
			for _, x := range higher {
				d := x.att*int64(k.param(0))/100 + int64(float64(x.att)*k.tjY()/100)
				d = min(d, u.att) // 单次吸取不超过自身当前攻击 100%（FAQ）
				b.changeAtt(x, k.id, -d)
				b.changeAtt(u, k.id, d)
			}
			b.buff(u, int32(k.param(1)), 1)
			b.grantRound(u, int32(k.param(3)))
			b.grantRound(u, int32(k.param(4)))
		case len(opp) > 0:
			b.show(u, k.id, u)
			b.buff(u, int32(k.param(2)), 1)
			b.grantRound(u, int32(k.param(5)))
			b.grantRound(u, int32(k.param(6)))
			if k.tjOn() {
				b.grantRound(u, levelID(famTuXi, int(k.tjW())))
			}
		}
	}
	if k := u.tal(famMoLong); k != nil {
		// 吸取 a%×当前攻击 点场上其他护法的结界（优先敌方、优先法术结界）；自身结界总数超过生命上限
		// 则获得蹈火 / 会猎 / 毒爪 / 异态（参数 2-5，buff 魔龙x）直到战斗结束。
		need := u.att * int64(k.param(0)) / 100
		// 吸取顺序（FAQ）：敌方法术结界 → 敌方物理结界 → 我方法术结界 → 我方物理结界，同项里结界最少的先吸；
		// 不吸自己，也不吸另一个带魔龙 / 飞仙的护法。
		var got [2]int64 // 物理、法术
		shown := false
		for _, grp := range []struct {
			us    []*unit
			magic bool
		}{{b.foes(u), true}, {b.foes(u), false}, {b.allies(u), true}, {b.allies(u), false}} {
			var pool []*unit
			for _, x := range grp.us {
				if x != u && x.tals[famMoLong] == nil && x.tals[famFeiXian] == nil {
					pool = append(pool, x)
				}
			}
			val := func(x *unit) int64 {
				if grp.magic {
					return x.mshield
				}
				return x.shield
			}
			sort.SliceStable(pool, func(i, j int) bool { return val(pool[i]) < val(pool[j]) })
			for _, x := range pool {
				if need <= 0 || val(x) == 0 {
					continue
				}
				if !shown {
					shown = true
					b.show(u, k.id, u)
				}
				n := b.cutShield(x, need, grp.magic)
				need -= n
				if grp.magic {
					got[1] += n
				} else {
					got[0] += n
				}
			}
		}
		b.addShield(u, k.tjBoost2(got[0]), false)
		b.addShield(u, k.tjBoost2(got[1]), true)
		if !u.molong && u.shield+u.mshield > u.maxHP {
			u.molong = true
			b.show(u, k.id, u)
			b.buff(u, k.Buff, 1)
			for _, p := range k.Param[1:] {
				b.grant(u, int32(p))
			}
			if k.tjOn() {
				b.grant(u, levelID(famBaoJi, int(k.tjW())))
			}
		}
	}
	if k := u.tal(famBuJian); k != nil && b.chance(k.param(0)) {
		b.addSword(u, k)
	}
	if k := u.tal(famHuZhen); k != nil && b.chance(k.param(0)) {
		allies := b.allies(u)
		b.show(u, k.id, allies...)
		for _, x := range allies {
			b.heal(x, k.id, x.maxHP*int64(k.param(1))/100)
		}
	}
	if k := u.tal(famBiZhen); k != nil && b.chance(k.param(0)) {
		// 回合开始 a% 概率治疗生命最少的友方 b%；目标濒危（< 25%，估计）且有入阵技时 c% 概率与后场护法换位。
		// 官方战报：ACT_DO 指向自己，ADD_HP 给目标，随后 MOVE（目标退到后阵，后阵补上）。
		if x := b.mostHurt(u.side); x != nil {
			// 濒危在回血之前判断（官方：羲和 14% → 回血后 39% 照样换位）
			endangered := x.hp*100 < x.maxHP*25
			b.show(u, k.id, u)
			b.heal(x, k.id, x.maxHP*int64(k.param(1))/100+int64(float64(x.maxHP)*k.tjX()/100))
			if x.alive() && x.front() && endangered && hasAny(x, enterFamilies) && b.chance(k.param(2)) {
				if bn := b.bench(u.side); len(bn) > 0 {
					b.swap(x, bn[b.rng.IntN(len(bn))])
				}
			}
		}
	}
	if k := u.tal(famJinHua); k != nil {
		for _, o := range b.foes(u) {
			if o.slot() != u.slot() {
				continue
			}
			if u.att < o.att && b.chance(k.param(0)) {
				b.show(u, k.id, u)
				b.changeAtt(u, k.id, k.tjBoost(u.baseAtt*int64(k.param(1))/100)) // 抓包：初始攻击 × b%
				if k.tjOn() && b.chance(int(k.tjY())) {
					u.spd++
				}
			}
			if u.maxHP < o.maxHP && b.chance(k.param(0)) {
				b.show(u, k.id, u)
				grow := k.tjBoost(u.maxHP * int64(k.param(1)) / 100)
				b.changeMaxHP(u, k.id, grow)
				b.heal(u, k.id, grow)
			}
		}
	}
	if k := u.tal(famYuanXing); k != nil {
		adj := b.adjacent(u)
		if len(adj) > 0 {
			// 官方战报：ACT_DO 只指向自己，左右两人的吸血合在一条 SUB_HP 里，随后 ATT_CHANGE、CHG_MAXHP、ADD_HP，
			// 最后给自己挂 BUFF 5371（代为承受的标记，slot 2、13）。之前 ACT_DO 指向邻位、分两条 SUB_HP，
			// 客户端把技能动画和数字都叠在邻位卡上。
			b.show(u, k.id, u)
			var sum int64
			var drained [][2]int64
			for _, x := range adj {
				n := max(min(x.hp*int64(k.param(0))/100, x.hp-1), 0)
				sum += n
				if k.tjOn() && b.chance(k.tjPct()) { // 激发：不扣邻位的血也照样转化
					continue
				}
				drained = append(drained, [2]int64{int64(x.Pos), n})
				x.hp -= n
			}
			if len(drained) > 0 {
				b.w.u8(CmdSubHP)
				b.w.u32(int64(k.id))
				b.w.u8(len(drained))
				for _, d := range drained {
					b.w.u8(int(d[0]))
					b.w.u32(d[1])
				}
			}
			sum += b.moyu(u)
			b.changeAtt(u, k.id, sum*int64(k.param(1))/100)
			grow := sum * int64(k.param(2)) / 100
			b.changeMaxHP(u, k.id, grow)
			b.heal(u, k.id, grow)
			b.buff(u, yuanxingBuff, 13)
		}
	}
	if k := u.tal(famTunShi); k != nil && !u.devoured && b.chance(k.param(0)) {
		// 优先吞噬敌方阵亡护法（锢魂的不能吞），没有再吞己方的；被吞的敌方获得锢魂，己方的不加（FAQ）
		for _, side := range []int{1 - u.side, u.side} {
			for _, x := range b.units[side] {
				if !x.alive() && !x.eaten && !u.devoured && x.has(stNoSoul) == nil {
					u.devoured = !k.tjOn() || !b.chance(k.tjPct())
					x.gone, x.eaten = true, true
					b.show(u, k.id, u)
					if side != u.side {
						b.buff(x, 5141, maxRounds) // 锢魂
					}
					b.changeAtt(u, k.id, x.baseAtt*int64(k.param(1))/100)
					grow := int64(x.MaxHP) * int64(k.param(1)) / 100
					b.changeMaxHP(u, k.id, grow)
					b.heal(u, k.id, grow)
					if !u.devoured {
						return
					}
				}
			}
		}
	}
	if k := u.tal(famFengMo); k != nil && b.chance(k.param(0)) {
		n := min(int64(u.MaxHP)*int64(k.param(1))/100, u.hp-1)
		if n > 0 {
			b.show(u, k.id, u)
			if !k.tjOn() || !b.chance(k.tjPct()) {
				b.hurt(u, u, k.id, n, false)
			}
			b.changeAtt(u, k.id, n)
			for _, x := range b.team(u.side) { // 疯狂：锦毛鼠获得此次疯魔 a% 的攻击力（在后阵也生效，FAQ）
				if c := x.tal(famFengKuang); c != nil && x != u {
					b.show(x, c.id, x)
					b.changeAtt(x, c.id, n*int64(c.param(0))/100)
					if x.fengkuang++; c.tjOn() && x.fengkuang == 5 {
						b.grant(x, levelID(famLuanWu, c.tj))
					}
				}
			}
		}
	}
}

func (b *battle) roundEnd(u *unit) {
	// 燃烧：所有层合计一次结算（ACT_DO(被烧者, 显示的燃烧图标号, 自己) → SUB_HP 17531，生命流失）
	if n := u.burnPct(); n > 0 && u.alive() && u.burnShown != 0 {
		b.show(u, u.burnShown, u)
		b.loseHP(nil, u, burnMagic, u.maxHP*int64(n)/100)
	}
	for i := 0; i < len(u.poison) && u.alive(); i++ {
		p := &u.poison[i]
		if !p.burn {
			b.loseHP(nil, u, p.from, u.maxHP*int64(p.pct)/100) // 中毒：生命流失
		}
		p.left--
	}
	kept := u.poison[:0]
	for _, p := range u.poison {
		if p.left > 0 {
			kept = append(kept, p)
		}
	}
	if u.alive() {
		left := map[int32]bool{}
		for _, p := range kept {
			left[p.buff] = true
		}
		for _, p := range u.poison {
			if p.left > 0 {
				continue
			}
			if p.stacked {
				b.rmPoisonIcon(u, p)
			} else if !left[p.buff] {
				left[p.buff] = true // 同一种毒只移除一次
				b.rmBuff(u, p.buff)
			}
		}
	}
	u.poison = kept
	if k := u.tal(famYuHe); k != nil && u.alive() && u.hp < u.maxHP {
		b.show(u, k.id, u)
		b.heal(u, k.id, u.maxHP*int64(k.param(0))/100)
		if k.tjOn() && b.chance(k.tjPct()) {
			for b.clearOneDebuff(u) {
			}
		}
	}
	if k := u.tal(famLongWei); k != nil && u.alive() {
		// 吸取友军总计 a% 攻击力点生命，并为被吸纳者增加等量的法术结界。分摊（FAQ）：先按战位依次吸带暴走神通
		// 且未暴走的护法，各吸到其上限 20% 为止；还有剩余则平摊给其余护法（已暴走的视同无暴走神通），吸后
		// 最低保留 5 点；再有剩余作废。
		left := u.att * int64(k.param(0)) / 100
		var rage, rest []*unit
		for _, x := range b.allies(u) {
			if x == u {
				continue
			}
			if hasAny(x, rampageTalents) && !x.raged {
				rage = append(rage, x)
			} else {
				rest = append(rest, x)
			}
		}
		sort.SliceStable(rage, func(i, j int) bool { return rage[i].slot() < rage[j].slot() })
		take := map[*unit]int64{}
		var order []*unit
		for _, x := range rage {
			if n := min(left, max(x.hp-x.maxHP*20/100, 0)); n > 0 {
				take[x], left = n, left-n
				order = append(order, x)
			}
		}
		if left > 0 && len(rest) > 0 {
			share := left / int64(len(rest))
			for _, x := range rest {
				if n := min(share, max(x.hp-5, 0)); n > 0 {
					take[x] = n
					order = append(order, x)
				}
			}
		}
		if len(order) > 0 {
			b.show(u, k.id, order...)
			for _, x := range order {
				got := b.loseHP(x, x, k.id, take[x])
				if h := b.heal(u, k.id, got); k.tjOn() {
					b.addShield(u, int64(float64(h)*k.tjX()/100), true)
				}
				b.addShield(x, got*int64(k.param(1))/100, true)
			}
		}
	}
	if k := u.tal(famShenSi); k != nil && u.alive() && b.round%2 == 0 {
		// 偶数回合结束时击败一名本方护法（优先携死亡技）：本方全体回复亡者 a% 当前生命，
		// 敌方全体受到亡者 b% 攻击力的土属性伤害。
		// 选人（FAQ）：优先带死亡技的（多个按战位顺序），没有则随机；死过一次的护法视作不具备死亡技。
		// 回复 / 伤害按亡者的当前生命、当前攻击。
		var mates, doomed []*unit
		for _, x := range b.allies(u) {
			if x != u {
				mates = append(mates, x)
				if hasAny(x, deathFamilies) && x.deaths == 0 {
					doomed = append(doomed, x)
				}
			}
		}
		var v *unit
		if len(doomed) > 0 {
			sort.SliceStable(doomed, func(i, j int) bool { return doomed[i].slot() < doomed[j].slot() })
			v = doomed[0]
		} else if len(mates) > 0 {
			v = mates[b.rng.IntN(len(mates))]
		}
		if v != nil {
			hp, att := v.hp, v.att
			b.show(u, k.id, v)
			b.hurt(v, v, k.id, v.hp, false)
			if !v.alive() && !b.over {
				allies, foes := b.allies(u), b.foes(u)
				b.show(u, k.id, allies...)
				for _, x := range allies {
					b.heal(x, k.id, k.tjBoost(hp*int64(k.param(0))/100))
				}
				b.show(u, k.id, foes...)
				for _, x := range foes {
					b.propertyHurt(u, x, k.id, k.tjBoost(att*int64(k.param(1))/100), 1)
				}
			}
		}
	}
	if k := u.tal(famMingShuYouWo); k != nil && u.alive() {
		// 对敌方攻击最高者造成 本方死亡数(至多 c)×a%×目标上限 + 剩余回合数×b%×目标攻击 的土属性伤害
		if foes := b.foes(u); len(foes) > 0 {
			sortByAtt(foes)
			t := foes[0]
			dead := 0
			for _, x := range b.units[u.side] {
				if !x.alive() {
					dead++
				}
			}
			dead = min(dead, k.param(2))
			n := int64(dead*k.param(0))*t.maxHP/100 + int64((maxRounds-b.round)*k.param(1))*t.att/100
			if n > 0 {
				b.show(u, k.id, t)
				b.propertyHurt(u, t, k.id, n, 1)
				if k.tjOn() && dead >= b.round-1 && t.alive() {
					b.changeAtt(t, k.id, -int64(float64(t.att)*k.tjX()/100))
				}
			}
		}
	}
	if k := u.tal(famDingTouQiJian); k != nil && u.alive() {
		// 7 把钉头箭射向对面，每把 a% 当前攻击力的物理伤害；某把被减伤（结界吸收等），
		// 下一把提高被减伤值的 b%。
		if t := b.pickTarget(u); t != nil {
			b.show(u, k.id, t)
			var bonus int64
			for range 7 {
				if !t.alive() {
					if t = b.pickTarget(u); t == nil {
						break
					}
				}
				n := u.att*int64(k.param(0))/100 + int64(float64(u.att)*k.tjW()/100) + bonus
				if k.tjOn() && t.att > u.att && b.chance(int(k.tjParam(1)*0.1*float64(k.tj))) {
					b.changeAtt(t, k.id, -int64(float64(t.att)*k.tjParam(2)/100))
				}
				hp := t.hp
				got := b.hurt(u, t, k.id, n, true)
				bonus = 0
				if got < n && got < hp {
					bonus = (n - got) * int64(k.param(1)) / 100
				}
			}
		}
	}
	if k := u.tal(famFeiXian); k != nil && u.alive() {
		b.feixian(u, k)
	}
	b.doomTick(u)
}

// doomTick：不灭躯复活后 n 回合自行死亡（不受僵化影响）。
func (b *battle) doomTick(u *unit) {
	if u.doomIn > 0 && u.alive() {
		if u.doomIn--; u.doomIn == 0 {
			b.doomKill(u)
		}
	}
}

// doomKill：活尸自行死亡——BUFF 5141「锢魂」→ SUB_HP(5141, 全部生命) → DIE → RM_BUFF 活尸。
func (b *battle) doomKill(u *unit) {
	b.buff(u, 5141, maxRounds)
	b.hurt(u, u, 5141, u.hp, false)
}

func (b *battle) clearPoison(t *unit) {
	if len(t.poison) > 0 {
		for _, p := range t.poison {
			b.rmPoisonIcon(t, p)
		}
		t.poison = nil
		b.burnIcon(t)
	}
}

// 暴走：回合开始时濒危（生命 < a%）或被复活后，本回合不受伤害，并获得参数 2 起的神通直到战斗结束
// （魔灵等后四族赠送多个；参数里不是神通号的值跳过）。
var rampageTalents = []family{famBuQu, famLuanZhan, famBoMing, famShenSu, famXueZhan, famMoLing, famJingYue, famYinRen, famYinYangLuan, famLieMing, famWuXiJiangGe}

func (b *battle) rampage(u *unit) {
	if !u.alive() || u.gone { // 死了（或等待复活中）不暴走
		return
	}
	for _, fam := range rampageTalents {
		k := u.tal(fam)
		limit := int64(k.rampageLimit())
		if k == nil || u.raged || !(u.hp*100 < u.maxHP*limit || u.revived) {
			continue
		}
		u.raged = true
		b.show(u, k.id, u)
		b.setStatus(u, stImmune, 0, 1, false, 5321, slotBuff) // 官方战报：暴走后 BUFF 5321「无敌」
		for _, p := range k.Param[1:] {
			b.grant(u, int32(p))
		}
		b.rampageTJ(u, k)
	}
}

// rampageLimit 是暴走的濒危线 %：激发后放宽为 $z（血战、神速、隐忍、猎命、静岳、舞兮江歌、阴阳乱）。
func (k *talent) rampageLimit() int {
	if k == nil {
		return 0
	}
	if z := int(k.tjZ()); k.tjOn() && k.tjParam(0) >= 30 && z > k.param(0) {
		return z
	}
	return k.param(0)
}

// rampageTJ：暴走激发——回复 $y% 上限（血战、神速、隐忍、猎命），或获得额外神通：
// 搏命 暴击$w、不屈 先攻$w、乱战 狂热$w、魔灵 兽妖$w、静岳 / 舞兮江歌 连击$y、阴阳乱 狂怒焰$v。
// 月之暗面激发：本方护法暴走时，天狗把自身攻击 / 上限超过初始值部分的 $x% 复制给它。
func (b *battle) rampageTJ(u *unit, k *talent) {
	if k.tjOn() {
		switch k.fam {
		case famXueZhan, famShenSu, famYinRen, famLieMing:
			b.heal(u, k.id, int64(float64(u.maxHP)*k.tjY()/100))
		case famBoMing:
			b.grant(u, levelID(famBaoJi, int(k.tjW())))
		case famBuQu:
			b.grant(u, levelID(famXianGong, int(k.tjW())))
		case famLuanZhan:
			b.grant(u, levelID(famKuangRe, int(k.tjW())))
		case famMoLing:
			b.grant(u, levelID(famShouYao, int(k.tjW())))
		case famJingYue, famWuXiJiangGe:
			b.grant(u, levelID(famLianJi, int(k.tjY())))
		case famYinYangLuan:
			b.grant(u, levelID(famKuangNuYan, int(k.tjV())))
		}
	}
	for _, c := range b.units[u.side] {
		if m := c.tal(famYueZhiAnMian); m != nil && m.tjOn() && c != u && c.alive() {
			if d := c.att - c.baseAtt; d > 0 {
				b.changeAtt(u, m.id, int64(float64(d)*m.tjX()/100))
			}
			if d := c.maxHP - int64(c.MaxHP); d > 0 {
				g := int64(float64(d) * m.tjX() / 100)
				b.changeMaxHP(u, m.id, g)
				b.heal(u, m.id, g)
			}
		}
	}
}

// yuanxingBuff 是元形给自己挂的标记（官方战报 BUFF 5371，talent.csv buffid）。
const yuanxingBuff = 5371

// moyu：魔欲入体——己方护法触发元形时，有（本方魔族数×25%）概率额外吸收随机两名前阵护法 a% 生命，
// 返回吸到的量（并入元形的转化）。载体在后阵也触发（FAQ）。
func (b *battle) moyu(u *unit) int64 {
	var sum int64
	for _, c := range b.team(u.side) {
		k := c.tal(famMoYuRuTi)
		if k == nil || !b.chance(25*int(countRace(b.allies(u), raceGhost))) {
			continue
		}
		var all []*unit
		for side := range b.units {
			for _, x := range b.frontOf(side) {
				if x != u {
					all = append(all, x)
				}
			}
		}
		if ts := b.pickN(all, 2); len(ts) > 0 {
			b.show(c, k.id, ts...)
			for _, x := range ts {
				sum += b.hurt(u, x, k.id, min(k.tjBoost(x.hp*int64(k.param(0))/100), x.hp-1), false)
			}
		}
	}
	return sum
}

// feixian：飞仙——回合结束时清除至多 a%×生命上限 点法术结界（优先敌方），其中 b% 转为自己的物理结界；
// 前阵血量最低的护法（优先入阵护法）有（本方入阵护法数×c%，至多 f%）概率获得神之力 / 神之护
// （buff 参数 4、5：+3%×等级），并遁入后阵。官方战报：ATT_CHANGE / CHG_MAXHP 以 buff 号为招式号，
// 随后 MOVE 到后阵，离开前阵时增益撤销（RM_BUFF + ATT_CHANGE op2）；无后阵可去则保留增益。
func (b *battle) feixian(u *unit, k *talent) {
	budget := u.maxHP * int64(k.param(0)) / 100
	var removed int64
	for _, x := range append(b.foes(u), b.allies(u)...) {
		if x != u && budget > removed && x.mshield > 0 {
			if removed == 0 {
				b.show(u, k.id, u)
			}
			removed += b.cutShield(x, budget-removed, true)
		}
	}
	b.addShield(u, removed*int64(k.param(1))/100, false)
	// 概率按本方存活的入阵护法数（前阵 + 后阵）计；目标是前阵血量最低的护法（优先入阵护法）。
	nEnter := 0
	for _, x := range b.units[u.side] {
		if x.alive() && !x.gone && hasAny(x, enterFamilies) {
			nEnter++
		}
	}
	var enterers, pool []*unit
	allies := b.allies(u)
	for _, x := range allies {
		if hasAny(x, enterFamilies) {
			enterers = append(enterers, x)
		}
	}
	pool = allies
	if len(enterers) > 0 {
		pool = enterers
	}
	var low *unit
	for _, x := range pool {
		if low == nil || x.hp < low.hp {
			low = x
		}
	}
	if low == nil || !b.chance(min(nEnter*k.param(2), k.param(5))) {
		return
	}
	b.show(u, k.id, u)
	pw, hu := int32(k.param(3)), int32(k.param(4))
	up := b.shenli(low, pw, int64(pw-5200)*3) // 神之力：遁入后阵时随回后阵清除
	grow := low.maxHP * int64(hu-5210) * 3 / 100
	b.buff(low, hu, maxRounds)
	b.changeMaxHP(low, hu, grow)
	b.heal(low, hu, grow)
	if to := b.dunruTarget(u.side); to != nil {
		low.dunru = true
		b.swap(low, to) // 回后阵：神之力清除、攻击 / 生命上限重置、图标去掉（benchReset）
		if front := b.allies(u); k.tjOn() && len(front) > 0 {
			n := int64(len(front))
			for _, x := range front {
				b.changeAtt(x, k.id, int64(float64(up)*k.tjX()/100)/n)
				g := int64(float64(grow)*k.tjX()/100) / n
				b.changeMaxHP(x, k.id, g)
				b.heal(x, k.id, g)
			}
		}
	} else {
		if hasAny(low, enterFamilies) && !low.dunru {
			low.dunru = true // 后阵无存活护法：触发 1 次入阵技（每个入阵护法最多 1 次）
			b.enter(low)
		}
		if k.tjOn() && b.chance(int(k.tjY())) {
			if g := newTalent(int32(k.tjParam(2))); g != nil {
				b.ganlu(u, g)
			}
		}
	}
}

// dunruTarget 是[遁入]交换的后阵护法（图鉴 FAQ）：未[遁入]过的入阵护法 → 已[遁入]过的入阵护法 → 无入阵技护法，
// 同档从左到右。
func (b *battle) dunruTarget(side int) *unit {
	bn := b.bench(side)
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
