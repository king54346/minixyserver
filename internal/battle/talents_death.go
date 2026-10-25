package battle

import "turnrpg/internal/gamedata"

// 死亡触发的神通、「x 回合后复活」倒计时。

func init() {
	register(
		famShenYou, famHunShou, famYiXiang, famWangYuZhou, famLuoWang, famJieLiZhou, famGuMao, famTongGuiJue, famYiMing, famKuangNuYan, famQunYaoJue, famGuiYuan, famHuanHun, famBuMieQu, famGuiXuZhen,
		famBaJiaoShan, famShengSiBie, famQingShang, famZhuFuXiaoSan, famJingWeiTianHai, famBuMieFaShen, famLieYanTunShi, famHuoZhiHun,
	)
}

// reviveBuff：自身 rate% 概率于 2 回合后复活，血量由返死 buff（5551-5560 → 8%×等级）给出。
func (b *battle) reviveBuff(t *unit, rate int, buff int) {
	if t.has(stNoSoul) != nil || !b.chance(rate) {
		return
	}
	t.reviveIn = reviveDelay
	t.revivePct = reviveHPPct
	if n := buff - 5550; n >= 1 && n <= 10 {
		t.revivePct = 8 * n
	}
}

const (
	baiguMagic = 12505 // 疯魔乱棍（情殇化身的白骨魔在奇数回合释放 5 级）
	baiguShape = 54005 // 白骨魔的形象（官方战报 CHG_FIGHTER）
	baiguSpeed = 12
)

// reviveLater：死亡神通参数末位是「自身 x% 概率于 2 回合后复活」，参数里的返死 N（BUFF 5551–5560）
// 决定复活后的生命（上限 8N%，如狂怒焰4：返死4 → 32%）。
func (b *battle) reviveLater(t *unit, k *talent) {
	if len(k.Param) < 3 {
		return
	}
	buff := 0
	for _, p := range k.Param[:len(k.Param)-1] {
		if p >= 5551 && p <= 5560 {
			buff = int(p)
		}
	}
	b.reviveBuff(t, int(k.Param[len(k.Param)-1]), buff)
}

// deathCurses 是「死亡：x% 概率对敌方三体……；自身有 y% 概率于 2 回合后复活」一族：
// tuxiHitBuff / zhasiBuff：突袭、诈死触发时挂的标记（官方战报 BUFF 5461「命中buff」、5411「诈死」）。
const (
	tuxiHitBuff = 5461
	zhasiBuff   = 5411
)

// 参数 [概率, 返死 buff, 复活概率]（竭力咒多一个参数在前）。
var deathCurses = []family{famYiXiang, famWangYuZhou, famLuoWang, famJieLiZhou, famGuMao}

const reviveDelay, reviveHPPct = 2, 8 // buff 5551 返死1：2 回合后复活，血量为上限的 8%

// died 在 t 阵亡（已输出 DIE）后调用。
func (b *battle) died(t *unit) {
	for _, f := range t.borrowed { // 摄魂术借来的神通死亡时移除
		delete(t.tals, f)
	}
	t.borrowed = nil
	t.swords = 0                   // 仙剑：死亡则清除（图鉴 FAQ）
	stiff := t.has(stStiff) != nil // 僵化（踏浪、蛟魔王、水之元…）中阵亡：死亡神通（神佑等）也发动不了（用户规则）
	b.leaveClear(t, true)
	if b.over {
		return
	}
	if t.reviveIn == 0 {
		defer b.markRevive(t)
	}
	if stiff {
		b.devour(t) // 对方的烈焰吞噬照常结算
		return
	}
	if hasAny(t, deathFamilies) {
		b.firstSkill(t, skDeath)
		b.deathSkillEvent(t)
	}
	if k := t.tal(famBuMieFaShen); k != nil && t.has(stNoSoul) == nil && (t.fashenAt == 0 || b.round-t.fashenAt >= k.param(3)) {
		// （蓄力数×a% + 剩余回合数×b%）概率满血复活，生命上限提升 c%，冷却 d 回合
		if b.chance(t.charge*k.param(0) + (maxRounds-b.round)*k.param(1)) {
			t.fashenAt = b.round
			if k.tjOn() && b.chance(k.tjPct()) {
				t.fashenAt = 0 // 激发：不附加冷却
			}
			b.show(t, k.id, t)
			if d := t.baseAtt - t.att; d != 0 { // 攻击回到初始值（复活后攻击、生命、buff 都是初始状态）
				b.changeAtt(t, k.id, d)
			}
			// 生命上限回到初始值再加初始 c%；激发 $y% 概率保留死亡时的生命上限（官方战报：恒 7029 = 14058×50%）
			if !(k.tjOn() && b.chance(int(k.tjY()))) {
				b.changeMaxHP(t, k.id, int64(t.MaxHP)-t.maxHP)
			}
			b.changeMaxHP(t, k.id, int64(t.MaxHP)*int64(k.param(2))/100)
			b.relive(t, k.id, t.maxHP)
			return
		}
	}
	if k := t.tal(famQingShang); k != nil && t.has(stNoSoul) == nil && !t.baigu {
		// 吸收所有场上护法当前生命的 a%，化身具有不侵9、守卫9、坚韧9且奇数回合释放疯魔乱棍5 的白骨魔。
		// 官方战报：ACT_DO(情殇 → 敌方前阵) → 逐个 SUB_HP + CHG_MAXHP（双方前阵）→ CHG_FIGHTER(白骨魔 54005、
		// 魔族、先攻 12)，没有 RELIVE——客户端靠 CHG_FIGHTER 换成白骨魔的形象。
		var all []*unit
		for side := range b.units {
			all = append(all, b.frontOf(side)...)
		}
		if len(all) > 0 {
			if foes := b.foes(t); len(foes) > 0 {
				b.show(t, k.id, foes...)
			} else {
				b.show(t, k.id, t)
			}
			var sum int64
			for _, x := range all {
				// 单个护法单次吸收不超过白晶晶死亡时生命上限 100%；激发额外吸收不超过其当前上限 50%（FAQ）
				sum += b.hurt(t, x, k.id, min(x.hp*int64(k.param(0))/100, t.maxHP), false)
				if d := int64(float64(x.maxHP) * k.tjX() / 100); k.tjOn() && x.alive() && d > 0 {
					d = min(d, x.maxHP/2, x.maxHP-1)
					b.changeMaxHP(x, k.id, -d)
					sum += d
				}
			}
			if sum > 0 {
				b.transform(t, baiguShape, raceGhost, baiguSpeed, sum)
				b.deathSkillEvent(t)
				t.baigu = true
				t.tals = map[family]*talent{}
				for _, id := range []int32{20039, 20759, 20439} { // 不侵9、守卫9、坚韧9
					b.grant(t, id)
				}
				b.huoshi(t, 5401) // 活尸（用户要求：白骨魔同不灭躯显示活尸状态；与锢魂相似，不能再被复活）
				b.setStatus(t, stNoSoul, 0, maxRounds, false, 0, 0)
				t.magics = nil
				if m := gamedata.Magics[baiguMagic]; m != nil {
					t.magics = []*castable{{id: baiguMagic, lv: 5, left: maxRounds, MagicInfo: m}}
				}
				return
			}
		}
	}
	// 当场复活的死亡技（不灭法身、神佑）是己方的，优先于对方的烈焰吞噬；不灭躯是「死亡后再复活」，
	// 可以被吞噬：先结算烈焰吞噬，被吞了（尸体带锢魂）就不再发动不灭躯。
	// 之后才结算烈焰吞噬（官方战报：DIE → 烈焰吞噬 → 同归诀等死亡技照常发动），被吞的尸体带锢魂，
	// 还魂 / 返死等延时复活都不再生效。
	if k := t.tal(famShenYou); k != nil && t.has(stNoSoul) == nil && (t.shenyouAt == 0 || b.round-t.shenyouAt >= 2) &&
		b.chance(k.param(0)) {
		t.shenyouAt = b.round // 两次神佑的回合差 ≥2（FAQ）
		b.show(t, k.id, t)
		b.resetInitial(t, k.id) // 原地满血复活，攻击 / 生命上限都回到初始值
		b.relive(t, k.id, t.maxHP)
		return
	}
	b.devour(t)
	if k := t.tal(famBuMieQu); k != nil && !t.eaten {
		// 死亡满血复活，攻击提升 a%，n 回合后自行死亡，无法被再次复活。
		// 官方战报：ACT_DO → ATT_CHANGE(攻击回到初始) → RELIVE → ATT_CHANGE(+a% 初始攻击) → BUFF param[2]「活尸」(255)。
		if t.has(stNoSoul) == nil {
			b.show(t, k.id, t)
			// 攻击回到初始值；激发：再次发动不灭躯时 $x% 概率保留大圣死亡时的攻击力
			keep := t.bumiequ > 0 && k.tjOn() && b.chance(k.tjPct())
			t.bumiequ++
			if d := t.att - t.baseAtt; d != 0 && !keep {
				b.changeAtt(t, k.id, -d)
			}
			b.relive(t, k.id, t.maxHP)
			b.changeAtt(t, k.id, t.baseAtt*int64(k.param(0))/100)
			if buff := int32(k.param(2)); buff != 0 {
				b.huoshi(t, buff)
			}
			if k.tjOn() && b.chance(k.tjPct()) {
				return // 激发：不带锢魂与自动死亡
			}
			b.setStatus(t, stNoSoul, 0, maxRounds, false, 0, 0)
			// 自杀回合：正系（21151-21156）param[1] 是 1-6，对应 2-7 回合；
			// 12 系（121151-121154）param[1] 直接是回合数。doomIn 多加 1，
			// 因为死亡同回合的 roundEnd 就会消耗一次。
			n := k.param(1)
			if k.id < 120000 {
				n++
			}
			t.doomIn = n + 1
		}
	}
	if k := t.tal(famHunShou); k != nil {
		allies := b.allies(t)
		if len(allies) > 0 {
			b.show(t, k.id, t)                                   // 官方战报：阵亡单位演出死亡神通，目标是自己
			shield := k.tjBoost(t.att * int64(k.param(1)) / 100) // $r = b% 攻击
			for _, x := range allies {
				b.heal(x, k.id, k.tjBoost(t.maxHP*int64(k.param(0))/100)) // $o = a% 生命
				b.addShield(x, shield, true)
			}
			for _, o := range b.foes(t) { // 优先由对位护法处吸取：对位失去同等数值的法术结界，至 0 为止（FAQ）
				if o.slot() == t.slot() && o.mshield > 0 {
					cut := min(o.mshield, shield)
					o.mshield -= cut
					b.w.u8(CmdMagicShield)
					b.w.u8(2)
					b.w.u8(1)
					b.w.u8(o.Pos)
					b.w.u32(cut)
				}
			}
		}
	}
	if k := t.tal(famHuoZhiHun); k != nil {
		// 死亡：释放法术对敌方全体造成 a% 攻击的火属性伤害。官方战报：ACT_DO 指向敌方全体，SUB_HP 用神通号。
		if foes := b.foes(t); len(foes) > 0 {
			b.show(t, k.id, foes...)
			for _, x := range foes {
				b.propertyHurt(t, x, k.id, t.att*int64(k.param(0))/100, 3)
			}
		}
		b.reviveLater(t, k)
	}
	if k := t.tal(famTongGuiJue); k != nil {
		for _, o := range b.foes(t) {
			if o.slot() == t.slot() {
				b.show(t, k.id, o)                                                                                      // 官方战报：同归诀的 ACT_DO 指向对位的敌人
				b.propertyHurt(t, o, k.id, int64(t.MaxHP)*int64(k.param(1))/100+int64(float64(t.maxHP)*k.tjX()/100), 1) // 初始上限 b% 的土属性伤害
			}
		}
		b.reviveLater(t, k)
	}
	if k := t.tal(famYiMing); k != nil {
		var dead []*unit
		for _, x := range b.units[t.side] {
			if x != t && !x.alive() && x.front() && x.has(stNoSoul) == nil {
				dead = append(dead, x)
			}
		}
		if len(dead) > 0 {
			x := dead[b.rng.IntN(len(dead))]
			b.show(t, k.id, t)
			b.relive(x, k.id, x.maxHP*int64(k.param(0))/100)
			b.setStatus(x, stNoSoul, 0, maxRounds, false, 5141, slotDebuff) // 官方战报：易命复活后 BUFF 5141 锢魂
		}
		if k.tjOn() {
			b.hitOpposite(t, k, int64(float64(t.maxHP)*k.tjX()/100))
		}
		b.reviveLater(t, k)
	}
	if k := t.tal(famKuangNuYan); k != nil {
		if foes := b.foes(t); len(foes) > 0 {
			// 官方战报：ACT_DO(死者, 狂怒焰, 敌方全体)，SUB_HP 用特效法术 15159，燃烧 buff 是该级的 BuffID（燃烧N：上限 2N%）。
			b.show(t, k.id, foes...)
			pct := 2
			if bi := gamedata.Buffs[k.Buff]; bi != nil && bi.Value() > 0 {
				pct = bi.Value()
			}
			for _, x := range foes {
				if x.alive() && !b.resist(x, stBurn) { // 燃烧（负面状态先于伤害结算（用户规则））：回合结束时损失上限 pct%，持续到战斗结束（可被法术结界 / 不侵 / 返火免疫）
					b.addPoison(x, poison{from: k.id, pct: pct, left: maxRounds, buff: k.Buff, burn: true})
					if k.tjOn() {
						// 激发：每次释放都额外叠加一层燃烧（第一次 2 层，再释放 3 层……）——燃烧一般高值覆盖 / 刷新回合，
						// 唯独狂怒焰激发的这层可以堆叠，各占一个图标
						b.addBurnStack(x, poison{from: k.id, pct: pct, left: maxRounds, buff: k.Buff})
					}
				}
				b.propertyHurt(t, x, fxMagic(k), t.att*int64(k.param(0))/100, 3)
			}
		}
		b.reviveLater(t, k)
	}
	for _, fam := range deathCurses {
		k := t.tal(fam)
		if k == nil {
			continue
		}
		p := k.Param
		reviveRate := int(p[len(p)-1])
		// 竭力咒没有触发概率（官方战报 5/5 次都生效）
		if fam == famJieLiZhou || b.chance(k.param(0)) {
			ts := b.pickN(b.foes(t), 3)
			if fam == famJieLiZhou {
				ts = b.foes(t) // 所有敌方
			}
			if len(ts) > 0 {
				b.show(t, k.id, ts...) // 官方战报：亡语咒 / 罗网 / 骨矛 / 遗香 / 竭力咒的 ACT_DO 指向中招的敌方
			}
			for _, x := range ts {
				switch {
				case !k.tjOn():
				case fam == famYiXiang, fam == famJieLiZhou:
					b.propertyHurt(t, x, k.id, int64(float64(t.att)*k.tjX()/100), 3)
				case fam == famLuoWang:
					b.propertyHurt(t, x, k.id, int64(float64(t.att)*k.tjX()/100), 1)
				}
				if !x.alive() {
					continue
				}
				switch fam {
				case famYiXiang:
					b.addStatus(x, stConfuse, 0, 1, true, k.Buff, slotDebuff)
				case famLuoWang:
					b.addStatus(x, stBind, 0, 1, true, k.Buff, slotDebuff)
				case famWangYuZhou:
					if b.addStatus(x, stNoHeal, 60, noHealRounds, false, 5086, slotDebuff) && k.tjOn() && // 禁疗6（表里 buffid 5090 是禁疗10）
						x.hp*100 < x.maxHP*25 && b.chance(k.tjPct()) {
						b.hurt(t, x, k.id, x.hp, false) // 激发：濒死的禁疗目标直接死亡
					}
				case famJieLiZhou: // 降低 $q = b% × 自身生命上限（官方战报 5/5）
					b.changeAtt(x, k.id, -t.maxHP*int64(k.param(1))/100)
					// 再挂「竭力」状态（BUFF 5481–5484，持续到战斗结束）：攻击力再降 5%，不侵可抵抗。
					// 官方战报：ATT_CHANGE(竭力咒) → [不侵] → BUFF(状态号, 13) → ATT_CHANGE(状态号, −5%)。
					// 竭力N：攻击力降低 5N%，持续到战斗结束，阵亡 / 进入后阵清除（FAQ），清除时按 1/(1-X%) 回升。
					if pct := int64(5 * min(max(int(k.Buff)-5480, 1), 10)); !b.resist(x, stJieli) {
						b.setStatus(x, stJieli, pct, maxRounds, false, k.Buff, slotDebuff)
						x.status[stJieli].foe = true
						b.changeAtt(x, k.Buff, -x.att*pct/100)
					}
				case famGuMao:
					b.propertyHurt(t, x, fxMagic(k), k.tjBoost(t.att*int64(k.param(0))/100), 1)
					if k.tjOn() && !x.alive() && b.chance(int(k.tjY())) {
						b.buff(x, 5141, maxRounds) // 激发：目标死亡 $y% 锢魂
						x.gone, x.eaten = true, true
					}
				}
			}
		}
		if t.has(stNoSoul) == nil && b.chance(reviveRate) {
			t.reviveIn = reviveDelay
		}
	}
	if k := t.tal(famQunYaoJue); k != nil {
		if b.chance(k.param(0)) {
			ts := b.pickN(b.foes(t), 3)
			if len(ts) > 0 {
				b.show(t, k.id, ts...) // 官方战报：群夭诀的 ACT_DO 指向中招的敌方
			}
			for _, x := range ts { // 早夭：可分担量 = 自身上限 40%，到当前回合结束（负面状态先于伤害结算（用户规则））
				b.addStatus(x, stZaoyao, x.maxHP*40/100, 1, false, k.Buff, slotDebuff)
				if k.tjOn() {
					b.propertyHurt(t, x, k.id, int64(float64(t.att)*k.tjX()/100), 1)
				}
			}
		}
		if t.has(stNoSoul) == nil && b.chance(k.param(2)) {
			t.reviveIn = reviveDelay
			t.revivePct = reviveHPPct
			if n := k.param(1) - 5550; n >= 1 && n <= 4 { // 复活血量 8/16/24/32% 由 param[1]（5551-5554）隐含
				t.revivePct = 8 * n
			}
		}
	}
	if k := t.tal(famHuanHun); k != nil {
		// 死亡 a% 概率于 2 回合后复活，血量为上限的 b%（param[1] 直接是百分比）。
		if t.has(stNoSoul) == nil && b.chance(k.param(0)) {
			b.show(t, k.id, t)
			t.reviveIn = reviveDelay
			t.revivePct = k.param(1)
			t.reviveMark = k.Buff // 还魂N（5150+N）
			if k.tjOn() {
				b.hitOpposite(t, k, int64(float64(t.maxHP)*k.tjX()/100))
			}
		}
	}
	if k := t.tal(famGuiXuZhen); k != nil {
		// 死亡：敌方全体 a% 攻击的土属性伤害，每人 BuffRate 概率捆缚 1 回合；
		// 自身 param[2] 概率于 2 回合后复活 8/16/24/32% 上限血。
		if foes := b.foes(t); len(foes) > 0 {
			b.show(t, k.id, foes...)
			for _, x := range foes {
				if x.alive() && b.chance(k.BuffRate) {
					b.addStatus(x, stBind, 0, 1, true, k.Buff, slotDebuff) // 负面状态先于伤害结算（用户规则）
				}
				b.propertyHurt(t, x, fxMagic(k), t.att*int64(k.param(0))/100+int64(float64(t.att)*k.tjX()/100), 1)
			}
		}
		if t.has(stNoSoul) == nil && b.chance(k.param(2)) {
			t.reviveIn = reviveDelay
			if n := k.param(1) - 5550; n >= 1 && n <= 4 {
				t.revivePct = 8 * n
			}
		}
	}
	if k := t.tal(famGuiYuan); k != nil {
		// 归元：死亡化身为火之卵（攻击 / 生命为朱雀的 a% / b%），火之卵每次行动、以及自身死亡时必放火系群法，不能普攻。
		// 官方战报：朱雀死亡 → ACT_DO(归元 → 自己) → CHG_FIGHTER(火之卵 54004、兽族、先攻 10、满血) → BUFF 5141（锢魂，
		// 火之卵不能再被复活）；这时不放火法。火之卵行动时 ACT_DO(17022 火之魂 → 敌方全体) + SUB_HP(17022)；
		// 火之卵自带死亡技火之魂（21471）：死亡时 ACT_DO(21471 → 敌方全体) + SUB_HP(21471)，见上面 famHuoZhiHun。
		if !t.egg && t.has(stNoSoul) == nil {
			t.egg = true
			b.show(t, k.id, t)
			hp := int64(t.MaxHP)*int64(k.param(1))/100 + int64(float64(t.MaxHP)*k.tjX()/100)
			att := t.baseAtt*int64(k.param(0))/100 + int64(float64(t.baseAtt)*k.tjX()/100)
			if k.tjOn() {
				b.grant(t, levelID(famJinHua, int(k.tjY())))
			}
			b.grant(t, eggDeathTalent)
			b.grant(t, levelID(famFengMo, 10)) // 火之卵自带疯魔10（FAQ 归元）
			t.att, t.baseAtt = att, att
			b.transform(t, eggShape, raceBeast, eggSpeed, hp)
			b.setStatus(t, stNoSoul, 0, maxRounds, false, 5141, slotDebuff)
		}
	}
	b.deathMore(t)
}

// deathMore 是 T5 补的死亡族（died 末尾调用）。
func (b *battle) deathMore(t *unit) {
	if k := t.tal(famBaJiaoShan); k != nil {
		// 扣除敌方每个护法的物理 / 法术结界最多各 a%×其生命上限 点；处于燃烧中则立即造成已有燃烧 b 次结算的
		// 伤害：一次结算 = 上限 × 合计燃烧 %（至多燃烧10），单次不超过芭蕉扇护法自身攻击力（FAQ），燃烧仍持续
		if foes := b.foes(t); len(foes) > 0 {
			b.show(t, k.id, foes...)
			for _, x := range foes {
				most := x.maxHP * int64(k.param(0)) / 100
				b.cutShield(x, most, false)
				b.cutShield(x, most, true)
				// 已有燃烧的一次结算 = 合计燃烧（至多燃烧10），单次不超过芭蕉扇护法自身攻击
				n := min(x.maxHP*int64(x.burnPct())/100, t.att)
				if n > 0 && len(k.Param) > 1 {
					b.hurt(t, x, k.id, int64(float64(n)*k.Param[1]), false)
				}
				// 激发：结算伤害后 $x% 概率[驱逐]敌方每个单位（逐一结算，规则同驱逐，FAQ）
				if k.tjOn() && x.alive() && x.front() && b.chance(k.tjPct()) {
					if y := b.quzhuPick(x); y != nil {
						b.quzhu(x, y)
					}
				}
			}
		}
		b.reviveBuff(t, k.param(3), k.param(2))
	}
	if k := t.tal(famShengSiBie); k != nil {
		// 对位三体流失禺狨王 a% 上限的生命，血量低于禺狨王上限 b% 的跳过一次行动（捆缚）
		// 对位三体 = 对位及其左右两槽（锁定站位：对位已死 / 为空也只打这几个槽位，用户规则）
		if ts := b.facing3(t); len(ts) > 0 {
			b.show(t, k.id, ts...)
			for _, x := range ts {
				b.loseHP(t, x, k.id, k.tjBoost(t.maxHP*int64(k.param(0))/100))
				if x.alive() && x.hp*100 < t.maxHP*int64(k.param(1)) {
					b.addStatus(x, stBind, 0, 1, true, 5231, slotDebuff)
				}
			}
		}
		b.reviveBuff(t, k.param(3), k.param(2))
	}
	if k := t.tal(famZhuFuXiaoSan); k != nil {
		// 造成 攻击×a% + 生命上限 的物理伤害，由敌方所有前阵存活护法平摊
		if foes := b.foes(t); len(foes) > 0 {
			share := (t.att*int64(k.param(0))/100 + t.maxHP) / int64(len(foes))
			b.show(t, k.id, foes...)
			top := foes[0]
			for _, x := range foes {
				if x.att > top.att {
					top = x
				}
			}
			for _, x := range foes {
				n := share
				if k.tjOn() {
					b.cutShield(x, int64(float64(share)*k.tjX()/100), true)
					if x == top {
						n = k.tjBoost2(n)
					}
				}
				b.hurt(t, x, k.id, n, true)
			}
		}
		b.reviveBuff(t, k.param(1), k.param(3))
	}
	if k := t.tal(famJingWeiTianHai); k != nil {
		// 给己方攻击力最高的两个护法附加遗志（buff 5645-5648：攻击 +15/20/25/30%，攻击魔族额外 +10%）
		allies := b.allies(t)
		sortByAtt(allies)
		var ts []*unit
		for _, x := range allies {
			if len(ts) < 2 && x.has(stYizhi) == nil {
				ts = append(ts, x)
			}
		}
		if len(ts) > 0 {
			buff := int32(k.param(0))
			b.show(t, k.id, ts...)
			for _, x := range ts {
				b.changeAtt(x, k.id, k.tjBoost(x.att*int64(15+5*(buff-5645))/100))
				b.setStatus(x, stYizhi, 10, maxRounds, false, buff, slotBuff)
			}
		}
		b.reviveBuff(t, k.param(2), k.param(1))
	}
}

// devour：烈焰吞噬——敌方护法阵亡（且不会复活）时吞噬其尸体，获得其 a% 的攻击与生命上限，尸体锢魂。
// 官方战报：ATT_CHANGE = 亡者攻击 40%、CHG_MAXHP / ADD_HP = 亡者上限 40%、BUFF 5141 给亡者（参数 20，
// 另一半是激发「吞噬效果提升」）。触发几率第 1 回合 100%，此后每回合减少 1/3。
func (b *battle) devour(t *unit) {
	if t.alive() || t.eaten {
		return
	}
	for _, c := range b.foes(t) {
		k := c.tal(famLieYanTunShi)
		if k == nil || !b.chance(lieyanChance(b.round)) {
			continue
		}
		t.gone, t.eaten = true, true
		b.show(c, k.id, c)
		b.changeAtt(c, k.id, k.tjBoost(t.att*int64(k.param(0))/100))
		grow := k.tjBoost(t.maxHP * int64(k.param(0)) / 100)
		b.changeMaxHP(c, k.id, grow)
		b.heal(c, k.id, grow)
		b.buff(t, 5141, maxRounds) // 锢魂
		if t.status == nil {
			t.status = map[stKey]*status{}
		}
		t.status[stNoSoul] = &status{left: maxRounds, buff: 5141} // 尸体上的锢魂：死亡技里的复活都会跳过
		return
	}
}

// markRevive：死亡技定下「2 回合后复活」时给尸体挂状态（官方战报：遗香 / 罗网…之后 BUFF 5551-5555「返死N」，
// 还魂之后 BUFF 515N「还魂N」），复活时去掉（relive）。
func (b *battle) markRevive(t *unit) {
	if t.reviveIn == 0 || t.alive() || t.eaten {
		t.reviveMark = 0
		return
	}
	if t.reviveMark == 0 {
		n := max(t.revivePct, reviveHPPct) / 8
		t.reviveMark = int32(5550 + min(max(n, 1), 10))
	}
	t.reviveAt = b.round + reviveDelay
	b.buff(t, t.reviveMark, reviveDelay*2)
}

// huoshi 给 t 挂「活尸」状态图标（不灭躯、光阴碎激发、白骨魔；BUFF 5401-5410，last 255）并记下，死亡时去掉。
func (b *battle) huoshi(t *unit, buff int32) {
	b.buff(t, buff, 255)
	t.huoshiBuff = buff
}

// resetInitial 把阵亡的 t 的攻击、生命上限还原成初始值（复活前调用：神佑、延时复活）。
func (b *battle) resetInitial(t *unit, id int32) {
	if d := t.baseAtt - t.att; d != 0 {
		b.changeAtt(t, id, d)
	}
	if d := int64(t.MaxHP) - t.maxHP; d != 0 {
		b.changeMaxHP(t, id, d)
	}
}

// pendingRevive：「2 回合后复活」（返死 / 还魂）。官方战报：死后第 2 个回合的回合结束时（下一个 TURN 之前，
// 后阵也行）复活——ATT_CHANGE / CHG_MAXHP 把攻击、上限还原成初始值，RELIVE（招式号 = 返死 / 还魂 buff，
// 生命 = 初始上限 × 比例），再 RM_BUFF 去掉这个 buff。
func (b *battle) pendingRevive(u *unit) {
	if u.reviveIn == 0 || u.alive() || u.eaten || b.round < u.reviveAt || u.has(stNoSoul) != nil {
		return
	}
	pct := u.revivePct
	if pct <= 0 {
		pct = reviveHPPct
	}
	id := u.reviveMark
	b.resetInitial(u, id)
	b.relive(u, id, u.maxHP*int64(pct)/100)
}

// shareZaoyao 结算早夭分担：t 受到物理伤害 n 后，其同方其他带早夭者各失去 n 的 50%，
// 计入各自 40% 上限，扣满后状态消失（由 hurt 调用，分担本身是非物理伤害，不连锁）。
func (b *battle) shareZaoyao(src, t *unit, n int64) {
	for _, m := range b.allies(t) {
		if m == t {
			continue
		}
		s := m.has(stZaoyao)
		if s == nil {
			continue
		}
		take := min(n/2, s.val)
		if take <= 0 {
			continue
		}
		s.val -= take
		b.hurt(src, m, 0, take, false) // 无招式号（攻击后还原临时攻击同样用 0）
		if s.val <= 0 {
			b.clearStatus(m, stZaoyao)
		}
	}
}

// lieyanChance 是烈焰吞噬在第 round 回合的触发几率 %。
// 「触发几率每回合减少 1/3」按原值线性递减：第 1 回合 100%、66%、33%，第 4 回合起固定 10%。
func lieyanChance(round int) int {
	switch {
	case round <= 1:
		return 100
	case round == 2:
		return 66
	case round == 3:
		return 33
	}
	return 10
}

// hitOpposite 让 t 的对位敌人失去 n 点生命（易命、还魂激发）。
func (b *battle) hitOpposite(t *unit, k *talent, n int64) {
	for _, o := range b.foes(t) {
		if o.slot() == t.slot() {
			b.show(t, k.id, o)
			b.hurt(t, o, k.id, n, false)
			return
		}
	}
}

const (
	eggMagic       = 17022 // 火之魂（火系群法：敌方全体 100% 攻击的火属性伤害）
	eggShape       = 54004 // 火之卵的形象
	eggSpeed       = 10
	eggDeathTalent = 21471 // 火之魂：火之卵的死亡技
)

// eggFire：火之卵行动时放火系群法（法术 17022）。
func (b *battle) eggFire(u *unit, id int32) {
	foes := b.foes(u)
	if len(foes) == 0 {
		return
	}
	b.show(u, id, foes...)
	for _, x := range foes {
		b.propertyHurt(u, x, eggMagic, u.att, 3)
	}
}
