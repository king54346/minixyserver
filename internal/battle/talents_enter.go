package battle

// 入阵（后阵补位进入前阵）触发的神通；开场就在前阵的不触发。

func init() {
	register(
		famXiuLin, famKaoLao, famKuangLei, famXunDian, famLangYong, famQianKunXiu, famShenWei, famPanTaoYan, famGanLu, famDaoZhiShi, famGuangYinSui, famShuiZhiYuan, famZhuXianZhen,
		famYuanHu, famLongYue, famPoChuJinGu, famZiQingJian, famTianGangJianQi, famShiXinLongZhou, famHuoPaoZhiShu, famShiRiShengMu, famTeDingRuoHua,
	)
}

const (
	qkxBindBuff = 5231 // 捆缚（官方战报乾坤袖）
	hiddenBuff  = 5111 // 隐身
)

// weakenPct 是特定弱化（助人海星，29001-29043）使敌人攻击力临时降低的 %（数值只在描述里，按描述抄）。
var weakenPct = map[int32]int64{
	29001: 1, 29002: 2, 29003: 3, 29004: 4, 29005: 5, 29006: 6, 29007: 7, 29008: 8, 29009: 9, 29010: 10,
	29011: 11, 29012: 20, 29013: 30, 29014: 40, 29015: 50,
	29021: 1, 29022: 3, 29023: 5, 29024: 6, 29025: 8, 29026: 10, 29027: 20, 29028: 30, 29029: 40, 29030: 50,
	29031: 1, 29032: 2, 29033: 3, 29034: 4, 29035: 5, 29036: 6, 29037: 7, 29038: 9, 29039: 18, 29040: 27,
	29041: 36, 29042: 45, 29043: 54,
}

// afterOpening 在开场入阵之后：十日圣母献祭自身，生命降至 1（在后阵也献祭；官方战报 SUB_HP = hp-1）。
func (b *battle) afterOpening() {
	for side := range b.units {
		for _, u := range b.units[side] {
			if k := u.tal(famShiRiShengMu); k != nil && !u.sacDone && u.alive() {
				u.sacDone = true
				if n := u.hp - 1; n > 0 {
					b.show(u, k.id, u)
					u.sacrificed = b.hurt(u, u, k.id, n, false)
				}
			}
		}
	}
}

// battleStart 在开场入阵之前：特定弱化使敌方全体攻击力降低 x%（整场）。
// 描述限定「12-5-10 等关卡的敌人」，战斗里没有关卡号，按带了就生效处理；
// 「气甲、仙衣、分水弱化为 n 级」不做。
func (b *battle) battleStart() {
	for side := range b.units {
		for _, u := range b.units[side] {
			if k := u.tals[famShiXinLongZhou]; k != nil && k.tjOn() && u.tals[famFanJi] == nil {
				b.grant(u, levelID(famFanJi, int(k.tjW()))) // 噬心龙咒激发：获得【反击 2/4/6/8/10】（常驻，不必入阵）
			}
			if k := u.tals[famLeiYingBiHu]; k != nil && k.tjOn() {
				g := int64(float64(u.MaxHP) * k.tjX() / 100)
				b.changeMaxHP(u, k.id, g)
				b.heal(u, k.id, g)
			}
		}
	}
	for side := range b.units {
		for _, u := range b.units[side] {
			k := u.tals[famTeDingRuoHua]
			if k == nil || weakenPct[k.id] == 0 {
				continue
			}
			b.show(u, k.id, b.frontOf(1-side)...)
			for _, x := range b.units[1-side] {
				b.changeAtt(x, k.id, -x.att*weakenPct[k.id]/100)
			}
		}
	}
}

// initPellets：火炮之术战斗开始时获得（本方先攻护法数 × b）枚不稳定弹丸（前阵后阵都给，FAQ）。
func (b *battle) initPellets(u *unit) {
	if k := u.tal(famHuoPaoZhiShu); k != nil && !u.pelletInit {
		u.pelletInit = true
		for _, x := range b.units[u.side] {
			if x.tals[famXianGong] != nil {
				u.pellets += k.param(1)
			}
		}
		b.pelletCount(u)
	}
}

// pelletCount 刷新小夫子头像上的弹丸数（客户端小夫子与阿修罗共用 XIULUO_COUNT 计数，war/config.lua isXiuLuo）。
func (b *battle) pelletCount(u *unit) {
	b.w.u8(CmdXiuluoCount)
	b.w.u8(u.Pos)
	b.w.u8(min(max(u.pellets, 0), 255))
}

// enter 是单位从后阵补位进入前阵时的「入阵」神通。开场就在前阵的不算入阵：
// 官方战报（session-01/02）里入阵神通 636 次全在 MOVE 之后，第一个 TURN 之前一次也没有。
func (b *battle) enter(u *unit) {
	if !u.alive() {
		return
	}
	u.fpCut = 0 // 返璞归真：回到前阵复原（客户端入阵时计数也重置为 10）
	b.reshowSwords(u)
	defer func() {
		if u.alive() && hasAny(u, enterFamilies) {
			b.firstSkill(u, skEnter)
		}
	}()
	for _, c := range append(b.foes(u), b.allies(u)...) {
		// 噬心龙咒：敌我任意护法入阵时，位于前阵的龙夺取其 a% 的攻击和气血（FAQ：敌我全体，自身在前排）
		if k := c.tal(famShiXinLongZhou); k != nil && c != u && u.alive() {
			b.show(c, k.id, u)
			d := u.att * int64(k.param(0)) / 100
			b.changeAtt(u, k.id, -d)
			b.changeAtt(c, k.id, d)
			// 夺取的气血累计（用户确认）：生命上限同样增加，再回复同量
			if got := b.hurt(c, u, k.id, u.hp*int64(k.param(0))/100, false); got > 0 {
				b.changeMaxHP(c, k.id, got)
				b.heal(c, k.id, got)
			}
		}
		if k := c.tal(famHuoPaoZhiShu); k != nil && u.alive() && c.side != u.side {
			for range 1 + k.tj { // 敌方护法入阵后对其发射一颗弹丸；激发额外 1–5 颗
				b.cannon(c, k, u)
			}
		}
	}
	if !u.alive() {
		return
	}
	if k := u.tal(famShiXinLongZhou); k != nil && u.peakAtt > u.att {
		b.show(u, k.id, u) // 入阵：攻击力恢复至本场最高值
		b.changeAtt(u, k.id, u.peakAtt-u.att)
	}
	b.initPellets(u)
	if k := u.tal(famYuanHu); k != nil {
		// 对敌方三体造成 b% 攻击力的水属性伤害，回复我方全体各自上限 c% 的生命
		if ts := b.pickN(b.foes(u), 3); len(ts) > 0 {
			b.show(u, k.id, ts...)
			for _, x := range ts {
				b.propertyHurt(u, x, k.id, u.att*int64(k.param(1))/100, 2)
			}
		}
		if allies := b.allies(u); len(allies) > 0 && u.alive() { // 官方战报：援护只有指向敌方的一次 ACT_DO，回血直接 ADD_HP
			for _, x := range allies {
				b.heal(x, k.id, x.maxHP*int64(k.param(2))/100+int64(float64(x.maxHP)*k.tjX()/100))
			}
		}
	}
	if k := u.tal(famLongYue); k != nil {
		// 对敌方全体造成 a% 攻击的水属性伤害，各 b% 概率混乱
		if foes := b.foes(u); len(foes) > 0 {
			b.show(u, k.id, foes...)
			for _, x := range foes {
				n := u.att * int64(k.param(0)) / 100
				if x.mshield > 0 {
					n = k.tjBoost(n)
				}
				if x.alive() && b.chance(k.param(1)) {
					b.addStatus(x, stConfuse, 0, 1, true, 5051, slotDebuff) // 负面状态先于伤害结算（用户规则）
				}
				b.propertyHurt(u, x, k.id, n, 2)
			}
		}
	}
	if k := u.tal(famPoChuJinGu); k != nil {
		// 我方气血最低的三个护法恢复其上限 a% 的气血，b% 概率移除其所有负面状态
		allies := b.allies(u)
		for i := range allies {
			for j := i + 1; j < len(allies); j++ {
				if allies[j].hp < allies[i].hp {
					allies[i], allies[j] = allies[j], allies[i]
				}
			}
		}
		if ts := allies[:min(3, len(allies))]; len(ts) > 0 {
			b.show(u, k.id, ts...)
			for _, x := range ts {
				b.heal(x, k.id, x.maxHP*int64(k.param(0))/100)
				if b.chance(k.param(1)) {
					for b.clearOneDebuff(x) {
					}
				}
				if k.tjOn() && b.chance(k.tjPct()) {
					b.setStatus(x, stImmune, 0, 1, false, 5321, slotBuff)
					b.zhanhu(x, 5265)
				}
			}
		}
	}
	if k := u.tal(famZiQingJian); k != nil && u.jianyi > 0 {
		// 消耗所有剑意，每层提升 a% 初始攻击，每 2 层额外增加 1 把仙剑
		n := u.jianyi
		u.jianyi = 0
		b.show(u, k.id, u)
		b.changeAtt(u, k.id, u.baseAtt*int64(k.param(0)*n)/100)
		for range n / 2 {
			b.giveSword(u)
		}
	}
	if k := u.tal(famTianGangJianQi); k != nil {
		// 消耗生命最高的仙族护法 a% 上限生命，使随机敌人受到两倍该值的伤害
		var god *unit
		for _, x := range b.allies(u) {
			if x.Race == raceGod && (god == nil || x.hp > god.hp) {
				god = x
			}
		}
		if foes := b.foes(u); god != nil && len(foes) > 0 {
			// 官方战报：仙族护法失去其上限 32%（不封顶，可致死），敌人受到两倍
			o := foes[b.rng.IntN(len(foes))]
			cost := god.maxHP * int64(k.param(0)) / 100
			b.show(u, k.id, o)
			b.hurt(u, o, k.id, cost*2, false)
			if k.tjOn() {
				var rest []*unit
				for _, x := range b.foes(u) {
					if x != o {
						rest = append(rest, x)
					}
				}
				if len(rest) > 0 {
					x := rest[b.rng.IntN(len(rest))]
					b.show(u, k.id, x)
					b.hurt(u, x, k.id, int64(float64(cost*2)*k.tjX()/100), false)
				}
			}
			b.hurt(god, god, k.id, cost, false)
		}
	}
	if k := u.tal(famXiuLin); k != nil {
		ts := b.pickN(b.allies(u), 3)
		b.show(u, k.id, ts...)
		for _, x := range ts {
			b.clearOneDebuff(x)
		}
	}
	if k := u.tal(famKaoLao); k != nil {
		ts := b.pickN(b.allies(u), 3)
		b.show(u, k.id, ts...)
		for _, x := range ts {
			if x.has(stTempMax) == nil {
				grow := x.maxHP * 15 / 100
				b.changeMaxHP(x, k.id, grow)
				b.heal(x, k.id, grow)
				b.setStatus(x, stTempMax, grow, 1, false, k.Buff, slotBuff)
			}
		}
	}
	for _, fam := range []family{famKuangLei, famXunDian, famLangYong} {
		k := u.tal(fam)
		if k == nil {
			continue
		}
		ts := b.pickN(b.foes(u), 3)
		if len(ts) == 0 {
			continue
		}
		b.show(u, k.id, ts...)
		kind := 4 // 雷
		if fam == famLangYong {
			kind = 2 // 水
		}
		for _, x := range ts {
			n := u.att * int64(k.param(0)) / 100
			if fam != famLangYong && k.tjOn() && b.chance(k.tjPct()) {
				n *= 2
			}
			if fam != famLangYong && x.alive() && b.chance(k.BuffRate) {
				b.addStatus(x, stSilence, 0, 1, true, k.Buff, slotDebuff) // 负面状态先于伤害结算（用户规则）
			}
			b.propertyHurt(u, x, fxMagic(k), n, kind)
			if fam == famLangYong && k.tjOn() && b.chance(k.tjPct()) {
				b.restoreInitial(x, k.id)
			}
		}
	}
	if k := u.tal(famQianKunXiu); k != nil && b.chance(k.param(0)) {
		// 捆缚敌方攻击最高的两名，另有 b% 概率使敌方攻击最低的两名本回合隐身。
		// 官方战报：ACT_DO(乾坤袖 → 敌方全体前阵) → BUFF 5231（捆缚，被不侵抵抗的不挂）。以前捆缚不带 buff 号，
		// 客户端没有 BUFF 命令就不播捆缚特效。
		foes := b.foes(u)
		sortByAtt(foes)
		if len(foes) > 0 {
			b.show(u, k.id, foes...)
			for _, x := range foes[:min(2, len(foes))] {
				b.addStatus(x, stBind, 0, 1, true, qkxBindBuff, slotDebuff)
			}
			if low := foes[max(0, len(foes)-2):]; len(foes) > 2 && b.chance(k.param(1)) {
				for _, x := range low {
					b.setStatus(x, stHidden, 0, 1, false, hiddenBuff, slotBuff)
				}
			}
		}
		if k.tjOn() && b.chance(k.tjPct()) {
			b.changeAtt(u, k.id, u.att*60/100)
		}
	}
	if k := u.tal(famShenWei); k != nil {
		if b.chance(k.param(0)) {
			for _, o := range b.foes(u) {
				if o.slot() != u.slot() {
					continue
				}
				if x := b.quzhuPick(o); x != nil { // 驱逐对位（规则同驱逐：后阵已阵亡的也可能被换上来）
					b.show(u, k.id, o)
					b.quzhu(o, x)
				}
				break
			}
		}
		if ts := b.pickN(b.foes(u), 3); len(ts) > 0 {
			b.show(u, k.id, ts...)
			for _, x := range ts {
				b.propertyHurt(u, x, k.id, u.att*int64(k.param(1))/100, 2)
			}
		}
		if k.tjOn() && b.chance(k.tjPct()) {
			b.setStatus(u, stImmune, 0, 1, false, 5321, slotBuff)
		}
	}
	if k := u.tal(famPanTaoYan); k != nil {
		allies := b.allies(u)
		b.show(u, k.id, allies...)
		for _, x := range allies {
			grow := x.maxHP * int64(k.param(0)) / 100
			b.changeMaxHP(x, k.id, grow)
			b.heal(x, k.id, grow)
		}
		if k.tjOn() && b.chance(k.tjPct()) {
			for _, x := range allies {
				x.spd++
			}
		}
	}
	if k := u.tal(famZhenYan); k != nil && u.slot() == 3 {
		b.show(u, k.id, u)
		b.changeAtt(u, k.id, u.att*int64(k.param(0))/100)
		grow := u.maxHP * int64(k.param(0)) / 100
		b.changeMaxHP(u, k.id, grow)
		b.heal(u, k.id, grow)
	}
	if k := u.tal(famGanLu); k != nil {
		b.ganlu(u, k)
		if k.tjOn() && b.chance(k.tjPct()) { // 激发：额外复活一个
			b.ganlu(u, k)
		}
	}
	if k := u.tal(famDaoZhiShi); k != nil {
		// 入阵：我方全体恢复 a% 上限的生命。
		allies := b.allies(u)
		b.show(u, k.id, allies...)
		for _, x := range allies {
			n := x.maxHP * int64(k.param(0)) / 100
			b.heal(x, fxMagic(k), n)
			if k.tjOn() {
				// 激发：我方全体额外获得治疗量 $x% 的法术结界——按治疗量算，满血（实际没回血）的也有结界，保证全体前排都加上
				b.addShield(x, int64(float64(n)*k.tjX()/100), true)
			}
		}
	}
	if k := u.tal(famGuangYinSui); k != nil {
		// 入阵：战意 +a；自身及相邻护法 b% 概率获得无敌，到当前回合结束。
		// 官方战报：ACT_DO 只指向自己（入阵动画），ZHANYI，再一条 BUFF 5321 列出获得无敌的单位。
		b.show(u, k.id, u)
		b.addZY(u.side, k.param(0))
		for _, x := range append([]*unit{u}, b.adjacent(u)...) {
			if b.chance(k.param(1)) {
				b.setStatus(x, stImmune, 0, 1, false, 5321, slotBuff)
			}
		}
		if k.tjOn() {
			for _, x := range b.units[u.side] {
				if !x.alive() && !x.gone && x.front() && abs(x.slot()-u.slot()) == 1 && x.has(stNoSoul) == nil &&
					b.chance(k.tjPct()) {
					b.show(u, k.id, x)
					b.relive(x, k.id, x.maxHP)
					x.doomIn = 2
					b.setStatus(x, stNoSoul, 0, maxRounds, false, 0, 0)
					b.huoshi(x, 5402) // 活尸2
				}
			}
		}
	}
	if k := u.tal(famShuiZhiYuan); k != nil {
		// 入阵：敌方全体 a% 攻击的水属性伤害，b% 概率僵化（下次行动后清除）。
		if foes := b.foes(u); len(foes) > 0 {
			b.show(u, k.id, foes...)
			for _, x := range foes {
				if x.alive() && b.chance(k.param(1)) &&
					b.addStatus(x, stStiff, 0, 1, true, int32(k.param(2)), slotDebuff) && k.tjOn() && b.chance(int(k.tjY())) {
					b.restoreInitial(x, k.id) // 负面状态先于伤害结算（用户规则）：僵化（及激发的恢复初始值）先于伤害
				}
				b.propertyHurt(u, x, fxMagic(k), k.tjBoost(u.att*int64(k.param(0))/100), 2)
			}
		}
	}
	if k := u.tal(famZhuXianZhen); k != nil {
		if k.id >= 120981 && k.id <= 120984 {
			// 12 系 desc 是「提升所有本方护法 a% 攻击力上限」，引擎内无攻击上限概念，
			// 近似为入阵时本方全体永久 +a%（以 baseAtt 为基数，如狂热）
			allies := b.allies(u)
			b.show(u, k.id, allies...)
			for _, x := range allies {
				b.changeAtt(x, k.id, x.baseAtt*int64(k.param(0))/100)
			}
		} else if ts := b.pickN(b.allies(u), k.param(0)); len(ts) > 0 {
			// 携 a 柄仙剑分给本方护法（含自己），每人至多 1 柄；沿用布剑的剑与剑伤机制
			b.show(u, k.id, ts...)
			for _, x := range ts {
				b.giveSword(x)
			}
		}
		if k.tjOn() {
			// 激发：对位敌人按本方持剑数受伤——动画只播一次，伤害逐个结算
			var hit []*unit
			var dmg []int64
			for _, x := range b.allies(u) {
				if x.swords == 0 {
					continue
				}
				for _, o := range b.foes(u) {
					if o.slot() == x.slot() {
						hit = append(hit, o)
						dmg = append(dmg, int64(float64(o.maxHP)*k.tjX()/100)*int64(x.swords))
					}
				}
			}
			if len(hit) > 0 {
				b.show(u, k.id, hit...)
				for i, o := range hit {
					b.loseHP(u, o, k.id, dmg[i]) // 诛仙阵激发：对位护法流失上限 $x%×飞剑数 的生命（生命流失）
				}
			}
		}
	}
}

// ganlu：甘露——随机复活一名已死亡的友方（生命为上限 a%），目标携带锢魂。
func (b *battle) ganlu(u *unit, k *talent) {
	// 随机复活本方已阵亡的护法：前阵或已被补位换到后阵的都算（官方战报 lundao:71：甘露 RELIVE 后阵 6、7 号位）
	if dead := b.deadAllies(u.side); len(dead) > 0 {
		x := dead[b.rng.IntN(len(dead))]
		b.show(u, k.id, x)
		b.relive(x, k.id, x.maxHP*int64(k.param(0))/100)
		b.setStatus(x, stNoSoul, 0, maxRounds, false, 5141, slotDebuff) // 官方战报：甘露复活后 BUFF 5141 锢魂
	}
}

// restoreInitial 把 x 的攻击、生命上限恢复到进入战斗的初始值（浪涌、水之元激发）。
func (b *battle) restoreInitial(x *unit, id int32) {
	if !x.alive() {
		return
	}
	if d := x.baseAtt - x.att; d != 0 {
		b.changeAtt(x, id, d)
	}
	if d := int64(x.MaxHP) - x.maxHP; d != 0 {
		b.changeMaxHP(x, id, d)
	}
}
