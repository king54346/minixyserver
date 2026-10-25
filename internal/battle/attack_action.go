package battle

// 攻击行为（图鉴 FAQ：乱舞「产生三次[攻击行为]，可独立触发该时机下的其他神通」，连击同理；反击「视同进行一次
// [攻击行为]…无法触发[反击]，但可以触发[先攻]」；轩辕剑 / 枪出游龙 / 三板斧的每一击「均视为[攻击行为]」）。
//
// 一次攻击行为（attackAction）按顺序经过这些时机：
//
//  1. 攻击时（beforeAttack）：出手前判定，多是给自己加临时攻击 / 改这一下的性质。
//     暴击激发、背水、突袭类（异态、驱雷、踏浪、道火、破焰、灭法、破妄、魔煞、魔主、兽妖、兽王、人皇、人圣、
//     仙尊、仙帝、仙衣……）、横冲直撞、九尾仙车、以命演天、摄魂术等。
//  2. 发动攻击：分击——确定第二个目标并按 a% 攻击力出手（与主攻击同一下结算，主攻击被闪避也照打）。
//  3. 命中（attack）：先攻 → 闪避 / 盲目 → 暴击 → 守卫 / 护盾 / 受击减伤 → 扣血。
//  4. 攻击特效（attackEffects，作用于实际受击者）：化血、毒爪、会猎、象形、驱逐、弱化、血弱、驱雷 / 踏浪 / 道火 / 破焰的附加状态。
//  5. 命中后溅射（launchEffects，命中就触发，目标无敌也触发）：挥扫、撞击。
//  6. 攻击后（selfAfterAttack，作用于自身 / 全场）：狂热、咆哮、血气、武道、背水、龙息、阵眼、负重、贪吃、
//     天威降临、火炮之术、仙剑攻击等。
//  7. 追加攻击（followUps，只由主攻击产生，避免连锁）：连击、乱舞、横冲直撞、修罗隐身、天地灵气。
//  8. 结束：清除这次攻击行为加的临时攻击力（「攻击后清除」；主攻击时魔煞 / 兽妖激发保留一部分）。
//
// 攻击种类只决定第 7 步和先攻 / 反击能不能触发，1–6 步每次攻击行为都走：
//
//	种类         用在                                                               追加攻击  先攻  反击
//	atkMain     普通攻击                                                             ✓        ✓     ✓
//	atkFollow   连击、乱舞、横冲直撞、修罗隐身、天地灵气、枪出游龙二连击、三板斧三连乱击、轩辕剑三连击  ✗        ✓     ✓
//	atkCounter  反击（蓄力已满先释放蓄力神通，同先攻）                                       ✗        ✓     ✗
//	atkPre      先攻（先攻时蓄力已满先释放蓄力神通，如脱帽退敌、轩辕剑）                      ✗        ✗     ✓
type atkKind int

// maxAtkDepth：攻击行为嵌套（追加攻击 → 先攻 → 反击 …）的上限，防止神通互相触发无限递归。
const maxAtkDepth = 6

const (
	atkMain atkKind = iota
	atkFollow
	atkCounter
	atkPre
)

// attackAction 让 u 对 t 进行一次攻击行为（pct = 攻击力百分比），返回造成的伤害。
func (b *battle) attackAction(u, t *unit, pct int, kind atkKind) int64 {
	if t == nil || u == nil || !t.alive() || !u.alive() || b.over || b.atkDepth >= maxAtkDepth {
		return 0
	}
	if t.side != u.side && t.has(stHidden) != nil {
		return 0 // 隐身：无法成为物理攻击目标（反击、先攻、追击都打不到）
	}
	b.atkDepth++
	defer func() { b.atkDepth-- }()
	// 出手前的神通（脱帽退敌、驱逐类）把前阵的目标换到了后阵：改打现在的对位，不追到后阵去打（用户报：薛仁贵
	// 脱帽把对位换下去后还追到后阵把它打死）。原本就打后阵的（少数神通）不改。
	enemyFront := t.side != u.side && t.front()
	retarget := func() bool {
		if enemyFront && !t.front() {
			if t = b.pickTarget(u); t == nil {
				return false
			}
		}
		return t.alive()
	}
	if kind == atkPre || kind == atkCounter {
		if k := chargeTalent(u); k != nil && chargeNeed(k) > 0 && u.charge >= chargeNeed(k) {
			b.releaseCharge(u, k) // 先攻 / 反击时蓄力已满：先释放蓄力神通（如脱帽退敌）
			if !u.alive() || b.over || !retarget() {
				return 0
			}
		}
	}
	temp0, fired0, epoch0 := u.tempAtt, u.fired, u.tempEpoch
	u.fired = nil
	prevBase, prevUp := u.tempBase, u.dmgUp
	u.tempBase = temp0 // 外层攻击行为（主攻击）的临时加攻不算进这次的加攻基数，见 atkBase
	u.dmgUp = 0        // 伤害提高只管这一次攻击行为（追加攻击各自判定，不叠外层的）
	defer func() { u.tempBase, u.dmgUp = prevBase, prevUp }()
	split := (*talent)(nil)
	b.beforeAttack(u, t)
	if !u.alive() || b.over || !retarget() {
		b.endAction(u, temp0, fired0, epoch0, kind)
		return 0
	}
	// 分击是「发动攻击」时机（在攻击时神通之后、命中之前）：每次攻击行为都触发。
	if split = u.tal(famFenJi); split != nil {
		// 分击：「攻击时以 a% 攻击力对目标及敌方先攻值最高护法造成伤害」——第二个目标在命中时同一下结算
		//（attack 里主攻击扣血之后），伤害取主攻击的出手值。敌方只剩一个时第二下也打它。
		pct = pct * (split.param(0) + split.tjPct()) / 100
		// 第二下打敌方先攻值最高的护法，可以就是主目标本人（同一人挨两下；与下凡必须两个不同的人不同）
		u.splitTo, u.splitID = nil, split.id
		for _, x := range b.foes(u) {
			if x.has(stHidden) == nil && (u.splitTo == nil || x.speed() > u.splitTo.speed()) {
				u.splitTo = x
			}
		}
		if u.splitTo == nil {
			u.splitTo = t
		}
	}
	// 「击败目标」按实际承受这一下的人算：被守卫 / 元形代挨时打死的是守卫也算（用户报：横冲直撞打死守卫不继续）
	deaths0 := map[*unit]int{}
	for _, x := range b.units[t.side] {
		deaths0[x] = x.deaths
	}
	u.atkTarget = t // 原定目标（飞剑用；被守卫代承时 lastVictim 是守卫）
	dmg := b.attack(u, t, pct, kind)
	// 用攻击者自己记的受击者：b.lastVictim 会被这一下引发的反击（对方打回来）覆盖成攻击者本人，攻击后的状态就挂不上
	// （踏浪被反击后不僵化）。
	victim := u.atkVictim
	killed := t.deaths > deaths0[t] || victim != nil && victim != t && victim.deaths > deaths0[victim]
	defer func() { u.lastKill = killed }() // 放在最后：这次攻击行为里嵌套的其他攻击（先攻…）不覆盖
	if kind == atkMain {
		u.mainKill = killed // 击败了目标（之后被神佑等复活也算）
	}
	if to := u.splitTo; to != nil { // 主攻击没打出去（闪避 / 未命中）：第二个目标照样按 a% 攻击力挨打
		u.splitTo = nil
		if u.alive() && to.alive() && !b.over {
			b.show(u, u.splitID, to)
			b.hurt(u, to, u.splitID, u.att*int64(pct)/100, true)
		}
	}
	if victim == nil {
		victim = t
	}
	if u.alive() && u.front() && !b.over { // 攻击中被换到后阵（反击方脱帽、驱逐）：攻击后的效果都不再结算
		b.attackEffects(u, victim, dmg)
		if u.alive() && !b.over {
			b.launchEffects(u, victim, dmg)
		}
		if u.alive() && !b.over {
			b.selfAfterAttack(u, victim, dmg)
		}
		if kind == atkMain {
			b.followUps(u, t, dmg)
		}
	}
	if split != nil {
		b.afterSplit(u)
	}
	b.endAction(u, temp0, fired0, epoch0, kind)
	return dmg
}

// endAction 清除这次攻击行为加的临时攻击力（主攻击走魔煞 / 兽妖激发的保留）。
//
// 攻击中途被驱逐 / 脱帽换到后阵（攻击重置为初始值）或阵亡（神佑等复活也是初始攻击）时，临时加攻已经随重置没了，
// 不能再扣一次（用户报：3000 攻击的斗战胜佛被驱逐再入阵变 600 多）。
func (b *battle) endAction(u *unit, temp0 int64, fired0 map[family]int64, epoch0 int, kind atkKind) {
	u.attachSoul, u.attachNoHeal = false, 0
	if u.tempEpoch != epoch0 {
		u.tempAtt, u.fired = 0, fired0
		return
	}
	if up := u.tempAtt - temp0; up != 0 {
		keep := int64(0)
		if kind == atkMain {
			keep = b.keepTemp(u) // 魔煞 / 兽妖激发：保留部分加值
		}
		if u.alive() {
			b.changeAtt(u, 0, -(up - keep)) // 抓包：攻击后以 magic 0 还原临时攻击
		}
		u.tempAtt = temp0
	}
	u.fired = fired0
}

// guardFor 找替 t 挨这一下的队友：守卫（t 生命不足 a% 且低于守卫者，伤害减 b%）、
// 元形（相邻护法总是代为承受）。返回护卫者与减伤 %。
func (b *battle) guardFor(t *unit) (*unit, int, bool) {
	for _, x := range b.allies(t) {
		if x == t {
			continue
		}
		// 守卫：目标无敌时不必代为承受；元形的替代是强制的（FAQ），照常
		if k := x.tal(famShouWei); k != nil && !b.invincible(t) && t.hp*100 < t.maxHP*int64(k.param(0)) && t.hp < x.hp {
			b.show(x, k.id, x) // 守卫词条（官方没有这一条，按需求补上；在护卫者冲出去之前播）
			if k.tjOn() {      // 激发：触发后增加 $x% 上限并回复同量
				g := int64(float64(x.maxHP) * k.tjX() / 100)
				b.changeMaxHP(x, k.id, g)
				b.heal(x, k.id, g)
			}
			return x, k.param(1), true
		}
		if x.tal(famYuanXing) != nil && abs(x.slot()-t.slot()) == 1 {
			return x, 0, false // 元形：视同更换受击目标，原目标不受任何影响（FAQ）
		}
	}
	return nil, 0, false
}

// elemAttack：驱雷 / 踏浪 / 破岩 / 蹈火这次触发了时，普攻是对应属性的伤害（雷 4、水 2、土 1、火 3），否则 -1。
// 属性伤害走 propertyHurt（仙衣、易伤、寒冰甲、分水、润泽），不触发护盾、气甲（FAQ：护盾「如驱雷…等造成属性伤害的
// 攻击…均不会触发」；官方战报：破岩、蹈火的普攻后目标的仙衣触发）。
func elemAttack(u *unit) int {
	for _, e := range []struct {
		fam  family
		kind int
	}{{famQuLei, 4}, {famTaLang, 2}, {famPoYan, 1}, {famDaoHuo, 3}} {
		if u.fired[e.fam] > 0 {
			return e.kind
		}
	}
	return -1
}

// attack 打一次普攻：pct 为攻击力百分比；main 为本次行动的主攻击（只有主攻击会触发
// 连击、先攻、受击神通等，避免无限连锁）。返回造成的伤害。
func (b *battle) attack(u, t *unit, pct int, kind atkKind) int64 {
	b.lastVictim, u.atkVictim = nil, nil
	u.atkDealt, u.atkLanded = 0, false
	if !t.alive() || !u.alive() { // 追加攻击前攻击者可能已被死亡神通反杀
		return 0
	}
	// 驱雷 / 踏浪 / 破岩 / 蹈火的属性攻击（用户规则）：无法闪避，只能被分水等 / 法术结界 / 不侵抵消；攻击方出手的特效先
	// 结算完（附带状态先挂上），才轮到被攻击方反应——踏浪的僵化没被抵消，对方的先攻、护盾、仙衣、反击等神通都发动不了。
	elemental := t.side != u.side && elemFired(u)
	var guard *unit
	guardCut, shared, guardDone := 0, false, false // 守卫：shared = 原目标与守卫一同受到此次攻击，仍受 1 点
	if elemental {
		// 有守卫 / 元形代为承受时，状态挂在代挨的人身上（用户确认），所以先定下谁来挨这一下
		if g, cut, sh := b.guardFor(t); g != nil {
			guard, guardCut, shared = g, cut, sh
			b.show(g, attackGuard, t)
		}
		guardDone = true
		recv := t
		if guard != nil {
			recv = guard
		}
		b.elemDebuffs(u, recv)
		if !t.alive() || !u.alive() || !recv.alive() {
			return 0
		}
	}
	// 先攻：「只要将受到[攻击行为]即可触发」（先攻自己的那一下不再触发对方先攻，避免循环）
	if k := t.tal(famXianGong); k != nil && kind != atkPre && t.side != u.side && !u.tuxiTJ() && b.chance(k.param(0)) {
		b.show(t, k.id, t)
		b.attackAction(t, u, max(k.param(1), 1), atkPre)
		if !u.alive() || !t.alive() {
			return 0
		}
	}
	b.lastVictim = t
	u.atkVictim = t // 被闪避时的受击者就是原目标（命中后改成实际承受的人：守卫等）
	sure := u.tempAtt > 0 && u.tal(famTuXi) != nil || elemental
	miss := func() int64 {
		b.show(u, attackDodge, t)
		b.chargeUp(t) // 躲过伤害也蓄力；与受击一样 ADD_POWER 在 SUB_HP 之前
		b.w.u8(CmdSubHP)
		b.w.u32(attackDodge)
		b.w.u8(1)
		b.w.u8(t.Pos)
		b.w.u32(0)
		b.fireEnchant(u, t, 0)
		return 0
	}
	// 命中判定（FAQ 人圣）：命中率 = max(100 − 盲目, 25)，再减去受击方的闪避率——一次掷骰。
	blind := 0.0
	if s := u.has(stBlind); s != nil {
		blind = float64(min(s.val, 75))
	}
	dodge := 0.0
	d := t.tal(famShanBi)
	if d != nil {
		dodge = max(d.Param[0]-float64(u.lead.hit), 0) // 激发后可能是小数（闪避9 + 激发5 = 58.5%）
	}
	if !sure && blind+dodge > 0 {
		r := b.rng.Float64() * 100
		switch {
		case r < blind:
			return miss()
		case r < blind+dodge:
			miss()
			b.show(t, d.id, t)
			return 0
		}
	}
	if !sure && t.side != u.side && b.chance(t.lead.dodge-u.lead.hit) { // 队长技：躲闪 / 命中
		return miss()
	}
	u.atkLanded = true
	atype := int32(attackNormal)
	dmg := u.att * int64(pct) / 100
	if c := u.tal(famBaoJi); c != nil && b.chance(c.param(0)) {
		atype = attackCrit
		dmg = dmg * int64(100+max(c.param(1), 100)) / 100
		dmg = scale(dmg, u.lead.crit, t.lead.critres) // 队长技：暴击伤害 / 抵抗暴击伤害
		if c.tjOn() {
			dmg += int64(float64(u.att) * c.tjX() / 100) // 激发：暴击后伤害额外增加当前攻击 $x%
		}
	}
	if k := u.tal(famRenHuang); k != nil && k.tjOn() {
		dmg += int64(float64(u.shield) * k.tjX() / 100) // 人皇激发：物理攻击加身上物理结界的 $x%
	}
	if k := u.tal(famShouWang); k != nil && k.tjOn() {
		dmg += int64(float64(u.mshield) * k.tjX() / 100) // 兽王激发：物理攻击加身上法术结界的 $x%
	}
	out := dmg // 出手值（分击的第二个目标用同一个数）
	victim := t
	if t.side != u.side && !guardDone {
		if g, cut, sh := b.guardFor(t); g != nil {
			guard, guardCut, shared = g, cut, sh
			b.show(g, attackGuard, t)
		}
	}
	if guard != nil && (!guard.alive() || !guard.front()) { // 提前定下的守卫在先攻等反应里倒下 / 被换下：原目标自己挨
		guard, shared = nil, false
	}
	if guard != nil {
		victim = guard
		dmg -= dmg * int64(guardCut) / 100
	}
	elem := elemAttack(u)
	var shieldT *talent
	if g := victim.tal(famHuDun); g != nil && elem < 0 && b.chance(g.param(0)) {
		shieldT = g // 护盾：只有攻击行为的物理伤害才触发
		dmg = dmg * int64(100-g.param(1)) / 100
	}
	// 官方战报：有护卫时 ACT_DO(护卫, 4, 原目标) → ACT_DO(攻击方, 攻击, 护卫) → ACT_DO(护卫, 4, 护卫) → SUB_HP(护卫)。
	// 攻击方的目标必须是护卫：客户端按 ACT_DO 的目标决定冲向谁、在谁身上播受击和扣血数字。
	b.show(u, atype, victim)
	if guard != nil {
		b.show(guard, attackGuard, guard)
	}
	if shieldT != nil {
		b.show(victim, shieldT.id, victim)
		if shieldT.tjOn() {
			// 激发：降低攻击者 $x% 攻击（永久），单次不超过护盾护法当前攻击 50%（FAQ）。按不含临时加成（突袭这类攻击后
			// 清除的）的攻击算——否则突袭越高被永久扣得越多（用户报：斗战胜佛突袭后攻击反而比出手前低一大截）。
			b.changeAtt(u, shieldT.id, -min(int64(float64(u.att-u.tempAtt)*shieldT.tjX()/100), victim.att/2))
		}
	}
	b.lastVictim = victim
	u.atkVictim = victim
	// 负面状态先于伤害（用户规则）：攻击附带的状态在出手动作之后、扣血之前挂上（这一下打死也生效）。毒爪激发的生命流失
	// 可能先把目标打死、目标的死亡神通又可能反杀攻击者——那这一下就不再结算。
	b.attachDebuffs(u, victim)
	if !victim.alive() || !u.alive() {
		b.lastDealt = 0
		return 0
	}
	if u.dmgUp > 0 {
		dmg = dmg * int64(100+u.dmgUp) / 100 // 灭法 / 异态的「伤害提高 a%」：结算伤害时乘（触发时已判过目标条件）
	}
	dmg = b.attackTaken(u, victim, dmg)
	if elem < 0 {
		dmg = scale(dmg, u.lead.ndmg, victim.lead.nres) // 队长技：普通伤害 / 普通伤害抗性（属性伤害在 propertyHurt 里按法术算）
	}
	var done int64
	if elem >= 0 {
		done = b.propertyHurt(u, victim, atype, dmg, elem)
	} else {
		done = b.hurt(u, victim, atype, dmg, true) // 受击蓄力在 hurt 里
	}
	dealt := b.lastDealt // 伤害值（结界抵消后、未按剩余血量截断），吸血 / 血气按它算（FAQ）
	u.atkDealt = dealt
	if shared && t.alive() && u.alive() {
		// 守卫：原目标受固定 1 点（无敌不掉），照常触发受击神通（坚韧、蓄力等）。不经队长技抗性 / 阵法 / 会猎缩放——
		// 原来 1 点按抗性百分比取整成 0，被守卫的护法毫无反应（用户报：李世民被守卫不触发坚韧）。
		b.fixedHit = true
		b.hurt(u, t, atype, 1, true)
		if t.alive() && u.alive() {
			b.onHit(u, t, 1, kind) // 视作受到攻击行为
		}
	}
	if to := u.splitTo; to != nil { // 分击：第二个目标与主攻击同一下、同样的出手值
		u.splitTo = nil
		if to.alive() && u.alive() && !b.over {
			b.show(u, u.splitID, to)
			b.hurt(u, to, u.splitID, out, true)
		}
	}
	b.fireEnchant(u, victim, done)
	if v := u.tal(famXiXue); v != nil && dealt > 0 && b.chance(v.param(0)) {
		b.show(u, v.id, u)
		b.heal(u, v.id, dealt*int64(v.param(1))/100)
		if v.tjOn() && victim.alive() {
			b.changeMaxHP(victim, v.id, -int64(float64(done)*v.tjX()/100))
		}
	}
	if victim.alive() && u.alive() {
		b.onHit(u, victim, done, kind)
	}
	return done
}

// fireEnchant：火附魔（烈焰怒吼）——这次攻击造成的伤害低于初始攻击时，差值的 val% 转为生命流失。
// 官方战报（session-01 duobao:138）：攻击被闪避（SUB_HP 0）后紧跟 SUB_HP(10002 火属性) 1675。
func (b *battle) fireEnchant(u, t *unit, done int64) {
	s := u.has(stFireEnchant)
	if s == nil || !t.alive() || t.side == u.side || b.over {
		return
	}
	if gap := u.baseAtt - done; gap > 0 {
		b.loseHP(u, t, magicFireHurt, gap*s.val/100)
	}
}
