package battle

import "testing"

func TestBiXueAllFrontTargetsAndCurrentHPBasis(t *testing.T) {
	owner := newUnit(fuh(1, 100, 100, 1000, 1, levelID(famBiXue, 10)), 0)
	a := newUnit(fuh(2, 10, 100, 10000, 1), 0)
	z := newUnit(fuh(5, 10, 100, 10000, 1), 0)
	back := newUnit(fuh(6, 10, 100, 10000, 1), 0)
	dead := newUnit(fu(3, 10, 1000, 1), 0)
	dead.hp = 0
	enemy := newUnit(fuh(101, 10, 100, 1000, 1), 1)
	setParam(owner.tals[famBiXue], 0, 100)
	b := faqBattle(owner, a, z, back, dead, enemy)
	for _, limit := range []int64{1000, 2000} {
		owner.maxHP = limit
		a.hp, z.hp = 100, 100
		b.biXue(owner)
		want := int64(100) + limit*12/100
		if a.hp != want || z.hp != want || owner.hp != 100 || back.hp != 100 || enemy.hp != 100 || dead.hp != 0 {
			t.Fatal("Bixue must heal every other living front ally using current owner HP limit", limit, a.hp, z.hp)
		}
	}
	cmds, err := DecodeRecord(b.w.b)
	if err != nil {
		t.Fatal(err)
	}
	for _, pos := range []int64{2, 5} {
		if !hasCmd(cmds, CmdAddHP, 20380, pos, 120) || !hasCmd(cmds, CmdAddHP, 20380, pos, 240) {
			t.Fatal("missing actual per-target heal record", pos)
		}
	}
}

func TestBiXueTianjiangDoubleHealsAllTargets(t *testing.T) {
	for _, rank := range []int{1, 5} {
		owner := newUnit(tjf(fuh(1, 100, 100, 1000, 1, levelID(famBiXue, 10)), rank), 0)
		a := newUnit(fuh(2, 10, 100, 10000, 1), 0)
		z := newUnit(fuh(5, 10, 100, 10000, 1), 0)
		k := owner.tals[famBiXue]
		if k.tjPct() != rank*10 {
			t.Fatal("wrong double-heal probability", rank, k.tjPct())
		}
		setParam(k, 0, 100)
		b := faqBattle(owner, a, z)
		doubles := 0
		for range 1000 {
			a.hp, z.hp = 100, 100
			b.biXue(owner)
			if a.hp != z.hp || (a.hp != 220 && a.hp != 340) || owner.hp != 100 {
				t.Fatal("invalid healing scope/amount")
			}
			if a.hp == 340 {
				doubles++
			}
		}
		if doubles < rank*100-60 || doubles > rank*100+60 {
			t.Fatal("wrong double-heal distribution", rank, doubles)
		}
	}
}

func TestBiXueAttackBehaviorIncludesDodgeAndShield(t *testing.T) {
	for _, dodge := range []bool{false, true} {
		owner := newUnit(fuh(1, 10, 1000, 1000, 1, levelID(famBiXue, 10), levelID(famShanBi, 10)), 0)
		a := newUnit(fuh(2, 10, 100, 10000, 1), 0)
		z := newUnit(fuh(3, 10, 100, 10000, 1), 0)
		enemy := newUnit(fu(101, 100, 10000, 1), 1)
		setParam(owner.tals[famBiXue], 0, 100)
		setParam(owner.tals[famShanBi], 0, 0)
		owner.shield = 100000
		if dodge {
			setParam(owner.tals[famShanBi], 0, 100)
		}
		b := faqBattle(owner, a, z, enemy)
		b.attackAction(enemy, owner, 100, atkMain)
		if a.hp != 220 || z.hp != 220 || owner.hp != 1000 {
			t.Fatal("zero-damage attack did not heal both teammates", dodge, a.hp, z.hp, owner.hp)
		}
	}
}
