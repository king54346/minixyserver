package battle

import "turnrpg/internal/gamedata"

// 法术附带的 buff（法术表 buffid，几率 buff_rate(LV)%，法术命中后对每个目标判定）与精修 buff 共用的效果。
// 效果照 auto/buff.lua 的描述，数值是该级 param（$a）；同族各级的号连续，按族首号分派：
//
//	僵化 5001 无法发动神通，下次行动后清除        麻痹 5011 无法发动法术，下次行动后清除
//	燃烧 5021 回合结束损失上限 a%，到战斗结束    毒砂 5031 回合开始损失 a% 攻击力，到战斗结束
//	盲目 5041 a% 几率不命中，下次行动后清除      混乱 5051 随机攻击己方，下次行动后清除
//	会猎 5061 受物理伤害额外 +a%，到战斗结束    禁疗 5081 生命恢复降低 a%，5 回合
//	加速 5091 先攻 +a，到当前回合结束（本回合出手顺序已定，只显示）
//	隐身 5111 / 5112 无法成为物理攻击目标，到当前回合 / 下 a 回合结束
//	毒化 5131 回合结束损失上限 a%，生效两次      锢魂 5141 无法被复活，到战斗结束
//	血诅咒 5171 降低 a% 生命与上限               攻诅咒 5181 降低 a% 攻击力
//	神之力 5201 提升 a% 攻击力                   神之护 5211 提升 a% 生命上限
//	捆缚 5231 无法行动，下次行动后清除            早夭 5251 分担友方物理伤害，达到上限 a% 或回合结束消失
//	战呼 5261 提升 10%×等级 攻击力，下次行动后清除  无敌 5321 不受伤害，到当前回合结束
//	凝滞 5331 先攻 −4，1 回合                    嘲讽 5471 攻击嘲讽来源，且对其攻击力降低 a%
//	火附魔 5649–5651（烈焰怒吼）：攻击伤害低于初始攻击时，差值的 [法术等级 × a%] 转为火属性生命流失，下次行动后清除
//
// 估计：敌方施加的负面效果过不侵 / 分水的抵抗（与神通施加的同样处理）；毒砂取最高级不叠加。

// buffFamily 是 buff 号所在族的首号（同族各级号连续，族首号个位为 1）。
func buffFamily(id int32) int32 {
	switch {
	case id == 5111 || id == 5112 || id >= 5649 && id <= 5651:
		return id
	case id%10 == 0:
		return id - 9
	}
	return id - id%10 + 1
}

// magicBuff：法术命中目标后按 buff_rate 判定附带 buff。
func (b *battle) magicBuff(u, t *unit, c *castable) {
	if c.Buff == 0 || !t.alive() {
		return
	}
	if rate := c.BuffRate[0] + c.BuffRate[1]*float64(c.lv); rate > 0 && b.chance(int(rate)) {
		if fam := buffFamily(c.Buff); fam >= 5649 && fam <= 5651 { // 火附魔：转化比例与法术等级有关
			a := int64(0)
			if info := gamedata.Buffs[c.Buff]; info != nil {
				a = int64(info.Value())
			}
			b.setStatus(t, stFireEnchant, min(a*int64(c.lv), 100), 1, true, c.Buff, slotBuff)
			return
		}
		b.applyBuff(u, t, c.Buff)
	}
}

// applyBuff 由 u 给 t 上 buff id。
func (b *battle) applyBuff(u, t *unit, id int32) {
	if !t.alive() {
		return
	}
	a := int64(0)
	if info := gamedata.Buffs[id]; info != nil {
		a = int64(info.Value())
	}
	foe := t.side != u.side
	debuff := func(key stKey, val int64, left int, byAction bool) bool {
		if foe {
			return b.addStatus(t, key, val, left, byAction, id, slotDebuff)
		}
		b.setStatus(t, key, val, left, byAction, id, slotDebuff)
		return true
	}
	resisted := func(key stKey) bool { return foe && b.resist(t, key) }
	switch buffFamily(id) {
	case 5001:
		debuff(stStiff, 0, 1, true)
	case 5011:
		debuff(stSilence, 0, 1, true)
	case 5021, 5131: // 燃烧 / 毒化
		key := stWeak
		if buffFamily(id) == 5021 {
			key = stBurn
		}
		if resisted(key) {
			return
		}
		left := maxRounds
		if buffFamily(id) == 5131 {
			left = 2
		}
		b.addPoison(t, poison{from: id, pct: int(a), left: left, buff: id, burn: buffFamily(id) == 5021})
	case 5031: // 毒砂
		if resisted(stDusha) {
			return
		}
		if int(a) > t.dusha {
			t.dusha, t.dushaBuff = int(a), id
		}
		b.buff(t, id, maxRounds)
	case 5041:
		debuff(stBlind, a, 1, true)
	case 5051:
		debuff(stConfuse, 0, 1, true)
	case 5061: // 会猎
		if resisted(stWeak) {
			return
		}
		if int(a) > t.huilie { // 同一状态不叠加，取最高级
			t.hurtUp += int(a) - t.huilie
			t.huilie = int(a)
			if t.huilieBuff != 0 && t.huilieBuff != id {
				b.rmBuff(t, t.huilieBuff)
			}
			t.huilieBuff = id
		}
		b.buff(t, id, maxRounds)
	case 5081:
		debuff(stNoHeal, a, noHealRounds, false)
	case 5091: // 加速
		b.buff(t, id, 1)
	case 5111:
		b.setStatus(t, stHidden, 0, 1, false, id, slotBuff)
	case 5112:
		b.setStatus(t, stHidden, 0, 1+int(max(a, 1)), false, id, slotBuff)
	case 5141:
		debuff(stNoSoul, 0, maxRounds, false)
	case 5171: // 血诅咒
		if resisted(stWeak) {
			return
		}
		b.changeMaxHP(t, id, -t.maxHP*a/100)
		b.buff(t, id, maxRounds)
	case 5181: // 攻诅咒
		if resisted(stWeak) {
			return
		}
		b.changeAtt(t, id, -t.att*a/100)
		b.buff(t, id, maxRounds)
	case 5201: // 神之力：下次行动后清除
		b.shenli(t, id, a)
	case 5211: // 神之护
		g := t.maxHP * a / 100
		b.changeMaxHP(t, id, g)
		b.heal(t, id, g)
		b.buff(t, id, maxRounds)
	case 5231:
		debuff(stBind, 0, 1, true)
	case 5251:
		debuff(stZaoyao, t.maxHP*a/100, 1, false)
	case 5261:
		b.zhanhu(t, id)
	case 5321:
		b.setStatus(t, stImmune, 0, 1, false, id, slotBuff)
	case 5331:
		debuff(stSlow, 4, 1, false)
	case 5471: // 嘲讽
		if foe && debuff(stTaunt, a, 1, true) {
			t.status[stTaunt].src = u
		}
	case 5649, 5650, 5651:
		b.buff(t, id, maxRounds)
	}
}
