package battle

// 蓄力类神通：受到 / 躲过伤害或回合结束时位于后阵蓄力 +1，满后在攻击时释放。

func init() {
	register(
		famFeiShi, famSanBanFu, famTianBian, famChongXing, famFengShen, famLiJian, famJiuSi, famWenXiang, famGuaXiang, famBaiCao, famXuanYuanJian, famHuaBing, famQianKunPo,
		famLiuYi, famJianGe, famRenWu, famQiangChuYouLong, famTuoMaoTuiDi, famLongHuXianDan,
	)
}

// 蓄力神通：受到/躲过伤害或回合结束时位于后阵蓄力 +1，满 chargeNeed 层后在攻击时释放。
// 不灭法身只蓄不放（死亡时按蓄力算复活概率）。
var chargeTalents = []family{famFeiShi, famSanBanFu, famTianBian, famChongXing, famFengShen, famLiJian, famJiuSi, famWenXiang, famGuaXiang, famBaiCao,
	famXuanYuanJian, famHuaBing, famQianKunPo, famLiuYi, famJianGe, famRenWu, famQiangChuYouLong, famTuoMaoTuiDi, famLongHuXianDan, famBuMieFaShen}

// chargeNeed 是攻击时释放所需的蓄力层数（0 = 不在攻击时释放）。多数族是参数 1；
// 剑歌、刃舞有蓄力就在攻击时消耗；脱帽退敌参数 1 是每层减伤，满 4 释放。
func chargeNeed(k *talent) int {
	switch k.fam {
	case famJianGe, famRenWu:
		return 1
	case famTuoMaoTuiDi:
		return 4
	case famBuMieFaShen:
		return 0
	}
	return max(k.param(0), 1)
}

// chargeCap 是蓄力上限（剑歌、刃舞参数 1 = 5；不灭法身参数 5 = 5；其余满了即释放）。
func chargeCap(k *talent) int {
	switch k.fam {
	case famJianGe, famRenWu:
		return k.param(0)
	case famBuMieFaShen:
		return k.param(4)
	}
	return chargeNeed(k)
}

// PowerFull 是战报里这张卡的满蓄力层数（S_FIGHT_RECORD.otherparam idx 1「站位:层数」，war/netwar.lua powerSetting；
// 不发时客户端按 4 显示，满 5 的百草、救死等会在 4 层就亮「满」）。没有蓄力神通返回 0。
func PowerFull(f *Fighter) int {
	for _, fam := range chargeTalents {
		for _, id := range f.Talents {
			if k := newTalent(id); k != nil && k.fam == fam {
				return chargeCap(k)
			}
		}
	}
	return 0
}

func chargeTalent(u *unit) *talent {
	for _, f := range chargeTalents {
		if k := u.tal(f); k != nil {
			return k
		}
	}
	return nil
}

// chargeUp：蓄力 +1。僵化（无法发动神通）时照样积累，只是满了也放不出来（用户报：薛仁贵带僵化不蓄力）。
func (b *battle) chargeUp(u *unit) {
	var k *talent
	for _, f := range chargeTalents {
		if k = u.tals[f]; k != nil {
			break
		}
	}
	if k != nil && u.charge < chargeCap(k) {
		n := 1
		if k.fam == famJianGe && k.tjOn() && u.charge+1 < chargeCap(k) && b.chance(k.tjPct()) {
			n++ // 剑歌激发：蓄力时 $x% 额外一层
		}
		b.setCharge(u, u.charge+n)
	}
}

// setCharge 把 u 的蓄力改成 n 并输出 ADD_POWER（官方战报：每加一层 op 1 num 1，释放时 op 2 num = 层数，
// 在释放神通的 ACT_DO 之前；客户端靠它显示蓄力层数）。
func (b *battle) setCharge(u *unit, n int) {
	d := n - u.charge
	u.charge = n
	if d == 0 {
		return
	}
	op := 1
	if d < 0 {
		op, d = 2, -d
	}
	b.w.u8(CmdAddPower)
	b.w.u8(op)
	b.w.u8(1)
	b.w.u8(u.Pos)
	b.w.u8(d)
}

// releaseCharge：蓄力满后在攻击时释放。
func (b *battle) releaseCharge(u *unit, k *talent) {
	n := u.charge
	if !(k.fam == famLiuYi && k.tjOn() && b.chance(k.tjPct())) { // 六艺激发：$x% 概率不清空蓄力
		b.setCharge(u, 0)
	}
	foes := b.foes(u)
	switch k.fam {
	case famLiuYi: // 使 b 名友方随机获得一种增益：物理 / 法术结界、无敌、加速3、战呼3、犒赏3
		ts := b.pickN(b.allies(u), k.param(1))
		b.show(u, k.id, ts...)
		for _, x := range ts {
			b.boon(x)
		}
	case famJianGe: // 消耗蓄力，每层提升 b% 当前攻击（攻击后清除）；c% 概率把消耗的层数转给另一位队友
		b.tempUp(u, k, u.atkBase()*int64(k.param(1)*n)/100)
		if b.chance(k.param(2)) {
			// 转给战位最前且未满蓄力的护法，加满为止，溢出消失（FAQ）
			var to *unit
			for _, x := range b.units[u.side] {
				if k2 := chargeTalent(x); x != u && x.alive() && !x.gone && k2 != nil && x.charge < chargeCap(k2) &&
					(to == nil || x.slot() < to.slot()) {
					to = x
				}
			}
			if to != nil {
				b.setCharge(to, min(to.charge+n, chargeCap(chargeTalent(to))))
			}
		}
	case famRenWu: // 消耗蓄力以 b% 攻击力挥扫多名敌方（目标数 = 蓄力层数）
		// 目标从敌方前阵全体里选（可含主目标），满 5 层就是 5 个目标
		if ts := b.pickN(foes, n); len(ts) > 0 {
			b.show(u, k.id, ts...)
			var sum int64
			for _, x := range ts {
				sum += b.hurt(u, x, k.id, u.att*int64(k.param(1))/100, true)
			}
			if k.tjOn() {
				b.addShield(u, int64(float64(sum)*k.tjX()/100), true)
			}
		}
	case famQiangChuYouLong: // 以 b% 攻击力二连击
		b.show(u, k.id, u)
		t := b.pickTarget(u)
		for range 2 {
			if t == nil || !t.alive() || !t.front() { // 目标阵亡或被换下场：改打当前对位
				t = b.pickTarget(u)
			}
			if t == nil || !u.alive() {
				break
			}
			b.attackAction(u, t, k.param(1), atkFollow) // 枪出游龙：每一击都是完整的攻击行为（触发分击等）
			if k.tjOn() && !t.alive() && b.chance(50+10*k.tj) {
				b.setCharge(u, max(u.charge, 2))
			}
		}
	case famTuoMaoTuiDi: // b% 概率令敌方最高攻击护法与后排攻击最低护法换位
		if !b.chance(k.param(1)) || len(foes) == 0 {
			break
		}
		// 被换上的护法不要求存活（FAQ）：后阵的任何一张卡都算，包括已阵亡、已被补位换下去的（被吞噬的除外）。
		// 后阵一张卡都没有时不换——客户端没法把卡放到从没有过卡的位置（会画到界面外，用户报）。
		var bn []*unit
		for _, x := range b.units[1-u.side] {
			if !x.front() && !x.eaten {
				bn = append(bn, x)
			}
		}
		sortByAtt(foes)
		sortByAtt(bn)
		b.show(u, k.id, foes[0])
		if k.tjOn() && b.chance(k.tjPct()) {
			b.addStatus(foes[0], stNoSoul, 0, maxRounds, false, 5141, slotDebuff)
		}
		if len(bn) > 0 {
			b.quzhu(foes[0], bn[len(bn)-1]) // 换上来的死者留在前阵，回合开始由后阵补位
		}
	case famLongHuXianDan: // 本方易死护法获得 b%×自身上限的物理结界；无续命丹的易死护法获得续命丹（buff 参数 3）
		x := b.mostHurt(u.side)
		if x == nil {
			break
		}
		b.show(u, k.id, x)
		b.addShield(x, u.maxHP*int64(k.param(1))/100, false)
		if k.tjOn() {
			b.addShield(u, int64(float64(u.maxHP)*k.tjX()/100), false)
		}
		var low *unit
		for _, y := range b.allies(u) {
			if y.has(stXuming) == nil && (low == nil || y.hp*low.maxHP < low.hp*y.maxHP) {
				low = y
			}
		}
		if low != nil {
			b.setStatus(low, stXuming, 11, maxRounds, false, int32(k.param(2)), slotBuff)
		}
	case famFeiShi: // 令敌方生命最少的护法失去 b% 上限生命
		var low *unit
		for _, x := range foes {
			if low == nil || x.hp < low.hp {
				low = x
			}
		}
		if low != nil {
			b.show(u, k.id, low)
			b.loseHP(u, low, k.id, low.maxHP*int64(k.param(1))/100)
		}
	case famSanBanFu: // 以 b% 攻击力三连乱击
		b.show(u, k.id, u)
		for i := range 3 {
			if foes := b.attackable(u); len(foes) > 0 && u.alive() {
				x := foes[b.rng.IntN(len(foes))]
				if t := b.pickTarget(u); i == 0 && t != nil {
					x = t // 首次打原定目标，后两次随机（FAQ）
				}
				// 三板斧：每一刀都是完整的普通攻击行为（攻击时 / 攻击后神通、暴击、挥扫……都触发）
				b.attackAction(u, x, k.param(1)+k.tjPct(), atkFollow)
			}
		}
	case famTianBian: // 敌方全体 b% 攻击的雷属性法术伤害
		b.show(u, k.id, foes...)
		for _, x := range foes {
			b.propertyHurt(u, x, fxMagic(k), u.att*int64(k.param(1))/100+int64(float64(u.baseAtt)*k.tjX()/100), 4)
		}
	case famChongXing, famFengShen, famLiJian: // 消除三体 $r（b% 攻击）法术结界，c% 概率捆缚 / 僵化 / 混乱
		ts := b.facing3(u) // 对方三体 = 对位及其左右（锁定站位，用户规则）
		if len(ts) == 0 {
			break
		}
		b.show(u, k.id, ts...)
		st := map[family]stKey{famChongXing: stBind, famFengShen: stStiff, famLiJian: stConfuse}[k.fam]
		for _, x := range ts {
			if k.fam == famFengShen && k.tjOn() { // 封神激发：僵化前造成 $x% 攻击的土属性伤害
				b.propertyHurt(u, x, k.id, int64(float64(u.att)*k.tjX()/100), 1)
			}
			b.cutShield(x, u.att*int64(k.param(1))/100, true)
			if b.chance(k.param(2)) && b.addStatus(x, st, 0, 1, true, k.Buff, slotDebuff) &&
				k.fam == famChongXing && k.tjOn() { // 宠幸激发：被捆缚目标下降当前攻击 $x%
				b.changeAtt(x, k.id, -min(int64(float64(x.att)*k.tjX()/100), u.att/2)) // 单次不超过宠幸护法当前攻击 50%（FAQ）
			}
		}
	case famJiuSi: // 复活 1 名友方，生命为上限 b%
		if dead := b.deadAllies(u.side); len(dead) > 0 { // 从本方已阵亡护法中随机选取（FAQ）
			x := dead[b.rng.IntN(len(dead))]
			b.show(u, k.id, x)
			b.relive(x, k.id, x.maxHP*int64(k.param(1))/100)
			if k.tjOn() && b.chance(k.tjPct()) {
				b.setStatus(x, stImmune, 0, 1, false, 5321, slotBuff)
				b.zhanhu(x, 5265)
			}
		}
	case famWenXiang: // 回复伤重友方 b% 生命
		if x := b.mostHurt(u.side); x != nil {
			b.show(u, k.id, x)
			b.heal(x, k.id, x.maxHP*int64(k.param(1))/100)
			if k.tjOn() {
				b.changeAtt(x, k.id, int64(float64(x.att)*k.tjX()/100))
			}
		}
	case famGuaXiang: // 全体友方物理结界：每人吸收 b% 上限（$q 按族描述取每名受者的自身上限）
		allies := b.allies(u)
		b.show(u, k.id, allies...)
		for _, x := range allies {
			b.addShield(x, x.maxHP*int64(k.param(1))/100, false)
			if k.tjOn() {
				b.heal(x, k.id, int64(float64(x.maxHP)*k.tjX()/100))
			}
		}
	case famBaiCao: // 回复全体友方，量为自身上限 b%（官方战报：5337 = 载体上限 13343 × 40%）
		allies := b.allies(u)
		b.show(u, k.id, allies...)
		for _, x := range allies {
			b.heal(x, k.id, u.maxHP*int64(k.param(1))/100)
			if k.tjOn() {
				b.changeAtt(x, k.id, int64(float64(x.att)*k.tjX()/100))
			}
		}
	case famXuanYuanJian: // 以 b% 攻击力三连击：都打同一目标，目标被击败就停（不换目标，与三板斧不同）。
		b.show(u, k.id, u)
		t := b.pickTarget(u)
		hits := 3
		if k.tjOn() && b.chance(int(k.tjY())) {
			hits++ // 激发：$y% 额外一次连击
		}
		for range hits {
			if t == nil || !t.alive() || !u.alive() || b.over {
				break
			}
			if k.tjOn() && b.chance(k.tjPct()) {
				b.loseHP(u, t, k.id, u.att*int64(k.param(1))/100) // 激发：$x% 概率伤害转为生命流失
				continue
			}
			// 每一击都是完整的攻击行为（攻击时 / 攻击后神通、暴击、挥扫……都触发，FAQ「每次均视为[攻击行为]」）
			b.attackAction(u, t, k.param(1), atkFollow)
		}
	case famHuaBing: // 敌方最高攻者流失 b%×其攻击的生命（非物理伤害，不闪避不暴击，如飞石）。
		var best *unit
		for _, x := range b.foes(u) {
			if best == nil || x.att > best.att {
				best = x
			}
		}
		if best == nil {
			return
		}
		b.show(u, k.id, best)
		b.loseHP(u, best, k.id, min(best.att*int64(k.param(1))/100, u.att*10)) // 生命流失，单次不超过自身攻击 ×10（FAQ）
		if k.tjOn() && b.chance(k.tjPct()) {
			fs := b.foes(u)
			sortByAtt(fs)
			for _, x := range fs {
				if x != best {
					b.show(u, k.id, x)
					b.loseHP(u, x, k.id, min(x.att*int64(k.param(1))/100, u.att*10)) // 激发：次高攻击护法同样生命流失
					break
				}
			}
		}
	case famQianKunPo:
		// 以 30% 攻击力对当前生命最低者 c 连射（可触发暴击）。官方战报：ACT_DO(乾坤破 → 目标) 后每一射
		// [暴击时 ACT_DO(暴击 → 自己)] + SUB_HP(乾坤破)，不走普攻（没有普攻动作和闪避）。
		pct := int64(k.param(1)) + int64(float64(u.baseAtt)*k.tjX()/float64(max(u.att, 1)))
		var cur *unit
		for range max(k.param(2), 1) {
			low := b.easyDie(1 - u.side)
			if low == nil || !u.alive() {
				break
			}
			if low != cur {
				b.show(u, k.id, low)
				cur = low
			}
			dmg := u.att * pct / 100
			if c := u.tal(famBaoJi); c != nil && b.chance(c.param(0)) {
				b.show(u, c.id, u)
				dmg = dmg * int64(100+max(c.param(1), 100)) / 100
			}
			b.hurt(u, low, k.id, dmg, true)
		}
	}
}

// boon 是六艺的随机增益。结界量描述未给，取受者上限 10%；加速只演出（速度是 Fighter 静态字段）。
func (b *battle) boon(x *unit) {
	switch b.rng.IntN(6) {
	case 0:
		b.addShield(x, x.maxHP*10/100, false)
	case 1:
		b.addShield(x, x.maxHP*10/100, true)
	case 2:
		b.setStatus(x, stImmune, 0, 1, false, 5321, slotBuff) // 无敌，到当前回合结束
	case 3:
		b.buff(x, 5093, 1) // 加速3
	case 4:
		b.zhanhu(x, 5263)
	case 5: // 犒赏3：提升 30% 生命上限，下次行动后清除
		if x.has(stTempMax) == nil {
			grow := x.maxHP * 30 / 100
			b.changeMaxHP(x, 0, grow)
			b.setStatus(x, stTempMax, grow, 1, true, 5273, slotBuff)
		}
	}
}
