package battle

import "slices"

// 状态、入阵、死亡触发。状态数值来自 auto/buff.lua（盲目 20% 不命中、禁疗 60%、返死 8% 等），
// 「下次行动后清除」类在单位行动后清掉，其余按回合数到期。

// stKey 是状态键（状态名与数值见 auto/buff.lua）。
type stKey int

const (
	stBlind       stKey = iota + 1 // 盲目：val = 不命中 %
	stConfuse                      // 混乱：随机攻击己方
	stBind                         // 捆缚：无法行动
	stSilence                      // 沉默：无法释放法术书
	stNoHeal                       // 禁疗：val = 治疗降低 %
	stNoSoul                       // 锢魂：无法被复活
	stWeak                         // 削弱：val = 被扣掉的攻击，清除时还回
	stTempMax                      // 犒劳：val = 临时加的生命上限，到期扣回
	stZaoyao                       // 早夭：群夭诀：val = 剩余可分担量（40% 上限），回合结束消失
	stImmune                       // 免伤：暴走：本回合不受伤害
	stStiff                        // 僵化：无法发动神通
	stTaunt                        // 嘲讽：val = 攻击来源时攻击力降低 %，src = 来源（buff 5471-5480 / 5601-5604）
	stZhanhu                       // 战呼：val = 加的攻击，下次行动后扣回（buff 5261-5270：+10%×等级）
	stXuming                       // 续命丹：val = 致命伤害时保留的上限 %（buff 5611：11%）
	stHidden                       // 隐身：无法成为物理攻击目标，到当前回合结束（buff 5111）
	stYizhi                        // 遗志：val = 攻击魔族时额外提升 %（buff 5645-5648）
	stSlow                         // 凝滞：val = 先攻减值，持续 1 回合（人圣激发）
	stFireEnchant                  // 火附魔（烈焰怒吼 buff 5649–5651）：val = 差值转生命流失的 %（法术等级 × a），下次行动后清除
	stBurn                         // 燃烧（只用于抵抗判定：实际效果在 unit.poison）
	stDusha                        // 毒砂（只用于抵抗判定：实际效果在 unit.dusha）
	stJieli                        // 竭力：val = 攻击降低 %（竭力咒，buff 5481-5484）
	stZhasi                        // 诈死：本回合免疫所有伤害（生命流失除外），回合结束清除（buff 5411）
)

const slotDebuff, slotBuff = 2, 3

// resistable 是敌方施加、可被不侵（分水：僵化 / 禁疗）抵抗的持续效果。
var resistable = map[stKey]bool{stBlind: true, stConfuse: true, stBind: true, stSilence: true, stNoHeal: true,
	stNoSoul: true, stWeak: true, stStiff: true, stZaoyao: true, stTaunt: true, stSlow: true, stJieli: true}

// elemGuards：分水 / 逆雷 / 辟土 / 返火——受到对应属性伤害时 a% 概率对来源造成等值物理伤害；另以同样概率（单独判定）
// 免疫两种负面状态（FAQ：「反伤与免疫的几率为单独计算」）。
var elemGuards = []struct {
	fam    family
	kind   int
	immune [2]stKey
}{
	{famFenShui, 2, [2]stKey{stStiff, stNoHeal}},
	{famNiLei, 4, [2]stKey{stSilence, stBlind}},
	{famPiTu, 1, [2]stKey{stDusha, stSlow}},
	{famFanHuo, 3, [2]stKey{stBurn, stNoSoul}},
}

// noHealRounds：禁疗「生命恢复降低，持续5回合，无法被清除」（分水等神通说明里的状态解释）。
const noHealRounds = 5

type status struct {
	val      int64
	left     int  // 剩余行动次数（byAction）或回合数
	byAction bool // 「下次行动后清除」
	buff     int32
	slot     int
	src      *unit // 嘲讽来源
	foe      bool  // 敌方施加的（addStatus）：负面状态，可被清除类效果移除
	// fresh：行动中敌方挂到行动者自己身上的「下次行动后清除」负面状态（如攻击蛟魔王被寒冰甲僵化 / 激发捆缚）——这次行动
	// 结束时不算，到它下一次行动后才清除（之前当场就被清掉，等于没生效）
	fresh bool
}

// addStatus 是敌方施加状态：负面效果先过不侵 / 分水的抵抗，返回是否上了。
func (b *battle) addStatus(t *unit, key stKey, val int64, left int, byAction bool, buff int32, slot int) bool {
	if !t.alive() || resistable[key] && b.resist(t, key) {
		return false
	}
	b.setStatus(t, key, val, left, byAction, buff, slot)
	if s := t.status[key]; s != nil {
		s.foe = true
		s.fresh = byAction && b.acting == t
	}
	return true
}

// resist：敌方给予的负面持续状态——有法术结界时免疫（FAQ：「持续状态施放时可被[法术结界]免疫」，官方战报 5861 次
// 负面状态只有 3 次挂在带法术结界的目标上）；不侵 a% 抵抗；分水 / 逆雷 / 辟土 / 返火同概率免疫各自的两种状态。
func (b *battle) resist(t *unit, key stKey) bool {
	if t.mshield > 0 {
		return true
	}
	if k := t.tal(famBuQin); k != nil && b.chance(k.param(0)) {
		b.show(t, k.id, t)
		return true
	}
	for _, g := range elemGuards {
		if k := t.tal(g.fam); k != nil && (key == g.immune[0] || key == g.immune[1]) && b.chance(k.param(0)+k.tjPct()) {
			b.show(t, k.id, t)
			return true
		}
	}
	if k := t.tal(famYiTai); k != nil && k.tjOn() && b.chance(k.tjPct()) {
		b.show(t, k.id, t)
		b.heal(t, k.id, t.maxHP*15/100)
		return true
	}
	return false
}

// setStatus 给 t 上状态并输出 BUFF（已有同名状态则刷新），不判抵抗（增益、本方施加的锢魂）。
func (b *battle) setStatus(t *unit, key stKey, val int64, left int, byAction bool, buff int32, slot int) {
	if !t.alive() {
		return
	}
	if t.status == nil {
		t.status = map[stKey]*status{}
	}
	if old := t.status[key]; old != nil && key == stBlind && old.left > 0 {
		val = min(old.val+val, 75) // 盲目可叠加，命中率最低 25%（FAQ 人圣）
	}
	if old := t.status[key]; old != nil && old.buff != 0 && old.buff != buff {
		b.rmBuff(t, old.buff) // 同一状态换了来源（如暴走的免伤顶掉光阴碎的无敌）：旧特效要先拿掉
	}
	t.status[key] = &status{val: val, left: left, byAction: byAction, buff: buff, slot: slot}
	if buff != 0 {
		b.buff(t, buff, max(left, 1))
	}
}

func (t *unit) has(key stKey) *status {
	if s := t.status[key]; s != nil && s.left > 0 {
		return s
	}
	return nil
}

func (b *battle) clearStatus(t *unit, key stKey) {
	s := t.status[key]
	if s == nil {
		return
	}
	delete(t.status, key)
	switch key {
	case stWeak:
		b.changeAtt(t, 0, s.val)
	case stTempMax:
		b.changeMaxHP(t, 0, -s.val)
	case stZhanhu:
		b.changeAtt(t, 0, -s.val)
	case stJieli:
		b.changeAtt(t, 0, min(t.att*s.val/max(100-s.val, 1), t.baseAtt/2))
	}
	if s.buff != 0 && t.alive() {
		b.rmBuff(t, s.buff)
	}
}

// leaveClear：阵亡 / 进入后阵时清除持续状态（FAQ：负面状态「阵亡、进入后阵则清除」，锢魂不清除；燃烧、毒砂
// 持续到战斗结束）。dead 时增益也一并清掉（官方战报：DIE 之后逐个 RM_BUFF）。按 stKey 顺序，输出稳定。
func (b *battle) leaveClear(t *unit, dead bool) {
	keys := make([]stKey, 0, len(t.status))
	for k := range t.status {
		if k != stNoSoul && (dead || slices.Contains(benchClear, k)) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		s := t.status[k]
		delete(t.status, k)
		switch k {
		case stWeak:
			t.att += s.val // 削弱还回（不演出：阵亡 / 后阵）
		case stZhanhu:
			t.att -= s.val
		case stJieli: // 失去竭力：攻击按 1/(1-X%) 回升，额外提升不超过初始攻击 50%（FAQ）
			if up := min(t.att*s.val/max(100-s.val, 1), t.baseAtt/2); up > 0 {
				if t.alive() {
					b.changeAtt(t, 0, up)
				} else {
					t.att += up
				}
			}
		}
		if s.buff != 0 {
			b.rmBuff(t, s.buff)
		}
	}
	// 「持续到战斗结束」的状态（燃烧、毒砂、会猎、竭力、锢魂、结界、续命丹……）回后阵都保留，只在阵亡时清除
	//（FAQ 里会猎 / 竭力写的进入后阵清除，以用户规则为准）。
	if dead { // 会猎
		if t.huilieBuff != 0 {
			b.rmBuff(t, t.huilieBuff)
		}
		t.hurtUp, t.huilie, t.huilieBuff = 0, 0, 0
	}
	if dead && t.huweiBuff != 0 { // 狐尾：死亡时清除（FAQ）
		b.rmBuff(t, t.huweiBuff)
		t.huweiBuff, t.huweiLv, t.huweiFloor = 0, 0, 0
	}
	if dead && t.huoshiBuff != 0 { // 活尸：死亡时去掉图标（官方战报 3812：DIE 后 RM_BUFF 活尸）
		b.rmBuff(t, t.huoshiBuff)
		t.huoshiBuff = 0
	}
	// 燃烧（含狂怒焰激发叠加层）：阵亡清除（神佑 / 不灭法身复活后不再带燃烧）；回后阵保留层数，在后阵不结算，
	// 回到前阵接着烧。中毒（毒爪、毒化，不是持续到战斗结束）：阵亡、回后阵都清除。
	kept := t.poison[:0]
	for _, p := range t.poison {
		if p.burn && !dead {
			kept = append(kept, p)
			continue
		}
		b.rmPoisonIcon(t, p)
	}
	t.poison = kept
	b.burnIcon(t)
	if dead && t.dusha > 0 { // 毒砂：持续到战斗结束，只在阵亡时清除
		if t.dushaBuff != 0 {
			b.rmBuff(t, t.dushaBuff)
		}
		t.dusha, t.dushaBuff = 0, 0
	}
	// 神之力：只在这一次站前阵存活期间有效，阵亡、回后阵都清除（攻击由死亡复活 / benchReset 重置）
	if t.shenliShown != 0 {
		b.rmBuff(t, t.shenliShown)
	}
	t.shenliPct, t.shenliAtt, t.shenliShown = 0, 0, 0
	if dead {
		// 阵亡：剩下的增益图标（神之护、神之力……）和物理 / 法术结界一并清掉，复活后是初始状态；
		// 锢魂留着（决定能不能被复活）
		keep := map[int32]bool{5141: true}
		if s := t.status[stNoSoul]; s != nil && s.buff != 0 {
			keep[s.buff] = true
		}
		ids := make([]int32, 0, len(t.buffSlot))
		for id := range t.buffSlot {
			if !keep[id] {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		for _, id := range ids {
			b.rmBuff(t, id)
		}
		if t.shield > 0 {
			b.cutShield(t, t.shield, false)
		}
		if t.mshield > 0 {
			b.cutShield(t, t.mshield, true)
		}
	}
}

// benchClear 是进入后阵时清除的负面状态。
var benchClear = []stKey{stBlind, stConfuse, stBind, stSilence, stNoHeal, stWeak, stZaoyao, stStiff, stTaunt, stSlow, stZhanhu, stHidden}

// tickStatus：byAction=true 在单位行动后调用，false 在回合结束时调用。
func (b *battle) tickStatus(t *unit, byAction bool) {
	keys := make([]stKey, 0, len(t.status))
	for k := range t.status {
		keys = append(keys, k)
	}
	slices.Sort(keys) // 输出顺序固定
	for _, key := range keys {
		s := t.status[key]
		if s == nil || s.byAction != byAction {
			continue
		}
		if byAction && s.fresh {
			s.fresh = false
			continue
		}
		if s.left--; s.left <= 0 {
			b.clearStatus(t, key)
		}
	}
}

// hasDebuff：身上有任一负面效果（异态用；定义同 clearOneDebuff）。
func (t *unit) hasDebuff() bool {
	return debuffCount(t) > 0
}

// bench 是一方存活的后阵单位。
func (b *battle) bench(side int) []*unit {
	var out []*unit
	for _, x := range b.units[side] {
		if x.alive() && !x.gone && !x.front() {
			out = append(out, x)
		}
	}
	return out
}

// quzhuPick：[驱逐] t 时换上来的人——随机一名后阵护法，有可能是已阵亡的（被吞噬的除外，FAQ 驱逐）；没有返回 nil。
func (b *battle) quzhuPick(t *unit) *unit {
	var bn []*unit
	for _, x := range b.units[t.side] {
		if x.slot() > frontSlots && (x.alive() && !x.gone || !x.alive() && !x.eaten) {
			bn = append(bn, x)
		}
	}
	if len(bn) == 0 {
		return nil
	}
	return bn[b.rng.IntN(len(bn))]
}

// quzhu：[驱逐] t，换上 x（驱逐、神威、芭蕉扇及其激发、脱帽退敌共用）。换上来的死者留在前阵，回合开始由后阵补位；
// 换上来的活人被【捆缚】（用户规则：驱逐 / 换位上来的卡无法行动，下次行动后清除）。
func (b *battle) quzhu(t, x *unit) {
	b.swap(t, x)
	if !x.alive() {
		x.gone = false
	}
	b.bindEntered(x)
}

// bindEntered：被驱逐 / 换位法术从后阵换到前阵的护法【捆缚】（入阵神通照常发动；捆缚下次行动后清除）。
func (b *battle) bindEntered(x *unit) {
	if !x.alive() || !x.front() {
		return
	}
	b.setStatus(x, stBind, 0, 1, true, qkxBindBuff, slotDebuff)
	if s := x.status[stBind]; s != nil {
		s.foe = true
	}
}

// swap 交换两个单位的站位（客户端 CWar:moveTo 对 MOVE 就是交换）。
// 从后阵换到前阵的一方触发入阵神通（官方战报：遁让 / 驱逐换上来的羲和立即放蟠桃宴，与阵亡补位相同）。
// 入阵神通本身也可能换位（驱逐），enterDepth 防止无限连锁。
func (b *battle) swap(x, y *unit) {
	wasFront := map[*unit]bool{x: x.front(), y: y.front()}
	b.w.u8(CmdMove)
	b.w.u8(x.Pos)
	b.w.u8(y.Pos)
	x.Pos, y.Pos = y.Pos, x.Pos
	b.zfSync(x) // 阵法：固定加成跟着阵位走
	b.zfSync(y)
	for _, z := range []*unit{x, y} {
		if z.alive() && !z.front() {
			b.backToBench(z)
		}
	}
	for _, z := range []*unit{x, y} {
		if z.alive() && z.front() && !wasFront[z] && b.enterDepth < 3 {
			b.enterDepth++
			b.enter(z)
			b.enterDepth--
		}
	}
}

// ---- 仙剑（布剑）：按属性的添剑命令（SWORD_DAO / EARTH / WATER / FIRE / LIGHT）；攻击后 SWORD_ATTACK + 每把剑一下伤害 ----

const (
	CmdSwordEarth  = 31
	CmdSwordAttack = 32
	magicEarthHurt = 10003 // 普通土属性伤害（官方战报剑伤用它）
	magicFireHurt  = 10002 // 普通火属性伤害（官方战报血池攻击用它）
	maxSwords      = 5
)

// swordCmd 发一把剑的添剑命令：按仙剑属性用 SWORD_DAO 39 / EARTH 31 / WATER 30 / FIRE 29 / LIGHT 28（雷）
// （官方战报：雷剑 SWORD_LIGHT + SUB_HP 10001、道剑 SWORD_DAO + 10006…；之前一律发 31，客户端全显示成土剑）。
func (b *battle) swordCmd(x *unit) {
	b.w.u8(swordCmds[x.swordKind])
	b.w.u8(x.Pos)
}

// reshowSwords：后阵的护法补位 / 换位到前阵时客户端会把它的小头像换成大卡，身上的剑不跟过去，
// 重新发一遍添剑命令让新卡显示出来（不改剑数）。
func (b *battle) reshowSwords(x *unit) {
	for range x.swords {
		b.swordCmd(x)
	}
}

var swordCmds = [5]int{39, CmdSwordEarth, 30, 29, 28}

// swordMagic / swordBuffs 按仙剑属性（0 道 1 土 2 水 3 火 4 雷）：伤害招式号、5% 附带的状态。
var (
	swordMagic = [5]int32{10006, magicEarthHurt, 10004, magicFireHurt, 10001}
	swordBuffs = [5]int32{0, 5035, 5001, 5025, 5011}
)

func (b *battle) addSword(u *unit, k *talent) {
	if u.swords >= maxSwords {
		return
	}
	b.show(u, k.id, u)
	b.giveSword(u)
}

// giveSword 给 x 添一柄仙剑（满 5 柄不再加），返回是否加了。
func (b *battle) giveSword(x *unit) bool {
	if x.swords >= maxSwords {
		return false
	}
	if !x.swordSet {
		// 第一次获得仙剑时确定属性（道 土 水 火 雷，FAQ 布剑），整场不变：剑攻击后消散再拿新剑时如果重新随机，
		// 客户端会在同一张卡上显示两种剑（用户报：逆鳞白龙两种剑）
		x.swordKind, x.swordSet = b.rng.IntN(5), true
	}
	x.swords++
	b.swordCmd(x) // 官方只发属性剑命令，不挂 BUFF 5361：挂了客户端会多画一把旧样式的剑（「双色剑」，实际只有一把）
	return true
}

// swordStrike：攻击后所有仙剑以 50% 初始攻击各打一次，之后剑消散。
func (b *battle) swordStrike(u, t *unit) {
	if u.swords == 0 || !t.alive() {
		return
	}
	b.w.u8(CmdSwordAttack)
	b.w.u8(u.Pos)
	b.w.u8(t.Pos)
	b.w.u8(u.swords)
	// 每把剑各打一下 50% 初始攻击（官方战报：SWORD_ATTACK{count 3} 后 3 条 SUB_HP，目标死了就停）。
	// 仙剑属性决定伤害类型（官方战报：SUB_HP 10001 雷 / 10002 火 / 10003 土 / 10004 水 / 10006 道，雷剑触发仙衣）；
	// 道属性仙剑是生命流失（FAQ）。每把剑攻击后 5% 概率附带：水 僵化、火 燃烧5、雷 麻痹、土 毒砂5。
	id := swordMagic[u.swordKind]
	for i := range u.swords {
		if b.over {
			break
		}
		if !t.alive() { // 打死当前目标后剩下的仙剑改打当前对位继续出手（用户：有几把放几次）
			if t = b.pickTarget(u); t == nil {
				break
			}
			b.w.u8(CmdSwordAttack)
			b.w.u8(u.Pos)
			b.w.u8(t.Pos)
			b.w.u8(u.swords - i)
		}
		n := u.baseAtt / 2
		if k := u.tal(famBuJian); k != nil && k.tjOn() && b.chance(k.tjPct()) {
			n *= 2 // 布剑激发：飞剑 $x% 概率双倍伤害
		}
		if k := b.wanxian(u.side); u.swordKind == 0 || k != nil && k.tjOn() && b.chance(k.tjPct()) {
			b.loseHP(u, t, id, n) // 万仙来朝激发：仙剑伤害 $x% 概率变为生命流失
		} else {
			b.propertyHurt(u, t, id, n, u.swordKind)
		}
		if buff := swordBuffs[u.swordKind]; buff != 0 && t.alive() && b.chance(5) {
			b.applyBuff(u, t, buff) // FAQ「每次仙剑攻击后有5%几率产生buff」：攻击后，在伤害之后
		}
	}
	swords := u.swords
	u.swords = 0
	for _, c := range b.allies(u) {
		k := c.tal(famZiQingJian)
		if k == nil || !k.tjOn() || !t.alive() || !b.chance(int(k.tjX())*swords) {
			continue
		}
		for _, x := range b.allies(u) { // 合击：非本护法的仙剑伤害为原伤害 60%
			if x != u && x.swords > 0 && t.alive() {
				b.show(c, k.id, t)
				if n := x.baseAtt * int64(x.swords) / 2 * 60 / 100; x.swordKind == 0 { // 伤害来源与属性仍是该仙剑的拥有者（FAQ）
					b.loseHP(x, t, swordMagic[0], n)
				} else {
					b.propertyHurt(x, t, swordMagic[x.swordKind], n, x.swordKind)
				}
			}
		}
		break
	}
}

func sortByAtt(l []*unit) {
	for i := range l {
		for j := i + 1; j < len(l); j++ {
			if l[j].att > l[i].att {
				l[i], l[j] = l[j], l[i]
			}
		}
	}
}

// clearOneDebuff 移除一个负面效果，返回是否移除了。负面效果 = 中毒 / 燃烧、敌方施加的持续状态（盲目、混乱、
// 捆缚、麻痹、禁疗、削弱、僵化、锢魂、竭力、嘲讽、凝滞、早夭…，按 stKey 顺序）、毒砂、会猎。
// 本方给的锢魂（甘露 / 易命复活后、审死）和活尸不算，清不掉。
func (b *battle) clearOneDebuff(t *unit) bool {
	if len(t.poison) > 0 {
		b.clearPoison(t)
		return true
	}
	keys := make([]stKey, 0, len(t.status))
	for k, s := range t.status {
		if s.foe && s.left > 0 && k != stNoHeal { // 禁疗「持续5回合，无法被清除」（神通说明），驱散类效果清不掉
			keys = append(keys, k)
		}
	}
	if len(keys) > 0 {
		slices.Sort(keys)
		b.clearStatus(t, keys[0])
		return true
	}
	if t.dusha > 0 {
		t.dusha = 0
		b.rmBuff(t, t.dushaBuff)
		return true
	}
	if t.huilie > 0 {
		t.hurtUp -= t.huilie
		t.huilie = 0
		b.rmBuff(t, t.huilieBuff)
		return true
	}
	return false
}

// clearAllDebuffs 移除全部负面效果（清除特效：「移除目标身上的所有负面状态」）。
func (b *battle) clearAllDebuffs(t *unit) {
	for b.clearOneDebuff(t) {
	}
}

// pickN 从 l 里随机取至多 n 个（不重复）。
func (b *battle) pickN(l []*unit, n int) []*unit {
	l = append([]*unit(nil), l...)
	b.rng.Shuffle(len(l), func(i, j int) { l[i], l[j] = l[j], l[i] })
	return l[:min(n, len(l))]
}

// ---- 入阵 ----

// ---- 死亡 ----
