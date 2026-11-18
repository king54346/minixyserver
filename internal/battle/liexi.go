package battle

// 时空裂隙的战地因素（客户端 auto/liexi_factor.lua，id 1–20）。每个关卡 / 竞技场每周随机几个，作用于双方。
// 「命 / 攻 / 人 / 魔 / 仙 / 兽」改的是双方的初始属性，由服务端生成 Fighter 时直接算进去（这样战报里的阵容数值
// 就是加成后的）；「奖」是关卡奖励翻倍，也在服务端。其余在战斗里结算，见各钩子。
const (
	LxFire    = 1  // 火：每回合结束时场上所有护法获得【燃烧X】（X 为回合数，可叠加）
	LxPhys    = 2  // 物：受到的物理伤害减少 20%
	LxMagic   = 3  // 法：所有法术施法几率双倍
	LxHeal    = 4  // 疗：回合结束时恢复 30% 上限的生命
	LxShuffle = 5  // 乱：回合结束时场上护法受到【乱阵】（20% 几率与后阵换位）
	LxHP      = 6  // 命：双方初始生命上限 +50%（服务端）
	LxAtt     = 7  // 攻：双方初始攻击 +50%（服务端）
	LxShield  = 8  // 盾：第 1/3/5 回合开始时 X 个前阵存活护法获得【无敌】到回合结束（X 为回合数）
	LxBlast   = 9  // 炸：护法死亡时对相邻护法造成其上限 20% 的生命流失
	LxBlow    = 10 // 吹：回合开始时每位前阵护法 20% 与后阵护法换位
	LxHunt    = 11 // 伤：回合结束时场上所有前阵护法获得【会猎X】（X 为回合数）
	LxEnter2  = 12 // 入：入阵技生效两次
	LxDeath2  = 13 // 死：死亡技生效两次
	LxRampage = 14 // 暴：暴走后额外提升 100% 初始攻击
	LxCharge  = 15 // 蓄：每次蓄力增加 2 层
	LxHuman   = 16 // 人 / 魔 / 仙 / 兽：该族护法初始攻击 +100%（服务端）
	LxGhost   = 17
	LxGod     = 18
	LxBeast   = 19
	LxReward  = 20 // 奖：关卡胜利奖励翻倍（服务端）
)

// LxFactorPct 是各因素的参数（liexi_factor.lua param）。
var LxFactorPct = map[int]int{LxPhys: 20, LxHeal: 30, LxShuffle: 20, LxHP: 50, LxAtt: 50, LxBlast: 20, LxBlow: 20,
	LxRampage: 100, LxHuman: 100, LxGhost: 100, LxGod: 100, LxBeast: 100}

func (b *battle) factor(id int) bool { return b.factors[id] }

// lxRoundStart：回合开始（各单位回合开始神通之后）——盾、吹。
func (b *battle) lxRoundStart() {
	if b.factor(LxShield) && (b.round == 1 || b.round == 3 || b.round == 5) {
		for side := range b.units {
			for _, x := range b.pickN(b.frontOf(side), b.round) {
				b.setStatus(x, stImmune, 0, 1, false, 5321, slotBuff)
			}
		}
	}
	if b.factor(LxBlow) {
		b.lxShuffle(LxBlow)
	}
}

// lxRoundEnd：回合结束（各单位回合结束神通之后、状态计时之前）——火、伤、疗、乱。
func (b *battle) lxRoundEnd() {
	lv := min(b.round, 10)
	for side := range b.units {
		for _, x := range b.frontOf(side) {
			if b.factor(LxFire) {
				b.addBurnStack(x, poison{pct: 2 * lv, left: maxRounds, buff: int32(5020 + lv), burn: true}) // 燃烧X：每回合损失上限 2X%
			}
			if b.factor(LxHunt) {
				b.applyBuff(x, x, int32(5060+lv)) // 会猎X
			}
			if b.factor(LxHeal) && x.alive() {
				b.heal(x, 0, x.maxHP*int64(LxFactorPct[LxHeal])/100)
			}
		}
	}
	if b.factor(LxShuffle) {
		b.lxShuffle(LxShuffle)
	}
}

// lxShuffle：每位前阵存活护法按几率与本方后阵随机一名存活护法换位（乱阵 / 吹）。
func (b *battle) lxShuffle(id int) {
	for side := range b.units {
		for _, x := range b.frontOf(side) {
			if bn := b.bench(side); len(bn) > 0 && x.alive() && x.front() && b.chance(LxFactorPct[id]) {
				b.swap(x, bn[b.rng.IntN(len(bn))])
			}
		}
	}
}

// lxBlast：炸——t 阵亡时，同一方相邻的前阵护法流失 t 上限 20% 的生命。
func (b *battle) lxBlast(t *unit) {
	if !b.factor(LxBlast) || b.over {
		return
	}
	for _, x := range b.adjacent(t) {
		b.loseHP(nil, x, 0, t.maxHP*int64(LxFactorPct[LxBlast])/100)
	}
}
