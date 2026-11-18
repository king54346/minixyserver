package battle

import (
	"math/rand/v2"
	"testing"
)

// TestRaceHP：仙师 3 让本方仙族生命上限 +3% × 带它的护法的上限，满血开场；人族不受影响。
func TestRaceHP(t *testing.T) {
	holder := newUnit(raced(fu(1, 100, 10000, 1, levelID(famXianShi, 3)), raceHuman), 0)
	god := newUnit(raced(fu(2, 100, 1000, 1), raceGod), 0)
	foe := newUnit(raced(fu(101, 100, 1000, 1), raceGod), 1)
	b := faqBattle(holder, god, foe)
	b.raceHP()
	if god.maxHP != 1000+300 || god.hp != god.maxHP || holder.maxHP != 10000 || foe.maxHP != 1000 {
		t.Fatalf("god %d/%d holder %d foe %d", god.hp, god.maxHP, holder.maxHP, foe.maxHP)
	}
}

func TestRaceHPFourthTalentUsesConfiguredPercent(t *testing.T) {
	u := newUnit(raced(fu(1, 100, 10000, 1, 121210), raceGod), 0)
	b := faqBattle(u)
	b.raceHP()
	if u.maxHP != 12000 || u.hp != 12000 {
		t.Fatalf("fourth talent: %d/%d, want 12000", u.hp, u.maxHP)
	}
}

// Eight set bonuses plus Wangmu's fourth talent must contribute nine times,
// using initial HP even as each previous bonus raises current maximum HP.
func TestNineRaceAurasOpeningAndNishiReplay(t *testing.T) {
	for _, tc := range []struct {
		name        string
		race        int
		set, fourth int32
	}{
		{"仙师", raceGod, 21210, 121210},
		{"人杰", raceHuman, 21230, 121230},
		{"魔王", raceGhost, 21250, 121250},
		{"兽灵", raceBeast, 21270, 121270},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var team []*Fighter
			var units []*unit
			for pos := 1; pos <= 8; pos++ {
				tals := []int32{20790, tc.set}
				if pos == 1 {
					tals = []int32{20790, tc.fourth, tc.set}
				}
				f := raced(fu(pos, 1000, 10000, 1, tals...), tc.race)
				team = append(team, f)
				units = append(units, newUnit(f, 0))
			}
			b := faqBattle(units...)
			b.raceHP()
			for _, u := range units {
				if u.maxHP != 20000 || u.hp != 20000 {
					t.Fatalf("pos %d: %d/%d, want 20000", u.Pos, u.hp, u.maxHP)
				}
			}
			if b.skills[tc.name] != 9 {
				t.Fatalf("got %d aura sources, want 9", b.skills[tc.name])
			}
			opening, err := DecodeRecord(b.w.b)
			if err != nil {
				t.Fatal(err)
			}
			maxChanges, heals := 0, 0
			for _, c := range opening {
				switch c.ID {
				case CmdActDo:
					t.Fatal("passive aura requested a native cast animation")
				case CmdChgMaxHP:
					maxChanges++
				case CmdAddHP:
					heals++
				}
			}
			if maxChanges != 72 || heals != 72 {
				t.Fatalf("lost aura effects: maximum changes=%d heals=%d, want 72 each", maxChanges, heals)
			}
			for seed := uint64(0); seed < 32; seed++ {
				var foes []*Fighter
				for pos := 101; pos <= 108; pos++ {
					foes = append(foes, raced(fu(pos, 1500, 20000, 2, 20790), tc.race))
				}
				result := Simulate(CloneTeam(team), foes, rand.New(rand.NewPCG(seed, 7)))
				if _, err := DecodeRecord(result.Record); err != nil {
					t.Fatal(err)
				}
				if _, err := Replay(team, foes, result.Record); err != nil {
					t.Fatalf("seed %d: %v", seed, err)
				}
			}
		})
	}
}

func TestRaceAurasOnlyAffectLivingAlliedRace(t *testing.T) {
	for _, tc := range []struct {
		name        string
		race        int
		set, fourth int32
	}{
		{"仙师", raceGod, 21210, 121210},
		{"人杰", raceHuman, 21230, 121230},
		{"魔王", raceGhost, 21250, 121250},
		{"兽灵", raceBeast, 21270, 121270},
	} {
		t.Run(tc.name, func(t *testing.T) {
			holder := newUnit(raced(fu(1, 100, 10000, 1, tc.set, tc.fourth), tc.race), 0)
			back := newUnit(raced(fu(8, 100, 1000, 1), tc.race), 0)
			other := newUnit(raced(fu(2, 100, 1000, 1), tc.race%4+1), 0)
			foe := newUnit(raced(fu(101, 100, 1000, 1), tc.race), 1)
			dead := newUnit(raced(fu(3, 100, 1000, 1), tc.race), 0)
			dead.hp = 0
			b := faqBattle(holder, back, other, foe, dead)
			b.raceHP()
			if holder.maxHP != 13000 || holder.hp != 13000 || back.maxHP != 4000 || back.hp != 4000 {
				t.Fatalf("stacked aura: holder %d/%d back %d/%d", holder.hp, holder.maxHP, back.hp, back.maxHP)
			}
			if other.maxHP != 1000 || foe.maxHP != 1000 || dead.maxHP != 1000 || dead.hp != 0 {
				t.Fatal("aura affected another race, an enemy, or a dead ally")
			}
			b.changeMaxHP(holder, 5213, 600)
			powang := newTalent(int32(famPoWang))
			setParam(powang, 0, 100)
			b.onPoWang(foe, powang)
			if holder.maxHP != 13000 || holder.baseHP != 13000 || holder.Fighter.MaxHP != 10000 {
				t.Fatalf("破妄后上限=%d，战斗基础上限=%d，阵容上限=%d", holder.maxHP, holder.baseHP, holder.Fighter.MaxHP)
			}
		})
	}
}
