package xymsrv

import (
	"math/rand/v2"
	"testing"

	"turnrpg/internal/gamedata"
)

// Exercise the real card selection, including the independent daily rare roll.
// Reset pity before each single draw to measure the natural rate separately.
func TestRecruitNaturalGoldRates(t *testing.T) {
	for _, tc := range []struct {
		typ  int
		want float64
	}{{1, .03}, {2, .01}, {5, .02 + .98*.02}} {
		for race := 1; race <= 4; race++ {
			mall := &Mall{Race: race, Pity: map[int]int{}}
			rng := rand.New(rand.NewPCG(uint64(tc.typ), uint64(race)))
			gold := 0
			const draws = 30000
			for range draws {
				mall.Pity[tc.typ] = 0
				card := drawPets(recruitPools[tc.typ], mall, 1, rng)[0]
				if gamedata.Pets[card].Star == 5 {
					gold++
				}
			}
			rate := float64(gold) / draws
			if rate < tc.want-.004 || rate > tc.want+.004 {
				t.Fatalf("pool %d race %d: natural gold %.4f, want %.4f", tc.typ, race, rate, tc.want)
			}
			t.Logf("pool %d race %d: natural gold %.2f%%", tc.typ, race, 100*rate)
		}
	}
}

func TestRecruitReducedGoldWithPity(t *testing.T) {
	for _, typ := range []int{1, 5} {
		mall := &Mall{Race: 3, Pity: map[int]int{typ: 9}}
		rng := rand.New(rand.NewPCG(uint64(typ), 31))
		if card := drawPets(recruitPools[typ], mall, 1, rng)[0]; gamedata.Pets[card].Star != 5 || mall.Pity[typ] != 0 {
			t.Fatalf("pool %d: existing pity progress was not honored", typ)
		}
		gold, previousGold, consecutive, since := 0, false, 0, 0
		const batches = 10000
		for range batches {
			for _, card := range drawPets(recruitPools[typ], mall, 10, rng) {
				isGold := gamedata.Pets[card].Star == 5
				since++
				if isGold {
					gold++
					if previousGold {
						consecutive++
					}
					since = 0
				}
				if since >= 10 {
					t.Fatalf("pool %d: ten draws without gold", typ)
				}
				previousGold = isGold
			}
		}
		// Includes all guarantees: at least one per ten; random extras stay low.
		if gold < batches || gold > batches*14/10 || consecutive > batches/10 {
			t.Fatalf("pool %d: gold %d consecutive %d in %d draws", typ, gold, consecutive, batches*10)
		}
		t.Logf("pool %d: gold %.2f%% including pity; consecutive-gold pairs %d", typ, float64(gold)/float64(batches*10)*100, consecutive)
	}
}
