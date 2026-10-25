package battle

import (
	"sort"

	"turnrpg/internal/gamedata"
)

// 法术与神通的效果。法术规则全部来自客户端（logic/magic_mgr.lua 的说明文字与参数表）；神通按
// auto/talent.lua 各族的描述实现，演出方式照官方战报（analysis/tools/fight_record.py 可看）。
// 没实现的神通族不生效，覆盖情况见 TestTalentCoverage。

const (
	raceHuman = 1
	raceGhost = 2 // 魔
	raceGod   = 3
	raceBeast = 4
)

// 法术伤害上限（magic_mgr.lua MAGIC_DESC13/14，按法术星级）：
// 伤害 = min(攻击, 上限, 上限*等级/100+底数) * hurt(LV)/1000，抓包核对比值 ≈1.0。
var (
	magicCap  = [6]int64{0, 500, 900, 1300, 2700, 3600}
	magicBase = [6]int64{0, 20, 30, 40, 58, 100}
)

func magicPower(u *unit, star int) int64 {
	if star < 1 || star >= len(magicCap) {
		return u.att
	}
	return min(u.att, magicCap[star], magicCap[star]*int64(u.Grade)/100+magicBase[star])
}

// castMagic：按顺序看身上的法术（天生在前、法术书在后），第一个满足「次数没用完、本方战意够、
// 按概率触发」的施放（在普攻之前额外出手），每次行动最多施放一个。
func (b *battle) castMagic(u *unit) {
	for _, c := range u.magics {
		if c.left > 0 && b.zy[u.side] >= c.CondZY && c.roundOK(b.round) && b.chance(c.Rate+c.bonus) {
			b.cast(u, c)
			return
		}
	}
}

// roundOK：法术表 cond_round 限定了施放回合（如朝云暮雨 2、4 回合，下凡 1、4、7 回合）时只在这些回合施放。
func (c *castable) roundOK(round int) bool {
	if len(c.CondRound) == 0 {
		return true
	}
	for _, r := range c.CondRound {
		if r == round {
			return true
		}
	}
	return false
}

func (b *battle) cast(u *unit, c *castable) []*unit {
	m := c.MagicInfo
	lv := float64(c.lv)
	sp, spVal := m.SP[0], int64(float64(m.SP[1])+float64(m.SP[2])/10*lv)
	targets := b.magicTargets(u, m.Target, sp == 5, m.TargetParam)
	if len(targets) == 0 {
		return nil
	}
	c.left--
	// 施放不扣战意（官方战报 2000 多次施法没有一次 ZHANYI 减少；cond_zy 只是发动条件）。
	id := c.id
	if sp == 9 || sp == 13 {
		b.show(u, id, u) // 无双 / 疯狂（疯魔乱棍）：官方战报 ACT_DO 指向自己，之后逐下 ACT_DO(普攻) + SUB_HP
	} else {
		b.show(u, id, targets...)
	}
	amount := magicPower(u, m.Star) * int64(m.Hurt[0]+m.Hurt[1]*lv) / 1000
	for _, t := range targets {
		// 负面状态先于伤害结算（用户规则）：法术附带的 buff、精修 buff 在扣血之前挂上——这一下打死目标时，
		// 锢魂之类照样生效（不会被神佑等复活）。复生法术的目标是死者，复活后再挂。
		if sp != 5 && t.alive() {
			b.magicBuff(u, t, c)
			b.jingxiu(u, t, c)
		}
		switch {
		case sp == 5: // 复生
			b.relive(t, id, t.maxHP*spVal/100)
			continue
		case amount > 0 && m.Kind == 5:
			b.heal(t, id, amount)
		case amount > 0:
			victim, cut := t, int64(0)
			for _, x := range b.allies(t) {
				if k := x.tal(famJiuYuan); k != nil && x != t && t.side != u.side && !b.invincible(t) && t.hp*100 < t.maxHP*int64(k.param(0)) {
					victim, cut = x, int64(k.param(1))
					// 救援：词条 → 冲到队友身前（ACT_DO 4 → 队友）→ 退回（ACT_DO 4 → 自己），再结算伤害。
					// 之前只发了冲过去，没有退回，客户端的救援者一直停在队友那里回不了槽位。
					b.show(x, k.id, x)
					b.show(x, attackGuard, t)
					b.show(x, attackGuard, x)
					if k.tjOn() {
						g := int64(float64(x.maxHP) * k.tjX() / 100)
						b.changeMaxHP(x, k.id, g)
						b.heal(x, k.id, g)
					}
					break
				}
			}
			b.propertyHurt(u, victim, id, amount-amount*cut/100, m.Kind)
			if victim != t && t.alive() {
				b.propertyHurt(u, t, id, 1, m.Kind) // 救援：原目标一同受到此次攻击，仍受 1 点属性伤害（FAQ）
			}
		}
		if !t.alive() {
			continue
		}
		b.spEffect(u, t, id, sp, spVal)
		for _, e := range m.ExtSP { // 附加特效（ext_sp，如沐浴日晖：回复 + 清除负面）
			b.spEffect(u, t, id, e[0], int64(float64(e[1])+float64(e[2])/10*lv))
		}
	}
	switch sp {
	case 9, 13: // 无双 3 人 / 疯狂 6 人：以 a% 攻击力随机打敌方
		n := 3
		if sp == 13 {
			n = 6
		}
		for range n {
			if !u.alive() || b.over { // 中途被反伤打死
				break
			}
			if foes := b.foes(u); len(foes) > 0 {
				// 官方战报（疯魔乱棍 12505）：每一下是 ACT_DO(普攻 1 → 目标) + SUB_HP(法术号)，客户端据此播连续出手
				t := foes[b.rng.IntN(len(foes))]
				b.show(u, attackNormal, t)
				b.hurt(u, t, id, u.att*spVal/100, true)
			}
		}
	}
	return targets
}

// propertyHurt 是法术 / 属性伤害：先过仙衣减免，受击后触发润泽。返回实际扣血。
func (b *battle) propertyHurt(u, t *unit, id int32, n int64, kind int) int64 {
	if u != nil && u.side != t.side {
		n = scale(n, u.lead.mdmg, t.lead.mres) // 队长技：法术伤害 / 法术伤害抗性
		if t.vineArmor {
			n = n * 120 / 100 // 藤甲：受到法术伤害提高 20%
		}
	}
	// 遁让：属性伤害超出上限 a% 时，b% 概率与后场护法交换并躲过
	// 遁让是免除伤害后与后阵换位：无敌时本来就不受伤，不发动
	if k := t.tal(famDunRang); k != nil && t.front() && !b.invincible(t) && n*100 > t.maxHP*int64(k.param(0)) && b.chance(k.param(1)) {
		if bn := b.bench(t.side); len(bn) > 0 {
			b.show(t, k.id, t)
			b.swap(t, bn[b.rng.IntN(len(bn))])
			if k.tjOn() && u != nil && u.alive() { // 遁让激发：伤害来源流失 $x% 触发值的生命（生命流失）
				b.loseHP(t, u, k.id, int64(float64(n)*k.tjX()/100))
			}
			return 0
		}
	}
	if b.lifeLoss(u) {
		return b.loseHP(u, t, id, n) // 万仙来朝：伤害变为生命流失
	}
	if x := t.tal(famXianYi); x != nil && kind >= 0 && kind <= 4 && b.chance(x.param(0)) {
		b.show(t, x.id, t) // 官方战报：仙衣减免时 ACT_DO 指向自己（激发的反伤 SUB_HP 随后）
		cut := n * int64(x.param(1)) / 100
		n -= cut
		if x.tjOn() && u != nil && u.alive() && u.side != t.side { // 仙衣激发：反弹 $x% 减免值的生命流失伤害
			b.loseHP(t, u, x.id, int64(float64(cut)*x.tjX()/100))
		}
	}
	if q := t.tal(famQiJia); q != nil && q.tjOn() && b.chance(q.tjPct()) {
		n = min(n, t.maxHP*int64(q.param(1))/100)
	}
	n = b.propertyTaken(u, t, n, kind)
	if t.egg && kind >= 0 && kind <= 4 {
		n = min(n, 1) // 火之卵自带仙衣10：所有属性伤害降低为 1（FAQ 归元）
	}
	done := b.hurt(u, t, id, n, false)
	dealt := b.lastDealt
	defer func() { b.lastDealt = dealt }() // 分水反伤等不覆盖
	for _, g := range elemGuards {         // 分水 / 逆雷 / 辟土 / 返火：受到对应属性伤害时对来源造成等值的物理伤害
		if k := t.tal(g.fam); k != nil && kind == g.kind && done > 0 && u != nil && u.side != t.side && u.alive() && b.chance(k.param(0)) {
			b.show(t, k.id, u)
			b.hurt(t, u, k.id, done, true)
		}
	}
	if kind >= 0 && kind <= 4 { // 道属性也算（官方战报：逆势 20786 后润泽、仙衣都触发）
		b.runze(t)
	}
	return done
}

// runze：润泽——受到伤害后为友方伤重护法回复 a% 攻击的生命并提升其 b% 生命上限。
func (b *battle) runze(t *unit) {
	if r := t.tal(famRunZe); r != nil && t.alive() {
		if x := b.mostHurt(t.side); x != nil {
			b.show(t, r.id, t)
			b.heal(x, r.id, t.att*int64(r.param(0))/100) // $p = a% 攻击
			grow := x.maxHP * int64(r.param(1)) / 100
			b.changeMaxHP(x, r.id, grow)
			b.heal(x, r.id, grow)
			if r.tjOn() && b.chance(r.tjPct()) {
				b.clearOneDebuff(x) // 激发：$x% 概率清除目标一个负面状态
			}
		}
	}
}

// transform 是变身（CHG_FIGHTER）：客户端换形象、种族、先攻，生命与上限都设为 hp（满血的新单位）。
// 用于归元的火之卵、情殇的白骨魔；阵亡单位就此复活（不再发 RELIVE）。
func (b *battle) transform(t *unit, shape int32, race, speed int, hp int64) {
	t.hp, t.maxHP = max(hp, 1), max(hp, 1)
	t.Icon, t.Race, t.Speed = shape, race, speed
	t.revived, t.reviveIn = true, 0
	b.w.u8(CmdChgFighter)
	b.w.u8(t.Pos)
	b.w.u32(int64(shape))
	b.w.u32(t.hp)
	b.w.u32(t.att)
	b.w.u8(speed)
	b.w.u8(1)
	b.w.u8(race)
	b.w.u8(0)
}

func (b *battle) relive(t *unit, id int32, hp int64) {
	if t.alive() || hp <= 0 {
		return
	}
	t.hp = min(hp, t.maxHP)
	t.revived = true
	t.reviveIn = 0
	t.gone = false // 后阵的死者复活后回到后阵候补（之后照常补位）
	t.fpCut = 0    // 返璞归真：被复活时复原
	defer func() {
		if t.reviveMark != 0 {
			b.rmBuff(t, t.reviveMark) // 返死 / 还魂标记
			t.reviveMark = 0
		}
	}()
	b.w.u8(CmdRelive)
	b.w.u32(int64(id))
	b.w.u8(1)
	b.w.u8(t.Pos)
	b.w.u32(t.hp)
	b.zfSync(t) // 阵法：复活后按所在阵位重算固定加成
	b.reviveSkill(t)
	b.deathSkillEvent(t)
}

// magicTargets 按 magic_mgr.lua MAGIC_DESC11 的 17 种规则选目标。
func (b *battle) magicTargets(u *unit, code int, revive bool, param []int32) []*unit {
	foes, allies := b.foes(u), b.allies(u)
	if revive {
		for _, x := range b.units[u.side] {
			if !x.alive() && x.front() && !x.eaten && x.has(stNoSoul) == nil { // 锢魂：无法被复活（用户报：带锢魂的达摩被死者复苏救起）
				return []*unit{x}
			}
		}
		return nil
	}
	pickBy := func(l []*unit, less func(x, y *unit) bool) []*unit {
		if len(l) == 0 {
			return nil
		}
		best := l[0]
		for _, x := range l[1:] {
			if less(x, best) {
				best = x
			}
		}
		return []*unit{best}
	}
	main := b.pickTarget(u)
	withMain := func(extra ...*unit) []*unit {
		if main == nil {
			return nil
		}
		out := []*unit{main}
		for _, x := range extra {
			if x != main {
				out = append(out, x)
			}
		}
		return out
	}
	switch code {
	case 1:
		return pickBy(foes, func(x, y *unit) bool { return x.hp < y.hp })
	case 2:
		return pickBy(foes, func(x, y *unit) bool { return x.hp > y.hp })
	case 3:
		return pickBy(foes, func(x, y *unit) bool { return x.att > y.att })
	case 4:
		return pickBy(foes, func(x, y *unit) bool { return x.att < y.att })
	case 5:
		return pickBy(foes, func(x, y *unit) bool { return x.Speed > y.Speed })
	case 6:
		return pickBy(foes, func(x, y *unit) bool { return x.Speed < y.Speed })
	case 7:
		return foes
	case 8:
		return withMain()
	case 9, 16:
		if main == nil {
			return nil
		}
		return withMain(b.adjacent(main)...)
	case 10: // 目标 + 先攻最高的敌人；二者相同时取次高（同先攻从左到右第一个），必定 2 个目标
		var rest []*unit
		for _, x := range foes {
			if x != main {
				rest = append(rest, x)
			}
		}
		sort.SliceStable(rest, func(i, j int) bool { return rest[i].slot() < rest[j].slot() })
		return withMain(pickBy(rest, func(x, y *unit) bool { return x.Speed > y.Speed })...)
	case 11:
		if len(allies) == 0 {
			return nil
		}
		return []*unit{allies[b.rng.IntN(len(allies))]}
	case 12:
		return []*unit{u}
	case 13:
		return pickBy(allies, func(x, y *unit) bool { return x.hp < y.hp })
	case 14:
		return allies
	case 17: // 本方带某神通的护法（target_param 是该族任一级的神通号；如离火兽烈焰怒吼只给带乱舞的）
		if len(param) == 0 || gamedata.Talents[param[0]] == nil {
			return nil
		}
		fam := family(gamedata.Talents[param[0]].FamilyID)
		var out []*unit
		for _, x := range allies {
			if x.tal(fam) != nil {
				out = append(out, x)
			}
		}
		return out
	case 15:
		return append([]*unit{u}, b.adjacent(u)...)
	}
	return nil
}

// ---- 神通 ----

// spEffect 是法术特效（sp_type / ext_sp_type）对单个目标的效果；无双 / 疯狂打随机敌人，在 cast 里处理。
func (b *battle) spEffect(u, t *unit, id int32, sp int, spVal int64) {
	if !t.alive() {
		return
	}
	switch sp {
	case 1: // 摧毁（羊脂玉净瓶等）：a% 概率直接秒杀
		if t.side != u.side && b.chance(int(spVal)) {
			b.destroy(u, t, id)
		}
	case 2: // 反噬：目标当前生命 a% 的道属性伤害
		b.propertyHurt(u, t, id, t.hp*spVal/100, 0)
	case 3: // 失血：流失上限 a% 的生命（生命流失，无敌挡不住）
		b.loseHP(u, t, id, pctLoss(t, spVal))
	case 4: // 蚕食：流失上限 a% 的生命并回复同量（生命流失，如程咬金开山斧）
		b.heal(u, id, b.loseHP(u, t, id, pctLoss(t, spVal)))
	case 6: // 回复
		b.heal(t, id, t.maxHP*spVal/100)
	case 7: // 清除：a% 几率移除目标身上的所有负面状态（八九玄功、时光回溯…；原来只清了中毒 / 燃烧）
		if b.chance(int(spVal)) {
			b.clearAllDebuffs(t)
		}
	case 8: // 换位（大鹏展翅、放下屠刀、入剑诀、狂风、风鹏正举）：a% 概率令目标与其后阵随机一名存活护法换位，
		// 换上来的护法被捆缚（用户规则）
		if t.front() && b.chance(int(spVal)) {
			if bn := b.bench(t.side); len(bn) > 0 {
				x := bn[b.rng.IntN(len(bn))]
				b.swap(t, x)
				b.bindEntered(x)
			}
		}
	case 10: // 物理结界
		b.addShield(t, u.maxHP*spVal/100, false)
	case 11: // 属性结界
		b.addShield(t, u.maxHP*spVal/100, true)
	}
}
