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
