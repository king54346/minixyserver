package battle

import "slices"

// 由别的单位的事件触发的神通：首次发动诈死 / 死亡技 / 入阵技、带死亡技的护法死亡或复活、
// 人族提升攻击、护法回到后阵、分击护法攻击后。

func init() {
	register(famZhuJian, famTianDiLingQi, famTianMoJueXing, famXiuLuoYinShen, famCangJianYuShen, famHuiMeng, famJueMingZhuiLie)
}

// deathFamilies / enterFamilies 是「死亡技」「入阵技」（铸剑、天魔觉醒、飞仙、审死等判定用）。
var (
	deathFamilies = []family{famShenYou, famHunShou, famYiXiang, famWangYuZhou, famLuoWang, famJieLiZhou, famGuMao, famTongGuiJue, famYiMing, famKuangNuYan, famQunYaoJue,
		famGuiYuan, famHuanHun, famBuMieQu, famGuiXuZhen, famBaJiaoShan, famShengSiBie, famQingShang, famZhuFuXiaoSan, famJingWeiTianHai, famBuMieFaShen, famHuoZhiHun}
	enterFamilies = []family{famXiuLin, famKaoLao, famKuangLei, famXunDian, famLangYong, famQianKunXiu, famShenWei, famPanTaoYan, famGanLu, famDaoZhiShi, famGuangYinSui,
		famShuiZhiYuan, famZhuXianZhen, famYuanHu, famLongYue, famPoChuJinGu, famZiQingJian, famTianGangJianQi, famShiXinLongZhou}
)

func hasAny(u *unit, families []family) bool {
	for _, f := range families {
		if u.tals[f] != nil {
			return true
		}
	}
	return false
}

// 铸剑 / 天地灵气的三种触发（FAQ：「我方护法触发【诈死】、【死亡技】、【入阵技】后……每种效果仅触发1次」）。
const (
	skZhasi = 1 << iota // 诈死
	skDeath             // 死亡技
	skEnter             // 入阵技
	skAll   = skZhasi | skDeath | skEnter
)

// firstSkill：本方护法 x 触发了 kind（诈死 / 死亡技 / 入阵技）。每个铸剑 / 天地灵气持有者对每种只响应一次
// （持有者在后阵也生效）：
//   - 铸剑：本方全体（前后阵）当前攻击与生命上限提升 a%（并回复同量）；
//   - 天地灵气：混世魔王下次攻击有 a% 概率额外攻击一次。
//
// 激发：三种都触发过后重置可触发次数（skillDone 清零）——铸剑之后的效果提升 $x%，天地灵气在重置时混世魔王攻击、
// 气血提升 $x%（FAQ）。持有者被复活时同样重置（reviveSkill）。
func (b *battle) firstSkill(x *unit, kind int) {
	for _, c := range b.units[x.side] {
		if !c.alive() || c.gone || c.skillDone&kind != 0 {
			continue
		}
		zj, td := c.tal(famZhuJian), c.tal(famTianDiLingQi)
		if zj == nil && td == nil {
			continue
		}
		c.skillDone |= kind
		if k := zj; k != nil {
			b.show(c, k.id, c)
			for _, y := range b.units[c.side] {
				if !y.alive() || y.gone {
					continue
				}
				b.changeAtt(y, k.id, c.skillBoost(k, y.att*int64(k.param(0))/100))
				grow := c.skillBoost(k, y.maxHP*int64(k.param(0))/100)
				b.changeMaxHP(y, k.id, grow)
				b.heal(y, k.id, grow)
			}
		}
		if td != nil {
			c.extraAtk = true
		}
		if c.skillDone == skAll && (zj != nil && zj.tjOn() || td != nil && td.tjOn()) {
			c.skillDone = 0 // 激发：三种都触发过，重置可触发次数
			c.skillUps++
			if k := td; k != nil && k.tjOn() { // 天地灵气激发：重置时混世魔王攻击、气血提升 $x%
				b.show(c, k.id, c)
				b.changeAtt(c, k.id, int64(float64(c.att)*k.tjX()/100))
				g := int64(float64(c.maxHP) * k.tjX() / 100)
				b.changeMaxHP(c, k.id, g)
				b.heal(c, k.id, g)
			}
		}
	}
}

// reviveSkill：铸剑 / 天地灵气的持有者被复活时，重置它的可触发次数。
func (b *battle) reviveSkill(t *unit) {
	t.skillDone = 0
}

// skillBoost：铸剑激发——重置过可触发次数之后，效果提升 $x%。
func (c *unit) skillBoost(k *talent, n int64) int64 {
	if c.skillUps > 0 {
		return k.tjBoost(n)
	}
	return n
}

// deathSkillEvent：带死亡技的护法死亡或复活时（双方都算）——
// 天魔觉醒：天女魃（前后阵都生效，FAQ；阵亡不生效）提升 (10+a)% 初始攻击（至多 7 次）；修罗隐身：阿修罗修罗点 +1。
func (b *battle) deathSkillEvent(x *unit) {
	if !hasAny(x, deathFamilies) {
		return
	}
	for side := range b.units {
		for _, c := range b.units[side] {
			if k := c.tal(famTianMoJueXing); k != nil && c.alive() && !c.gone && c.tianmo < 7 {
				c.tianmo++
				b.show(c, k.id, c)
				b.changeAtt(c, k.id, c.baseAtt*int64(10+k.param(0))/100+int64(float64(c.baseAtt)*k.tjX()/100))
			}
		}
		for _, c := range b.units[side] {
			if c.tals[famXiuLuoYinShen] != nil && !c.eaten { // 阿修罗在后阵 / 已阵亡也累积修罗点（图鉴 FAQ）
				b.setXiuluo(c, c.xiuluo+1)
			}
		}
	}
}

// setXiuluo 改修罗点并刷新客户端头像计数（XIULUO_COUNT）。
func (b *battle) setXiuluo(c *unit, n int) {
	c.xiuluo = max(n, 0)
	b.w.u8(CmdXiuluoCount)
	b.w.u8(c.Pos)
	b.w.u8(min(c.xiuluo, 255))
}

// attRaised：t 的攻击刚提升。记录本场最高攻击（噬心龙咒）；
// 藏剑于身：本方人族护法提升攻击时吕洞宾攻击提升 a% 初始攻击（每回合至多 6 次）。
// 藏剑于身：官方战报（session-01/02 统计）只由神通带来的持续加攻触发（坚韧 1650 次、疯魔、铸剑…），
// 突袭这类攻击后清除的临时加攻（732 次）和 buff 加攻（战呼 5262、神之力 5203/5204…）从不触发；
// 吕洞宾在后阵也会触发。
func (b *battle) attRaised(t *unit, id int32) {
	// 本场最高攻击只算留得住的攻击：不含「攻击后清除」的临时加攻（突袭、踏浪…）和战呼（下次行动后清除）——否则龙再入阵
	// 恢复的「最高值」里带着当时的临时加成
	if !b.inTempUp {
		keep := t.att - t.tempAtt
		if s := t.has(stZhanhu); s != nil {
			keep -= s.val
		}
		t.peakAtt = max(t.peakAtt, keep)
	}
	if t.Race != raceHuman || id < 20000 || b.inTempUp {
		return
	}
	for _, c := range b.units[t.side] {
		if !c.alive() || c.gone {
			continue
		}
		if k := c.tal(famCangJianYuShen); k != nil && c != t && c.perRound[1] < 6 {
			c.perRound[1]++
			b.show(c, k.id, c)
			b.changeAtt(c, k.id, c.att*int64(k.param(0))/100) // 当前攻击 a%（官方战报：61 = 1536×4%）
		}
	}
}

// backToBench：回梦——友方护法回到后阵时，瑶姬仙子提升 a% 初始生命（每回合至多 3 次）。
// 吕洞宾 / 瑶姬仙子在后阵也生效（图鉴 FAQ）。
func (b *battle) backToBench(x *unit) {
	if x.huoshiBuff != 0 && x.alive() { // 活尸（不灭躯、光阴碎激发、白骨魔）回到后阵直接死亡
		b.doomKill(x)
		return
	}
	pre := x.att
	b.leaveClear(x, false)
	x.att = pre // leaveClear 对削弱 / 战呼 / 神之力是不发命令地还原，这里先按客户端看到的值算，下面统一重置
	for _, c := range b.units[x.side] {
		if c == x || !c.alive() || c.gone {
			continue
		}
		if k := c.tal(famCangJianYuShen); k != nil && k.tjOn() && x.att < x.baseAtt {
			b.show(c, k.id, c)
			b.changeAtt(c, k.id, int64(float64(x.baseAtt-x.att)*k.tjX()/100))
		}
		if k := c.tal(famHuiMeng); k != nil && c.perRound[2] < 3 {
			c.perRound[2]++
			grow := k.tjBoost(int64(c.MaxHP) * int64(k.param(0)) / 100)
			b.show(c, k.id, c)
			b.changeMaxHP(c, k.id, grow)
			b.heal(c, k.id, grow)
		}
	}
	b.benchReset(x)
}

// benchReset：护法回到后阵时攻击力、生命上限重置为进入战斗的初始值，去掉加成类图标（神之护、神之力……）；
// 燃烧、锢魂、狐尾等还在的状态图标保留。
func (b *battle) benchReset(x *unit) {
	// 逆鳞白龙也照样重置（用户确认）；它再入阵时噬心龙咒把攻击恢复到本场最高值（talents_enter.go）
	if d := x.baseAtt - x.att; d != 0 {
		b.changeAtt(x, 0, d)
	}
	if d := int64(x.MaxHP) - x.maxHP; d != 0 {
		b.changeMaxHP(x, 0, d)
	}
	keep := map[int32]bool{5141: true, x.burnShown: true, x.huweiBuff: true, x.dushaBuff: true, x.huilieBuff: true}
	for _, s := range x.status {
		keep[s.buff] = true
	}
	ids := make([]int32, 0, len(x.buffSlot))
	for id := range x.buffSlot {
		if !keep[id] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		b.rmBuff(x, id)
	}
}

// afterSplit：绝命追猎——己方分击护法攻击后，哮天犬提升 a% 初始攻击（a 可带小数）；哮天犬在后阵也触发（FAQ）。
func (b *battle) afterSplit(u *unit) {
	for _, c := range b.team(u.side) {
		if k := c.tal(famJueMingZhuiLie); k != nil && len(k.Param) > 0 { // 哮天犬自己分击也算
			if n := k.tjBoost(int64(float64(c.baseAtt) * k.Param[0] / 100)); n > 0 {
				b.show(c, k.id, c)
				b.changeAtt(c, k.id, n)
			}
		}
	}
}

// zhanhu 给 x 上战呼（buff 5261-5270：提升 10%×等级 攻击力，下次行动后清除）。
func (b *battle) zhanhu(x *unit, buff int32) {
	lv := int64(buff - 5260)
	if lv < 1 || lv > 10 || x.has(stZhanhu) != nil {
		return
	}
	b.inTempUp = true                       // 战呼到期清除：不算进本场最高攻击（attRaised）
	n := b.changeAtt(x, 0, x.att*lv*10/100) // 实际加了多少（坚韧护法封顶），到期按它扣回
	b.inTempUp = false
	b.setStatus(x, stZhanhu, n, 1, true, buff, slotBuff)
}

// shenli 给 x 加神之力（用户给的规则）：提升初始攻击的 pct%（精修每级 3%，神之力N = 3N%），在前阵存活期间一直有效；
// 同效果叠加无上限、不同来源分别计算后相加（9% + 15% = 初始攻击的 24%）。回后阵、阵亡清除（leaveClear）。
// 只显示一个图标，等级按累计百分比换算（24% 显示神之力8，客户端最高神之力10，超出仍按实际累加）。返回实际加的攻击。
func (b *battle) shenli(x *unit, buff int32, pct int64) int64 {
	if !x.alive() || pct <= 0 {
		return 0
	}
	n := b.changeAtt(x, buff, x.baseAtt*pct/100)
	x.shenliPct += pct
	x.shenliAtt += n
	b.shenliIcon(x)
	return n
}

// shenliIcon 让神之力图标与累计百分比一致（0 = 去掉）。
func (b *battle) shenliIcon(x *unit) {
	var id int32
	if x.shenliPct > 0 {
		id = 5200 + int32(min(max((x.shenliPct+2)/3, 1), 10))
	}
	if id == x.shenliShown {
		return
	}
	if x.shenliShown != 0 {
		b.rmBuff(x, x.shenliShown)
	}
	if id != 0 {
		b.buff(x, id, maxRounds)
	}
	x.shenliShown = id
}

// cutShield 扣掉 x 至多 n 点结界（magic 为法术结界），输出 SHIELD / MAGIC_SHIELD op 2，返回扣了多少。
func (b *battle) cutShield(x *unit, n int64, magic bool) int64 {
	s, cmd := &x.shield, CmdShield
	if magic {
		s, cmd = &x.mshield, CmdMagicShield
	}
	n = min(n, *s)
	if n <= 0 {
		return 0
	}
	*s -= n
	b.w.u8(cmd)
	b.w.u8(2)
	b.w.u8(1)
	b.w.u8(x.Pos)
	b.w.u32(n)
	return n
}
