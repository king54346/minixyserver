// 用户报的战斗 bug 的回归测试（每个测试注释写明对应的问题）。

package battle

import (
	"math/rand/v2"
	"testing"

	"turnrpg/internal/gamedata"
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
		if c.ID == CmdXiuluoCount && c.Actor == 6 {
			// 一个先攻护法 × 每先攻 12 枚（2026-11-15 调整，原为 8）。
			if c.Magic != 12 {
				t.Fatalf("pellet count = %d, want 12", c.Magic)
			}
			return
		}
	}
	t.Fatal("bench 小夫子 pellet count never sent")
}

// TestHuoPaoEntryFixedShots：敌方护法入阵固定发射 5 颗弹丸，不再随天将激发增加（2026-11-15 调整）。
func TestHuoPaoEntryFixedShots(t *testing.T) {
	for _, tj := range []int{0, 5} {
		foe := newUnit(tjf(fu(101, 0, 10000, 1, levelID(famHuoPaoZhiShu, 4)), tj), 1)
		foe.pellets = 10
		out := newUnit(fu(1, 0, 1000, 1), 0)
		in := newUnit(fu(6, 0, 1000, 1), 0)
		b := faqBattle(out, in, foe)
		b.swap(out, in)
		n := 0
		for _, c := range mustDecode(t, b.w.b) {
			if c.ID == CmdActDo && c.Magic == int64(foe.tals[famHuoPaoZhiShu].id) {
				n++
			}
		}
		if n != huoPaoEntryShots {
			t.Fatalf("tj=%d entry shots=%d, want %d", tj, n, huoPaoEntryShots)
		}
	}
}

// TestHuoPaoTJRefill：火炮之术天匠（激发）每 3 回合把弹丸回满；没有激发时只按回合减半（2026-11-15 调整）。
func TestHuoPaoTJRefill(t *testing.T) {
	for _, tj := range []int{0, 5} {
		u := newUnit(tjf(fu(6, 100, 1000, 1, levelID(famHuoPaoZhiShu, 4)), tj), 0)
		ally := newUnit(fu(1, 0, 1000, 1, levelID(famXianGong, 1)), 0)
		foe := newUnit(fu(101, 0, 1000, 1), 1)
		b := faqBattle(ally, u, foe)
		b.initPellets(u)
		if u.pellets != 12 || u.pellFull != 12 {
			t.Fatalf("tj=%d 开场 pellets=%d full=%d", tj, u.pellets, u.pellFull)
		}
		for i := 0; i < huoPaoTJResetRounds; i++ {
			b.roundOver()
		}
		if tj > 0 && u.pellets != 12 {
			t.Fatalf("带激发 3 回合后应回满 12，实际 %d", u.pellets)
		}
		if tj == 0 && u.pellets == 12 {
			t.Fatalf("无激发不应回满，实际 %d", u.pellets)
		}
	}
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

// TestHuisaoGuardCenter：被守卫代挨时，挥扫打守卫的左右。
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
		t.Fatal("守卫没触发")
	}
	if nx.hp != 100000 || gl.hp != 99500 {
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

// TestShenweiQuzhu：神威入阵先对对位及左右输出，再驱逐对位；对位无活口时不施放；敌方后阵只有已阵亡的护法时也能换上来。
func TestShenweiQuzhu(t *testing.T) {
	u := newUnit(fu(2, 100, 100000, 1, levelID(famShenWei, 4)), 0)
	o1 := newUnit(fu(101, 10, 100000, 1), 1)
	o2 := newUnit(fu(102, 10, 100000, 1), 1) // 对位
	o3 := newUnit(fu(103, 10, 100000, 1), 1)
	o5 := newUnit(fu(105, 10, 100000, 1), 1) // 非相邻，不应受神威伤害
	d := newUnit(fu(106, 10, 1000, 1), 1)
	d.hp = 0
	b := faqBattle(u, o1, o2, o3, o5, d)
	setParam(u.tals[famShenWei], 0, 100)
	b.enter(u)
	// 先对对位及左右（101, 102, 103）造成伤害，然后再把对位 102 驱逐到后阵（105 不受伤）
	if o1.hp >= o1.maxHP || o2.hp >= o2.maxHP || o3.hp >= o3.maxHP || o5.hp != o5.maxHP {
		t.Fatalf("shenwei facing3 hurt: o1=%d o2=%d o3=%d o5=%d", o1.hp, o2.hp, o3.hp, o5.hp)
	}
	if o2.front() || d.slot() != 2 {
		t.Fatalf("o2 front %v, dead slot %d", o2.front(), d.slot())
	}

	// 对位为空仍伤害相邻存活目标，非相邻目标不受影响。
	hp1, hp3, hp5 := o1.hp, o3.hp, o5.hp
	b.enter(u)
	if o1.hp >= hp1 || o3.hp >= hp3 || o5.hp != hp5 {
		t.Fatalf("shenwei fired without living opposite: o1=%d o3=%d o5=%d", o1.hp, o3.hp, o5.hp)
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

// 踏浪封住目标时，这一击也不会触发目标的减伤或反击。
func TestTalangStiffBeforeHit(t *testing.T) {
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
	cmds, err := DecodeRecord(b.w.b)
	if err != nil {
		t.Fatal(err)
	}
	for i, cmd := range cmds {
		if cmd.ID == CmdActDo && cmd.Magic == attackNormal && cmd.Actor == u.Pos {
			if !hasCmd(cmds[:i], CmdBuff, talangIceBuff, 101, -1) {
				t.Fatal("踏浪僵化应在普攻出手前写入战报")
			}
			return
		}
	}
	t.Fatal("战报缺少普攻出手")
}

func TestTalangRunzeAndIceResistance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fenshui float64
		buqin   float64
		shield  int64
		hp      int64
		frozen  bool
	}{
		{"survives", 0, 0, 0, 1000, true},
		{"fenshui", 100, 0, 0, 1000, false},
		{"buqin", 0, 100, 0, 1000, false},
		{"shield_breaks", 0, 0, 1, 1000, true},
		{"dies", 0, 0, 0, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := newUnit(fu(1, 100, 1000, 1, levelID(famTaLang, 10)), 0)
			x := newUnit(fu(101, 10, 1000, 1, levelID(famRunZe, 1), levelID(famFenShui, 10), levelID(famBuQin, 10), levelID(famFanJi, 10)), 1)
			x.hp, x.mshield = tc.hp, tc.shield
			setParam(u.tals[famTaLang], 0, 100)
			setParam(x.tals[famFenShui], 0, tc.fenshui)
			setParam(x.tals[famBuQin], 0, tc.buqin)
			setParam(x.tals[famFanJi], 0, 100)
			b := faqBattle(u, x)
			b.attackAction(u, x, 100, atkMain)
			cmds, err := DecodeRecord(b.w.b)
			if err != nil {
				t.Fatal(err)
			}
			canReact := x.alive() && !tc.frozen
			if got := hasCmd(cmds, CmdActDo, int64(famRunZe), 101, -1); got != canReact {
				t.Fatalf("润泽触发=%v, 预期=%v", got, canReact)
			}
			if got := hasCmd(cmds, CmdActDo, int64(x.tals[famFanJi].id), 101, -1); got != canReact {
				t.Fatalf("反击触发=%v, 预期=%v", got, canReact)
			}
			if got := x.has(stStiff) != nil; got != tc.frozen {
				t.Fatalf("冰封=%v, 预期=%v", got, tc.frozen)
			}
			if tc.fenshui == 100 && u.hp == u.maxHP {
				t.Fatal("分水挡住冰封后没有结算反伤")
			}
		})
	}
}

func TestXixueMaxHPCutRequiresDamage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		immune bool
		shield int64
		hp     int64
		maxHP  int64
	}{
		{"immune", true, 0, 100, 1000},
		{"fully_absorbed", false, 100, 100, 1000},
		{"partially_absorbed", false, 99, 200, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := newUnit(fu(1, 100, 1000, 1, levelID(famXiXue, 10)), 0)
			x := newUnit(fu(101, 10, 1000, 1), 1)
			u.hp = 100
			u.tals[famXiXue].tj = 5
			x.shield = tc.shield
			b := faqBattle(u, x)
			if tc.immune {
				b.setStatus(x, stImmune, 0, 1, false, 5321, slotBuff)
			}
			b.attackAction(u, x, 100, atkMain)
			if u.hp != tc.hp || x.maxHP != tc.maxHP {
				t.Fatalf("回血后=%d，目标生命上限=%d，预期=%d/%d", u.hp, x.maxHP, tc.hp, tc.maxHP)
			}
		})
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
	if x.alive() || y.hp != y.maxHP-v.baseAtt/2 || v.swords != 1 {
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

// TestBatch1023：自带 + 套装两个突袭各自判定；踏浪封住闪避和先攻；
func TestSwordStrikeStopsWhenDeathSkillKillsOwner(t *testing.T) {
	owner := newUnit(fu(1, 1000, 100, 1), 0)
	dying := newUnit(fu(101, 1000, 100, 1, 21794), 1) // 诸佛消散
	next := newUnit(fu(103, 100, 10000, 1), 1)
	b := faqBattle(owner, dying, next)
	owner.swords, owner.swordKind, owner.swordSet = 3, 0, true
	b.fireSwords(owner, dying)
	if owner.alive() || owner.swords != 0 || next.hp != next.maxHP {
		t.Fatalf("owner alive=%t, swords=%d, next hp=%d", owner.alive(), owner.swords, next.hp)
	}
	count := 0
	for _, cmd := range mustDecode(t, b.w.b) {
		if cmd.ID == CmdSwordAttack {
			count++
			if cmd.Targets[0] != [2]int64{101, 1} {
				t.Fatalf("unexpected sword animation: %+v", cmd)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected one sword animation, got %d", count)
	}
}

// 仙剑打死目标后剩下的改打对位；噬心龙咒的龙回后阵照样重置，再入阵攻击恢复到本场最高值。
func TestBatch1023(t *testing.T) {
	u := newUnit(fu(1, 9462, 100000, 1, levelID(famTuXi, 10), levelID(famTuXi, 5)), 0)
	x := newUnit(fu(101, 10, 1000000, 1), 1)
	b := faqBattle(u, x)
	for _, k := range append([]*talent{u.tals[famTuXi]}, u.dupTals[famTuXi]...) {
		setParam(k, 0, 100)
	}
	b.tuxi(u)
	if u.att != 18924 || u.tempAtt != 9462 {
		t.Fatalf("double tuxi: attack=%d temporary=%d", u.att, u.tempAtt)
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

// TestMagicAudit1027：法术校对——救援时原目标受固定 1 点（不吃阵法加伤）；驱散清掉增益；附带状态几率按等级 − 1。
func TestMagicAudit1027(t *testing.T) {
	f := fu(3, 3000, 100000, 1)
	f.Grade = 100
	u := newUnit(f, 0)
	x := newUnit(fu(101, 100, 200000, 1, levelID(famJiuYuan, 10)), 1)
	tt := newUnit(fuh(103, 100, 2000, 25000, 1), 1)
	b := faqBattle(u, x, tt)
	n := len(b.w.b)
	b.cast(u, &castable{id: 12501, lv: 10, left: 2, MagicInfo: gamedata.Magics[12501]})
	cmds, _ := DecodeRecord(append(append([]byte{CmdWarStart, 0}, b.w.b[n:]...), CmdWarEnd, 1))
	var toT []int64
	for _, c := range cmds {
		if c.ID == CmdSubHP && c.Targets[0][0] == 103 {
			toT = append(toT, c.Targets[0][1])
		}
	}
	if len(toT) != 1 || toT[0] != 1 || b.fixedHit {
		t.Fatalf("rescued target hits %v fixedHit left %v", toT, b.fixedHit)
	}

	y := newUnit(fu(101, 1000, 100000, 1), 1)
	b2 := faqBattle(newUnit(fu(1, 100, 1000, 1), 0), y)
	b2.setStatus(y, stImmune, 0, 1, false, 5321, slotBuff)
	b2.shenli(y, 5205, 15)
	b2.spEffect(b2.units[0][0], y, 15354, 12, 100)
	if y.has(stImmune) != nil || y.att != 1000 {
		t.Fatalf("驱散: immune %v att %d", y.has(stImmune) != nil, y.att)
	}
}

// TestMolongOrder：魔龙（应龙）回合开始吸取 a%×当前攻击 的结界：敌方法术 → 敌方物理 → 我方法术 → 我方物理，额度用完为止。
func TestMolongOrder(t *testing.T) {
	u := newUnit(fu(1, 1000, 100000, 1, levelID(famMoLong, 4)), 0) // 140% × 1000 = 1400
	a := newUnit(fu(2, 10, 100000, 1), 0)
	e1 := newUnit(fu(101, 10, 100000, 1), 1)
	e2 := newUnit(fu(102, 10, 100000, 1), 1)
	b := faqBattle(u, a, e1, e2)
	e1.mshield, e1.shield, e2.mshield, a.mshield, a.shield = 500, 300, 200, 1000, 1000
	b.roundStart(u)
	if e1.mshield != 0 || e2.mshield != 0 || e1.shield != 0 || a.mshield != 600 || a.shield != 1000 || u.mshield != 1100 || u.shield != 300 {
		t.Fatalf("e1 %d/%d e2 %d ally %d/%d self %d/%d", e1.mshield, e1.shield, e2.mshield, a.mshield, a.shield, u.mshield, u.shield)
	}
}

// TestAudit1：神通校对第 1 批——人圣按盲目 b（人圣10 = 盲目5）；突袭不触发反击 / 护体；驭兵按剑数保留加值；
// 兽王死亡时有法术结界触发神佑10。
func TestAudit1(t *testing.T) {
	u := newUnit(raced(fu(1, 100, 100000, 1, levelID(famRenSheng, 10)), raceHuman), 0)
	x := newUnit(fu(101, 100, 100000, 1), 1)
	setParam(u.tals[famRenSheng], 0, 100)
	b := faqBattle(u, x)
	b.onRenSheng(u, u.tals[famRenSheng])
	if s := x.has(stBlind); s == nil || s.val != 50 {
		t.Fatalf("人圣10 盲目 %+v", s)
	}

	v := newUnit(fu(1, 1000, 100000, 1, levelID(famTuXi, 10)), 0)
	y := newUnit(fu(101, 100, 100000, 1, levelID(famFanJi, 10), levelID(famHuTi, 10)), 1)
	setParam(v.tals[famTuXi], 0, 100)
	setParam(y.tals[famFanJi], 0, 100)
	setParam(y.tals[famHuTi], 0, 100)
	b2 := faqBattle(v, y)
	b2.attackAction(v, y, 100, atkMain)
	if v.hp != v.maxHP {
		t.Fatalf("突袭后仍被反击 / 护体：v 掉血 %d", v.maxHP-v.hp)
	}

	w := newUnit(fu(1, 1000, 100000, 1, levelID(famYuBing, 10)), 0)
	z := newUnit(fu(101, 10, 10000000, 1), 1)
	setParam(w.tals[famYuBing], 0, 100)
	w.swords, w.swordKind, w.swordSet = 2, 3, true
	b3 := faqBattle(w, z)
	b3.attackAction(w, z, 100, atkMain)
	if want := int64(1000 + 500*40/100); w.att != want { // 加 50%（500），2 把剑保留 40%
		t.Fatalf("驭兵保留：att %d want %d", w.att, want)
	}

	k := newUnit(fu(1, 100, 1000, 1, levelID(famShouWang, 10)), 0)
	o := newUnit(fu(101, 100000, 100000, 1), 1)
	b4 := faqBattle(k, o)
	b4.rng = rand.New(rand.NewPCG(3, 3))
	revived := false
	for i := 0; i < 20 && !revived; i++ {
		k.hp, k.mshield, k.shenyouAt, b4.round = 1000, 10, 0, 1
		b4.hurt(o, k, 1, 100000, true) // 物理伤害：法术结界不抵消，阵亡时还在
		revived = k.alive()
	}
	if !revived {
		t.Fatal("兽王带法术结界阵亡 20 次一次都没触发神佑10")
	}
}

// TestRaceSum：魔煞等「每名同族护法 b%×其攻击力」按每名同族各自的数值相加（用户确认）。
func TestRaceSum(t *testing.T) {
	u := newUnit(raced(fu(1, 3000, 100000, 1, levelID(famMoSha, 10)), raceGhost), 0)
	a := newUnit(raced(fu(2, 2000, 100000, 1), raceGhost), 0)
	c := newUnit(raced(fu(6, 1000, 100000, 1), raceGhost), 0) // 后阵也算
	x := newUnit(fu(101, 10, 10000000, 1), 1)
	setParam(u.tals[famMoSha], 0, 100)
	b := faqBattle(u, a, c, x)
	b.beforeAttack(u, x)
	if want := int64(6000 * u.tals[famMoSha].param(1) / 100); u.tempAtt != want {
		t.Fatalf("魔煞加攻 %d want %d", u.tempAtt, want)
	}
}

// TestAudit2：第 2 批——削弱同时降先攻；化血被闪避不触发；弱化降的攻击不超过攻击方当前攻击。
func TestAudit2(t *testing.T) {
	u := newUnit(fu(1, 1000, 100000, 5, levelID(famXueRuo, 10)), 0)
	x := newUnit(fu(101, 1000, 100000, 10), 1)
	setParam(u.tals[famXueRuo], 1, 100)
	b := faqBattle(u, x)
	b.attackAction(u, x, 100, atkMain)
	if x.has(stWeak) == nil || x.has(stSlow) == nil || x.speed() != max(10-u.tals[famXueRuo].param(0), 1) {
		t.Fatalf("削弱：weak %v slow %v speed %d", x.has(stWeak) != nil, x.has(stSlow) != nil, x.speed())
	}

	v := newUnit(fu(1, 1000, 100000, 1, levelID(famHuaXue, 10)), 0)
	y := newUnit(fu(101, 10, 100000, 1, levelID(famShanBi, 10)), 1)
	setParam(v.tals[famHuaXue], 0, 100)
	setParam(y.tals[famShanBi], 0, 100)
	b2 := faqBattle(v, y)
	b2.attackAction(v, y, 100, atkMain)
	if y.hp != y.maxHP {
		t.Fatalf("化血被闪避仍触发：掉血 %d", y.maxHP-y.hp)
	}

	w := newUnit(fu(1, 100, 100000, 1, levelID(famRuoHua, 10)), 0)
	z := newUnit(fu(101, 100000, 10000000, 1), 1)
	setParam(w.tals[famRuoHua], 0, 100)
	b3 := faqBattle(w, z)
	b3.attackAction(w, z, 100, atkMain)
	if z.att < 100000-100 {
		t.Fatalf("弱化降攻超过攻击方攻击：%d", 100000-z.att)
	}
}

// TestAudit3：第 3 批——碧血被闪避也触发；诈死激发：生效后受到的伤害转为物理结界。
func TestAudit3(t *testing.T) {
	u := newUnit(fu(1, 1000, 100000, 1), 0)
	x := newUnit(fu(101, 10, 10000, 1, levelID(famShanBi, 10), levelID(famBiXue, 10)), 1)
	y := newUnit(fuh(102, 10, 100, 10000, 1), 1)
	setParam(x.tals[famShanBi], 0, 100)
	setParam(x.tals[famBiXue], 0, 100)
	b := faqBattle(u, x, y)
	b.attackAction(u, x, 100, atkMain)
	if y.hp == 100 {
		t.Fatal("碧血：被闪避也应触发")
	}

	z := newUnit(tjf(fu(101, 10, 1000, 1, levelID(famZhaSi, 10)), 5), 1)
	b2 := faqBattle(u, z)
	z.setStatusForTest(stZhasi)
	b2.hurt(u, z, 1, 500, true)
	if z.shield == 0 || z.hp != 1000 {
		t.Fatalf("诈死激发：shield %d hp %d", z.shield, z.hp)
	}
}

func (u *unit) setStatusForTest(k stKey) {
	if u.status == nil {
		u.status = map[stKey]*status{}
	}
	u.status[k] = &status{left: 1}
}

// TestGuaXiangShield：卦象给全体友方的物理结界是 $q = b% × 释放者生命上限（客户端 talent_mgr 的 $q）。
func TestGuaXiangShield(t *testing.T) {
	u := newUnit(fu(1, 100, 20000, 1, levelID(famGuaXiang, 4)), 0)
	a := newUnit(fu(2, 100, 5000, 1), 0)
	b := faqBattle(u, a, newUnit(fu(101, 10, 1000, 1), 1))
	k := u.tals[famGuaXiang]
	u.charge = chargeNeed(k)
	b.releaseCharge(u, k)
	if want := int64(20000 * k.param(1) / 100); a.shield != min(want, 2*a.maxHP) && a.shield != want {
		t.Fatalf("卦象结界 %d want %d", a.shield, want)
	}
}

// TestAudit5：第 5 批（死亡）——芭蕉扇扣结界按 $p = a% × 自身攻击；易命激发是「失去生命」（不被结界抵消）。
func TestAudit5(t *testing.T) {
	u := newUnit(fu(1, 100, 100, 1, levelID(famBaJiaoShan, 4)), 0)
	x := newUnit(fu(101, 10, 100000, 1), 1)
	b := faqBattle(u, x)
	x.shield, x.mshield = 50000, 50000
	b.deathMore(u)
	if x.shield != 50000-120 || x.mshield != 50000-120 {
		t.Fatalf("芭蕉扇扣结界 %d / %d，应各扣 120（攻击 100 × 120%%）", x.shield, x.mshield)
	}

	y := newUnit(tjf(fu(1, 100, 1000, 1, levelID(famYiMing, 4)), 5), 0)
	o := newUnit(fu(101, 10, 10000, 1), 1)
	b2 := faqBattle(y, o)
	o.shield = 100000
	y.hp = 0
	b2.onYiMing(y, y.tals[famYiMing])
	if o.hp != 10000-300 || o.shield != 100000 {
		t.Fatalf("易命激发：hp %d shield %d", o.hp, o.shield)
	}

	// 遗志4：+30%，受遗志的护法自己是魔族再 +10%
	j := newUnit(fu(1, 10, 10, 1, levelID(famJingWeiTianHai, 4)), 0)
	g := newUnit(fu(2, 100, 1000, 1), 0)
	g.Race = raceGhost
	h := newUnit(fu(3, 100, 1000, 1), 0)
	b3 := faqBattle(j, g, h, newUnit(fu(101, 10, 1000, 1), 1))
	j.hp = 0
	b3.deathMore(j)
	if g.att != 140 || h.att != 130 {
		t.Fatalf("遗志：魔族 %d 其他 %d", g.att, h.att)
	}
}

// TestZhenYanOpening：阵眼开场就在 3 号位也提升攻击、上限（不只入阵）。
func TestZhenYanOpening(t *testing.T) {
	u := newUnit(fu(3, 100, 1000, 1, levelID(famZhenYan, 10)), 0)
	b := faqBattle(u, newUnit(fu(101, 10, 1000, 1), 1))
	b.battleStart()
	if p := int64(u.tals[famZhenYan].param(0)); u.att != 100+p || u.maxHP != 1000+10*p {
		t.Fatalf("阵眼开场 att %d maxHP %d", u.att, u.maxHP)
	}
}

// TestAudit7：第 7 批（回合开始 / 结束）。
func TestAudit7(t *testing.T) {
	// 命数由我：单次伤害不超过自身上限 300%
	u := newUnit(fu(1, 100, 100, 1, levelID(famMingShuYouWo, 4)), 0)
	d := newUnit(fu(2, 10, 100, 1), 0)
	d.hp = 0
	o := newUnit(fu(101, 100000, 10000000, 1), 1)
	b := faqBattle(u, d, o)
	b.onMingShuYouWo(u, u.tals[famMingShuYouWo])
	if lost := int64(10000000) - o.hp; lost > 300 || lost == 0 {
		t.Fatalf("命数由我伤害 %d，应封顶 300", lost)
	}

	// 进化：对位比自己弱、但相邻比自己强时也会触发
	j := newUnit(fu(1, 100, 1000, 1, levelID(famJinHua, 10)), 0)
	setParam(j.tals[famJinHua], 0, 100)
	b2 := faqBattle(j, newUnit(fu(101, 10, 10, 1), 1), newUnit(fu(102, 1000, 10000, 1), 1))
	b2.onJinHua(j, j.tals[famJinHua])
	if j.att == 100 || j.maxHP == 1000 {
		t.Fatalf("进化：att %d maxHP %d", j.att, j.maxHP)
	}

	// 水之形：只吸攻击最高的那一个
	s := newUnit(fu(2, 100, 1000, 1, levelID(famShuiZhiXing, 4)), 0)
	x1 := newUnit(fu(101, 200, 1000, 1), 1)
	x2 := newUnit(fu(102, 300, 1000, 1), 1)
	b3 := faqBattle(s, x1, x2)
	b3.onShuiZhiXing(s, s.tals[famShuiZhiXing])
	if x1.att != 200 || x2.att == 300 {
		t.Fatalf("水之形：x1 %d x2 %d", x1.att, x2.att)
	}

	// 钉头七箭：不触发坚韧
	c := newUnit(fu(1, 1000, 1000, 1, levelID(famDingTouQiJian, 4)), 0)
	g := newUnit(fu(101, 100, 1000000, 1, levelID(famJianRen, 10)), 1)
	setParam(g.tals[famJianRen], 0, 100)
	b4 := faqBattle(c, g)
	b4.onDingTouQiJian(c, c.tals[famDingTouQiJian])
	if g.att != 100 {
		t.Fatalf("钉头七箭触发了坚韧：att %d", g.att)
	}
}

// TestShiHouShielded：狮吼——吸完结界后对方仍有法术结界，混乱被免疫，但照样吸取生命上限（FAQ）。
func TestShiHouShielded(t *testing.T) {
	w := newUnit(fu(101, 10, 1000, 1, levelID(famShiHou, 4)), 1)
	setParam(w.tals[famShiHou], 0, 100)
	u := newUnit(fu(1, 100, 10000, 1), 0)
	x := newUnit(fu(2, 100, 10000, 1), 0)
	x.mshield = 100000
	b := faqBattle(u, x, w)
	b.onHit(u, w, 1, atkMain)
	if x.maxHP == 10000 || x.has(stConfuse) != nil {
		t.Fatalf("狮吼：maxHP %d confuse %v", x.maxHP, x.has(stConfuse) != nil)
	}
}

// TestShiHouAfterConfuse：狮吼——混乱挂上时不吸，混乱结束（一次行动后）才吸取生命上限（用户确认）。
func TestShiHouAfterConfuse(t *testing.T) {
	w := newUnit(fu(101, 10, 1000, 1, levelID(famShiHou, 4)), 1)
	setParam(w.tals[famShiHou], 0, 100)
	u := newUnit(fu(1, 100, 10000, 1), 0)
	x := newUnit(fu(2, 100, 10000, 1), 0)
	b := faqBattle(u, x, w)
	b.onHit(u, w, 1, atkMain)
	if x.has(stConfuse) == nil || x.maxHP != 10000 {
		t.Fatalf("混乱中不应吸：confuse %v maxHP %d", x.has(stConfuse) != nil, x.maxHP)
	}
	b.tickStatus(x, true)
	if x.has(stConfuse) != nil || x.maxHP == 10000 || w.maxHP == 1000 {
		t.Fatalf("混乱结束后应吸：x %d w %d", x.maxHP, w.maxHP)
	}
}

// TestKaoLaoSelfAdjacent：犒劳「友方三体」= 自己及左右。
func TestKaoLaoSelfAdjacent(t *testing.T) {
	u := newUnit(fu(2, 10, 1000, 1, levelID(famKaoLao, 1)), 0)
	l := newUnit(fu(1, 10, 1000, 1), 0)
	r := newUnit(fu(3, 10, 1000, 1), 0)
	far := newUnit(fu(5, 10, 1000, 1), 0)
	b := faqBattle(u, l, r, far, newUnit(fu(101, 10, 1000, 1), 1))
	b.enter(u)
	if u.maxHP == 1000 || l.maxHP == 1000 || r.maxHP == 1000 || far.maxHP != 1000 {
		t.Fatalf("犒劳：%d %d %d %d", u.maxHP, l.maxHP, r.maxHP, far.maxHP)
	}
}

// TestBuQinBlocksQuzhu：不侵不会被驱逐，也不会被霆威一击必杀。
func TestBuQinBlocksQuzhu(t *testing.T) {
	u := newUnit(fu(1, 100, 100000, 1, levelID(famQuZhu, 10), levelID(famTingWei, 10)), 0)
	setParam(u.tals[famQuZhu], 0, 100)
	setParam(u.tals[famTingWei], 0, 100)
	setParam(u.tals[famTingWei], 1, 100)
	x := newUnit(fuh(101, 10, 5000, 1000000, 1, levelID(famBuQin, 1)), 1)
	bn := newUnit(fu(106, 10, 1000, 1), 1)
	b := faqBattle(u, x, bn)
	b.attackAction(u, x, 100, atkMain)
	if !x.alive() || !x.front() {
		t.Fatalf("不侵：alive %v front %v", x.alive(), x.front())
	}
}

// TestGrantRoundKeepsHigher：以命演天激发给的暴击3 / 分水3 不覆盖队友自身更高等级的同族神通；没有的照常获得。
func TestGrantRoundKeepsHigher(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, levelID(famBaoJi, 10)), 0)
	x := newUnit(fu(2, 100, 1000, 1), 0)
	b := faqBattle(u, x, newUnit(fu(101, 10, 1000, 1), 1))
	for _, y := range []*unit{u, x} {
		b.grantRound(y, levelID(famBaoJi, 3))
		b.grantRound(y, levelID(famFenShui, 3))
	}
	if u.tals[famBaoJi].id != levelID(famBaoJi, 10) || x.tals[famBaoJi] == nil || x.tals[famFenShui] == nil {
		t.Fatalf("u 暴击 %d, x 暴击 %v 分水 %v", u.tals[famBaoJi].id, x.tals[famBaoJi], x.tals[famFenShui])
	}
	b.roundOver()
	if u.tals[famBaoJi].id != levelID(famBaoJi, 10) || x.tals[famBaoJi] != nil || x.tals[famFenShui] != nil {
		t.Fatal("回合结束后应还原")
	}
}
