package battle

import (
	"math/rand/v2"
	"testing"
)

func TestGanluTianjiangProbabilityAndTargets(t *testing.T) {
	for lv := 0; lv <= 5; lv++ {
		for id := int32(21351); id <= 21354; id++ {
			k := newTalent(id)
			k.applyTJ(lv)
			if k.tjPct() != 10*lv {
				t.Fatalf("talent=%d level=%d probability=%d", id, lv, k.tjPct())
			}
		}
		const samples = 2000
		extra := 0
		for seed := uint64(0); seed < samples; seed++ {
			u := newUnit(tjf(fu(1, 0, 1000, 1, 21354), lv), 0)
			a := newUnit(fu(2, 0, 1000, 1), 0)
			z := newUnit(fu(6, 0, 1000, 1), 0)
			locked := newUnit(fu(3, 0, 1000, 1), 0)
			eaten := newUnit(fu(4, 0, 1000, 1), 0)
			foe := newUnit(fu(101, 0, 1000, 1), 1)
			b := faqBattle(u, a, z, locked, eaten, foe)
			b.rng = rand.New(rand.NewPCG(seed, seed+1))
			b.setStatus(locked, stNoSoul, 0, maxRounds, false, 5141, slotDebuff)
			a.hp, z.hp, locked.hp, eaten.hp, foe.hp = 0, 0, 0, 0, 0
			z.gone, eaten.eaten = true, true
			b.enter(u)
			revived := 0
			for _, x := range []*unit{a, z} {
				if x.alive() {
					revived++
					if x.hp != 800 || x.has(stNoSoul) == nil {
						t.Fatal("revival must retain 80% HP and soul lock")
					}
				}
			}
			if revived < 1 || locked.alive() || eaten.alive() || foe.alive() {
				t.Fatalf("level=%d seed=%d revived=%d locked=%d eaten=%d foe=%d lock=%v", lv, seed, revived, locked.hp, eaten.hp, foe.hp, locked.has(stNoSoul))
			}
			if revived == 2 {
				extra++
			}
		}
		want := samples * 10 * lv / 100
		if lv == 0 && extra != 0 || abs(extra-want) > samples*5/100 {
			t.Fatalf("level %d: extra revivals=%d/%d want about %d", lv, extra, samples, want)
		}
		t.Logf("level %d: extra revival %d/%d (expected %d%%)", lv, extra, samples, 10*lv)
	}
}

func TestGanluExtraRevivalNeedsAnotherCorpse(t *testing.T) {
	for _, count := range []int{0, 1} {
		for seed := uint64(0); seed < 100; seed++ {
			u := newUnit(tjf(fu(1, 0, 1000, 1, 21354), 5), 0)
			b := faqBattle(u)
			b.rng = rand.New(rand.NewPCG(seed, seed+1))
			if count == 1 {
				x := newUnit(fu(6, 0, 1000, 1), 0)
				x.hp = 0
				b.units[0] = append(b.units[0], x)
			}
			b.enter(u)
			revived := 0
			for _, x := range b.units[0] {
				if x.revived {
					revived++
				}
			}
			if revived != count {
				t.Fatalf("corpses=%d revived=%d", count, revived)
			}
		}
	}
}
