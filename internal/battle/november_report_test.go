package battle

import "testing"

func TestReportedDunrangRandomRegardlessOfEntry(t *testing.T) {
	lei := newUnit(fu(6, 10, 1000, 1, levelID(famLeiYingBiHu, 4)), 0)
	entry := newUnit(fu(7, 10, 1000, 1, levelID(famPanTaoYan, 4)), 0)
	b := faqBattle(lei, entry, newUnit(fu(101, 10, 1000, 1), 1))
	seen := map[*unit]bool{}
	for i := 0; i < 100; i++ {
		seen[b.dunrangTo(0)] = true
	}
	if len(seen) != 1 || !seen[entry] {
		t.Fatalf("雷震子 should stay back while another bench unit exists: %v", seen)
	}
	entry.hp = 0
	if b.dunrangTo(0) != lei {
		t.Fatal("雷震子 must be picked when he is the only bench unit")
	}
}
func TestReportedSwordUsesCurrentAttack(t *testing.T) {
	for _, att := range []int64{200, 2000} {
		u := newUnit(fu(1, 1000, 10000, 1), 0)
		foe := newUnit(fu(101, 1, 100000, 1), 1)
		b := faqBattle(u, foe)
		u.att = att
		u.swords = 2
		u.swordKind = 0
		b.fireSwords(u, foe)
		if foe.hp != 100000-att {
			t.Fatalf("attack %d hp %d", att, foe.hp)
		}
	}
}
func TestReportedYuanxingRequiresRoundActivation(t *testing.T) {
	u := newUnit(fu(6, 100, 10000, 1, levelID(famYuanXing, 10)), 0)
	ally := newUnit(fu(2, 10, 10000, 1), 0)
	replacement := newUnit(fu(1, 10, 10000, 1), 0)
	b := faqBattle(u, ally, replacement, newUnit(fu(101, 10, 10000, 1), 1))
	b.swap(u, replacement)
	if g, _, _ := b.guardFor(ally); g != nil {
		t.Fatal("protected before transformation")
	}
	b.onYuanXing(u, u.tals[famYuanXing])
	if g, _, _ := b.guardFor(ally); g != u {
		t.Fatal("no protection after transformation")
	}
	b.swap(u, replacement)
	b.swap(u, replacement)
	if g, _, _ := b.guardFor(ally); g != nil {
		t.Fatal("bench entry retained transformation")
	}
}

func TestYuanxingSkipsAlreadyTransformedNeighbors(t *testing.T) {
	const skill = 20180
	left := newUnit(fu(1, 100, 10000, 1, skill), 0)
	center := newUnit(fu(2, 100, 10000, 1, skill), 0)
	right := newUnit(fu(3, 100, 10000, 1, skill), 0)
	b := faqBattle(left, center, right)
	b.onYuanXing(left, left.tal(famYuanXing))
	leftHP := left.hp
	b.onYuanXing(center, center.tal(famYuanXing))
	if left.hp != leftHP || right.hp != 7000 || center.att != 550 || center.maxHP != 14500 {
		t.Fatalf("left HP=%d, right HP=%d, center attack=%d, center max HP=%d", left.hp, right.hp, center.att, center.maxHP)
	}
	b.onYuanXing(right, right.tal(famYuanXing))
	if !right.yuanxingActive || right.att != 100 || right.maxHP != 10000 {
		t.Fatalf("right active=%t, attack=%d, max HP=%d", right.yuanxingActive, right.att, right.maxHP)
	}
}

func TestReportedYiMingRevivesDisplacedCorpse(t *testing.T) {
	u := newUnit(fu(1, 100, 10000, 1, levelID(famYiMing, 4)), 0)
	dead := newUnit(fu(6, 100, 10000, 1), 0)
	dead.hp = 0
	dead.gone = true
	b := faqBattle(u, dead, newUnit(fu(101, 10, 10000, 1), 1))
	u.hp = 0
	b.died(u)
	if !dead.alive() || dead.has(stNoSoul) == nil {
		t.Fatal("displaced corpse did not revive")
	}
}
func TestReportedHiddenGhostAxeKillAddsRevive(t *testing.T) {
	u := newUnit(fu(1, 10000, 10000, 1), 0)
	dead := newUnit(fu(6, 100, 1000, 1), 0)
	dead.hp = 0
	dead.eaten = true
	foe := newUnit(fu(101, 1, 10, 1), 1)
	b := faqBattle(u, dead, foe, newUnit(fu(102, 1, 100000, 1), 1))
	b.setStatus(dead, stNoSoul, 0, maxRounds, false, 5141, slotDebuff)
	b.applyBuff(u, u, 5111)
	b.applyBuff(u, u, 5635)
	if u.has(stGhostAxe).val != 50 {
		t.Fatal("ghost axe chance")
	}
	b.attackAction(u, foe, 100, atkMain)
	if dead.reviveMark != 5151 || dead.reviveIn != reviveDelay || dead.eaten || dead.has(stNoSoul) != nil {
		t.Fatal("kill did not add soul revival")
	}
	b.round = dead.reviveAt
	b.pendingRevive(dead)
	if !dead.alive() {
		t.Fatal("marked corpse failed to revive")
	}
}
func TestReportedIronFanDeathBurst(t *testing.T) {
	u := newUnit(fu(1, 100, 10, 1, levelID(famBaJiaoShan, 4)), 0)
	foe := newUnit(fu(101, 100, 10000, 1), 1)
	b := faqBattle(u, newUnit(fu(2, 1, 10000, 1), 0), foe)
	foe.shield = 1000
	foe.mshield = 1000
	b.addPoison(foe, poison{from: 21145, pct: 10, left: maxRounds, buff: 5025, burn: true})
	b.loseHP(foe, u, 0, 10)
	if foe.burnPct() != 10 {
		t.Fatal("death skill consumed existing burn")
	}
	if foe.shield != 880 || foe.mshield != 680 || foe.hp != 10000 {
		t.Fatalf("shield burst %d %d hp %d", foe.shield, foe.mshield, foe.hp)
	}
}

func TestReportedIronFanTianjiangRates(t *testing.T) {
	for _, lv := range []int{1, 5} {
		k := newTalent(21524)
		k.applyTJ(lv)
		if k.tjPct() != 8*lv {
			t.Fatalf("level %d chance %d", lv, k.tjPct())
		}
	}
}

func TestReportedIronFanEveryBurnLayerCappedSeparately(t *testing.T) {
	u := newUnit(fu(1, 100, 10, 1, 21524), 0)
	foe := newUnit(fu(101, 1, 10000, 1), 1)
	b := faqBattle(u, newUnit(fu(2, 1, 10000, 1), 0), foe)
	for _, pct := range []int{10, 8, 1} {
		b.addPoison(foe, poison{from: 21145, pct: pct, left: maxRounds, buff: 5025, burn: true})
	}
	b.loseHP(foe, u, 0, 10)
	if foe.hp != 9400 || len(foe.poison) != 3 {
		t.Fatalf("hp=%d layers=%d", foe.hp, len(foe.poison))
	}
}
func TestReportedDeathSkillEventsBothSides(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, 22074), 0)
	other := newUnit(fu(101, 10, 1000, 1, 22074), 1)
	a := newUnit(fu(2, 1, 1000, 1, 21524), 0)
	e := newUnit(fu(102, 1, 1000, 1, 21334), 1)
	b := faqBattle(u, other, a, e)
	for _, x := range []*unit{a, e} {
		x.hp = 0
		b.deathSkillEvent(x)
		b.relive(x, 1, 100)
	}
	if u.xiuluo != 4 || other.xiuluo != 4 {
		t.Fatalf("counts %d %d", u.xiuluo, other.xiuluo)
	}
}
