package xymsrv

import (
	"math/rand/v2"
	"testing"

	"turnrpg/internal/gamedata"
)

// TestXianlingExcluded：仙灵招募不出取经组（斗战胜佛等）和助人海星。
func TestXianlingExcluded(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 3))
	for range 300 {
		for _, g := range xlNewGoods(rng, xlRarePets(rng, 1)[0]) {
			if xlExcluded(g.Pet) {
				t.Fatalf("excluded pet %d (%s)", g.Pet, gamedata.Pets[g.Pet].Name)
			}
		}
	}
	if !xlExcluded(35030) || !xlExcluded(50001) {
		t.Fatal("斗战胜佛 / 助人海星 should be excluded")
	}
}
