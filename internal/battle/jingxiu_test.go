package battle

import "testing"

// TestJingxiu：火球术（精修 buff 燃烧）精修 10 次，施放后目标挂燃烧10（5030）并在回合结束流失上限 20%。
func TestJingxiu(t *testing.T) {
	f := fu(1, 100, 100000, 10)
	f.Books, f.BookJX = [][2]int32{{12101, 10}}, []int{10}
	runFamCase(t, 0, famCase{
		t1: []*Fighter{f}, t2: []*Fighter{fu(101, 1, 100000, 1)},
		check: func(_ Result, c []Cmd) string {
			return want(hasCmd(c, CmdBuff, 5030, 101, -1), "no buff 5030", contains(subHPTo(c, 101), 20000), "no 20% burn")
		}})
	// 没精修不挂
	g := fu(1, 100, 100000, 10)
	g.Books = [][2]int32{{12101, 10}}
	for seed := uint64(0); seed < 20; seed++ {
		_, cmds := simCheck(t, []*Fighter{g}, []*Fighter{fu(101, 1, 100000, 1)}, seed)
		if hasCmd(cmds, CmdBuff, 5030, 101, -1) {
			t.Fatal("unrefined book applied jingxiu buff")
		}
	}
}
