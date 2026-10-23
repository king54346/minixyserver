package battle

import "testing"

// TestBenchCarriers：FAQ 写明「护法处于后阵也可触发」的神通，载体放在后阵（前阵站满 5 人）时照样发动。
func TestBenchCarriers(t *testing.T) {
	front := func(lead *Fighter) []*Fighter {
		return []*Fighter{lead, fu(2, 10, 1000000, 0), fu(3, 10, 1000000, 0), fu(4, 10, 1000000, 0), fu(5, 10, 1000000, 0)}
	}
	foes := []*Fighter{fu(101, 300, 1000000, 9), fu(102, 10, 1000000, 0)}
	cases := []struct {
		name  string
		lead  *Fighter
		bench family
	}{
		{"藏剑于身", raced(fu(1, 100, 1000000, 0, levelID(famJianRen, 9)), raceHuman), famCangJianYuShen},
		{"绝命追猎", fu(1, 100, 1000000, 5, levelID(famFenJi, 9)), famJueMingZhuiLie},
		{"疯狂", fu(1, 100, 1000000, 5, levelID(famFengMo, 10)), famFengKuang},
		{"天魔觉醒", fu(1, 100, 100, 0, levelID(famKuangNuYan, 4)), famTianMoJueXing},
	}
	for _, c := range cases {
		k := levelID(c.bench, 4)
		found := false
		for seed := uint64(0); seed < 20 && !found; seed++ {
			t1 := append(front(CloneTeam([]*Fighter{c.lead})[0]), fu(6, 1000, 100000, 0, k))
			_, cmds := simCheck(t, t1, CloneTeam(foes), seed)
			for _, x := range cmds {
				if x.ID == CmdActDo && x.Actor == 6 && x.Magic == int64(k) {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s: 后阵载体没有发动", c.name)
		}
	}
}

// TestXuanyuanStops：轩辕剑三连击打同一目标，目标被击败就停，不换目标。
func TestXuanyuanStops(t *testing.T) {
	u := newUnit(fu(1, 1000, 100000, 1, levelID(famXuanYuanJian, 4)), 0)
	x := newUnit(fu(101, 10, 500, 1), 1)
	y := newUnit(fu(102, 10, 100000, 1), 1)
	b := faqBattle(u, x, y)
	b.releaseCharge(u, u.tals[famXuanYuanJian])
	if x.alive() || y.hp != 100000 {
		t.Fatalf("x hp %d, y hp %d", x.hp, y.hp)
	}
}

// TestZhujianPerKind：铸剑对诈死 / 死亡技 / 入阵技每种只响应一次（两个护法先后入阵只加一次）；激发时三种都触发过
// 后重置可再触发、效果增强；持有者被复活时也重置。
func TestZhujianPerKind(t *testing.T) {
	c := newUnit(fu(1, 100, 1000, 1, levelID(famZhuJian, 4)), 0)
	a := newUnit(fu(2, 100, 1000, 1), 0)
	o := newUnit(fu(101, 100, 1000, 1), 1)
	b := faqBattle(c, a, o)
	att := a.att
	b.firstSkill(a, skEnter)
	b.firstSkill(c, skEnter) // 第二次入阵技：不再响应
	if got := a.att - att; got != 20 {
		t.Fatalf("enter twice: +%d", got)
	}
	b.firstSkill(a, skDeath)
	b.firstSkill(a, skZhasi)
	if c.skillDone != skAll {
		t.Fatalf("done %b", c.skillDone)
	}
	b.firstSkill(a, skEnter)
	if a.att-att != 20*3 && a.att <= att { // 没激发：三种都用完不会重置
		t.Fatalf("att %d", a.att)
	}
	before := a.att
	b.firstSkill(a, skEnter)
	if a.att != before {
		t.Fatal("no-tj triggered after all three")
	}
	c.hp = 0
	b.relive(c, 1, 500)
	if c.skillDone != 0 {
		t.Fatal("revive did not reset")
	}

	// 激发：三种都触发后重置，之后效果提升
	c2 := newUnit(tjf(fu(1, 100, 1000, 1, levelID(famZhuJian, 4)), 5), 0)
	b2 := faqBattle(c2, newUnit(fu(101, 100, 1000, 1), 1))
	for _, k := range []int{skEnter, skDeath, skZhasi} {
		b2.firstSkill(c2, k)
	}
	if c2.skillDone != 0 || c2.skillUps != 1 {
		t.Fatalf("tj reset done %b ups %d", c2.skillDone, c2.skillUps)
	}
}
