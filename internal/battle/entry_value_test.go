package battle

import "testing"

func TestMaxHPGrowthCap(t *testing.T) {
	u := newUnit(fu(1, 2500, 15000, 1), 0)
	b := faqBattle(u)
	b.changeMaxHP(u, 1, 100000)
	b.growAndHeal(u, 1, 10000)
	if u.maxHP != 120000 || u.hp != 20000 {
		t.Fatalf("capped growth: hp=%d maxHP=%d", u.hp, u.maxHP)
	}
	written := len(b.w.b)
	b.growAndHeal(u, 1, 10000)
	if len(b.w.b) != written || u.hp != 20000 {
		t.Fatal("growth at cap emitted a change or heal")
	}
	if got := b.changeMaxHP(u, 1, -1000); got != -1000 {
		t.Fatalf("max HP reduction = %d, want -1000", got)
	}
	b.growAndHeal(u, 1, 10000)
	if u.maxHP != 120000 || u.hp != 21000 {
		t.Fatalf("growth after reduction: hp=%d maxHP=%d", u.hp, u.maxHP)
	}
}

// 用户规则 item3：初始值以进场值为准 = 基础血 + 装备血 + 缘分加成 + 套装气血（破妄/不侵等）
// + 阵位/试炼非临时加成。xymsrv.petStats 把这些合成为面板值，newUnit 把面板值固化为
// baseAtt/MaxHP；战斗内临时改动不影响「初始值」，三种归位都恢复到进场值。
func TestEntryValueIsInitialValue(t *testing.T) {
	// 2500 攻 / 15000 血 = 基础 + 装备 + 缘分 + 套装气血 + 阵位/试炼 合成后的进场面板。
	u := newUnit(fu(1, 2500, 15000, 1, levelID(famTuXi, 10)), 0)
	b := faqBattle(u)

	if u.baseAtt != 2500 || u.MaxHP != 15000 || u.maxHP != 15000 || u.att != 2500 {
		t.Fatalf("entry snapshot wrong: baseAtt=%d att=%d MaxHP=%d maxHP=%d", u.baseAtt, u.att, u.MaxHP, u.maxHP)
	}

	for _, tc := range []struct {
		name  string
		reset func()
	}{
		{"restoreInitial", func() { b.restoreInitial(u, 1) }},
		{"resetInitial", func() { b.resetInitial(u, 1) }},
		{"benchReset", func() { b.benchReset(u) }},
	} {
		// 突袭式临时攻击 + 破妄式套装气血加成。
		b.tempUp(u, u.tals[famTuXi], 800)
		b.changeMaxHP(u, 1, 6000)
		if u.att != 3300 || u.maxHP != 21000 {
			t.Fatalf("%s boost wrong: att=%d maxHP=%d", tc.name, u.att, u.maxHP)
		}
		tc.reset()
		if u.att != 2500 || u.tempAtt != 0 || u.maxHP != 15000 {
			t.Fatalf("%s must restore entry value: att=%d temp=%d maxHP=%d", tc.name, u.att, u.tempAtt, u.maxHP)
		}
	}
}
