package battle

import (
	"slices"

	"turnrpg/internal/gamedata"
)

// 西天取经首领 / 打魂关的专用神通（客户端怪物表 fight_npc 36001–36321 的怪身上，xymsrv/qujing.go）。
// 其中几种共用一个族（「获得特效」：无敌 / 混乱 / 早夭 / 坚硬；「变身」：火人 / 宝藏守卫 / 活死人；「自爆」两种），
// 一只怪可能同时带两个同族的，所以这里按原始神通号（Fighter.Talents）处理，不走按族查找的 u.tals。
// 首领机制说明见客户端 QuJingTeamView.lua TipsBossDesc。
const (
	qjBianHuoRen   = 130151 // 变身火人：死后变身为火人（param 目标怪）
	qjGeiShenLi    = 130152 // 给予神力：死亡后立即满血原地复活，并给对方一个神之力5
	qjBianBaoZang  = 130153 // 变身宝藏守卫：第 a–b 回合间死亡则变身（param c 目标怪）
	qjBianHuoSiRen = 130154 // 变身活死人：同上
	qjBuXiu        = 130155 // 不朽：a% 不受锢魂、封魂
	qjFuHuo        = 130156 // 复活队友：每 a 回合开始复活队友，上限设为死亡时的 b%
	qjYiChu        = 130157 // 溢出：受到溢出治疗时令敌方流失生命 = 溢出 / 上限 × 自身攻击 × a%
	qjGeiShenTong  = 130158 // 给予神通（溢出）：战斗开始给敌方全体 param 号神通
	qjWuDi         = 130159 // BOSS-无敌：整场无敌
	qjZiBao        = 130160 // 自爆：第 a 回合开始自爆，对敌方全体造成 b% 攻击
	qjFangShen     = 130161 // 防身：场上有其他友方存活时 a% 获得无敌到回合结束
	qjJiuHuo       = 130162 // 救活队友：每 a 回合结束复活所有队友，生命 b%
	qjFenLie1      = 130163 // 分裂 1–3：生命降到 a% 以下时复制自身（25 / 50 / 75）
	qjFenLie3      = 130165
	qjFanJiBoss    = 130166 // 反击（孙悟空）：入阵时给敌方 a 次全体火属性伤害 = 失血比例 × 攻击（第 1 回合不生效）
	qjZiBao3       = 130169 // 自爆：每 a 的倍数回合开始自爆，对敌方前阵造成 b% 攻击
	qjJianYing     = 130170 // 坚硬：受到除生命流失外的伤害 a% 只扣 1 点
	qjHunLuan      = 130177 // BOSS-混乱：整场混乱
	qjZaoYao       = 130178 // BOSS-早夭：整场早夭（分担量 100% 上限）
	qjBossJianYing = 130179 // BOSS-坚硬：整场「无论受到什么伤害只扣 1 点」（buff 5571 概率 100，含生命流失）
	qjJiBai        = 130180 // BOSS-击败：死亡时 a% 击败所有队友
	qjGeiMiaoShou  = 130196 // 给予神通（妙手）：战斗开始给敌方全体 param 号神通
)

// qjRaceVuln：来自某族的伤害变为 a%（人 / 魔 / 仙 / 兽族易伤）。
var qjRaceVuln = map[int32]int{130167: raceHuman, 130173: raceGhost, 130174: raceGod, 130175: raceBeast}

// bossTal 返回 u 身上的首领神通 id（按原始神通号），没有返回 nil。
func (u *unit) bossTal(id int32) *gamedata.TalentInfo {
	if slices.Contains(u.Talents, id) {
		return gamedata.Talents[id]
	}
	return nil
}

func tparam(ti *gamedata.TalentInfo, i int) int {
	if ti == nil || i >= len(ti.Param) {
		return 0
	}
	return int(ti.Param[i])
}

// qjBossStart：战斗开始——给予神通、无敌 / 混乱 / 早夭 / 坚硬。
func (b *battle) qjBossStart() {
	for side := range b.units {
		for _, u := range b.units[side] {
			for _, id := range []int32{qjGeiShenTong, qjGeiMiaoShou} {
				if ti := u.bossTal(id); ti != nil {
					b.show(u, id, u)
					for _, x := range b.units[1-side] {
						b.grantFrom(u, x, int32(tparam(ti, 0)))
					}
				}
			}
			if u.bossTal(qjWuDi) != nil {
				b.show(u, qjWuDi, u)
				b.setStatus(u, stImmune, 0, maxRounds+1, false, 5542, slotBuff)
			}
			if u.bossTal(qjHunLuan) != nil {
				b.show(u, qjHunLuan, u)
				b.setStatus(u, stConfuse, 0, maxRounds+1, false, 5543, slotDebuff)
			}
			if u.bossTal(qjZaoYao) != nil {
				b.show(u, qjZaoYao, u)
				b.setStatus(u, stZaoyao, u.maxHP, maxRounds+1, false, 5544, slotDebuff)
			}
			if u.bossTal(qjBossJianYing) != nil {
				b.show(u, qjBossJianYing, u)
				b.buff(u, 5571, maxRounds+1)
				u.hard, u.hardAll = 100, true // buff 5571 参数 100；「无论受到什么伤害」：生命流失也只扣 1 点
			}
			if ti := u.bossTal(qjJianYing); ti != nil {
				u.hard = tparam(ti, 0)
			}
		}
	}
}

// qjVuln：t 的种族易伤（来自 src 种族的伤害变为 a%）。
func qjVuln(src, t *unit, n int64) int64 {
	if src == nil {
		return n
	}
	for id, race := range qjRaceVuln {
		if src.Race == race {
			if ti := t.bossTal(id); ti != nil {
				n = n * int64(tparam(ti, 0)) / 100
			}
		}
	}
	return n
}

// qjRoundStart：回合开始——防身、自爆、复活队友。
func (b *battle) qjRoundStart(u *unit) {
	if !u.alive() || !u.front() {
		return
	}
	if ti := u.bossTal(qjFangShen); ti != nil && len(b.allies(u)) > 1 && b.chance(tparam(ti, 0)) {
		b.show(u, qjFangShen, u)
		b.setStatus(u, stImmune, 0, 1, false, 5321, slotBuff)
	}
	if ti := u.bossTal(qjFuHuo); ti != nil && b.round%max(tparam(ti, 0), 1) == 0 {
		b.qjReviveMates(u, qjFuHuo, -1, tparam(ti, 1))
	}
	for _, id := range []int32{qjZiBao, qjZiBao3} {
		ti := u.bossTal(id)
		if ti == nil {
			continue
		}
		a := max(tparam(ti, 0), 1)
		if id == qjZiBao && b.round != a || id == qjZiBao3 && b.round%a != 0 {
			continue
		}
		foes := b.foes(u)
		b.show(u, id, foes...)
		n := u.att * int64(tparam(ti, 1)) / 100
		for _, x := range foes {
			b.hurt(u, x, id, n, true)
		}
		if u.alive() {
			b.loseHP(nil, u, id, u.hp) // 自爆后自己阵亡
		}
		return
	}
}

// qjRoundEnd：回合结束——救活队友。
func (b *battle) qjRoundEnd(u *unit) {
	if ti := u.bossTal(qjJiuHuo); ti != nil && u.alive() && u.front() && b.round%max(tparam(ti, 0), 1) == 0 {
		b.qjReviveMates(u, qjJiuHuo, tparam(ti, 1), -1)
	}
}

// qjReviveMates 复活 u 的阵亡队友：hpPct ≥ 0 时生命为上限的 hpPct%；maxPct ≥ 0 时上限设为死亡时的 maxPct%，满血。
func (b *battle) qjReviveMates(u *unit, id int32, hpPct, maxPct int) {
	var dead []*unit
	for _, x := range b.units[u.side] {
		if x != u && !x.alive() && !x.eaten && x.has(stNoSoul) == nil {
			dead = append(dead, x)
		}
	}
	if len(dead) == 0 {
		return
	}
	b.show(u, id, dead...)
	for _, x := range dead {
		hp := x.maxHP
		if maxPct >= 0 {
			x.maxHP = max(x.maxHP*int64(maxPct)/100, 1)
			hp = x.maxHP
		}
		if hpPct >= 0 {
			hp = max(x.maxHP*int64(hpPct)/100, 1)
		}
		b.relive(x, id, hp)
	}
}

// qjBossDeath：首领类的死亡结算；返回 true 表示已复活 / 变身（其余死亡技不再结算）。
func (b *battle) qjBossDeath(t *unit) bool {
	if ti := t.bossTal(qjJiBai); ti != nil && b.chance(tparam(ti, 0)) {
		b.qjDefeated[t.side] = true // 首领倒下：小怪不再靠给予神力复活
		mates := b.allies(t)
		if len(mates) > 0 {
			b.show(t, qjJiBai, mates...)
		}
		for _, x := range mates {
			b.loseHP(t, x, qjJiBai, x.hp)
		}
		return false
	}
	if ti := t.bossTal(qjGeiShenLi); ti != nil && !b.qjDefeated[t.side] && t.has(stNoSoul) == nil {
		b.show(t, qjGeiShenLi, t)
		b.relive(t, qjGeiShenLi, t.maxHP)
		// 给对方一个神之力5：给最近出手的敌人（打死它的），没有就给对方前阵第一个
		var x *unit
		if a := b.actor; a != nil && a.side != t.side && a.alive() {
			x = a
		} else if foes := b.foes(t); len(foes) > 0 {
			x = foes[0]
		}
		if x != nil {
			b.shenli(x, int32(ti.Buff), 15)
		}
		return true
	}
	for _, id := range []int32{qjBianHuoRen, qjBianBaoZang, qjBianHuoSiRen} {
		ti := t.bossTal(id)
		if ti == nil {
			continue
		}
		target := int32(tparam(ti, 0))
		if id != qjBianHuoRen { // a–b 回合间死亡才变
			if b.round < tparam(ti, 0) || b.round > tparam(ti, 1) {
				continue
			}
			target = int32(tparam(ti, 2))
		}
		if f := b.qjNPCs[target]; f != nil {
			b.qjTransform(t, id, f)
			return true
		}
	}
	return false
}

// qjTransform：t 变身为怪 f（形象、种族、先攻、攻击、生命上限、神通都换成 f 的），满血。
func (b *battle) qjTransform(t *unit, id int32, f *Fighter) {
	b.show(t, id, t)
	t.Name, t.Talents, t.Magic, t.MagicLv = f.Name, slices.Clone(f.Talents), f.Magic, f.MagicLv
	t.tals = map[family]*talent{}
	for _, tid := range f.Talents {
		if k := newTalent(tid); k != nil {
			t.tals[k.fam] = k
		}
	}
	t.att, t.baseAtt = int64(f.Att), int64(f.Att)
	b.transform(t, f.Icon, f.Race, f.Speed, int64(f.MaxHP))
}

// qjSplit：分裂——生命降到 75% / 50% / 25% 以下时各复制自身一次到己方前阵空位（官方战报 PET_CLONE{from, to}）。
func (b *battle) qjSplit(t *unit) {
	for i, id := range []int32{qjFenLie3, qjFenLie3 - 1, qjFenLie1} { // 75、50、25
		ti := t.bossTal(id)
		if ti == nil || t.splitDone[i] || !t.alive() || t.hp*100 >= t.maxHP*int64(tparam(ti, 0)) {
			continue
		}
		t.splitDone[i] = true
		slot := b.emptyFrontSlot(t.side)
		if slot == 0 {
			continue
		}
		b.show(t, id, t)
		f := *t.Fighter
		f.Pos = t.Pos - t.slot() + slot
		f.Talents = slices.DeleteFunc(slices.Clone(t.Talents), func(x int32) bool { return x >= qjFenLie1 && x <= qjFenLie3 })
		c := newUnit(&f, t.side)
		c.hp, c.maxHP, c.att, c.baseAtt = t.hp, t.maxHP, t.att, t.baseAtt
		c.baseHP = t.baseHP
		b.units[t.side] = append(b.units[t.side], c)
		b.w.u8(CmdPetClone)
		b.w.u8(t.Pos)
		b.w.u8(c.Pos)
	}
}

// emptyFrontSlot 是 side 一方前阵（1–5）里没有任何单位站过的槽位，没有返回 0。
func (b *battle) emptyFrontSlot(side int) int {
	used := map[int]bool{}
	for _, x := range b.units[side] {
		used[x.slot()] = true
	}
	for s := 1; s <= 5; s++ {
		if !used[s] {
			return s
		}
	}
	return 0
}

// qjBossEnter：反击（孙悟空）——入阵时给敌方 a 次全体火属性伤害，伤害 = 失血比例 × 攻击；第 1 回合不生效。
func (b *battle) qjBossEnter(u *unit) {
	ti := u.bossTal(qjFanJiBoss)
	if ti == nil || b.round <= 1 || u.maxHP <= 0 {
		return
	}
	for range tparam(ti, 0) {
		foes := b.foes(u)
		if len(foes) == 0 || !u.alive() {
			return
		}
		b.show(u, qjFanJiBoss, foes...)
		n := u.att * (u.maxHP - u.hp) / u.maxHP
		for _, x := range foes {
			b.propertyHurt(u, x, qjFanJiBoss, n, 3)
		}
	}
}

// qjOverflow：溢出——带溢出的单位受到溢出治疗时，敌方全体前阵流失生命 = 溢出量 / 上限 × 自身攻击 × a%。
func (b *battle) qjOverflow(t *unit, over int64) {
	ti := t.bossTal(qjYiChu)
	if ti == nil {
		if k := t.tals[family(qjYiChu)]; k != nil { // 给予神通得到的（grant 进 tals）
			ti = k.TalentInfo
		}
	}
	if ti == nil || over <= 0 || t.maxHP <= 0 {
		return
	}
	n := over * t.att * int64(tparam(ti, 0)) / 100 / t.maxHP
	if n <= 0 {
		return
	}
	for _, x := range b.foes(t) {
		b.loseHP(t, x, qjYiChu, n)
	}
}
