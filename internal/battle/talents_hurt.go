package battle

// 受到伤害时触发的神通：属性易伤 / 减免、攻击减伤、伤害转移与代承。
// 抵抗持续效果的不侵、分水见 battle_status.go 的 resist；分水的反伤在 propertyHurt。

func init() {
	register(
		famDaoYiShang, famTuYiShang, famShuiYiShang, famHuoYiShang, famLeiYiShang, famBuQin, famFenShui, famHanBingJia, famWuZhiShan, famYueZhiAnMian, famFanPuGuiZhen, famLeiYingBiHu,
		famWanXianLaiChao,
	)
}

// vulnFamilies 按属性（0 道 1 土 2 水 3 火 4 雷）：受到的该属性伤害变为 a%（暴伤同族，参数更大）。
var vulnFamilies = [5]family{famDaoYiShang, famTuYiShang, famShuiYiShang, famHuoYiShang, famLeiYiShang}

// propertyTaken：属性伤害 n 落到 t 身上前的易伤 / 减免。
func (b *battle) propertyTaken(u, t *unit, n int64, kind int) int64 {
	if kind >= 0 && kind < len(t.ElemRes) && t.ElemRes[kind] > 0 { // 洞府修为：属性减伤
		n -= n * int64(min(t.ElemRes[kind], 100)) / 100
	}
	if kind == 0 && t.DaoImmune > 0 && b.chance(t.DaoImmune) { // 长生殿：几率免疫道属性伤害
		return 0
	}
	n = raceBoost(u, t, n)
	if kind >= 0 && kind < len(vulnFamilies) {
		if k := t.tal(vulnFamilies[kind]); k != nil {
			n = n * int64(k.param(0)) / 100
		}
	}
	if k := t.tal(famHanBingJia); k != nil && (kind == 0 || kind == 2) {
		// 减免 a% 水、道属性伤害（官方战报：ACT_DO 指向自己）。僵化见 hanbingStiff。
		if n > 0 {
			b.show(t, k.id, t)
		}
		n -= n * int64(min(k.param(0), 100)) / 100
	}
	return n
}

// hanbingStiff：寒冰甲——被攻击行为命中后 b% 概率使攻击者僵化（buff 5001），激发再捆缚（5231）。
// 官方战报（lundao:212）：SUB_HP(普攻 → 蛟魔王) → ACT_DO(寒冰甲 → 攻击者) → BUFF 5001 → BUFF 5231。
func (b *battle) hanbingStiff(u, t *unit) {
	k := t.tal(famHanBingJia)
	if k == nil || u == nil || u.side == t.side || !u.alive() || !b.chance(k.param(1)) {
		return
	}
	b.show(t, k.id, u)
	if b.addStatus(u, stStiff, 0, 1, true, 5001, slotDebuff) && k.tjOn() && b.chance(k.tjPct()) {
		b.addStatus(u, stBind, 0, 1, true, 5231, slotDebuff)
	}
}

// attackTaken：普攻伤害落到 victim 前的减伤（五指山、脱帽退敌的蓄力、嘲讽来源）。
func (b *battle) attackTaken(u, victim *unit, dmg int64) int64 {
	dmg = raceBoost(u, victim, dmg)
	if k := victim.tal(famWuZhiShan); k != nil {
		dmg -= dmg * int64(k.param(0)) / 100
	}
	if k := victim.tal(famTuoMaoTuiDi); k != nil && victim.charge > 0 {
		dmg -= dmg * int64(min(victim.charge*k.param(0), 80)) / 100 // 每层蓄力减免 a%，上限 80%
	}
	if s := u.has(stTaunt); s != nil && s.src == victim {
		dmg -= dmg * s.val / 100
	}
	return max(dmg, 0)
}

// raceBoost：洞府修为的种族增伤（攻击方对目标种族 +RaceDmg%）。
func raceBoost(u, t *unit, n int64) int64 {
	if u != nil && t.Race >= 1 && t.Race < len(u.RaceDmg) && u.RaceDmg[t.Race] > 0 {
		n += n * int64(u.RaceDmg[t.Race]) / 100
	}
	return n
}

// leiying：雷影庇护（用户说明）——带遁让的护法受到致命伤害时（物理、法术都算），雷震子自己承受这次伤害，
// 让它遁入后阵。只要本方有带遁让的护法就会发动（雷震子在后阵也算），整场至多 a 次（4 级 4 次），遁让护法自己
// 遁让的次数不计在内；只有雷震子被僵化才不发动（tal 返回 nil）。在伤害结算的最前面判定（先于坚韧、气甲等：
// 致命的这一下先由雷震子挡）。返回 true 表示伤害已被代承。
func (b *battle) leiying(src, t *unit, n int64, physical bool) bool {
	if t.tals[famDunRang] == nil || !t.front() || !b.fatal(t, n, physical) {
		return false
	}
	for _, x := range b.units[t.side] {
		k := x.tal(famLeiYingBiHu)
		if x == t || !x.alive() || x.gone || k == nil || x.leiying >= k.param(0) {
			continue
		}
		x.leiying++
		b.show(x, k.id, t)
		b.hurt(src, x, k.id, n, physical)
		if bn := b.bench(t.side); len(bn) > 0 && t.alive() {
			b.show(t, t.tals[famDunRang].id, t)
			b.swap(t, bn[b.rng.IntN(len(bn))])
		}
		return true
	}
	return false
}

// fatal：n 点伤害落到 t 身上（会猎加成、结界抵消之后）会不会致命。
func (b *battle) fatal(t *unit, n int64, physical bool) bool {
	shield := t.mshield
	if physical {
		if t.hurtUp > 0 {
			n = n * int64(100+t.hurtUp) / 100
		}
		shield = t.shield
	}
	return n-min(n, shield) >= t.hp
}

// beforeHurt 在 hurt 扣结界前调用（src != t、n > 0）：返回改后的伤害；stop 表示伤害已被代承。
//
// 目标无敌（无敌 / 暴走 / 诈死后本回合）时本来就不扣血，免除 / 代承 / 转移伤害类的神通（雷影庇护、返璞归真）都不发动。
func (b *battle) beforeHurt(src, t *unit, id int32, n int64, physical bool) (int64, bool) {
	if b.invincible(t) {
		return n, false
	}
	if k := t.tal(famFanPuGuiZhen); k != nil && t.fpCut < 10 {
		// 伤害减少 a%，b% 概率触发额外效果：把减伤前 c% 转为敌方易死护法的生命流失（不超过自身当前生命 d%）。
		// 每生效（转移）一次，减伤与转移数值各减少原值的 10%，10 次后为 0（图鉴 FAQ：70%/40% → 63%/36% → 56%/32%…）；
		// 被驱逐到后阵再回来或被复活时复原（fpCut 清零）。
		// 官方战报：ACT_DO(返璞归真 → 被转移者) → SUB_HP(≤ 谛听当前生命 25%) → [激发 ATT_CHANGE] → DITING_COUNT(谛听)。
		left := int64(10 - t.fpCut)
		orig := n
		n -= n * int64(k.param(0)) * left / 1000
		if src != nil && src.side != t.side && b.chance(k.param(1)) {
			if x := b.mostHurt(src.side); x != nil {
				move := min(orig*int64(k.param(2))*left/1000, t.hp*int64(k.param(3))/100)
				t.fpCut++
				b.show(t, k.id, x)
				b.loseHP(t, x, k.id, move)
				if k.tjOn() && b.chance(k.tjPct()) {
					b.changeAtt(t, k.id, int64(float64(t.baseAtt)*k.tjY()/100))
				}
				b.w.u8(CmdDitingCount)
				b.w.u8(t.Pos)
			}
		}
	}
	return n, false
}

// moonTransfer：月之暗面——天狗有（本方暴走护法数 × a%）概率把即将扣除的真实血量（结界等结算之后）的 b%
// 以生命流失转给己方暴走护法（带暴走类神通、还没暴走过的护法），最多扣到其上限 c%，已 ≤ c% 的不再转移；
// 阵位优先级 6-7-8-1-2-3-4-5（图鉴 FAQ）。触发了但没有可转移的护法时获得参数 4-7 的神通（一次）。
func (b *battle) moonTransfer(src, t *unit, n int64) int64 {
	k := t.tal(famYueZhiAnMian)
	if k == nil || src == nil || src.side == t.side || n <= 0 {
		return n
	}
	var pool []*unit
	for _, x := range b.units[t.side] {
		if x != t && x.alive() && !x.gone && hasAny(x, rampageTalents) {
			pool = append(pool, x)
		}
	}
	if len(pool) == 0 || !b.chance(len(pool)*k.param(0)) {
		return n
	}
	prio := func(x *unit) int {
		if s := x.slot(); s > frontSlots {
			return s
		} else {
			return s + 100
		}
	}
	var to *unit
	for _, x := range pool {
		if !x.raged && x.hp*100 > x.maxHP*int64(k.param(2)) && (to == nil || prio(x) < prio(to)) {
			to = x
		}
	}
	if to == nil {
		if !t.moonGiven {
			t.moonGiven = true
			b.show(t, k.id, t)
			for _, p := range k.Param[3:] {
				b.grant(t, int32(p))
			}
		}
		return n
	}
	move := min(n*int64(k.param(1))/100, to.hp-to.maxHP*int64(k.param(2))/100)
	if move <= 0 {
		return n
	}
	b.show(t, k.id, to)
	b.loseHP(src, to, k.id, move)
	return n - move
}

// lifeLoss：万仙来朝——鸿钧老祖在前阵存活时，本方仙尊、阵眼护法造成的属性伤害有 a% 概率变为生命流失
// （跳过仙衣、易伤等减免）。
func (b *battle) lifeLoss(u *unit) bool {
	if u == nil || u.tal(famXianZun) == nil && u.tal(famZhenYan) == nil {
		return false
	}
	k := b.wanxian(u.side)
	return k != nil && b.chance(k.param(0))
}

// wanxian 返回一方在前阵存活的万仙来朝神通。
func (b *battle) wanxian(side int) *talent {
	for _, x := range b.frontOf(side) {
		if k := x.tal(famWanXianLaiChao); k != nil {
			return k
		}
	}
	return nil
}

// grant 让 u 获得神通 id（直到战斗结束；同族覆盖）。
func (b *battle) grant(u *unit, id int32) {
	if k := newTalent(id); k != nil {
		u.tals[k.fam] = k
	}
}

// grantRound 让 u 本回合获得神通 id，回合结束由 roundOver 还原。
func (b *battle) grantRound(u *unit, id int32) {
	k := newTalent(id)
	if k == nil {
		return
	}
	if u.roundTals == nil {
		u.roundTals = map[family]*talent{}
	}
	if _, ok := u.roundTals[k.fam]; !ok {
		u.roundTals[k.fam] = u.tals[k.fam]
	}
	u.tals[k.fam] = k
}
