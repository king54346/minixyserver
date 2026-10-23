package battle

import (
	"math/rand/v2"
	"sort"
	"testing"

	"turnrpg/internal/gamedata"
)

// TestZhenfaStress：随机神通 + 双方随机阵法，战报回放自洽。
func TestZhenfaStress(t *testing.T) {
	var ids []int32
	for id := range gamedata.Talents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	rng := rand.New(rand.NewPCG(5, 5))
	for iter := 0; iter < 1000; iter++ {
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
		zf := func() Formation {
			f := Formation{Type: rng.IntN(9), Stair: 1 + rng.IntN(5)}
			for i := range f.Lv {
				f.Lv[i] = 1 + rng.IntN(10)
			}
			return f
		}
		a, b := mk(0), mk(1)
		ra, rb := CloneTeam(a), CloneTeam(b)
		res := SimulateWith(a, b, Options{Formations: [2]Formation{zf(), zf()}}, rand.New(rand.NewPCG(uint64(iter), 7)))
		if _, err := Replay(ra, rb, res.Record); err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
	}
}
