package battle

import "testing"

func TestWuDaoTJRaisesMaxAndCurrentHP(t *testing.T) {
	u := newUnit(tjf(fu(1, 100, 1000, 1, 20230), 10), 0)
	ally := newUnit(fuh(2, 100, 400, 2000, 1), 0)
	bench := newUnit(fuh(6, 100, 300, 1000, 1), 0)
	foe := newUnit(fu(101, 0, 10000, 1), 1)
	b := faqBattle(u, ally, bench, foe)
	k := u.tal(famWuDao)
	b.selfAfterAttack(u, foe, 100)
	if u.maxHP != 1030 || u.hp != 1030 || ally.maxHP != 2060 || ally.hp != 460 {
		t.Fatalf("mislead did not raise and heal equally: caster %d/%d ally %d/%d", u.hp, u.maxHP, ally.hp, ally.maxHP)
	}
	if bench.hp != 300 || bench.maxHP != 1000 {
		t.Fatal("mislead affected the bench")
	}
	cmds, err := DecodeRecord(b.w.b)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCmd(cmds, CmdAddHP, int64(k.id), 1, 30) || !hasCmd(cmds, CmdAddHP, int64(k.id), 2, 60) {
		t.Fatal("missing healing commands")
	}
}

func TestTianZhanWorksFromBench(t *testing.T) {
	for _, pos := range []int{1, 6, 7, 8} {
		t.Run(string(rune('0'+pos)), func(t *testing.T) {
			u := newUnit(fu(pos, 1000, 10000, 1, 22182), 0)
			foe := newUnit(fu(101, 0, 10000, 1), 1)
			adj := newUnit(fu(102, 0, 10000, 1), 1)
			b := faqBattle(u, foe, adj)
			b.pool[0], b.poolBy[0], b.poolID[0] = 200, u, 21750
			b.bloodPool(0)
			if foe.hp != 9300 || adj.hp != 9700 {
				t.Fatalf("pos %d: target %d neighbor %d", pos, foe.hp, adj.hp)
			}
		})
	}
	for _, condition := range []string{"dead", "gone", "stiff"} {
		u := newUnit(fu(6, 1000, 10000, 1, 22182), 0)
		front := newUnit(fu(1, 10, 10000, 1), 0)
		foe := newUnit(fu(101, 0, 10000, 1), 1)
		b := faqBattle(u, front, foe)
		switch condition {
		case "dead":
			u.hp = 0
		case "gone":
			u.gone = true
		case "stiff":
			b.setStatus(u, stStiff, 0, 2, false, 5001, slotDebuff)
		}
		b.pool[0], b.poolBy[0], b.poolID[0] = 200, front, 21750
		b.bloodPool(0)
		if foe.hp != 9900 {
			t.Fatalf("%s bench caster still chased: %d", condition, foe.hp)
		}
	}
}

func TestHuiSaoUsesPreMitigationPrimaryDamage(t *testing.T) {
	for _, tc := range []struct {
		name         string
		hp, shield   int64
		crit, immune bool
		want         int64
	}{
		{"normal", 10000, 0, false, false, 1000},
		{"shield", 10000, 600, false, false, 400},
		{"overkill", 201, 0, false, false, 201},
		{"crit_overkill", 301, 0, true, false, 301},
		{"fully_absorbed", 10000, 1000, false, false, 0},
		{"invincible", 10000, 0, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := newUnit(tjf(fu(1, 1000, 10000, 1, 20260), 10), 0)
			if tc.crit {
				u.tals[famBaoJi] = newTalent(levelID(famBaoJi, 10))
				setParam(u.tals[famBaoJi], 0, 100)
			}
			foe := newUnit(fu(102, 0, 10000, 1), 1)
			foe.hp = tc.hp
			foe.shield = tc.shield
			left := newUnit(fu(101, 0, 10000, 1), 1)
			right := newUnit(fu(103, 0, 10000, 1), 1)
			b := faqBattle(u, left, foe, right)
			if tc.immune {
				b.setStatus(foe, stImmune, 0, 2, false, 0, 0)
			}
			done := b.attackAction(u, foe, 100, atkMain)
			basis := int64(1000)
			if tc.crit {
				basis = 2000
			}
			if done != tc.want || left.hp != 10000-basis/2 || right.hp != 10000-basis/2 {
				t.Fatalf("primary %d want %d; neighbors lost %d/%d", done, tc.want, 10000-left.hp, 10000-right.hp)
			}
		})
	}
}
