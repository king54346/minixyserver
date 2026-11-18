package battle

import "testing"

func TestTuxiUsesInitialAttackInFollowUp(t *testing.T) {
	for _, lv := range []int{0, 5} {
		u := newUnit(tjf(fu(1, 1000, 10000, 1, levelID(famTuXi, 10)), lv), 0)
		foe := newUnit(fu(101, 0, 100000, 1), 1)
		b := faqBattle(u, foe)
		// 1000 initial, 400 permanent growth, 600 temporary growth from an outer attack.
		u.att, u.tempAtt = 2000, 600
		want := int64(2500) // 2000 + 初始 1000 的 50%
		if lv == 5 {
			want = 2700
		} // 50% 初始 + 天将 20% 初始
		for i := 0; i < 2; i++ {
			if got := b.attackAction(u, foe, 100, atkFollow); got != want {
				t.Fatalf("lv %d follow-up damage=%d want=%d", lv, got, want)
			}
			if u.att != 2000 || u.tempAtt != 600 {
				t.Fatalf("follow-up failed to clear only its own increase: att=%d temp=%d", u.att, u.tempAtt)
			}
		}
		b.endAction(u, 0, nil, u.tempEpoch, atkMain)
		if u.att != 1400 || u.tempAtt != 0 {
			t.Fatal("outer temporary increase did not clear or permanent growth was lost")
		}
	}
}

func TestDoubleTuxiUsesInitialAttackForEachTrigger(t *testing.T) {
	u := newUnit(fu(1, 1000, 10000, 1, levelID(famTuXi, 10), levelID(famTuXi, 5)), 0)
	for _, k := range u.talAll(famTuXi) {
		setParam(k, 0, 100)
	}
	u.att, u.tempAtt, u.tempBase = 2000, 600, 600
	b := faqBattle(u)
	b.tuxi(u)
	// 两次都按初始攻击 1000 的 50%：2000 + 500 + 500 = 3000，不叠乘。
	if u.att != 3000 || u.tempAtt != 1600 {
		t.Fatalf("double trigger att=%d temp=%d", u.att, u.tempAtt)
	}
}

func TestTuxiIgnoresCurrentAttackReduction(t *testing.T) {
	u := newUnit(fu(1, 1000, 10000, 1, levelID(famTuXi, 10)), 0)
	u.att = 400
	b := faqBattle(u)
	b.tuxi(u)
	if u.att != 900 || u.tempAtt != 500 { // 加值按初始 1000 的 50%，当前被压低不影响
		t.Fatal("tuxi boost must follow initial attack despite current reduction")
	}
}
