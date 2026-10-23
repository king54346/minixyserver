package battle

import "testing"

// TestBurnTick：燃烧所有层合成一个图标、每回合合计结算一次（ACT_DO(被烧者, 图标号) → SUB_HP 17531）。
// 不激发：只有一层燃烧4（8%，图标燃烧4）；狂怒焰激发：每次释放必定额外叠一层同等级燃烧，合计 16% 显示燃烧8。
func TestBurnTick(t *testing.T) {
	k := levelID(famKuangNuYan, 4) // 燃烧4：上限 8%
	for _, c := range []struct {
		tj   int
		icon int64
		pct  int64
	}{{0, 5024, 8}, {5, 5028, 16}} {
		t1 := []*Fighter{tjf(fu(1, 100, 100, 0, k), c.tj), fu(2, 10, 1000000, 0)}
		t2 := []*Fighter{fu(101, 500, 100000, 9), fu(102, 10, 100000, 0)}
		_, cmds := simCheck(t, t1, t2, 1)
		ticks := 0
		for i, x := range cmds {
			if x.ID == CmdActDo && x.Actor == 101 && x.Magic == c.icon {
				n := cmds[i+1]
				if n.ID != CmdSubHP || n.Magic != burnMagic || n.Targets[0][1] > 100000*c.pct/100 || n.Targets[0][1] <= 0 {
					t.Fatalf("tj %d: tick %+v", c.tj, n)
				}
				ticks++
			}
		}
		if ticks == 0 {
			t.Fatalf("tj %d: no tick with icon %d", c.tj, c.icon)
		}
	}
}

// TestBurnIcon：多层燃烧只显示一个图标，等级 = 合计 % 对应的燃烧N（上限燃烧10），去掉时只发一次 RM_BUFF。
func TestBurnIcon(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1), 0)
	o := newUnit(fu(101, 100, 1000, 1), 1)
	b := faqBattle(u, o)
	b.addPoison(u, poison{from: 1, pct: 10, left: maxRounds, buff: 5025, burn: true})
	b.addBurnStack(u, poison{from: 1, pct: 8, left: maxRounds, buff: 5024})
	if u.burnShown != 5029 || len(u.buffSlot) != 1 {
		t.Fatalf("icon %d slots %v", u.burnShown, u.buffSlot)
	}
	for range 3 {
		b.addBurnStack(u, poison{from: 1, pct: 8, left: maxRounds, buff: 5024})
	}
	if u.burnShown != 5030 || u.burnPct() != burnMaxPct { // 合计 42% 封顶燃烧10 = 20%
		t.Fatalf("cap icon %d pct %d", u.burnShown, u.burnPct())
	}
}

// TestBurnClearedOnDeath：回后阵时燃烧层数保留（持续到战斗结束）；阵亡时全部清除、图标去掉，复活后不再带燃烧。
func TestBurnClearedOnDeath(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1), 0)
	o := newUnit(fu(101, 100, 1000, 1), 1)
	b := faqBattle(u, o)
	b.addPoison(u, poison{from: 1, pct: 8, left: maxRounds, buff: 5024, burn: true})
	b.addBurnStack(u, poison{from: 1, pct: 8, left: maxRounds, buff: 5024})
	b.leaveClear(u, false)
	if len(u.poison) != 2 || u.burnShown != 5028 {
		t.Fatalf("bench burns: %d icon %d", len(u.poison), u.burnShown)
	}
	b.hurt(o, u, 1, u.hp, true)
	if u.alive() || len(u.poison) != 0 || u.burnShown != 0 {
		t.Fatalf("dead alive %v poison %d", u.alive(), len(u.poison))
	}
	b.relive(u, 1, 500)
	if len(u.poison) != 0 {
		t.Fatal("revived with burn")
	}
}
