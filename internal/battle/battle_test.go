package battle

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"turnrpg/internal/gamedata"
)

func TestSimulate(t *testing.T) {
	a := []*Fighter{{Pos: 1, Att: 50, HP: 100, MaxHP: 100, Speed: 5}, {Pos: 2, Att: 10, HP: 30, MaxHP: 30, Speed: 9}}
	b := []*Fighter{{Pos: 101, Att: 40, HP: 60, MaxHP: 60, Speed: 7}}
	res := Simulate(a, b, rand.New(rand.NewPCG(1, 2)))
	var got []string
	for _, c := range mustDecode(t, res.Record) {
		switch c.ID {
		case CmdActDo:
			got = append(got, fmt.Sprintf("act %d->%d", c.Actor, c.Targets[0][0]))
		case CmdSubHP:
			got = append(got, fmt.Sprintf("sub %d %d", c.Targets[0][0], c.Targets[0][1]))
		case CmdDie:
			got = append(got, fmt.Sprintf("die %d", c.Targets[0][0]))
		default:
			got = append(got, fmt.Sprint(c.ID))
		}
	}
	// 回合 1 按列出手：第 1 列 101（速 7）比 1 号（速 5）快，先打对位 1 号剩 60，1 号再打 101 剩 10；
	// 第 2 列 2 号的对位 102 不存在，打对方最左边的 101 → 胜
	want := []string{"11", "9", "act 101->1", "sub 1 40", "act 1->101", "sub 101 50",
		"act 2->101", "sub 101 10", "die 101", "12"}
	if fmt.Sprint(got) != fmt.Sprint(want) || !res.Win {
		t.Fatalf("got %v win=%v; want %v", got, res.Win, want)
	}
}

func TestEnemyTeamMatchesCapture(t *testing.T) {
	// 精英 60305：抓包 team2 第一个是 22501，hp 6604 att 2476 速 6，站位 101
	e := EnemyTeam(gamedata.Gates[60305].Mode[1])
	if len(e) == 0 || e[0].ID != 22501 || e[0].Pos != 101 || e[0].HP != 6604 || e[0].Att != 2476 || e[0].Speed != 6 {
		t.Fatalf("enemy %+v", e[0])
	}
}

func mustDecode(t *testing.T, b []byte) []Cmd {
	t.Helper()
	cmds, err := DecodeRecord(b)
	if err != nil {
		t.Fatal(err)
	}
	return cmds
}

// TestBookRateBonus：天将法术几率加成按下标挂到对应法术书上（Books 比 BookRate 长的部分为 0）。
func TestBookRateBonus(t *testing.T) {
	f := &Fighter{Pos: 1, HP: 10, MaxHP: 10, Books: [][2]int32{{11101, 1}, {11102, 1}}, BookRate: []int{6}}
	u := newUnit(f, 0)
	if len(u.magics) != 2 || u.magics[0].bonus != 6 || u.magics[1].bonus != 0 {
		t.Fatalf("magics %+v %+v", u.magics[0], u.magics[1])
	}
}

// TestSurvivors：Survivors 按开战站位报告存活（强敌打死 1 号位，2 号位远大于敌方攻击活下来）。
func TestSurvivors(t *testing.T) {
	t1 := []*Fighter{fu(1, 1, 10, 1), fu(2, 1, 100000, 1)}
	t2 := []*Fighter{fu(101, 100, 100000, 10)}
	r1, r2 := CloneTeam(t1), CloneTeam(t2)
	res := Simulate(t1, t2, nil)
	alive, err := Survivors(r1, r2, res.Record)
	if err != nil {
		t.Fatal(err)
	}
	if alive[1] || !alive[2] || !alive[101] {
		t.Fatalf("alive %v", alive)
	}
}

// TestMoveToEmpty：后阵补到空位（开局就没人）用 MOVETO_EMPTY，补到阵亡者的位置用 MOVE（客户端两条路径不同，
// 空位发 MOVE 会在 war/config.getPos 报错卡住）。
func TestMoveToEmpty(t *testing.T) {
	t1 := []*Fighter{fu(1, 1, 1, 1), fu(2, 0, 100000, 1), fu(4, 0, 100000, 1), fu(5, 0, 100000, 1),
		fu(6, 0, 100000, 1), fu(7, 0, 100000, 1)} // 3 号位空着
	t2 := []*Fighter{fu(101, 10, 100000, 10)}
	_, cmds := simCheck(t, t1, t2, 1)
	var empty, move []Cmd
	for _, c := range cmds {
		switch c.ID {
		case CmdMoveToEmpty:
			empty = append(empty, c)
		case CmdMove:
			move = append(move, c)
		}
	}
	if len(empty) != 1 || empty[0].Actor != 6 || empty[0].Magic != 3 {
		t.Fatalf("MOVETO_EMPTY %+v, want 6→3", empty)
	}
	if len(move) == 0 || move[0].Actor != 7 || move[0].Magic != 1 {
		t.Fatalf("MOVE %+v, want 7→1 after pos 1 dies", move)
	}
}

// TestOrderByColumn：按列从左到右出手，同列速度快的先；同速威望高的先，威望相同我方先。
func TestOrderByColumn(t *testing.T) {
	mk := func() ([]*Fighter, []*Fighter) {
		return []*Fighter{fu(1, 1, 100000, 9), fu(2, 1, 100000, 5), fu(3, 1, 100000, 1)},
			[]*Fighter{fu(101, 1, 100000, 1), fu(102, 1, 100000, 7), fu(103, 1, 100000, 1)}
	}
	first := func(prestige [2]int) []int {
		t1, t2 := mk()
		res := SimulateWith(t1, t2, Options{Prestige: prestige}, rand.New(rand.NewPCG(1, 2)))
		var order []int
		seen := map[int]bool{}
		for _, c := range mustDecode(t, res.Record) {
			if c.ID == CmdActDo && c.Magic == attackNormal && !seen[c.Actor] {
				seen[c.Actor] = true
				order = append(order, c.Actor)
			}
		}
		return order[:min(6, len(order))]
	}
	if got := first([2]int{}); fmt.Sprint(got) != "[1 101 102 2 3 103]" {
		t.Fatalf("order %v, want [1 101 102 2 3 103]", got)
	}
	if got := first([2]int{0, 5}); fmt.Sprint(got) != "[1 101 102 2 103 3]" {
		t.Fatalf("enemy prestige order %v, want [1 101 102 2 103 3]", got)
	}
}
