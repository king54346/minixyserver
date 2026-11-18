package battle

import (
	"math/rand/v2"
	"testing"
)

// One successful roll must cover the primary slot and both neighbors, including
// the case where a guard actually took the preceding ordinary attack.
func TestFuZhongOneRollAndConsistentCenter(t *testing.T) {
	for _, level := range []int32{21761, 21764} {
		for _, redirect := range []bool{false, true} {
			triggered := 0
			for seed := uint64(0); seed < 120; seed++ {
				src := newUnit(fu(3, 100, 100000, 1, level), 0)
				a := newUnit(fu(102, 1, 100000, 1), 1)
				center := newUnit(fu(103, 1, 100000, 1), 1)
				c := newUnit(fu(104, 1, 100000, 1), 1)
				b := faqBattle(src, a, center, c)
				b.rng = rand.New(rand.NewPCG(seed, 1))
				src.atkTarget = center
				target := center
				if redirect {
					target = a
				}
				b.selfAfterAttack(src, target, 0)
				count := 0
				for _, x := range []*unit{a, center, c} {
					if x.has(stTaunt) != nil {
						count++
					}
				}
				if count != 0 && count != 3 {
					t.Fatalf("level %d seed %d redirect %v: taunted %d", level, seed, redirect, count)
				}
				cmds := mustDecode(t, b.w.b)
				buffs := map[int]int{}
				for _, cmd := range cmds {
					if cmd.ID == CmdActDo && cmd.Magic == int64(level) && len(cmd.Targets) != 3 {
						t.Fatal("animation omitted targets")
					}
					if cmd.ID == CmdBuff && cmd.Magic >= 5601 && cmd.Magic <= 5604 {
						for _, v := range cmd.Targets {
							buffs[int(v[0])]++
						}
					}
				}
				if count == 3 {
					triggered++
					for _, x := range []*unit{a, center, c} {
						if buffs[x.Pos] != 1 {
							t.Fatalf("taunt duplicated or omitted: %v", buffs)
						}
					}
				}
			}
			if triggered == 0 {
				t.Fatal("no activation sampled")
			}
		}
	}
}

func TestFuZhongTJStillAppliesToAllTauntedTargets(t *testing.T) {
	for seed := uint64(0); seed < 100; seed++ {
		f := fu(3, 100, 100000, 1, 21764)
		f.TalentTJ = []int{5}
		src := newUnit(f, 0)
		a := newUnit(fu(102, 1, 100000, 1), 1)
		center := newUnit(fu(103, 1, 100000, 1), 1)
		c := newUnit(fu(104, 1, 100000, 1), 1)
		a.shield, c.shield = 1000, 1000
		b := faqBattle(src, a, center, c)
		b.rng = rand.New(rand.NewPCG(seed, 1))
		b.selfAfterAttack(src, center, 0)
		if center.has(stTaunt) == nil {
			continue
		}
		for _, x := range []*unit{a, center, c} {
			if x.hurtUp != 30 {
				t.Fatal("max activation omitted 会猎3")
			}
		}
		if a.shield != 1000 || c.shield != 1000 || a.hp != 99950 || c.hp != 99950 {
			t.Fatal("activated splash did not bypass physical shield")
		}
		return
	}
	t.Fatal("no activation sampled")
}

func TestTianWeiUsesDaoDamage(t *testing.T) {
	src := newUnit(fu(1, 1000, 100000, 1, 22202), 0)
	physical := newUnit(fu(101, 1, 1000, 1, 20330, 20350), 1)
	fairy := newUnit(fu(102, 1, 10000, 1, 20340), 1)
	dao := newUnit(fu(103, 1, 10000, 1), 1)
	dao.DaoImmune = 100
	shield := newUnit(fu(104, 1, 10000, 1), 1)
	shield.shield, shield.mshield = 1000, 1000
	b := faqBattle(src, physical, fairy, dao, shield)
	b.selfAfterAttack(src, physical, 0)
	if physical.hp != 600 {
		t.Fatalf("physical defenses affected dao damage: %d", physical.hp)
	}
	if fairy.hp != 10000 || dao.hp != 10000 {
		t.Fatal("dao damage skipped fairy armor or dao immunity")
	}
	if shield.shield != 1000 || shield.mshield != 600 || shield.hp != 10000 {
		t.Fatal("dao damage did not use magic shield")
	}
}

func TestTianWeiTriggersJianRenAndActivatedQiJia(t *testing.T) {
	src := newUnit(fu(1, 1000, 100000, 1, 22202), 0)
	tough := newUnit(fu(101, 100, 1000, 1, 20440), 1)
	armorF := fu(102, 1, 1000, 1, 20350)
	armorF.TalentTJ = []int{5}
	armor := newUnit(armorF, 1)
	b := faqBattle(src, tough, armor)
	b.selfAfterAttack(src, tough, 0)
	if tough.att != 112 || tough.hp != 612 {
		t.Fatalf("dao damage failed to trigger JianRen: att=%d hp=%d", tough.att, tough.hp)
	}
	if armor.hp != 960 {
		t.Fatalf("activated QiJia failed to cap property damage at 4 percent: hp=%d", armor.hp)
	}
}
