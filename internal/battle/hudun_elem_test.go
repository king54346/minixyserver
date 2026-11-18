package battle

import (
	"testing"
)

func TestHudunVsElemental(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, levelID(famTaLang, 10)), 0)
	setParam(u.tals[famTaLang], 0, 100)

	v := newUnit(tjf(fu(101, 100, 10000, 1, levelID(famHuDun, 10)), 5), 1)
	setParam(v.tals[famHuDun], 0, 100)

	b := faqBattle(u, v)
	att0 := u.att
	b.attackAction(u, v, 100, atkMain)

	t.Logf("u.att after attack: %d (att0 was %d)", u.att, att0)
	if u.att < att0 {
		t.Fatalf("HuDun reduced attacker's attack on TaLang! u.att=%d, att0=%d", u.att, att0)
	}

	// Now test fire enchant (stFireEnchant)
	u2 := newUnit(fu(2, 100, 1000, 1), 0)
	b.setStatus(u2, stFireEnchant, 100, 1, true, 5651, slotBuff)
	v2 := newUnit(tjf(fu(102, 100, 10000, 1, levelID(famHuDun, 10)), 5), 1)
	setParam(v2.tals[famHuDun], 0, 100)
	b2 := faqBattle(u2, v2)
	att0_2 := u2.att
	b2.attackAction(u2, v2, 100, atkMain)
	t.Logf("u2.att after attack: %d (att0 was %d)", u2.att, att0_2)
	if u2.att < att0_2 {
		t.Fatalf("HuDun reduced attacker's attack on Fire Enchant! u2.att=%d, att0=%d", u2.att, att0_2)
	}
}
