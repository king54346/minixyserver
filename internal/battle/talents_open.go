package battle

// 开场（第一个 TURN 之前）触发的神通：特定弱化、噬心龙咒 / 雷影庇护激发、十日圣母献祭、火炮之术装弹。

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
	b.qjBossStart() // 取经首领：给予神通、整场无敌 / 混乱 / 早夭 / 坚硬
	b.raceHP()
	for side := range b.units {
		for _, u := range b.units[side] {
			if k := u.tals[famShiXinLongZhou]; k != nil && k.tjOn() && u.tals[famFanJi] == nil {
				b.grant(u, levelID(famFanJi, int(k.tjW()))) // 噬心龙咒激发：获得【反击 2/4/6/8/10】（常驻，不必入阵）
			}
			if u.front() {
				b.zhenYan(u) // 阵眼：开场就在 3 号位（FAQ「位于三号战位时」，不只入阵）
			}
			if k := u.tals[famLeiYingBiHu]; k != nil && k.tjOn() {
				g := int64(float64(u.baseHP) * k.tjX() / 100)
				u.hpBonus += g // 复活重置时保留：resetInitial 回到 baseHP + hpBonus
				b.growAndHeal(u, k.id, g)
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

// huoPaoTJResetRounds：火炮之术的天匠（激发）效果——带激发时每 3 回合把弹丸回满（2026-11-15 调整）。
const huoPaoTJResetRounds = 3

// initPellets：火炮之术战斗开始时获得（本方先攻护法数 × b）枚不稳定弹丸（前阵后阵都给，FAQ）。
func (b *battle) initPellets(u *unit) {
	if k := u.tal(famHuoPaoZhiShu); k != nil && !u.pelletInit {
		u.pelletInit = true
		for _, x := range b.units[u.side] {
			if x.tals[famXianGong] != nil {
				u.pellets += k.param(1)
			}
		}
		u.pellFull = u.pellets // 天匠回满的目标值
		b.pelletCount(u)
	}
}

// pelletCount 刷新小夫子头像上的弹丸数（客户端小夫子与阿修罗共用 XIULUO_COUNT 计数，war/config.lua isXiuLuo）。
func (b *battle) pelletCount(u *unit) {
	b.xiuluoCount(u, u.pellets)
}

// raceHPFamilies：装备炼神的套装效果「场上所有X族生命上限提升(神通等级×护法气血上限/100)」（talentset.csv 21201–21261）。
var raceHPFamilies = []struct {
	fam  family
	race int
}{{famRenJie, raceHuman}, {famMoWang, raceGhost}, {famXianShi, raceGod}, {famShouLing, raceBeast}}

func init() { register(famXianShi, famRenJie, famMoWang, famShouLing) }

// RaceHPSet identifies equipment set talents that contribute to the lineup panel.
func RaceHPSet(id int32) (race, percent int) {
	k := newTalent(id)
	if k != nil {
		for _, effect := range raceHPFamilies {
			if k.fam == effect.fam {
				return effect.race, k.param(0)
			}
		}
	}
	return 0, 0
}

// raceHP：开场时，带仙师 / 人杰 / 魔王 / 兽灵的护法（在前阵或后阵都算）让本方前后阵所有该族护法（含自己）
// 基础生命上限和开场生命提升「神通等级 × 施加者生命上限 / 100」。
// 多个来源按开场时的上限独立计算后相加。
func (b *battle) raceHP() {
	bonuses := make(map[*unit]int64)
	for side := range b.units {
		for _, u := range b.units[side] {
			for _, rf := range raceHPFamilies {
				fam, race := rf.fam, rf.race
				for _, k := range u.talAll(fam) {
					// 第四神通使用 12xxxx 编号，其强度来自参数，不是与
					// 普通 21xxx 族号的差值。自带与套装各自生效。
					n := (u.baseHP - int64(u.PanelSetHP)) * int64(k.param(0)) / 100
					if n <= 0 {
						continue
					}
					var ts []*unit
					for _, x := range b.units[side] {
						if x.Race == race && x.alive() {
							ts = append(ts, x)
						}
					}
					if len(ts) == 0 {
						continue
					}
					b.passiveAura(u, k.id)
					for _, x := range ts {
						added := b.changeMaxHP(x, k.id, n)
						bonuses[x] += added
						if gain := min(added, x.maxHP-x.hp); gain > 0 {
							x.hp += gain
							b.hpCmd(CmdAddHP, k.id, x, gain)
						}
					}
				}
			}
		}
	}
	// 各来源按加套装前的基础上限计算，全部判定后再计入基础面板。
	for x, bonus := range bonuses {
		x.baseHP += bonus
	}
}
