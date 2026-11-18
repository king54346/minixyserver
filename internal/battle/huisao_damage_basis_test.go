package battle

import "testing"

func TestHuisaoYuanxingActualCenter(t *testing.T) {
	u := newUnit(fu(1, 1000, 10000, 1, 20260), 0)
	setParam(u.tals[famHuiSao], 0, 100)
	a := newUnit(fu(102, 0, 10000, 1), 1)
	g := newUnit(fu(103, 0, 10000, 1, levelID(famYuanXing, 10)), 1)
	g.yuanxingActive = true
	wrong := newUnit(fu(101, 0, 10000, 1), 1)
	right := newUnit(fu(104, 0, 10000, 1), 1)
	b := faqBattle(u, wrong, a, g, right)
	b.attackAction(u, a, 100, atkMain)
	if g.hp != 9000 || a.hp != 9500 || right.hp != 9500 || wrong.hp != 10000 {
		t.Fatalf("wrong center: guard=%d original=%d neighbor=%d old-neighbor=%d", g.hp, a.hp, right.hp, wrong.hp)
	}
}

func TestHuisaoSideTargetsKeepOwnDefenses(t *testing.T) {
	u := newUnit(fu(1, 1000, 10000, 1, 20260), 0)
	setParam(u.tals[famHuiSao], 0, 100)
	main := newUnit(fu(103, 0, 10000, 1, levelID(famHuDun, 10)), 1)
	setParam(main.tals[famHuDun], 0, 100)
	setParam(main.tals[famHuDun], 1, 80)
	left := newUnit(fu(102, 0, 10000, 1), 1)
	right := newUnit(fu(104, 0, 10000, 1), 1)
	left.shield = 200
	right.lead.res = 50
	b := faqBattle(u, main, left, right)
	b.attackAction(u, main, 100, atkMain)
	if main.hp != 9800 || left.hp != 9700 || right.hp != 9750 {
		t.Fatalf("main mitigation leaked to sides or side defense ignored: %d/%d/%d", main.hp, left.hp, right.hp)
	}
	b.setStatus(main, stImmune, 0, 2, false, 0, 0)
	b.setStatus(right, stImmune, 0, 2, false, 0, 0)
	b.attackAction(u, main, 100, atkMain)
	if main.hp != 9800 || left.hp != 9200 || right.hp != 9750 {
		t.Fatalf("immunity must be independent per target: %d/%d/%d", main.hp, left.hp, right.hp)
	}
}

func TestLifestealUsesPreMitigationDamage(t *testing.T) {
	for _, kind := range []string{"normal", "shield", "immune", "overkill", "guard", "reduction", "miss"} {
		t.Run(kind, func(t *testing.T) {
			u := newUnit(fuh(1, 1000, 1000, 10000, 1, 20270), 0)
			x := newUnit(fu(102, 0, 10000, 1), 1)
			g := newUnit(fu(104, 0, 10000, 1, levelID(famShouWei, 10)), 1)
			b := faqBattle(u, x)
			switch kind {
			case "shield":
				x.shield = 10000
			case "immune":
				b.setStatus(x, stImmune, 0, 2, false, 0, 0)
			case "overkill":
				x.hp = 1
			case "guard":
				x.hp = 100
				b.units[1] = append(b.units[1], g)
			case "reduction":
				x.tals[famHuDun] = newTalent(levelID(famHuDun, 10))
				setParam(x.tals[famHuDun], 0, 100)
				setParam(x.tals[famHuDun], 1, 80)
			case "miss":
				x.lead.dodge = 100
			}
			b.attackAction(u, x, 100, atkMain)
			want := int64(2000)
			if kind == "miss" || kind == "shield" || kind == "immune" {
				want = 1000
			}
			if u.hp != want {
				t.Fatalf("heal HP=%d want %d", u.hp, want)
			}
		})
	}
}

func TestHuisaoAndLifestealOutgoingBonusesOnce(t *testing.T) {
	u := newUnit(fuh(1, 1000, 1000, 10000, 1, 20260, 20270), 0)
	setParam(u.tals[famHuiSao], 0, 100)
	u.lead.ndmg, u.lead.dmg = 50, 100
	main := newUnit(fu(102, 0, 10000, 1), 1)
	main.shield = 1000
	left := newUnit(fu(101, 0, 10000, 1), 1)
	b := faqBattle(u, main, left)
	b.attackAction(u, main, 100, atkMain)
	if u.hp != 4000 || left.hp != 8500 {
		t.Fatalf("outgoing damage bonuses lost or doubled: self=%d side=%d", u.hp, left.hp)
	}
}
