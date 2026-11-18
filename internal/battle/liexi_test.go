package battle

import (
	"math/rand/v2"
	"sort"
	"testing"

	"turnrpg/internal/gamedata"
)

// TestLiexiFactorsStress：随机神通 + 随机战地因素，战报回放自洽。
func TestLiexiFactorsStress(t *testing.T) {
	var ids []int32
	for id := range gamedata.Talents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	rng := rand.New(rand.NewPCG(9, 9))
	for iter := 0; iter < 300; iter++ {
		mk := func(side int) []*Fighter {
			var out []*Fighter
			for s := 1; s <= 8; s++ {
				hp := int32(2000 + rng.IntN(15000))
				f := &Fighter{Pos: s + side*enemyBase, Att: int32(500 + rng.IntN(5000)), HP: hp, MaxHP: hp, Speed: rng.IntN(20), Race: 1 + rng.IntN(4), Grade: 50}
				for range 3 {
					f.Talents = append(f.Talents, ids[rng.IntN(len(ids))])
					f.TalentTJ = append(f.TalentTJ, rng.IntN(6))
				}
				out = append(out, f)
			}
			return out
		}
		var fs []int
		for f := 1; f <= 20; f++ {
			if rng.IntN(3) == 0 {
				fs = append(fs, f)
			}
		}
		a, b := mk(0), mk(1)
		ra, rb := CloneTeam(a), CloneTeam(b)
		res := SimulateWith(a, b, Options{Factors: fs}, rand.New(rand.NewPCG(uint64(iter), 3)))
		if _, err := Replay(ra, rb, res.Record); err != nil {
			t.Fatalf("iter %d factors %v: %v", iter, fs, err)
		}
	}
}

// TestLiexiFactorsBasic：火 / 疗 / 盾 / 物 的效果。
func TestLiexiFactorsBasic(t *testing.T) {
	u := newUnit(fu(1, 100, 10000, 1), 0)
	x := newUnit(fu(101, 100, 10000, 1), 1)
	b := faqBattle(u, x)
	b.factors = map[int]bool{LxFire: true, LxShield: true, LxPhys: true}
	b.round = 1
	b.lxRoundStart()
	if u.has(stImmune) == nil || x.has(stImmune) == nil {
		t.Fatal("盾：第 1 回合每方 1 个无敌")
	}
	b.clearStatus(u, stImmune)
	b.lxRoundEnd()
	if u.burnPct() != 2 {
		t.Fatalf("火：燃烧1 = 2%%，got %d", u.burnPct())
	}
	if got := b.hurt(x, u, 1, 1000, true); got != 800 {
		t.Fatalf("物：物理伤害 −20%%，got %d", got)
	}
}
