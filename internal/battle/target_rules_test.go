package battle

import (
	"math/rand/v2"
	"testing"
)

// 用户确认：飞仙的增益、遁入目标必须是其他友方；自身结界转换不受影响。
func TestFeiXianNeverRetreatsSelf(t *testing.T) {
	for _, peerTalent := range []int32{0, 20954} {
		triggered := 0
		for seed := uint64(0); seed < 100; seed++ {
			u := newUnit(fuh(1, 100, 10, 1000, 1, 22034, 20954), 0)
			peer := newUnit(fuh(2, 100, 100, 1000, 1, peerTalent), 0)
			bench := newUnit(fu(6, 100, 1000, 1), 0)
			b := faqBattle(u, peer, bench)
			b.rng = rand.New(rand.NewPCG(seed, 1))
			b.feixian(u, u.tal(famFeiXian))
			if u.Pos != 1 || u.att != 100 || u.maxHP != 1000 || u.hp != 10 || u.dunru {
				t.Fatalf("seed %d peer talent %d: 飞仙 acted on self: %+v", seed, peerTalent, u)
			}
			if peer.Pos == 6 {
				triggered++
			}
		}
		if triggered == 0 {
			t.Fatalf("no teammate received 飞仙, peer talent %d", peerTalent)
		}
	}
	u := newUnit(fuh(1, 100, 10, 1000, 1, 22034, 20954), 0)
	b := faqBattle(u)
	b.feixian(u, u.tal(famFeiXian))
	if len(b.w.b) != 0 || u.att != 100 || u.maxHP != 1000 || u.dunru {
		t.Fatal("飞仙 without another teammate must not select itself")
	}
}

// 避阵即使自身濒危，也只能治疗/换下其他队友；无队友时不对自身生效。
func TestBiZhenNeverHealsSelf(t *testing.T) {
	u := newUnit(fuh(1, 100, 10, 1000, 1, 20450, 20954), 0)
	peer := newUnit(fuh(2, 100, 100, 1000, 1), 0)
	b := faqBattle(u, peer)
	b.onBiZhen(u, u.tal(famBiZhen))
	if u.hp != 10 || u.maxHP != 1000 || u.Pos != 1 || peer.hp != 300 {
		t.Fatalf("避阵 target: self hp=%d pos=%d peer hp=%d", u.hp, u.Pos, peer.hp)
	}
	b = faqBattle(u)
	b.onBiZhen(u, u.tal(famBiZhen))
	if u.hp != 10 || len(b.w.b) != 0 {
		t.Fatal("避阵 without another teammate must not heal self")
	}
}

// 破妄对位及左右逐目标独立判定。
func TestPoWangRollsEachFacingTarget(t *testing.T) {
	for side := 0; side < 2; side++ {
		for pos := 1; pos <= 5; pos++ {
			for seed := uint64(0); seed < 100; seed++ {
				u := newUnit(fu(side*100+pos, 100, 1000, 1, 21720), side)
				b := faqBattle(u)
				b.rng = rand.New(rand.NewPCG(seed, 1))
				rolls := rand.New(rand.NewPCG(seed, 1))
				expected := map[int]bool{}
				for slot := 1; slot <= 5; slot++ {
					x := newUnit(fu((1-side)*100+slot, 100, 1000, 1), 1-side)
					x.maxHP, x.hp = 1500, 1500
					b.units[1-side] = append(b.units[1-side], x)
				}
				for _, x := range b.facing3(u) {
					if rolls.IntN(100) < u.tal(famPoWang).param(0) {
						expected[x.Pos] = true
					}
				}
				b.onPoWang(u, u.tal(famPoWang))
				for _, x := range b.units[1-side] {
					want := int64(1500)
					if expected[x.Pos] {
						want = 1000
					}
					if x.maxHP != want || x.hp != want {
						t.Fatalf("side %d pos %d seed %d target %d: hp=%d max=%d want=%d", side, pos, seed, x.Pos, x.hp, x.maxHP, want)
					}
				}
				cmds, err := DecodeRecord(b.w.b)
				if err != nil {
					t.Fatal(err)
				}
				for _, cmd := range cmds {
					if cmd.ID == CmdActDo {
						if len(cmd.Targets) != len(expected) {
							t.Fatal("破妄 animation omitted facing targets")
						}
						for _, target := range cmd.Targets {
							if !expected[int(target[0])] {
								t.Fatal("破妄 animation selected a different slot")
							}
						}
					}
				}
			}
		}
	}
}
