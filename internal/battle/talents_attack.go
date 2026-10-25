package battle

// 攻击前 / 攻击后 / 受击触发的神通。

func init() {
	register(
		famXianYi, famXianDi, famRenHuang, famLingYou, famRanHun, famTuXi, famPoYan, famQuLei, famTaLang, famDaoHuo, famBeiShui, famShiQiang, famYuBing, famTingWei, famLianJi, famLuanWu,
		famHuiSao, famZhuangJi, famXueQi, famKuangRe, famPaoXiao, famWuDao, famDuZhao, famHuaXue, famRuoHua, famHuiLie, famHuTi, famNiShi, famFanJi, famRenSheng, famMoSha, famShouWang,
		famZhenYan, famXueRuo, famBiXue, famMiaoShou, famXianZun, famShouYao, famMoZhu, famYiTai, famTaChao, famLongXi, famQuZhu, famPoWang, famMieFa,
		famQuMo, famZhuXian, famDouShou, famShouXing, famYiMingYanTian, famJiuWeiXianChe, famSheHunShu, famFuZhong, famTanChi, famXiangXing, famTianWeiJiangLin, famKuangLong, famHengChongZhiZhuang,
		famHuoPaoZhiShu, famShiHou,
	)
}

// raceBane：攻击某族护法有 a% 概率额外提升 b% 初始攻击力（攻击后清除）。
var raceBane = []struct {
	fam  family
	race int
}{{famQuMo, raceGhost}, {famZhuXian, raceGod}, {famDouShou, raceBeast}, {famShouXing, raceHuman}}

// tuxi：突袭——攻击时 a% 概率提升当前攻击 50%（攻击后清除），并必中。
// 官方战报（session-02）：突袭加当前攻击的 50%（1192/1202）。
func (b *battle) tuxi(u *unit) {
	// 自带突袭 + 套装突袭：各自判定，都中就加两次（用户报：只触发一次）
	for _, k := range u.talAll(famTuXi) {
		if b.chance(k.param(0)) {
			b.tempUp(u, k, u.atkBase()/2+int64(float64(u.atkBase())*k.tjX()/100)) // 激发：额外 $x%，且不触发先攻、逆势
			b.buff(u, tuxiHitBuff, 1)                                             // 官方战报：突袭后挂「命中buff」5461（必中）
		}
	}
}

func (b *battle) beforeAttack(u, t *unit) {
	if k := u.tal(famXianDi); k != nil && b.chance(k.param(0)) {
		allies := b.allies(u)
		b.show(u, k.id, allies...)
		n := b.raceCount(u.side, raceGod) // 「本方每有一名仙族护法…（最高4名）」
		for _, x := range allies {
			b.heal(x, k.id, k.tjBoost(x.maxHP*int64(k.param(1))*n/100))
			b.wanxianSword(x) // 万仙来朝：「仙帝造成回复时」受回复者判定仙剑（满血没有实际回血也算，之前满血时永远拿不到剑）
		}
	}
	if k := u.tal(famRenHuang); k != nil && b.chance(k.param(0)) {
		b.show(u, k.id, u)
		b.addShield(u, u.maxHP*int64(k.param(1))*b.raceCount(u.side, raceHuman)/100, false)
	}
	if k := u.tal(famLingYou); k != nil {
		if x := b.mostHurt(u.side); x != nil && x.hp < x.maxHP {
			b.show(u, k.id, x)
			// 回复 a%、法术结界 b% 的初始攻击。官方战报（session-02，敌方 30;15 / 40;20 / 50;25 / 70;35）：
			// 结界恒为初始攻击 × b%，回复为初始攻击 × a%（截到缺血）；激发过的是 1.5 倍。
			b.heal(x, k.id, k.tjBoost(u.baseAtt*int64(k.param(0))/100))
			b.addShield(x, k.tjBoost(u.baseAtt*int64(k.param(1))/100), true)
		}
	}
	if k := u.tal(famRanHun); k != nil && b.chance(k.param(0)) {
		up := u.att * int64(k.param(1)) / 100
		// 官方战报：ACT_DO(燃魂 → 自己) → ATT_CHANGE → SUB_HP(燃魂，自己) → BLOODPOOL_IN{自己, 蓄入量}
		b.tempUp(u, k, up)
		in := b.loseHP(u, u, k.id, max(min(u.att/2, u.hp-5), 0)) // 生命流失，最多扣到 5（FAQ）；失去的生命蓄入本方血池
		in += int64(float64(u.att) * k.tjX() / 100)              // 激发：额外蓄入当前攻击 $x%
		b.pool[u.side] += in
		b.poolBy[u.side], b.poolID[u.side] = u, k.id
		if in > 0 {
			b.w.u8(CmdBloodPoolIn)
			b.w.u8(u.Pos)
			b.w.u32(in)
		}
	}
	if k := u.tal(famRenSheng); k != nil {
		n := b.raceCount(u.side, raceHuman)
		if b.chance(k.param(0) * int(n)) {
			foes := b.foes(u)
			b.show(u, k.id, foes...)
			blind := int64(20) // buff 5042 盲目2：攻击有 20% 几率无法命中
			for _, x := range foes {
				b.addStatus(x, stBlind, blind, 1, true, int32(k.param(2)), slotDebuff)
				if k.tjOn() && b.chance(k.tjPct()) {
					b.addStatus(x, stSlow, 4, 1, false, 5331, slotDebuff) // BUFF 5331「凝滞」
				}
			}
		}
	}
	if k := u.tal(famMoSha); k != nil && b.chance(k.param(0)) {
		b.tempUp(u, k, u.baseAtt*int64(k.param(1))*b.raceCount(u.side, raceGhost)/100)
		u.attachSoul = true // 攻击附带锢魂：打中后才挂（attackEffects），闪避不挂
	}
	if k := u.tal(famXianZun); k != nil && b.chance(k.param(0)) {
		foes := b.foes(u)
		b.show(u, k.id, foes...)
		for _, x := range foes { // 群体道属性伤害：每名仙族护法 b% 攻击
			b.propertyHurt(u, x, k.id, k.tjBoost(u.att*int64(k.param(1))*b.raceCount(u.side, raceGod)/100), 0)
		}
		if !t.alive() {
			return
		}
	}
	if k := u.tal(famShouYao); k != nil && b.chance(k.param(0)) {
		b.tempUp(u, k, u.baseAtt*int64(k.param(1))*b.raceCount(u.side, raceBeast)/100)
		u.attachNoHeal = 5086 // 攻击附带禁疗（FAQ：禁疗6 = 回复降低 60%，持续 5 回合；param(2) 5451「带禁疗」是攻击方的标记）：同魔煞，打中后才挂
	}
	if k := u.tal(famMoZhu); k != nil && b.chance(k.param(0)) {
		b.show(u, k.id, u)
		got := b.heal(u, k.id, u.maxHP*int64(k.param(1))*b.raceCount(u.side, raceGhost)/100)
		if k.tjOn() && got > 0 {
			b.changeMaxHP(u, k.id, int64(float64(got)*k.tjX()/100))
		}
	}
	// 异态：「伤害提升 a%」是伤害提高，不是加攻击——结算伤害时乘（用户确认，同灭法）；触发时看应攻击的目标，之后与
	// 实际受击者无关（FAQ）。
	if k := u.tal(famYiTai); k != nil && (t.hasDebuff() || t.mshield > 0) {
		b.show(u, k.id, u)
		u.dmgUp += k.param(0)
	} else if u.huweiLv > 0 && (t.hasDebuff() || t.mshield > 0) && b.chance(u.huweiLv*6) {
		if k := newTalent(levelID(famYiTai, 4)); k != nil { // 狐尾：攻击时 (x×6)% 概率触发异态4
			b.show(u, k.id, u)
			u.dmgUp += k.param(0)
		}
	}
	for _, k := range u.talAll(famTaChao) {
		if b.chance(k.param(0)) {
			b.tempUp(u, k, u.baseAtt*int64(k.param(1))/100) // 「提升 60% 初始攻击力」
		}
	}
	if k := u.tal(famShouWang); k != nil && b.chance(k.param(0)) {
		b.show(u, k.id, u)
		b.addShield(u, u.maxHP*int64(k.param(1))*b.raceCount(u.side, raceBeast)/100, true)
	}
	// 突袭、驱雷 / 踏浪 / 破岩 / 蹈火放在「攻击之前」类（仙帝、人皇、人圣、仙尊、魔主、兽王…）之后、「选取目标」类
	// （背水、霆威…）之前：官方战报魔主在突袭之前、背水 / 霆威 / 分击在突袭之后。仙尊等群体伤害插在突袭和普攻
	// 之间会打断突袭的出手动画。
	b.tuxi(u)
	// 下面这些加攻类：自带 + 套装同族各自判定（同突袭，用户规则）
	for _, fam := range []family{famPoYan, famQuLei, famTaLang, famDaoHuo} { // 照描述加初始攻击的 50%
		for _, k := range u.talAll(fam) {
			if b.chance(k.param(0)) {
				b.tempUp(u, k, u.baseAtt/2)
			}
		}
	}
	for _, k := range u.talAll(famBeiShui) {
		if u.hp < t.hp && b.chance(k.param(0)) {
			b.tempUp(u, k, u.baseAtt*int64(k.param(1))/100)
		}
	}
	for _, k := range u.talAll(famShiQiang) {
		if u.att > t.att && b.chance(k.param(0)) {
			b.tempUp(u, k, u.baseAtt*int64(k.param(1))/100)
		}
	}
	for _, k := range u.talAll(famYuBing) {
		if b.chance(k.param(0)) {
			b.tempUp(u, k, u.atkBase()*int64(k.param(1))/100+int64(float64(u.atkBase())*k.tjX()/100))
		}
	}
	if k := u.tal(famTingWei); k != nil && t.maxHP > 0 && b.chance(k.param(0)) {
		b.tempUp(u, k, u.atkBase()*(t.maxHP-t.hp)/t.maxHP)
		// 目标濒危（< 25%）时 b% 概率一击必杀（摧毁，不受免疫伤害影响），激发再加 $x%（FAQ）
		if t.hp*100 < t.maxHP*25 && b.chance(k.param(1)+k.tjPct()) {
			b.destroy(u, t, k.id)
			if !t.alive() {
				return
			}
		}
	}
	if k := u.tal(famPoWang); k != nil {
		// 攻击前各有 a% 概率将对面三位护法生命上限恢复到进入战斗的初始值
		//（Fighter.MaxHP；changeMaxHP 会把超出上限的当前生命一并压回）。
		ts := b.facing3(u) // 对面三位 = 对位及其左右（锁定站位）
		var hit []*unit
		for _, x := range ts {
			if b.chance(k.param(0)) {
				hit = append(hit, x)
			}
		}
		if len(hit) > 0 {
			b.show(u, k.id, hit...)
			for _, x := range hit {
				if d := int64(x.MaxHP) - x.maxHP; d != 0 {
					b.changeMaxHP(x, k.id, d)
				}
			}
		}
	}
	if k := u.tal(famMieFa); k != nil && (t.tal(famXianYi) != nil || t.tal(famBuQin) != nil) {
		// 攻击时对具备仙衣 / 不侵的目标伤害提高 a%，且有同等概率清除无敌。伤害提高不是加攻击：结算伤害时乘
		// （attack 里 attackTaken 之前，用户确认），界面上攻击不变。
		b.show(u, k.id, u)
		u.dmgUp += k.param(0)
		if b.invincible(t) && b.chance(k.param(0)) { // 无敌、诈死的本回合免疫都清掉
			b.clearStatus(t, stImmune)
			b.clearStatus(t, stZhasi)
		}
	}
	if k := u.tal(famHengChongZhiZhuang); k != nil && k.tjOn() {
		b.tempUp(u, k, int64(float64(u.atkBase())*k.tjX()/100))
	}
	for _, rb := range raceBane {
		for _, k := range u.talAll(rb.fam) {
			if t.Race == rb.race && b.chance(k.param(0)) {
				b.tempUp(u, k, u.baseAtt*int64(k.param(1))/100)
			}
		}
	}
	if s := u.has(stYizhi); s != nil && t.Race == raceGhost { // 遗志：目标是魔族时额外提升
		n := u.att * s.val / 100
		b.changeAtt(u, 0, n)
		u.tempAtt += n
	}
	if k := u.tal(famYiMingYanTian); k != nil {
		// 全体前阵护法获得神之护3（+9% 生命上限，每次都加）；a% 概率使攻击力最高者获得战呼3。
		b.shenhu(u, k, 5213)
		if k.tjOn() {
			for _, x := range b.allies(u) {
				if b.chance(15 + k.tjPct()) {
					b.grantRound(x, levelID(famFenShui, 3))
					b.grantRound(x, levelID(famBaoJi, 3))
				}
			}
		}
		if b.chance(k.param(0)) {
			allies := b.allies(u)
			sortByAtt(allies)
			if len(allies) > 0 {
				b.show(u, k.id, allies[0])
				b.zhanhu(allies[0], 5263)
			}
		}
	}
	if k := u.tal(famJiuWeiXianChe); k != nil {
		// 全体前阵护法获得狐尾（buff 参数 1，含神之护3）；双方战意差为 d 时 (120 - d×等级)% 概率触发咆哮10（战意 +5）。
		b.shenhu(u, k, int32(k.param(0)))
		// 战意差 = 我方 − 敌方，负数按 0（FAQ）
		if b.chance(120 - max(b.zy[u.side]-b.zy[1-u.side], 0)*k.param(1)) {
			b.show(u, k.id, u)
			b.addZY(u.side, 5)
		}
	}
	if k := u.tal(famSheHunShu); k != nil && len(u.borrowed) < 4 {
		// 己方前排随机一名已死亡护法的第 1、2 神通各 a% 概率被吸取为临时神通（至多 4 个，死亡时移除）。
		var dead []*unit
		for _, x := range b.units[u.side] {
			if !x.alive() && x.front() {
				dead = append(dead, x)
			}
		}
		if len(dead) > 0 {
			x := dead[b.rng.IntN(len(dead))]
			for i := 0; i < 2 && i < len(x.Talents) && len(u.borrowed) < 4; i++ {
				g := newTalent(x.Talents[i])
				if g == nil || u.tals[g.fam] != nil || !b.chance(k.param(0)) {
					continue
				}
				b.show(u, k.id, x)
				u.tals[g.fam] = g
				u.borrowed = append(u.borrowed, g.fam)
				if k.tjOn() {
					d := int64(float64(u.maxHP) * k.tjX() / 100)
					b.changeMaxHP(u, k.id, d)
					b.heal(u, k.id, d)
				}
			}
		}
	}
}

// shenhu 给本方前阵护法上神之护3（+9% 生命上限，每次触发都加，可叠加）；九尾仙车的狐尾每次触发都给
// （新的替换旧的，FAQ）。
func (b *battle) shenhu(u *unit, k *talent, buff int32) {
	ts := b.allies(u)
	if len(ts) == 0 {
		return
	}
	// 官方战报（九尾仙车）：ACT_DO 指向自己；每人依次 BUFF 5213（神之护3）→ CHG_MAXHP(5213) → ADD_HP(5213)
	// → 狐尾 BUFF（九尾仙车，buff 参数 1）。
	b.show(u, k.id, u)
	for _, x := range ts {
		grow := x.maxHP * 9 / 100
		b.buff(x, shenhuBuff, 13)
		b.changeMaxHP(x, shenhuBuff, grow)
		b.heal(x, shenhuBuff, grow)
		if buff != shenhuBuff {
			b.huwei(u, k, x, buff)
		}
	}
}

// huwei：九尾仙车给 x 挂狐尾 x 级（BUFF 5641-5644，新的替换旧的）。狐尾 x（FAQ）：攻击时 (x×6)% 概率触发异态4；
// 生命上限受到减益时最少保留 (x×x/2 + 8)%（以进入战斗时的上限为准）；天将：保留比例再加
// （本方带暴走神通的护法数，前后阵都算，至多 4 名）× 激发等级 %。死亡时清除。
func (b *battle) huwei(u *unit, k *talent, x *unit, buff int32) {
	lv := int(buff - 5640)
	if lv < 1 || lv > 4 {
		return
	}
	if x.huweiBuff != 0 && x.huweiBuff != buff {
		b.rmBuff(x, x.huweiBuff)
	}
	b.buff(x, buff, 13)
	floor := lv*lv/2 + 8
	if k.tjOn() {
		n := 0
		for _, y := range b.units[u.side] {
			if !y.gone && hasAny(y, rampageTalents) {
				n++
			}
		}
		floor += min(n, 4) * k.tj
	}
	x.huweiBuff, x.huweiLv, x.huweiFloor = buff, lv, floor
}

const shenhuBuff = 5213 // 神之护3

// wanxianSword：万仙来朝——本方仙帝回复时 b% 概率使受回复者获得一柄仙剑，已有 5 柄则攻击提高 c%。
func (b *battle) wanxianSword(x *unit) {
	k := b.wanxian(x.side)
	if k == nil || !b.chance(k.param(1)) {
		return
	}
	for _, c := range b.allies(x) {
		if c.tal(famWanXianLaiChao) != nil {
			b.show(c, k.id, x)
			break
		}
	}
	if !b.giveSword(x) {
		b.changeAtt(x, k.id, x.att*int64(k.param(2))/100)
	}
}

// roarAgain：狂龙——咆哮、误导有 a% 概率额外触发一次。
func (b *battle) roarAgain(u *unit) int {
	if k := u.tal(famKuangLong); k != nil && b.chance(k.param(0)) {
		b.show(u, k.id, u)
		return 1
	}
	return 0
}

// followUps 是主攻击之后的追加攻击（每次都是一次新的攻击行为 atkFollow，可触发攻击时的神通，但不再产生追加攻击）。
// 连击 / 乱舞的目标阵亡后不再继续（三板斧另算，见 talents_charge.go）。
func (b *battle) followUps(u, t *unit, dmg int64) {
	// cur：追加攻击的目标。原目标活着但已离开前阵（被驱逐 / 脱帽换下等）时改打当前对位（用户报：驱逐后乱舞还打原护法）。
	cur := func() *unit {
		if t.alive() && !t.front() {
			if x := b.pickTarget(u); x != nil {
				t = x
			}
		}
		return t
	}
	if k := u.tal(famLianJi); k != nil && cur().alive() && b.chance(k.param(0)) {
		b.show(u, k.id, u)
		b.attackAction(u, cur(), max(k.param(1), 1), atkFollow)
	}
	if k := u.tal(famLuanWu); k != nil && cur().alive() && u.alive() && b.chance(k.param(0)) {
		b.show(u, k.id, u)
		for range 2 {
			if !cur().alive() || !u.alive() || b.over {
				break
			}
			b.attackAction(u, cur(), k.param(1), atkFollow)
		}
	}
	if k := u.tal(famHengChongZhiZhuang); k != nil {
		// 击败目标后 25%×等级 概率追击随机目标，每回合至多「等级」次；追击又击败目标时继续判定（连续追击）。
		// 「击败」按这一击有没有造成阵亡算：目标被神佑 / 死亡技当场复活也算击败（FAQ：敌方复活时也计算为一次击败）。
		for killed := u.mainKill; u.alive() && !b.over && killed && u.perRound[0] < k.param(0) && b.chance(25*k.param(0)); {
			foes := b.attackable(u)
			if len(foes) == 0 {
				break
			}
			u.perRound[0]++
			x := foes[b.rng.IntN(len(foes))]
			b.show(u, k.id, u)
			u.lastKill = false
			b.attackAction(u, x, 100, atkFollow)
			killed = u.lastKill
		}
	}
	// 修罗隐身激发：可连续触发（图鉴 FAQ）
	for k := u.tal(famXiuLuoYinShen); k != nil && k.tjOn() && cur().alive() && u.alive() && !b.over && u.xiuluo > 0 &&
		b.chance(5*k.tj+15*u.xiuluo); {
		b.setXiuluo(u, u.xiuluo-1)
		b.show(u, k.id, u)
		b.attackAction(u, cur(), 100, atkFollow)
	}
	if u.extraAtk { // 天地灵气
		u.extraAtk = false
		if k := u.tal(famTianDiLingQi); k != nil && b.chance(k.param(0)) {
			x := cur()
			if !x.alive() {
				x = b.pickTarget(u)
			}
			if x != nil {
				b.show(u, k.id, u)
				b.attackAction(u, x, 100, atkFollow)
			}
		}
	}
}

// launchEffects 是「发动攻击」时机的神通（talentset trigger：挥扫、撞击；化血在 attackEffects、分击在
// attackAction 里），每次攻击行为都判定，三板斧、轩辕剑的每一击也触发。
func (b *battle) launchEffects(u, t *unit, dmg int64) {
	if k := u.tal(famHuiSao); k != nil && u.atkLanded && b.chance(k.param(0)) { // 命中就触发：打无敌目标时相邻照样受伤
		if adj := b.adjacent(splashCenter(u, t)); len(adj) > 0 { // 原攻击目标的左右（被守卫代挨时也是）
			b.show(u, k.id, adj...)
			for _, x := range adj {
				b.hurt(u, x, k.id, k.tjBoost(u.att*int64(k.param(1))/100), true) // 抓包：自身攻击 × b%
			}
		}
	}
	if k := u.tal(famZhuangJi); k != nil && t.alive() && b.chance(k.param(0)) {
		cost := u.maxHP * int64(k.param(1)) / 100 // 扣除自身血量时可以导致阵亡（FAQ）
		b.show(u, k.id, t)
		b.hurt(u, u, k.id, cost, false)
		b.hurt(u, t, k.id, cost*2, true) // 第二段：物理伤害（非攻击行为）
	}
}

// attachDebuffs：先加状态再结算伤害的一组——魔煞锢魂、兽妖禁疗（「攻击前…使攻击附带」）和驱雷 / 踏浪 / 破岩 / 蹈火的
// 状态，命中后、扣血之前挂到实际受击者身上（被闪避不挂；这一下打死目标时锢魂照样生效）。削弱、毒爪、弱化、
// 会猎、象形是「攻击后」，在扣血之后，见 attackEffects。
func (b *battle) attachDebuffs(u, t *unit) {
	if t.side != u.side && t.alive() {
		if u.attachSoul {
			b.addStatus(t, stNoSoul, 0, maxRounds, false, 5141, slotDebuff) // 锢魂：无法被复活（BUFF 5141）
		}
		if u.attachNoHeal != 0 {
			b.addStatus(t, stNoHeal, 60, noHealRounds, false, u.attachNoHeal, slotDebuff)
		}
	}
	u.attachSoul, u.attachNoHeal = false, 0
}

// elemAttacks：替换攻击类（驱雷 / 踏浪 / 破岩 / 蹈火，21571–21601）发动时附带的状态——驱雷 麻痹、踏浪 僵化（下次行动后
// 清除）、破岩 毒砂5、蹈火 燃烧5（持续到战斗结束）。官方战报：破岩8 命中后 BUFF 5035。
var elemAttacks = []struct {
	fam  family
	buff int32
}{{famQuLei, 5011}, {famTaLang, 5001}, {famPoYan, 5035}, {famDaoHuo, 5025}}

// elemFired：这次攻击行为是替换攻击类的属性攻击。
func elemFired(u *unit) bool {
	for _, e := range elemAttacks {
		if u.fired[e.fam] > 0 {
			return true
		}
	}
	return false
}

// elemDebuffs：属性攻击先挂状态（在先攻、闪避、伤害之前，用户规则）；可被分水 / 逆雷 / 辟土 / 返火、法术结界、不侵免疫。
func (b *battle) elemDebuffs(u, t *unit) {
	for _, e := range elemAttacks {
		if u.fired[e.fam] > 0 && t.alive() {
			b.applyBuff(u, t, e.buff)
		}
	}
}

// splashCenter 是挥扫、负重、贪吃这类「对目标相邻单位」效果的中心：这次攻击行为的原攻击目标（被守卫 / 元形代挨时
// 不是代挨的人）；原目标已不在敌方阵上时退回实际受击者 t。
func splashCenter(u, t *unit) *unit {
	if a := u.atkTarget; a != nil && a.side == t.side && a.front() {
		return a
	}
	return t
}

// selfAfterAttack 是攻击后作用于自身 / 全场的神通（每次攻击行为都判定）。
func (b *battle) selfAfterAttack(u, t *unit, dmg int64) {
	if k := u.tal(famXueQi); k != nil && u.atkDealt > 0 {
		if x := b.mostHurt(u.side); x != nil {
			b.show(u, k.id, x)
			b.heal(x, k.id, u.atkDealt*int64(k.param(0))/100) // 按伤害值（不是扣血量）回复（FAQ）
			b.changeAtt(x, k.id, x.att*int64(k.param(1))/100)
		}
		// 血气激发：额外降低攻击目标 $x% 伤害值的生命上限，单次不超过血气护法当前上限 150%（FAQ）
		if tg := u.lastVictimOf(); k.tjOn() && tg != nil && tg.alive() && tg.side != u.side {
			b.changeMaxHP(tg, k.id, -min(int64(float64(u.atkDealt)*k.tjX()/100), u.maxHP*3/2))
		}
	}
	if k := u.tal(famKuangRe); k != nil && b.chance(k.param(0)) {
		b.show(u, k.id, u)
		b.changeAtt(u, k.id, u.baseAtt*int64(k.param(1))/100)
	}
	if k := u.tal(famPaoXiao); k != nil && b.chance(k.param(0)) {
		for range 1 + b.roarAgain(u) {
			b.show(u, k.id, u)
			b.addZY(u.side, k.param(1))
			b.roarTJ(u, k, false)
		}
	}
	if k := u.tal(famWuDao); k != nil && b.chance(k.param(0)) {
		for range 1 + b.roarAgain(u) {
			b.show(u, k.id, u)
			b.addZY(1-u.side, -k.param(1))
			b.roarTJ(u, k, true)
		}
	}
	if k := u.tal(famTianWeiJiangLin); k != nil { // 普攻被闪避也触发
		// 攻击后对敌我全体护法（含自己）造成当前攻击 a% 的物理伤害（官方战报：友方 = 攻击 40%，
		// 敌方再翻倍是激发「对敌伤害提升」；SUB_HP 里有武则天自己）。被打中的照常触发受击效果（坚韧、气甲在 hurt
		// 里，润泽见 runze）。
		var all []*unit
		for side := range b.units {
			all = append(all, b.frontOf(side)...)
		}
		if len(all) > 0 {
			b.show(u, k.id, all...)
			for _, x := range all {
				n := u.att * int64(k.param(0)) / 100
				if x.side != u.side {
					n = k.tjBoost(n) // 激发：对敌方伤害提升 $x%（20%×等级）
				} else {
					b.chargeUp(x) // 我方受到天威降临的伤害同样蓄力（hurt 只给敌方来源的伤害蓄力）
				}
				if x == u && b.invincible(u) {
					n = 0 // 打到自己也受无敌保护（hurt 对自身来源的伤害不看无敌）
				}
				if b.hurt(u, x, k.id, n, true) > 0 {
					b.runze(x)
				}
			}
		}
	}
	if k := u.tal(famHuoPaoZhiShu); k != nil && t.alive() {
		b.cannon(u, k, t)
	}
	for _, fam := range []family{famTanChi, famFuZhong} {
		// 攻击时各 a% 概率对目标相邻单位造成 b% 攻击力的物理伤害。
		// 贪吃：对目标及相邻单位伤害的 c% 回复自身；负重：目标及受伤的相邻单位获得嘲讽（buff 参数 3）。
		k := u.tal(fam)
		if k == nil || !u.alive() {
			continue
		}
		var hit []*unit
		for _, x := range b.adjacent(splashCenter(u, t)) {
			if b.chance(k.param(0)) {
				hit = append(hit, x)
			}
		}
		if len(hit) == 0 {
			continue
		}
		b.show(u, k.id, hit...)
		sum := u.atkDealt // 贪吃：对攻击目标按伤害值（同吸血），对相邻目标按将要造成的伤害计（FAQ）
		for _, x := range hit {
			n := u.att * int64(k.param(1)) / 100
			if fam == famFuZhong && k.tjOn() {
				b.loseHP(u, x, k.id, n) // 负重激发：对相邻目标转为生命流失
				continue
			}
			b.hurt(u, x, k.id, n, true) // 非攻击行为的物理伤害
			sum += n
		}
		if fam == famTanChi {
			want := sum * int64(k.param(2)) / 100
			got := b.heal(u, k.id, want)
			if k.tjOn() && want > got { // 激发：溢出的 $x% 回复本方重伤护法
				if x := b.mostHurt(u.side); x != nil {
					b.heal(x, k.id, int64(float64(want-got)*k.tjX()/100))
				}
			}
			continue
		}
		buff := int32(k.param(2))
		for _, x := range append([]*unit{t}, hit...) {
			if s := x.has(stTaunt); s != nil && s.buff >= 5471 && s.buff <= 5480 && s.src != nil && s.src.alive() {
				continue // 金身罗汉的嘲讽优先级高于至尊宝的（FAQ 负重）
			}
			if x.alive() && b.addStatus(x, stTaunt, int64(buff-5600)*10, 1, true, buff, slotDebuff) {
				x.status[stTaunt].src = u
				if k.tjOn() && b.chance(k.tjPct()) { // 激发：嘲讽者获得会猎3（受物理伤害 +30%）
					b.applyBuff(u, x, 5063) // 会猎3（BUFF 5063，与会猎同一状态，不叠加）
				}
			}
		}
	}
	if k := u.tal(famLongXi); k != nil && b.chance(k.param(1)) {
		foes := b.foes(u)
		b.show(u, k.id, foes...)
		for _, x := range foes {
			b.propertyHurt(u, x, k.id, u.att*int64(k.param(2))/100+int64(float64(u.att)*k.tjX()/100), 2)
		}
	}
	if k := u.tal(famZhenYan); k != nil && b.chance(k.param(1)) {
		foes := b.foes(u)
		b.show(u, k.id, foes...)
		for _, x := range foes {
			b.propertyHurt(u, x, k.id, u.att*int64(k.param(2))/100+int64(float64(u.att)*k.tjX()/100), 2) // 水属性
		}
	}
	if st := t; u.swords > 0 {
		// 飞剑打这次攻击原定的目标：守卫 / 元形代承之后目标指回原来的人
		if o := u.atkTarget; o != nil && o.alive() && o.side != u.side {
			st = o
		}
		// 混乱打了自己人、或这一下已把目标打死：飞剑改打当前对位（用户报：虞姬、薛仁贵打死人后自身仙剑不出手）
		if st.side == u.side || !st.alive() || !st.front() {
			st = b.pickTarget(u)
		}
		if !u.front() { // 攻击中被换到后阵（脱帽、驱逐）：仙剑不再出手
			st = nil
		}
		if st != nil && st.alive() {
			b.swordStrike(u, st)
		}
	}
	if k := u.tal(famBeiShui); k != nil && k.tjOn() && u.fired[famBeiShui] > 0 && dmg > 0 {
		b.addShield(u, int64(float64(dmg)*k.tjX()/100), false) // 背水激发：伤害量 $x% 的物理结界
	}
	if k := u.tal(famShiQiang); k != nil && k.tjOn() && u.fired[famShiQiang] > 0 && dmg > 0 {
		b.heal(u, k.id, int64(float64(dmg)*k.tjX()/100)) // 恃强激发：恢复伤害量的 $x%
	}
}

// attackEffects 是攻击特效：作用在实际承受这次攻击的单位 t 上（守卫 / 元形代挨时是护卫者；化血、弱化、削弱、
// 毒爪、会猎、象形、驱逐都随实际受击者走）。每次攻击行为都判定，包括三板斧、轩辕剑。
func (b *battle) attackEffects(u, t *unit, dmg int64) {
	if k := u.tal(famQuZhu); k != nil && t.alive() && t.front() && b.chance(k.param(0)) {
		if x := b.quzhuPick(t); x != nil {
			b.show(u, k.id, t)
			b.quzhu(t, x)
		}
	}
	if !t.alive() {
		return
	}
	// 「攻击后」给予的状态（用户规则：区分「攻击前附带」和「攻击后」——后者在伤害结算之后，且必须打中：被闪避不挂；
	// 加在实际承受攻击的目标上，FAQ 会猎）。
	hit := u.atkLanded && t.side != u.side
	if hit {
		if k := u.tal(famXueRuo); k != nil && t.has(stWeak) == nil && b.chance(k.param(1)) {
			cut := min(t.baseAtt*int64(k.param(2))/100, t.att)
			b.show(u, k.id, t)
			if b.addStatus(t, stWeak, cut, 1, true, k.Buff, slotDebuff) {
				b.changeAtt(t, k.id, -cut)
			}
		}
		if k := u.tal(famDuZhao); k != nil && b.chance(k.param(0)) && !b.showResist(u, k, t) {
			n := debuffCount(t)
			b.addPoison(t, poison{from: k.id, pct: k.param(1), left: 2, buff: k.Buff})
			if k.tjOn() && n > 0 { // 毒爪激发：每个其他负面状态失去 $x% 上限的生命（生命流失）
				b.loseHP(u, t, k.id, int64(float64(t.maxHP)*k.tjX()/100)*int64(n))
			}
		}
	}
	if !t.alive() {
		return
	}
	if k := u.tal(famHuaXue); k != nil && b.chance(k.param(0)) {
		// 目标上限 b% 的生命流失，不超过自身攻击 × c（官方战报：74852 = 18713×4）
		n := t.maxHP * int64(k.param(1)) / 100
		if c := k.param(2); c > 0 {
			n = min(n, u.att*int64(c))
		}
		b.show(u, k.id, t)
		b.loseHP(u, t, k.id, n) // 生命流失：不受结界、无敌、诈死影响（FAQ）
	}
	if hit && t.alive() {
		if k := u.tal(famRuoHua); k != nil && t.alive() && b.chance(k.param(0)) {
			b.show(u, k.id, t)
			cut := t.att * int64(k.param(1)) / 100
			b.changeAtt(t, k.id, -cut)
			if k.tjOn() && b.chance(k.tjPct()) {
				b.changeAtt(u, k.id, cut)
			}
		}
		if k := u.tal(famHuiLie); k != nil && t.alive() && b.chance(k.param(0)) {
			// 官方战报：ACT_DO(会猎 → 目标) → BUFF 506x「会猎」（持续到战斗结束）；可被法术结界 / 不侵免疫
			b.show(u, k.id, t)
			b.applyBuff(u, t, k.Buff)
		}
		if k := u.tal(famXiangXing); k != nil && t.alive() && b.chance(k.param(0)) {
			// 令目标消除等同于当前攻击力的法术结界并获得捆缚，捆缚后吸取对方 b% 攻击力
			b.show(u, k.id, t)
			// FAQ：先清除法术结界再添加捆缚；结界没清完则捆缚被免疫，但依然吸取攻击力。
			b.cutShield(t, u.att, true)
			b.addStatus(t, stBind, 0, 1, true, k.Buff, slotDebuff)
			d := t.att * int64(k.param(1)) / 100
			b.changeAtt(t, k.id, -d)
			b.changeAtt(u, k.id, k.tjBoost(d))
		}
	}
}

// debuffCount 是 t 身上的负面效果数（中毒算 1 个）。
func debuffCount(t *unit) int {
	n := 0
	if len(t.poison) > 0 {
		n++
	}
	for _, s := range t.status {
		if s.foe && s.left > 0 {
			n++
		}
	}
	if t.dusha > 0 {
		n++
	}
	if t.huilie > 0 {
		n++
	}
	return n
}

// roarTJ：咆哮 / 误导激发——$x% 概率提升本方前阵 b% 攻击（咆哮）/ 生命上限（误导）；狂龙激发：额外回复自身 $x% 生命。
func (b *battle) roarTJ(u *unit, k *talent, maxHP bool) {
	if k.tjOn() && b.chance(k.tjPct()) {
		pct := k.tjParam(1)
		for _, x := range b.allies(u) {
			if maxHP {
				b.changeMaxHP(x, k.id, int64(float64(x.maxHP)*pct/100))
			} else {
				b.changeAtt(x, k.id, int64(float64(x.att)*pct/100))
			}
		}
	}
	if kl := u.tal(famKuangLong); kl != nil && kl.tjOn() {
		b.heal(u, kl.id, int64(float64(u.maxHP)*kl.tjX()/100))
	}
}

// cannon：火炮之术——对目标发射一颗不稳定弹丸，造成 a% 初始攻击力的生命流失。
func (b *battle) cannon(u *unit, k *talent, t *unit) {
	if u.pellets <= 0 || !u.alive() {
		return
	}
	u.pellets--
	b.pelletCount(u)
	b.show(u, k.id, t)
	b.loseHP(u, t, k.id, u.baseAtt*int64(k.param(0))/100)
}

// onHit：t 被 u 的攻击行为命中后（t 还活着）。
func (b *battle) onHit(u, t *unit, dmg int64, kind atkKind) {
	b.hanbingStiff(u, t)
	if k := t.tal(famHuTi); k != nil && dmg > 0 && b.chance(k.param(0)) {
		b.show(t, k.id, u)
		n := dmg*int64(k.param(1))/100 + int64(float64(dmg)*k.tjX()/100) // 激发：额外 $x% 原伤害
		b.hurt(t, u, k.id, min(n, t.maxHP), true)                        // 物理伤害，不超过护体护法当前上限（FAQ）
	}
	if k := t.tal(famNiShi); k != nil && u.alive() && !u.tuxiTJ() && b.chance(k.param(0)) {
		ts := append([]*unit{u}, b.adjacent(u)...)
		b.show(t, k.id, ts...)
		for _, x := range ts {
			b.propertyHurt(t, x, k.id, t.maxHP*int64(k.param(1))/100+int64(float64(t.att)*k.tjX()/100), 0) // 激发：+$x% 当前攻击
		}
	}
	if k := t.tal(famBiXue); k != nil && b.chance(k.param(0)) {
		if x := b.mostHurtExcept(t); x != nil { // 「为队友回复」：前阵其他友方，不含自己
			b.show(t, k.id, x)
			n := t.maxHP * int64(k.param(1)) / 100 // b% × 自身生命上限
			if k.tjOn() && b.chance(k.tjPct()) {
				n *= 2 // 激发：$x% 概率双倍回复
			}
			b.heal(x, k.id, n)
		}
	}
	if k := t.tal(famMiaoShou); k != nil {
		if x := b.mostHurt(t.side); x != nil && x.hp < x.maxHP {
			b.show(t, k.id, x)
			got := b.heal(x, k.id, t.att*int64(k.param(0))/100) // $p = a% 攻击
			if k.tjOn() {
				b.addShield(x, int64(float64(got)*k.tjY()/100), false) // 激发：目标获得回复值 $y% 的物理结界
			}
		}
	}
	if k := t.tal(famShiHou); k != nil && t.alive() && b.chance(k.param(0)) {
		// 吸取攻击者相邻护法的法术结界（每人至多文殊上限 10%）并令其混乱，混乱后吸取其 b% 生命上限
		if adj := b.adjacent(u); len(adj) > 0 {
			b.show(t, k.id, adj...)
			for _, x := range adj {
				b.addShield(t, b.cutShield(x, t.maxHP*10/100, true), true)
				if b.addStatus(x, stConfuse, 0, 1, true, 5051, slotDebuff) {
					d := min(x.maxHP*int64(k.param(1))/100, x.maxHP-1)
					b.changeMaxHP(x, k.id, -d)
					b.changeMaxHP(t, k.id, k.tjBoost(d))
				}
			}
		}
	}
	if k := t.tal(famCangJianYuHua); k != nil && k.tjOn() && b.chance(int(k.tjW())) {
		if allies := b.allies(t); len(allies) > 0 {
			x := allies[b.rng.IntN(len(allies))]
			b.show(t, k.id, x)
			b.giveSword(x)
		}
	}
	// 反击的攻击行为不能再触发反击（FAQ）；先攻的那一下也不触发反击——FAQ 允许反击触发先攻，两者都放开会无限互相触发
	if k := t.tal(famFanJi); k != nil && kind != atkCounter && kind != atkPre && u.alive() && t.alive() && b.chance(k.param(0)) {
		b.show(t, k.id, t)
		b.attackAction(t, u, max(k.param(1), 1), atkCounter)
	}
}

// showResist 演出 u 对 t 发动 k，并判 t 是否以不侵抵抗（不经 addStatus 的持续效果：毒爪、会猎）。
func (b *battle) showResist(u *unit, k *talent, t *unit) bool {
	b.show(u, k.id, t)
	return b.resist(t, stWeak) // 法术结界 / 不侵
}
