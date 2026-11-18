package battle

import (
	"encoding/binary"

	"turnrpg/internal/gamedata"
)

// 命令输出与数值变化：ACT_DO 演出、扣血 / 回血、改攻击 / 上限、结界、战意、BUFF 图标。

type recordWriter struct{ b []byte }

func (w *recordWriter) u8(v int) { w.b = append(w.b, byte(v)) }

func (w *recordWriter) u32(v int64) { w.b = binary.LittleEndian.AppendUint32(w.b, uint32(v)) }

// hpCmd 输出单个目标的生命变化命令（SUB_HP / ADD_HP：招式号、1 个目标、站位、数值）。
func (b *battle) hpCmd(cmd int, id int32, t *unit, n int64) {
	b.w.u8(cmd)
	b.w.u32(int64(id))
	b.w.u8(1)
	b.w.u8(t.Pos)
	b.w.u32(n)
}

// shieldCmd 输出单个目标的结界变化命令（SHIELD / MAGIC_SHIELD：op 1 增加、2 减少，1 个目标、站位、数值）。
func (b *battle) shieldCmd(cmd, op int, t *unit, n int64) {
	b.w.u8(cmd)
	b.w.u8(op)
	b.w.u8(1)
	b.w.u8(t.Pos)
	b.w.u32(n)
}

// selfActFamilies：官方战报里这些族的技能动作只指向施放者自己（效果再作用到队友 / 敌人身上）。
// 指向队友时客户端不播入阵 / 回合技动画（蟠桃宴、道之始、甘露…），统一在 show 里改成自己。
// 依据：session-01/02 全部 ACT_DO 按目标分类（analysis 里统计，见 TestActDoTargets）。
var selfActFamilies = map[family]bool{famWuJiangZhiShang: true, famXianDi: true, famLiuYi: true, famGuaXiang: true,
	famMiaoShou: true, famGuiYuan: true, famHuZhen: true, famJiuSi: true, famWenXiang: true, famLingYou: true,
	famKaoLao: true, famGanLu: true, famBaiCao: true, famPoChuJinGu: true, famBiXue: true, famXiuLin: true,
	famJingWeiTianHai: true, famCangJianYuHua: true, famPanTaoYan: true, famXueQi: true, famZhuXianZhen: true,
	famDaoZhiShi: true, famLeiYingBiHu: true, famLongWei: true, famJiuWeiXianChe: true, famYiMingYanTian: true}

func (b *battle) show(owner *unit, id int32, targets ...*unit) {
	b.actor = owner
	if ti := gamedata.Talents[id]; ti != nil {
		b.skills[ti.Family]++
		if selfActFamilies[family(ti.FamilyID)] {
			targets = []*unit{owner}
		}
	} else if m := gamedata.Magics[id]; m != nil && id > attackDodge {
		b.skills["magic:"+m.Name]++
	}
	b.w.u8(CmdActDo)
	b.w.u8(owner.Pos)
	b.w.u32(int64(id))
	b.w.u8(len(targets))
	for _, t := range targets {
		b.w.u8(t.Pos)
	}
}

// Opening race auras are passive stat changes. The shipped client maps Xianshi
// and the other race auras to magic_90.plist, which is absent from the
// APK. An ACT_DO attempts to parse that missing plist in native CCDictMaker,
// causing a null-pointer crash before Lua can handle the failure. Keep the
// activation statistics and all HP commands, without an active-cast animation.
func (b *battle) passiveAura(owner *unit, id int32) {
	b.actor = owner
	if ti := gamedata.Talents[id]; ti != nil {
		b.skills[ti.Family]++
	}
}

// hurt 让 t 掉血并输出 SUB_HP；physical 为物理伤害（结界分开抵消）。返回实际扣血。
//
// 生命流失（loseHP，图鉴 FAQ：化血 / 哗兵 / 生死别…）「不属于伤害」：不触发与伤害相关的神通（蓄力、坚韧、
// 气甲、月之暗面、返璞归真、雷影庇护…），不被物理 / 法术结界抵消，无敌 / 暴走 / 诈死也挡不住。
func (b *battle) hurt(src *unit, t *unit, id int32, n int64, physical bool) int64 {
	pierce, fixed, noJianRen := b.pierce, b.fixedHit, b.noJianRen
	b.pierce, b.fixedHit, b.noJianRen = false, false, false // 只管这一下：之后触发的死亡技等是普通伤害
	var dealt int64
	defer func() { b.lastDealt = dealt }() // 放在最后：扣血引发的死亡技等嵌套 hurt 不覆盖
	if !t.alive() || n < 0 {
		return 0
	}
	if pierce {
		physical = false
	}
	// 脱帽退敌按受击前的蓄力减免所有伤害，生命流失和守卫的固定一点除外。
	if k := t.tal(famTuoMaoTuiDi); k != nil && !pierce && !fixed && src != t && t.charge > 0 {
		n -= n * int64(min(t.charge*k.param(0), 80)) / 100
	}
	if src != nil && src != t && src.side != t.side && !pierce {
		// 蓄力：受到敌方任何伤害（普攻、法术、神通）都 +1，ADD_POWER 在扣血之前（官方战报：不灭法身等在
		// 普攻 / 法术 IMPACT / 神通命中后、SUB_HP 前蓄力）。中毒、燃烧这类无来源的持续伤害不算。
		b.chargeUp(t)
	}
	if t.bossArmor && src != t {
		// 魔甲 / 藤甲：「免疫百分比流失生命的技能，免疫直接致死技能」。按目标生命百分比结算的法术特效、神通、
		// 持续伤害打在 9.99 亿血的首领身上会是天文数字（满配队伍几回合就能打死），所以：
		// 生命流失、没有来源的持续伤害（中毒 / 燃烧，按上限百分比）免疫；其余每一下至多攻击者当前攻击的
		// bossHitCap 倍（估计：普攻、法术、神通的正常伤害都远低于这个数）。
		switch {
		case pierce || src == nil:
			n = 0
		case src.side != t.side:
			n = min(n, src.att*bossHitCap)
		}
	}
	if src != nil && src.side != t.side && !fixed {
		n = scale(n, src.lead.dmg, t.lead.res) // 队长技：伤害 / 伤害抗性
		if b.qjRace > 0 && src.Race != b.qjRace {
			n /= 2 // 取经特殊关：非指定种族护法伤害减半
		}
		n = qjVuln(src, t, n) // 取经：来自某族的伤害变为 a%
	}
	if !pierce && !fixed {
		n = b.zfAdjust(src, t, n, physical) // 阵法：阵眼存活时的固定伤害加减，在其他减伤、护盾之前（zhenfa.go）
	}
	if src != t && n > 0 && !pierce && !b.invincible(t) && b.leiying(src, t, n, physical) {
		return 0 // 雷影庇护：雷震子代承，先于坚韧、气甲等判定
	}
	if k := t.tal(famJianRen); k != nil && src != t && n > 0 && !pierce && !noJianRen && b.chance(k.param(0)) {
		// 坚韧：受到任何伤害（不只普攻）时 a% 概率提升 b% 当前攻击力并减少等量伤害。
		// 官方战报：神通 / 属性伤害打到带坚韧的护法同样触发，演出在扣血之前。
		up := t.att * int64(k.param(1)) / 100
		b.show(t, k.id, t)
		b.changeAtt(t, k.id, up) // 攻击总上限 初始 ×8 在 changeAtt 里封
		if !fixed {
			n = max(n-up, 0) // 守卫的固定 1 点照扣（坚韧照常触发）
		}
		if k.tjOn() { // 激发：增加 $x% 上限并回复同量（官方战报：坚韧后 CHG_MAXHP + ADD_HP）
			g := int64(float64(t.maxHP) * k.tjX() / 100)
			b.growAndHeal(t, k.id, g)
		}
	}
	if src != t && n > 0 && !pierce {
		var stop bool
		if n, stop = b.beforeHurt(src, t, id, n, physical); stop {
			return 0
		}
	}
	// 转移伤害、反伤及死亡技可能在嵌套结算中杀死本次受击者。
	if !t.alive() {
		return 0
	}
	if physical && b.factor(LxPhys) && !fixed && src != t {
		n = n * int64(100-LxFactorPct[LxPhys]) / 100 // 裂隙「物」：受到的物理伤害减少 20%
	}
	if physical && t.hurtUp > 0 && !fixed {
		n = n * int64(100+t.hurtUp) / 100
	}
	var qijia *talent
	if k := t.tal(famQiJia); k != nil && physical && src != t && n > t.maxHP*int64(k.param(1))/100 {
		n = t.maxHP * int64(k.param(1)) / 100 // 气甲：所有物理伤害（含挥扫、分击、反伤等非攻击行为）不超过上限 b%
		qijia = k
	}
	if k := t.tal(famZhaSi); k != nil && k.tjOn() && src != t && !pierce && n > 0 && t.has(stZhasi) != nil {
		b.addShield(t, int64(float64(n)*k.tjX()/100), false) // 诈死激发：生效后受到的伤害，其值 $x% 转为物理结界
	}
	if src != t && !pierce && b.invincible(t) {
		// 无敌 / 暴走 / 诈死后本回合：伤害照常计算（坚韧、护盾、逆势、蓄力等照常触发），只是不扣血；
		// 生命流失（loseHP）照扣。
		n = 0
	}
	shield := &t.mshield
	cmd := CmdMagicShield
	if physical {
		shield, cmd = &t.shield, CmdShield
	}
	if *shield > 0 && n > 0 && !pierce {
		abs := min(*shield, n)
		*shield -= abs
		n -= abs
		b.shieldCmd(cmd, 2, t, abs) // op 2 = 减少
	}
	if !pierce {
		n = b.moonTransfer(src, t, n)
	}
	if n >= t.hp {
		if s := t.has(stXuming); s != nil { // 续命丹：致命伤害时血量扣为上限的 a%
			n = max(t.hp-max(t.maxHP*s.val/100, 1), 0)
			b.clearStatus(t, stXuming)
		} else if fake := t.tal(famZhaSi); fake != nil && !pierce && n*100 >= t.maxHP*int64(fake.param(0)) &&
			(t.zhasiAt == 0 || b.round-t.zhasiAt >= 3) && b.chance(fake.param(1)) {
			// 诈死：受到致命伤害、且这一下超过生命上限 a% 时 b% 概率存活（描述；按 FAQ「当前生命 ≥ 上限×比率」
			// 判时血量低的护法几乎触发不了，用户反馈改回按描述）；两次触发相隔 ≥3 回合；触发后本回合免疫所有伤害，
			// 只有生命流失（化血…）、灭法清除无敌、踏浪等僵化（诈死失效）能打死。
			t.zhasiAt = b.round
			b.show(t, fake.id, t)
			// 官方战报：诈死后挂 BUFF 5411「诈死」再扣血；本回合之后的伤害 SUB_HP 0（生命流失照扣），回合结束 RM_BUFF
			b.setStatus(t, stZhasi, 0, 1, false, zhasiBuff, slotBuff)
			if fake.tjOn() {
				defer b.addShield(t, int64(float64(n)*fake.tjX()/100), false)
			}
			n = max(t.hp-5, 0)
			b.firstSkill(t, skZhasi)
		}
	}
	dealt = n
	n = min(n, t.hp)
	if qijia != nil && n > 0 {
		defer func() {
			if t.alive() {
				b.show(t, qijia.id, t) // 官方战报：气甲封顶时在 SUB_HP 之后 ACT_DO 指向自己
			}
		}()
	}
	if t.hard > 0 && (!pierce || t.hardAll) && n > 1 && b.chance(t.hard) {
		n = 1 // 取经坚硬：只扣 1 点（坚硬10 不含生命流失，BOSS-坚硬 含）
	}
	// 显示值：只有「伤害超过目标剩余血量」这一种截断才发真实伤害（dealt），避免几千攻击只飘几百的数字。
	// 减伤 / 护盾 / 气甲都已算进 dealt；坚硬把 n 改成 1 时 n != t.hp，仍按实际扣血显示。
	show := n
	if dealt > n && n == t.hp { // 致死一击：客户端飘字用真实伤害，扣血仍按 n
		show = dealt
	}
	b.hpCmd(CmdSubHP, id, t, show)
	t.hp -= n
	if src != nil && src.side != t.side && n > 0 {
		b.hits[src.side]++
	}
	if t.alive() && n > 0 {
		b.qjSplit(t) // 取经孙悟空：分裂
	}
	if b.qjHitDeath > 0 && src != nil && src != t && n > 0 && t.alive() {
		if t.hitsTaken++; t.hitsTaken >= b.qjHitDeath { // 取经特殊关：受到 N 次伤害立即死亡
			defer b.loseHP(src, t, 0, t.hp)
		}
	}
	if !t.alive() {
		b.w.u8(CmdDie)
		b.w.u8(1)
		b.w.u8(t.Pos)
		t.deaths++
		b.died(t)
	}
	if physical && n > 0 && !b.over {
		// 早夭：同方其他带早夭者各分担 50%——这一下把受击者打死也照样分担（用户报：狮吼魔功的早夭没触发）；
		// 打死时在 DIE 之后结算（客户端要求扣到 0 后紧跟 DIE）
		b.shareZaoyao(src, t, n)
	}
	return n
}

// destroy 是摧毁效果（霆威一击必杀、法术摧毁特效）：目标直接阵亡，不经结界、不受免疫伤害影响（FAQ 霆威）；
// 首领（魔甲 / 藤甲）免疫直接致死。
func (b *battle) destroy(src, t *unit, id int32) {
	if k := t.tal(famBuMieBoss); k != nil && b.chance(k.param(0)) {
		b.show(t, k.id, t) // 不灭：a% 概率不受霆威 / 摧毁的秒杀效果影响
		return
	}
	if k := t.tal(famJinShenBoss); k != nil && b.chance(k.param(0)) {
		b.show(t, k.id, t)
		return
	}
	b.loseHP(src, t, id, t.hp)
}

// pctLoss 是按目标上限百分比扣血的量（失血、蚕食）；带「不灭」的首领改按星级 × 500 扣。
func pctLoss(t *unit, pct int64) int64 {
	if t.tal(famBuMieBoss) != nil || t.tal(famJinShenBoss) != nil {
		star := 5
		if pi := gamedata.Pets[t.Icon]; pi != nil && pi.Star > 0 {
			star = pi.Star
		}
		return int64(star) * 500
	}
	return t.maxHP * pct / 100
}

// loseHP 让 t 流失 n 点生命：与 hurt 相同，但不经结界（「转为生命流失」类激发效果）。
func (b *battle) loseHP(src, t *unit, id int32, n int64) int64 {
	b.pierce = true
	return b.hurt(src, t, id, n, false)
}

// heal 回血（不超过上限），输出 ADD_HP。
func (b *battle) heal(t *unit, id int32, n int64) int64 {
	if !t.alive() {
		return 0
	}
	// 治疗效果增减是加减法（FAQ 妙手）：实际 = 基础 ×（1 + 增益 − 减益），最低 0。
	pct := int64(100)
	if s := t.has(stNoHeal); s != nil {
		pct -= s.val
	}
	if k := t.tal(famMiaoShou); k != nil {
		pct += int64(k.param(1)) + int64(k.tjX()) // 自身受治疗效果增加 b%；激发再加 $x%
	}
	n = n * max(pct, 0) / 100
	n = scale(n, t.lead.healin, 0) // 队长技：被治疗效果
	if h := b.actor; h != nil && h.side == t.side {
		n = scale(n, h.lead.healout, 0) // 队长技：回复效果
	}
	if over := n - (t.maxHP - t.hp); over > 0 {
		defer b.qjOverflow(t, over) // 取经「溢出」：溢出的治疗让敌方流失生命
	}
	n = min(n, t.maxHP-t.hp)
	if n <= 0 {
		return 0
	}
	t.hp += n
	b.hpCmd(CmdAddHP, id, t, n)
	return n
}

// changeAtt 改攻击力并输出 ATT_CHANGE（op 1 加 2 减，减不到负数）。
//
// 带坚韧的护法攻击总上限是初始攻击 ×8（FAQ 坚韧「特殊限制」），任何来源的加攻都封顶。
// 返回实际改了多少（有符号；封顶 / 减到 0 时比 delta 小），按它扣回临时加攻。
func (b *battle) changeAtt(t *unit, id int32, delta int64) int64 {
	if delta > 0 && t.tals[famJianRen] != nil {
		delta = min(delta, max(t.baseAtt*8-t.att, 0))
	}
	op, n := 1, delta
	if delta < 0 {
		op, n = 2, min(-delta, t.att)
		t.att -= n
	} else {
		t.att += n
	}
	if n == 0 {
		return 0
	}
	b.w.u8(CmdAttChange)
	b.w.u32(int64(id))
	b.w.u8(1)
	b.w.u8(t.Pos)
	b.w.u8(op)
	b.w.u32(n)
	if op == 1 {
		b.attRaised(t, id)
		return n
	}
	return -n
}

// changeMaxHP 改生命上限（减少时当前生命跟着压到上限以内，客户端同样处理）。
// 上限降到当前生命以下时，超出的生命随后用 SUB_HP（同一个号）扣掉：客户端处理 CHG_MAXHP 只改上限、不动当前生命
// （war/object.lua changeMaxHP），不补这一条血条就停在旧值。官方战报（session-02 lundao:211 破妄）：
// CHG_MAXHP op 2 之后紧跟 SUB_HP(破妄, 超出部分)；当前生命本来就低于新上限的不扣。
// Returns the signed change applied after the cap and any minimum-HP limit.
func (b *battle) changeMaxHP(t *unit, id int32, delta int64) int64 {
	if delta > 0 {
		delta = min(delta, max(t.baseHP*8-t.maxHP, 0))
	}
	if delta < 0 && t.huweiFloor > 0 && t.alive() { // 狐尾：保留线在施加时快照，期间增减上限不重新计算。
		delta = -min(-delta, max(t.maxHP-t.huweiFloor, 0))
	}
	op, n := 1, delta
	if delta < 0 {
		op, n = 2, min(-delta, t.maxHP-1)
		t.maxHP -= n
	} else {
		t.maxHP += n
	}
	if n <= 0 {
		return 0
	}
	b.w.u8(CmdChgMaxHP)
	b.w.u32(int64(id))
	b.w.u8(op)
	b.w.u8(1)
	b.w.u8(t.Pos)
	b.w.u32(n)
	if over := t.hp - t.maxHP; over > 0 {
		t.hp = t.maxHP
		b.hpCmd(CmdSubHP, id, t, over)
	}
	if op == 2 {
		return -n
	}
	return n
}

// growAndHeal ties the accompanying heal to the max-HP increase that fits.
func (b *battle) growAndHeal(t *unit, id int32, growth int64) {
	if added := b.changeMaxHP(t, id, growth); added > 0 {
		b.heal(t, id, added)
	}
}

func (b *battle) addShield(t *unit, n int64, magic bool) {
	if n <= 0 {
		return
	}
	cmd := CmdShield
	if magic {
		cmd = CmdMagicShield
		t.mshield += n
	} else {
		n = min(n, max(t.maxHP*2-t.shield, 0)) // 物理结界最高不超过 2 倍当前生命上限（FAQ：人皇、卦象）
		if n <= 0 {
			return
		}
		t.shield += n
	}
	b.shieldCmd(cmd, 1, t, n)
}

// addZY 改一方战意，输出 ZHANYI（side 1 我方 2 敌方）。
func (b *battle) addZY(side, n int) {
	if n == 0 {
		return
	}
	op := 1
	if n < 0 {
		op, n = 2, min(-n, b.zy[side])
		if n == 0 {
			return
		}
		b.zy[side] -= n
	} else {
		n = min(n, maxZY-b.zy[side]) // 战意上限 100
		if n <= 0 {
			return
		}
		b.zy[side] += n
	}
	b.w.u8(CmdZhanyi)
	b.w.u8(side + 1)
	b.w.u8(op)
	b.w.u8(n)
}

// buff 给 t 挂 buff id（BUFF 命令）。客户端按槽位号记 buff（war/object.lua addBuff：data.buffs[槽位] = 这条），
// 同一槽位再挂别的 buff 只覆盖记录、不删旧特效，之后 RM_BUFF 只能删掉后挂的那个——所以每种 buff 在单位身上
// 各占一个槽位（官方战报也是 2、3、4、5…各不相同），同一种再挂时复用原槽位。
func (b *battle) buff(t *unit, id int32, last int) {
	if t.buffSlot == nil {
		t.buffSlot = map[int32]int{}
	}
	slot, ok := t.buffSlot[id]
	if !ok {
		slot = t.freeSlot()
		t.buffSlot[id] = slot
	} else {
		// 刷新已显示的 buff：先 RM_BUFF 再 BUFF。客户端特效按 BUFF 次数计数、RM_BUFF 只减一次（view/WarObjView.lua
		// addBuff / rmBuff），同槽重复 BUFF 会让特效永远去不掉（用户报：死后、换位后卡上一直在烧）。
		b.w.u8(CmdRmBuff)
		b.w.u8(t.Pos)
		b.w.u8(slot)
	}
	b.buffCmd(t, id, slot, last)
}

// buffCmd 写一条 BUFF（id 挂在 t 的 slot 槽位上，持续 last 回合）。
func (b *battle) buffCmd(t *unit, id int32, slot, last int) {
	b.w.u8(CmdBuff)
	b.w.u32(int64(id))
	b.w.u8(1)
	b.w.u8(t.Pos)
	b.w.u8(slot)
	b.w.u8(last)
}

// rmSlot 去掉 t 的 slot 槽位上的 BUFF（燃烧层这类不按 id 记的槽位）。
func (b *battle) rmSlot(t *unit, slot int) {
	b.w.u8(CmdRmBuff)
	b.w.u8(t.Pos)
	b.w.u8(slot)
}

// rmBuff 移除 t 身上的 buff id（没挂过就不发）。
func (b *battle) rmBuff(t *unit, id int32) {
	slot, ok := t.buffSlot[id]
	if !ok {
		return
	}
	delete(t.buffSlot, id)
	b.w.u8(CmdRmBuff)
	b.w.u8(t.Pos)
	b.w.u8(slot)
}
