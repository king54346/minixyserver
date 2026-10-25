// 用户报的战斗 bug 的回归测试（每个测试注释写明对应的问题）。

package battle

import (
	"math/rand/v2"
	"testing"
)

// TestTuomaoBench：脱帽退敌——敌方后阵只剩已阵亡（已被补位换下）的护法时也换（被换上的不要求存活）；后阵一张卡
// 都没有时不换（客户端没法把卡放到从没有过卡的位置）。
func TestTuomaoBench(t *testing.T) {
	u := newUnit(fu(1, 100, 10000, 1, levelID(famTuoMaoTuiDi, 4)), 0)
	big := newUnit(fu(101, 900, 10000, 1), 1)
	d := newUnit(fu(106, 10, 1000, 1), 1)
	d.hp, d.gone = 0, true
	b := faqBattle(u, big, d)
	setParam(u.tals[famTuoMaoTuiDi], 1, 100)
	b.releaseCharge(u, u.tals[famTuoMaoTuiDi])
	if big.front() || big.slot() != 6 || d.slot() != 1 {
		t.Fatalf("big slot %d dead slot %d", big.slot(), d.slot())
	}

	u2 := newUnit(fu(1, 100, 10000, 1, levelID(famTuoMaoTuiDi, 4)), 0)
	big2 := newUnit(fu(101, 900, 10000, 1), 1)
	b2 := faqBattle(u2, big2)
	setParam(u2.tals[famTuoMaoTuiDi], 1, 100)
	b2.releaseCharge(u2, u2.tals[famTuoMaoTuiDi])
	if !big2.front() || big2.slot() != 1 {
		t.Fatalf("moved without bench: slot %d", big2.slot())
	}
}

// TestCounterReleasesCharge：反击时蓄力已满先释放蓄力神通（同先攻）。
func TestCounterReleasesCharge(t *testing.T) {
	u := newUnit(fu(1, 100, 10000, 1, levelID(famBaiCao, 4)), 0)
	a := newUnit(fuh(2, 100, 100, 10000, 1), 0)
	o := newUnit(fu(101, 100, 10000, 1), 1)
	b := faqBattle(u, a, o)
	b.setCharge(u, 5)
	b.attackAction(u, o, 100, atkCounter)
	if u.charge != 0 || a.hp <= 100 {
		t.Fatalf("charge %d ally hp %d", u.charge, a.hp)
	}
}

// TestMoshaSoulOnHit：魔煞的锢魂只在打中时挂，被闪避不挂。
func TestMoshaSoulOnHit(t *testing.T) {
	for _, dodge := range []bool{true, false} {
		u := newUnit(fu(1, 100, 10000, 1, levelID(famMoSha, 10)), 0)
		x := newUnit(fu(101, 10, 100000, 1), 1)
		if dodge {
			x = newUnit(fu(101, 10, 100000, 1, levelID(famShanBi, 10)), 1)
			setParam(x.tals[famShanBi], 0, 100)
		}
		b := faqBattle(u, x)
		for range 20 {
			b.attackAction(u, x, 100, atkMain)
		}
		if got := x.has(stNoSoul) != nil; got == dodge {
			t.Fatalf("dodge %v: soul %v", dodge, got)
		}
	}
}

// TestGuardOnePoint：被守卫的护法固定受 1 点（队长技抗性不能把它缩成 0），照常触发坚韧。
func TestGuardOnePoint(t *testing.T) {
	u := newUnit(fu(1, 1000, 100000, 1), 0)
	x := newUnit(fuh(101, 10, 100, 10000, 1, levelID(famJianRen, 10)), 1)
	g := newUnit(fu(102, 10, 100000, 1, levelID(famShouWei, 10)), 1)
	x.lead.res = 50
	setParam(x.tals[famJianRen], 0, 100)
	b := faqBattle(u, x, g)
	att := x.att
	b.attackAction(u, x, 100, atkMain)
	if x.hp != 99 || x.att <= att {
		t.Fatalf("guarded hp %d att %d→%d", x.hp, att, x.att)
	}
}

// TestSplitSameTarget：分击第二下打先攻最高的护法，可以就是主目标本人。
func TestSplitSameTarget(t *testing.T) {
	u := newUnit(fu(1, 1000, 100000, 1, levelID(famFenJi, 10)), 0)
	x := newUnit(fu(101, 10, 100000, 20), 1)
	y := newUnit(fu(102, 10, 100000, 1), 1)
	b := faqBattle(u, x, y)
	b.attackAction(u, x, 100, atkMain)
	if y.hp != 100000 || x.hp >= 100000-int64(u.att)/2 {
		t.Fatalf("x %d y %d", x.hp, y.hp)
	}
}

// TestFollowUpAfterSwap：主攻击后目标被换下场，乱舞改打当前对位，不再打后阵的原目标。
func TestFollowUpAfterSwap(t *testing.T) {
	u := newUnit(fu(1, 100, 100000, 1, levelID(famLuanWu, 10)), 0)
	x := newUnit(fu(101, 10, 100000, 1), 1)
	y := newUnit(fu(106, 10, 100000, 1), 1)
	b := faqBattle(u, x, y)
	setParam(u.tals[famLuanWu], 0, 100)
	b.swap(x, y) // 模拟驱逐：x 到后阵
	b.followUps(u, x, 0)
	if x.hp != 100000 || y.hp == 100000 {
		t.Fatalf("x %d y %d", x.hp, y.hp)
	}
}

// TestPinXueAtRoundLimit：回合用完时战报里有拼血命令（客户端播拼血画面），且在 WAR_END 之前。
func TestPinXueAtRoundLimit(t *testing.T) {
	t1 := []*Fighter{fu(1, 1, 1000000, 1)}
	t2 := []*Fighter{fu(101, 1, 1000000, 1)}
	res := Simulate(t1, t2, rand.New(rand.NewPCG(1, 1)))
	cmds, err := DecodeRecord(res.Record)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(cmds); n < 2 || cmds[n-2].ID != CmdPinXue || cmds[n-1].ID != CmdWarEnd {
		t.Fatalf("tail %+v", cmds[max(n-3, 0):])
	}
}

// TestPelletCount：火炮之术开场前后阵都装弹，并把弹丸数发给客户端头像（XIULUO_COUNT）。
func TestPelletCount(t *testing.T) {
	k := levelID(famHuoPaoZhiShu, 4)
	t1 := []*Fighter{fu(1, 10, 1000000, 1, levelID(famXianGong, 1)), fu(2, 10, 1000000, 1), fu(3, 10, 1000000, 1),
		fu(4, 10, 1000000, 1), fu(5, 10, 1000000, 1), fu(6, 10, 1000000, 1, k)}
	t2 := []*Fighter{fu(101, 1, 1000000, 1)}
	res := Simulate(t1, t2, rand.New(rand.NewPCG(1, 1)))
	cmds, _ := DecodeRecord(res.Record)
	for _, c := range cmds {
		if c.ID == CmdXiuluoCount && c.Actor == 6 && c.Magic > 0 {
			return
		}
	}
	t.Fatal("bench 小夫子 pellet count never sent")
}

// setParam 改单个神通实例的参数（先复制整条 TalentInfo：talent 嵌的是全局神通表的指针，直接改会污染其他测试；
// 同 jifa.go applyTJ）。
func setParam(k *talent, i int, v float64) {
	info := *k.TalentInfo
	info.Param = append([]float64(nil), k.Param...)
	info.Param[i] = v
	k.TalentInfo = &info
}

// TestBajiaoQuzhu：芭蕉扇激发结算后按驱逐规则换人——后阵只有已阵亡的护法时也能换上来（FAQ 驱逐）。
func TestBajiaoQuzhu(t *testing.T) {
	for seed := uint64(0); seed < 50; seed++ {
		u := newUnit(tjf(fu(1, 100, 100, 1, levelID(famBaJiaoShan, 4)), 5), 0)
		x := newUnit(fu(101, 10, 100000, 1), 1)
		d := newUnit(fu(106, 10, 1000, 1), 1)
		d.hp = 0
		b := faqBattle(u, x, d)
		b.rng = rand.New(rand.NewPCG(seed, 1))
		b.deathMore(u)
		if !x.front() {
			if d.slot() != 1 {
				t.Fatalf("dead bench unit not swapped in: slot %d", d.slot())
			}
			return
		}
	}
	t.Fatal("芭蕉扇激发从未驱逐")
}

// TestHuisaoGuardCenter：被守卫代挨时，挥扫打原攻击目标的左右，不是守卫的左右。
func TestHuisaoGuardCenter(t *testing.T) {
	u := newUnit(fu(1, 1000, 100000, 1, levelID(famHuiSao, 10)), 0)
	x := newUnit(fuh(101, 10, 100, 10000, 1), 1)                     // 原目标（濒危，触发守卫）
	nx := newUnit(fu(102, 10, 100000, 1), 1)                         // 原目标右边
	gl := newUnit(fu(104, 10, 100000, 1), 1)                         // 守卫左边
	g := newUnit(fu(105, 10, 100000, 1, levelID(famShouWei, 10)), 1) // 守卫
	setParam(u.tals[famHuiSao], 0, 100)
	b := faqBattle(u, x, nx, gl, g)
	b.attackAction(u, x, 100, atkMain)
	if g.hp == 100000 {
		t.Skip("守卫没触发")
	}
	if nx.hp == 100000 || gl.hp != 100000 {
		t.Fatalf("原目标右边 %d，守卫左边 %d", nx.hp, gl.hp)
	}
}

// TestShenliStacks：神之力按初始攻击的 % 直接相加（9% + 15% = 24%），前阵存活期间一直有效（行动后不清），只显示
// 一个图标（累计 24% 显示神之力8）；回后阵、阵亡清除。
func TestShenliStacks(t *testing.T) {
	u := newUnit(fu(1, 1000, 100000, 1), 0)
	v := newUnit(fu(6, 100, 100000, 1), 0)
	o := newUnit(fu(101, 10, 100000, 1), 1)
	b := faqBattle(u, v, o)
	b.applyBuff(u, u, 5203) // 神之力3 +9%
	b.changeAtt(u, 0, 500)  // 别的加攻：不影响神之力按初始攻击算
	b.applyBuff(u, u, 5205) // 神之力5 +15%
	if u.att != 1000+90+500+150 || u.shenliShown != 5208 || len(u.buffSlot) != 1 {
		t.Fatalf("stack att %d icon %d slots %v", u.att, u.shenliShown, u.buffSlot)
	}
	b.tickStatus(u, true) // 行动后不清除
	b.swap(u, v)          // 回后阵：攻击重置为初始，神之力清除
	if u.att != 1000 || u.shenliShown != 0 || len(u.buffSlot) != 0 {
		t.Fatalf("bench att %d icon %d", u.att, u.shenliShown)
	}
	b.swap(u, v)
	b.applyBuff(u, u, 5203)
	b.hurt(o, u, 1, u.hp, true)
	if u.shenliPct != 0 || len(u.buffSlot) != 0 {
		t.Fatalf("dead kept 神之力: %d %v", u.shenliPct, u.buffSlot)
	}
}

// TestShenweiQuzhu：神威入阵驱逐对位，敌方后阵只有已阵亡的护法时也能换上来（规则同驱逐）。
func TestShenweiQuzhu(t *testing.T) {
	u := newUnit(fu(1, 100, 100000, 1, levelID(famShenWei, 4)), 0)
	o := newUnit(fu(101, 10, 100000, 1), 1)
	d := newUnit(fu(106, 10, 1000, 1), 1)
	d.hp = 0
	b := faqBattle(u, o, d)
	setParam(u.tals[famShenWei], 0, 100)
	b.enter(u)
	if o.front() || d.slot() != 1 {
		t.Fatalf("o front %v, dead slot %d", o.front(), d.slot())
	}
}

// TestBenchKeepsLongStatuses：回后阵时「持续到战斗结束」的状态（燃烧、毒砂、会猎、竭力）保留；攻击、生命上限重置为
// 初始值，神之护之类加成图标去掉；在后阵不结算燃烧。
func TestBenchKeepsLongStatuses(t *testing.T) {
	u := newUnit(fu(1, 1000, 10000, 1), 0)
	v := newUnit(fu(6, 100, 10000, 1), 0)
	o := newUnit(fu(101, 100, 100000, 1), 1)
	b := faqBattle(u, v, o)
	b.addPoison(u, poison{from: 1, pct: 8, left: maxRounds, buff: 5024, burn: true})
	b.applyBuff(o, u, 5035) // 毒砂5
	b.applyBuff(o, u, 5063) // 会猎3
	b.applyBuff(o, u, 5213) // 神之护3（加成）
	b.changeAtt(u, 0, 500)
	hp := u.hp
	b.swap(u, v)
	if len(u.poison) != 1 || u.dusha == 0 || u.huilie == 0 {
		t.Fatalf("bench lost long statuses: poison %d dusha %d huilie %d", len(u.poison), u.dusha, u.huilie)
	}
	if u.att != u.baseAtt || u.maxHP != int64(u.MaxHP) {
		t.Fatalf("not reset: att %d/%d maxHP %d/%d", u.att, u.baseAtt, u.maxHP, u.MaxHP)
	}
	if _, ok := u.buffSlot[5213]; ok {
		t.Fatal("神之护 icon kept on bench")
	}
	b.roundEnd(v) // 前阵的回合结束；后阵的 u 不结算
	if u.hp != min(hp, u.maxHP) {
		t.Fatalf("bench burned: hp %d", u.hp)
	}
}

// TestDebuffBeforeDamage：负面状态先于伤害——魔煞这一击打死带神佑的目标时，锢魂先挂上，神佑无法复活。
func TestDebuffBeforeDamage(t *testing.T) {
	u := newUnit(fu(1, 100000, 100000, 1, levelID(famMoSha, 10)), 0)
	x := newUnit(fu(101, 10, 1000, 1, levelID(famShenYou, 10)), 1)
	setParam(u.tals[famMoSha], 0, 100)
	setParam(x.tals[famShenYou], 0, 100)
	b := faqBattle(u, x)
	b.attackAction(u, x, 100, atkMain)
	if x.alive() || x.has(stNoSoul) == nil {
		t.Fatalf("alive %v soul %v", x.alive(), x.has(stNoSoul) != nil)
	}
}

// TestHudunTJIgnoresTemp：护盾激发降攻按攻击方不含临时加成（突袭等）的攻击算。
func TestHudunTJIgnoresTemp(t *testing.T) {
	u := newUnit(fu(1, 1000, 100000, 1), 0)
	x := newUnit(tjf(fu(101, 100000, 1000000, 1, levelID(famHuDun, 10)), 5), 1)
	setParam(x.tals[famHuDun], 0, 100)
	b := faqBattle(u, x)
	u.att, u.tempAtt = 2000, 1000 // 1000 是这次攻击的临时加成
	b.attack(u, x, 100, atkMain)
	if want := int64(2000) - int64(1000*x.tals[famHuDun].tjX()/100); u.att != want {
		t.Fatalf("att %d, want %d", u.att, want)
	}
}

// TestNoHealUndispellable：禁疗持续 5 回合、驱散类效果清不掉（其他负面状态照常清）。
func TestNoHealUndispellable(t *testing.T) {
	u := newUnit(fu(1, 1000, 10000, 1), 0)
	o := newUnit(fu(101, 100, 100000, 1), 1)
	b := faqBattle(u, o)
	b.applyBuff(o, u, 5085) // 禁疗5
	b.applyBuff(o, u, 5001) // 僵化
	if s := u.has(stNoHeal); s == nil || s.left != noHealRounds {
		t.Fatalf("no-heal %+v", s)
	}
	b.clearAllDebuffs(u)
	if u.has(stNoHeal) == nil || u.has(stStiff) != nil {
		t.Fatalf("dispel: noheal %v stiff %v", u.has(stNoHeal), u.has(stStiff))
	}
}

// TestAttachNeedsHit：魔煞锢魂、兽妖禁疗、象形捆缚、毒爪这类「攻击附带 / 攻击后」的状态必须打中才挂——被闪避不挂。
func TestAttachNeedsHit(t *testing.T) {
	for _, dodge := range []float64{100, 0} {
		u := newUnit(fu(1, 100, 100000, 1, levelID(famMoSha, 10), levelID(famXiangXing, 4), levelID(famDuZhao, 10)), 0)
		x := newUnit(fu(101, 10, 100000, 1, levelID(famShanBi, 10)), 1)
		setParam(u.tals[famMoSha], 0, 100)
		setParam(u.tals[famXiangXing], 0, 100)
		setParam(u.tals[famDuZhao], 0, 100)
		setParam(x.tals[famShanBi], 0, dodge)
		b := faqBattle(u, x)
		b.attackAction(u, x, 100, atkMain)
		got := x.has(stNoSoul) != nil || x.has(stBind) != nil || len(x.poison) > 0
		if want := dodge == 0; got != want || x.has(stNoSoul) != nil != want {
			t.Fatalf("dodge %v: soul %v bind %v poison %d", dodge, x.has(stNoSoul) != nil, x.has(stBind) != nil, len(x.poison))
		}
	}
}

// TestAttachBeforeVsAfter：「攻击前附带」（魔煞锢魂）在扣血前挂，这一下打死也有；「攻击后」（象形捆缚）在扣血后，
// 目标已被打死就不挂。
func TestAttachBeforeVsAfter(t *testing.T) {
	u := newUnit(fu(1, 100000, 100000, 1, levelID(famMoSha, 10), levelID(famXiangXing, 4)), 0)
	x := newUnit(fu(101, 10, 1000, 1, levelID(famShenYou, 10)), 1)
	setParam(u.tals[famMoSha], 0, 100)
	setParam(u.tals[famXiangXing], 0, 100)
	b := faqBattle(u, x)
	b.attackAction(u, x, 100, atkMain)
	if x.alive() || x.has(stNoSoul) == nil || x.has(stBind) != nil {
		t.Fatalf("alive %v soul %v bind %v", x.alive(), x.has(stNoSoul) != nil, x.has(stBind) != nil)
	}
}

// TestTuomaoNoChase：反击 / 先攻时释放脱帽把对位换到后阵，这一下改打换上来的护法，不追到后阵打原对位。
func TestTuomaoNoChase(t *testing.T) {
	u := newUnit(fu(1, 100000, 100000, 1, levelID(famTuoMaoTuiDi, 4)), 0)
	big := newUnit(fu(101, 900, 10000, 1), 1)
	y := newUnit(fu(106, 10, 1000, 1), 1)
	b := faqBattle(u, big, y)
	setParam(u.tals[famTuoMaoTuiDi], 1, 100)
	u.charge = chargeNeed(u.tals[famTuoMaoTuiDi])
	b.attackAction(u, big, 100, atkCounter)
	if big.front() || big.hp != big.maxHP || y.alive() {
		t.Fatalf("big front %v hp %d/%d, new front alive %v", big.front(), big.hp, big.maxHP, y.alive())
	}
}

// TestTalangStiffAfterHit：踏浪先加僵化再结算伤害（用户确认 21571–21601 一组）——这一下受击方的仙衣、反击就已失效。
func TestTalangStiffFirst(t *testing.T) {
	u := newUnit(fu(1, 100, 1000000, 1, levelID(famTaLang, 10)), 0)
	x := newUnit(fu(101, 100, 1000000, 1, levelID(famFanJi, 10), levelID(famXianYi, 10)), 1)
	setParam(u.tals[famTaLang], 0, 100)
	setParam(x.tals[famFanJi], 0, 100)
	setParam(x.tals[famXianYi], 1, 50)
	b := faqBattle(u, x)
	b.attackAction(u, x, 100, atkMain)
	if x.has(stStiff) == nil || u.hp != u.maxHP || x.maxHP-x.hp != 150 {
		t.Fatalf("stiff %v, counter dmg %d, taken %d", x.has(stStiff) != nil, u.maxHP-u.hp, x.maxHP-x.hp)
	}
}

// TestBatch1022b：锢魂的死者不被法术复活；早夭在受击者被打死时照样分担；僵化时照样蓄力；打死目标后仙剑改打对位。
func TestBatch1022b(t *testing.T) {
	u := newUnit(fu(1, 1000, 10000, 1, levelID(famTuoMaoTuiDi, 4)), 0)
	d := newUnit(fu(2, 10, 1000, 1), 0)
	o := newUnit(fu(101, 100, 1000, 1), 1)
	m := newUnit(fu(102, 100, 100000, 1), 1)
	b := faqBattle(u, d, o, m)
	b.setStatus(d, stNoSoul, 0, maxRounds, false, 5141, slotDebuff)
	d.hp = 0
	if ts := b.magicTargets(u, 0, true, nil); len(ts) != 0 {
		t.Fatalf("revive target with 锢魂: %v", ts[0].Pos)
	}

	b.setStatus(u, stStiff, 0, 1, true, 5001, slotDebuff)
	b.chargeUp(u)
	if u.charge != 1 {
		t.Fatalf("stiff charge %d", u.charge)
	}

	b.addStatus(m, stZaoyao, m.maxHP*40/100, 1, false, 5260, slotDebuff)
	hp := m.hp
	b.hurt(u, o, 1, 1000, true)
	if o.alive() || m.hp != hp-500 {
		t.Fatalf("zaoyao share on kill: o alive %v, m %d -> %d", o.alive(), hp, m.hp)
	}

	v := newUnit(fu(1, 1000, 10000, 1), 0)
	x := newUnit(fu(101, 10, 100, 1), 1)
	y := newUnit(fu(102, 10, 100000, 1), 1)
	b2 := faqBattle(v, x, y)
	v.swords, v.swordKind, v.swordSet = 1, 3, true
	b2.attackAction(v, x, 100, atkMain)
	if x.alive() || y.hp != y.maxHP-v.baseAtt/2 || v.swords != 0 {
		t.Fatalf("sword after kill: y %d/%d swords %d", y.hp, y.maxHP, v.swords)
	}
}

// TestBumiequDevoured：不灭躯是死亡后再复活，可被烈焰吞噬——被吞了就不复活。
func TestBumiequDevoured(t *testing.T) {
	u := newUnit(fu(1, 1000, 10000, 1, levelID(famLieYanTunShi, 4)), 0)
	x := newUnit(fu(101, 100, 1000, 1, levelID(famBuMieQu, 6)), 1)
	b := faqBattle(u, x)
	b.round = 1 // 第 1 回合吞噬 100%
	b.hurt(u, x, 1, 5000, true)
	if x.alive() || !x.eaten {
		t.Fatalf("alive %v eaten %v", x.alive(), x.eaten)
	}
}

// TestBatch1023：自带 + 套装两个突袭各自判定；属性攻击（踏浪）无法闪避，僵化先挂、对方先攻发动不了；
// 仙剑打死目标后剩下的改打对位；噬心龙咒的龙回后阵照样重置，再入阵攻击恢复到本场最高值。
func TestBatch1023(t *testing.T) {
	u := newUnit(fu(1, 1000, 100000, 1, levelID(famTuXi, 10), levelID(famTuXi, 5)), 0)
	x := newUnit(fu(101, 10, 1000000, 1), 1)
	b := faqBattle(u, x)
	for _, k := range append([]*talent{u.tals[famTuXi]}, u.dupTals[famTuXi]...) {
		setParam(k, 0, 100)
	}
	b.tuxi(u)
	if u.tempAtt != 500+750 {
		t.Fatalf("double tuxi temp %d", u.tempAtt)
	}

	v := newUnit(fu(1, 100, 100000, 1, levelID(famTaLang, 10)), 0)
	y := newUnit(fu(101, 100, 1000000, 1, levelID(famShanBi, 10), levelID(famXianGong, 10)), 1)
	setParam(v.tals[famTaLang], 0, 100)
	setParam(y.tals[famShanBi], 0, 100)
	setParam(y.tals[famXianGong], 0, 100)
	b2 := faqBattle(v, y)
	b2.attackAction(v, y, 100, atkMain)
	if y.hp == y.maxHP || y.has(stStiff) == nil || v.hp != v.maxHP {
		t.Fatalf("elemental: y took %d stiff %v, v took %d", y.maxHP-y.hp, y.has(stStiff) != nil, v.maxHP-v.hp)
	}

	w := newUnit(fu(1, 1000, 100000, 1), 0)
	z1 := newUnit(fu(101, 10, 100, 1), 1)
	z2 := newUnit(fu(102, 10, 100000, 1), 1)
	b3 := faqBattle(w, z1, z2)
	w.swords, w.swordKind, w.swordSet = 3, 3, true
	b3.swordStrike(w, z1)
	if z1.alive() || z2.maxHP-z2.hp != 2*w.baseAtt/2 {
		t.Fatalf("swords after kill: z2 took %d", z2.maxHP-z2.hp)
	}

	d := newUnit(fu(1, 1000, 10000, 1, levelID(famShiXinLongZhou, 4)), 0)
	e := newUnit(fu(6, 100, 10000, 1), 0)
	b4 := faqBattle(d, e, newUnit(fu(101, 10, 1000, 1), 1))
	b4.changeAtt(d, 0, 500)
	b4.changeMaxHP(d, 0, 700)
	b4.swap(d, e)
	if d.att != 1000 || d.maxHP != 10000 {
		t.Fatalf("dragon not reset on bench: att %d maxHP %d", d.att, d.maxHP)
	}
	b4.swap(e, d)
	if d.att != 1500 {
		t.Fatalf("dragon re-enter att %d, want peak 1500", d.att)
	}
}

// TestHengchongKillsGuard：横冲直撞打死替目标挨打的守卫也算击败，继续追击。
func TestHengchongKillsGuard(t *testing.T) {
	u := newUnit(fu(1, 100000, 1000000, 1, levelID(famHengChongZhiZhuang, 4)), 0)
	x := newUnit(fu(101, 10, 100000, 1), 1)
	g := newUnit(fu(102, 10, 50000, 1, levelID(famShouWei, 10)), 1)
	z := newUnit(fu(103, 10, 1000000, 1), 1)
	setParam(g.tals[famShouWei], 0, 100)
	setParam(g.tals[famShouWei], 1, 0)
	b := faqBattle(u, x, g, z)
	x.hp = 40000 // 低于守卫血量，触发守卫
	b.attackAction(u, x, 100, atkMain)
	if g.alive() || !u.mainKill || u.perRound[0] == 0 {
		t.Fatalf("guard alive %v mainKill %v chases %d", g.alive(), u.mainKill, u.perRound[0])
	}
}

// TestElemDebuffOnGuard：踏浪等属性攻击被守卫代为承受时，僵化挂在守卫身上，原目标不挂。
func TestElemDebuffOnGuard(t *testing.T) {
	u := newUnit(fu(1, 100, 1000000, 1, levelID(famTaLang, 10)), 0)
	x := newUnit(fu(101, 10, 100000, 1), 1)
	g := newUnit(fu(102, 10, 100000, 1, levelID(famShouWei, 10)), 1)
	setParam(u.tals[famTaLang], 0, 100)
	setParam(g.tals[famShouWei], 0, 100)
	b := faqBattle(u, x, g)
	x.hp = 40000
	b.attackAction(u, x, 100, atkMain)
	if g.has(stStiff) == nil || x.has(stStiff) != nil || g.hp == g.maxHP {
		t.Fatalf("guard stiff %v target stiff %v guard took %d", g.has(stStiff) != nil, x.has(stStiff) != nil, g.maxHP-g.hp)
	}
}

// TestMiefaNoCompound：灭法是伤害提高（结算时乘）不是加攻：乱舞的每一下伤害相同、攻击不变、不层层叠加。
func TestMiefaNoCompound(t *testing.T) {
	u := newUnit(fu(1, 1000, 1000000, 1, levelID(famMieFa, 10), levelID(famLuanWu, 10)), 0)
	x := newUnit(fu(101, 10, 10000000, 1, levelID(famXianYi, 1)), 1)
	setParam(u.tals[famLuanWu], 0, 100)
	b := faqBattle(u, x)
	n := len(b.w.b)
	b.attackAction(u, x, 100, atkMain)
	cmds, err := DecodeRecord(append(append([]byte{CmdWarStart, 0}, b.w.b[n:]...), CmdWarEnd, 1))
	if err != nil {
		t.Fatal(err)
	}
	var hits []int64
	for _, c := range cmds {
		if c.ID == CmdAttChange {
			t.Fatalf("attack changed: %+v", c)
		}
		if c.ID == CmdSubHP && c.Targets[0][0] == 101 {
			hits = append(hits, c.Targets[0][1])
		}
	}
	want := int64(1000 * (100 + u.tals[famMieFa].param(0)) / 100)
	if len(hits) < 2 || hits[0] != want || hits[1] != want {
		t.Fatalf("hits %v, want %d each", hits, want)
	}
}

// TestYitaiDamageUp：异态「伤害提升 a%」结算伤害时乘，不改攻击。
func TestYitaiDamageUp(t *testing.T) {
	u := newUnit(fu(1, 1000, 1000000, 1, levelID(famYiTai, 10)), 0)
	x := newUnit(fu(101, 10, 10000000, 1), 1)
	b := faqBattle(u, x)
	b.setStatus(x, stBlind, 0, 1, true, 5041, slotDebuff)
	x.status[stBlind].foe = true
	got := b.attackAction(u, x, 100, atkMain)
	if want := int64(1000 * (100 + u.tals[famYiTai].param(0)) / 100); got != want || u.att != 1000 {
		t.Fatalf("dmg %d want %d, att %d", got, want, u.att)
	}
}
