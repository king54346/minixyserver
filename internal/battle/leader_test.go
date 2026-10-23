package battle

import (
	"math/rand/v2"
	"testing"
)

// simLeaders 打一场带队长技的战斗并做 Replay 自洽检查。
func simLeaders(t *testing.T, t1, t2 []*Fighter, leaders [2]int32, seed uint64) []Cmd {
	t.Helper()
	r1, r2 := CloneTeam(t1), CloneTeam(t2)
	res := SimulateLeaders(t1, t2, leaders, rand.New(rand.NewPCG(seed, 1)))
	if _, err := Replay(r1, r2, res.Record); err != nil {
		t.Fatal(err)
	}
	return mustDecode(t, res.Record)
}

func TestLeaderBonusTiers(t *testing.T) {
	for _, c := range []struct {
		skill int32
		race  int
		want  leadBonus
	}{
		{90009, raceGhost, leadBonus{dmg: 10}}, // 高级魔族解放
		{90009, raceHuman, leadBonus{}},        // 种族不符不生效
		{90007, raceGhost, leadBonus{dmg: 5}},  // 初级：大量的 1/2
		{90008, raceGhost, leadBonus{dmg: 7}},  // 中级：大量的 3/4
		{90045, raceBeast, leadBonus{ndmg: 10}},
		{90123, raceGod, leadBonus{dodge: 14}},
		{90093, raceHuman, leadBonus{mres: 10}},
		{0, raceHuman, leadBonus{}},
	} {
		if got := leaderBonusFor(c.skill, c.race); got != c.want {
			t.Errorf("leader %d race %d: %+v, want %+v", c.skill, c.race, got, c.want)
		}
	}
}

// 高级魔族解放：我方魔族普攻 ×1.10（官方战报 8/8），人族不变。
func TestLeaderDamage(t *testing.T) {
	t1 := []*Fighter{raced(fu(1, 100, 1000, 10), raceGhost), raced(fu(2, 100, 1000, 10), raceHuman)}
	t2 := []*Fighter{fu(101, 0, 100000, 1), fu(102, 0, 100000, 1)}
	c := simLeaders(t, t1, t2, [2]int32{90009, 0}, 1)
	if got := subHPTo(c, 101); len(got) == 0 || got[0] != 110 {
		t.Fatalf("ghost hit %v, want 110", got)
	}
	if got := subHPTo(c, 102); len(got) == 0 || got[0] != 100 {
		t.Fatalf("human hit %v, want 100", got)
	}
}

// 高级魔皮：法术伤害抗性 10%，龙跃 80 → 72。
func TestLeaderMagicResist(t *testing.T) {
	t1 := []*Fighter{fu(1, 1, 100000, 1)}
	t2 := []*Fighter{fu(106, 100, 1000, 1, 21784)} // 后阵补位入阵才放龙跃
	c := simLeaders(t, t1, t2, [2]int32{90093, 0}, 1)
	if !hasCmd(c, CmdSubHP, 21784, 1, 72) {
		t.Fatal("no 72 water hit under 高级魔皮")
	}
}

// 高级灵巧：无闪避神通也会躲闪（约 14%）；没有队长技时从不躲闪。
func TestLeaderDodge(t *testing.T) {
	t1 := []*Fighter{fu(1, 0, 1000000, 1)}
	t2 := []*Fighter{fu(101, 1, 1000000, 10)}
	count := func(leader int32) (dodge, all int) {
		for seed := range uint64(20) {
			for _, x := range simLeaders(t, CloneTeam(t1), CloneTeam(t2), [2]int32{leader, 0}, seed) {
				if x.ID == CmdActDo && x.Actor == 101 && x.Magic <= attackDodge {
					all++
					if x.Magic == attackDodge {
						dodge++
					}
				}
			}
		}
		return
	}
	if d, n := count(0); d != 0 || n == 0 {
		t.Fatalf("no leader: %d/%d dodges", d, n)
	}
	d, n := count(90123)
	if r := float64(d) / float64(n); r < 0.08 || r > 0.20 {
		t.Fatalf("高级灵巧 dodge rate %.3f (%d/%d), want ~0.14", r, d, n)
	}
}

// 高级虔诚：被治疗效果 +10%，道之始 60% 回复 → 66%。
func TestLeaderHealIn(t *testing.T) {
	t1 := []*Fighter{fu(6, 0, 1000, 1, 20974), fuh(2, 0, 100, 1000, 1)} // 道之始补位入阵
	t2 := []*Fighter{fu(101, 0, 100000, 1)}
	c := simLeaders(t, t1, t2, [2]int32{90168, 0}, 1)
	if !hasCmd(c, CmdAddHP, 15158, 2, 660) {
		t.Fatal("no 660 heal under 高级虔诚")
	}
}

// 坚韧：属性伤害也触发（官方战报），龙跃 80 水伤 − 坚韧 100 攻 × 12% = 68，并加攻 12。
func TestToughOnPropertyDamage(t *testing.T) {
	t1 := []*Fighter{fu(6, 100, 1000, 1, 21784)} // 龙跃补位入阵
	t2 := []*Fighter{fu(101, 100, 100000, 1, 20440)}
	for seed := range uint64(50) {
		c := simLeaders(t, CloneTeam(t1), CloneTeam(t2), [2]int32{}, seed)
		if hasCmd(c, CmdAttChange, 20440, 101, 12) && hasCmd(c, CmdSubHP, 21784, 101, 68) {
			return
		}
	}
	t.Fatal("坚韧 never reduced the water hit")
}

func TestLieyanChance(t *testing.T) {
	for r, want := range map[int]int{1: 100, 2: 66, 3: 33, 4: 10, 9: 10} {
		if got := lieyanChance(r); got != want {
			t.Errorf("round %d: %d, want %d", r, got, want)
		}
	}
}
