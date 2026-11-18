package battle

import "testing"

// burnSlots 是 t 身上燃烧层的 (BUFF 号, 槽位)。
func burnSlots(t *unit) [][2]int {
	var out [][2]int
	for _, p := range t.poison {
		if p.burn {
			out = append(out, [2]int{int(p.buff), p.slot})
		}
	}
	return out
}

// TestBurnTick：燃烧逐层结算（每层 ACT_DO(被烧者, 该层 BUFF 号) → SUB_HP 17531）。
// 不激发：一层燃烧4（8%）；狂怒焰激发：额外叠一层，两层各结算 8%。
func TestBurnTick(t *testing.T) {
	k := levelID(famKuangNuYan, 4) // 燃烧4：上限 8%
	for _, tj := range []int{0, 5} {
		t1 := []*Fighter{tjf(fu(1, 100, 100, 0, k), tj), fu(2, 10, 1000000, 0)}
		t2 := []*Fighter{fu(101, 500, 100000, 9), fu(102, 10, 100000, 0)}
		_, cmds := simCheck(t, t1, t2, 1)
		ticks := 0
		for i, x := range cmds {
			if x.ID == CmdActDo && x.Actor == 101 && x.Magic == 5024 {
				n := cmds[i+1]
				if n.ID != CmdSubHP || n.Magic != burnMagic || n.Targets[0][1] > 100000*8/100 || n.Targets[0][1] <= 0 {
					t.Fatalf("tj %d: tick %+v", tj, n)
				}
				ticks++
			}
		}
		if ticks == 0 {
			t.Fatalf("tj %d: no tick", tj)
		}
	}
}

// TestBurnStack：所有燃烧都叠加、不封顶，每层一个槽位（同为燃烧4 的两层也是两个图标；燃烧4 与燃烧5 同时显示）。
func TestBurnStack(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1), 0)
	o := newUnit(fu(101, 100, 1000, 1), 1)
	b := faqBattle(u, o)
	b.w.b = nil
	b.addPoison(u, poison{from: 1, pct: 10, left: maxRounds, buff: 5025, burn: true})
	b.addPoison(u, poison{from: 1, pct: 8, left: maxRounds, buff: 5024, burn: true})
	for range 4 {
		b.addBurnStack(u, poison{from: 1, pct: 8, left: maxRounds, buff: 5024})
	}
	l := burnSlots(u)
	if len(l) != 6 || l[0][0] != 5025 || l[1][0] != 5024 || u.burnPct() != 10+8*5 {
		t.Fatalf("layers %v pct %d", l, u.burnPct())
	}
	slots := map[int]bool{}
	for _, x := range l {
		slots[x[1]] = true
	}
	cmds, err := DecodeRecord(b.w.b)
	if len(slots) != 6 || err != nil {
		t.Fatalf("slots %v %v", slots, err)
	}
	n := 0
	for _, c := range cmds {
		if c.ID == CmdBuff {
			n++
		}
	}
	if n != 6 {
		t.Fatalf("BUFF cmds %d", n)
	}
}

// TestBurnClearedOnDeath：回后阵时燃烧层数保留（持续到战斗结束）；阵亡时全部清除、每层的图标都去掉，复活后不再带燃烧。
func TestBurnClearedOnDeath(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1), 0)
	o := newUnit(fu(101, 100, 1000, 1), 1)
	b := faqBattle(u, o)
	b.addPoison(u, poison{from: 1, pct: 8, left: maxRounds, buff: 5024, burn: true})
	b.addBurnStack(u, poison{from: 1, pct: 8, left: maxRounds, buff: 5024})
	b.leaveClear(u, false)
	if len(burnSlots(u)) != 2 {
		t.Fatalf("bench burns: %v", burnSlots(u))
	}
	b.w.b = nil
	b.hurt(o, u, 1, u.hp, true)
	if u.alive() || len(u.poison) != 0 {
		t.Fatalf("dead alive %v poison %d", u.alive(), len(u.poison))
	}
	cmds, _ := DecodeRecord(b.w.b)
	rm := 0
	for _, c := range cmds {
		if c.ID == CmdRmBuff {
			rm++
		}
	}
	if rm < 2 {
		t.Fatalf("rm buff %d", rm)
	}
	b.relive(u, 1, 500)
	if len(u.poison) != 0 {
		t.Fatal("revived with burn")
	}
}
