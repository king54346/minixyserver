package battle

import "turnrpg/internal/gamedata"

// 队长技：一方队长的队长技对本方符合种族的全部单位生效（gamedata.LeaderSkills：race / effect / tier）。
//
// 原表只有「少量 / 中量 / 大量」的文字，没有数值。大量档按官方战报校准：
//   - 伤害类 +10%：高级魔族解放下我方魔族普攻恒 ×1.10（8/8），高级兽族怒吼下我方兽族普攻众数 ×1.10（340/764）；
//   - 闪避 +14%：高级灵巧下我方不计闪避神通的闪避率 481/3395 = 14.2%（其他队长下 0–0.4%）。
//

// leadBonus 是单位身上的队长技加成（百分点）。
type leadBonus struct {
	dmg, mdmg, ndmg, crit int // 伤害 / 法术伤害 / 普通伤害 / 暴击伤害 提升
	hit, dodge            int // 命中（抵消对方闪避）/ 躲闪
	res, mres, nres       int // 伤害 / 法术伤害 / 普通伤害 抗性
	critres               int // 抵抗暴击伤害
	healout, healin       int // 回复效果 / 被治疗效果
}

// leaderTop 是各效果大量档的数值（未列出的为 10）。
var leaderTop = map[string]int{"dodge": 14}

func leaderPct(effect string, tier int) int {
	top, ok := leaderTop[effect]
	if !ok {
		top = 10
	}
	switch tier {
	case 1:
		return top / 2
	case 2:
		return top * 3 / 4
	case 3:
		return top
	}
	return 0
}

// leaderBonusFor 返回队长技 skill 对种族 race 的单位的加成。
func leaderBonusFor(skill int32, race int) leadBonus {
	var l leadBonus
	ls := gamedata.LeaderSkills[skill]
	if ls == nil || ls.Race != 0 && ls.Race != race {
		return l
	}
	p := leaderPct(ls.Effect, ls.Tier)
	switch ls.Effect {
	case "dmg":
		l.dmg = p
	case "mdmg":
		l.mdmg = p
	case "ndmg":
		l.ndmg = p
	case "crit":
		l.crit = p
	case "hit":
		l.hit = p
	case "dodge":
		l.dodge = p
	case "res":
		l.res = p
	case "mres":
		l.mres = p
	case "nres":
		l.nres = p
	case "critres":
		l.critres = p
	case "healout":
		l.healout = p
	case "healin":
		l.healin = p
	}
	return l
}

// scale 按 +up% / −down% 缩放 n。
func scale(n int64, up, down int) int64 {
	if up != 0 {
		n = n * int64(100+up) / 100
	}
	if down != 0 {
		n = n * int64(100-min(down, 100)) / 100
	}
	return n
}

// plus 是两个队长技加成之和（援助：借用好友的队长护法时，自己和好友的队长技同时生效）。
func (l leadBonus) plus(o leadBonus) leadBonus {
	return leadBonus{dmg: l.dmg + o.dmg, mdmg: l.mdmg + o.mdmg, ndmg: l.ndmg + o.ndmg, crit: l.crit + o.crit,
		hit: l.hit + o.hit, dodge: l.dodge + o.dodge, res: l.res + o.res, mres: l.mres + o.mres, nres: l.nres + o.nres,
		critres: l.critres + o.critres, healout: l.healout + o.healout, healin: l.healin + o.healin}
}
