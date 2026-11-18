package battle

import "testing"

func TestTianGangPhysicalDefenses(t *testing.T) {
	for _, mode := range []string{"plain", "physical_shield", "magic_shield", "hudun", "qijia", "jianren"} {
		t.Run(mode, func(t *testing.T) {
			u := newUnit(fu(1, 100, 1000, 1, levelID(famTianGangJianQi, 4)), 0)
			god := newUnit(raced(fu(2, 0, 1000, 1), raceGod), 0)
			foe := newUnit(fu(101, 100, 1000, 1), 1)
			want := int64(640)
			switch mode {
			case "physical_shield":
				foe.shield = 200
				want = 440
			case "magic_shield":
				foe.mshield = 1000
			case "hudun":
				foe.tals[famHuDun] = newTalent(levelID(famHuDun, 10))
				setParam(foe.tals[famHuDun], 0, 100)
				setParam(foe.tals[famHuDun], 1, 50)
				want = 320
			case "qijia":
				foe.tals[famQiJia] = newTalent(levelID(famQiJia, 10))
				setParam(foe.tals[famQiJia], 1, 10)
				want = 100
			case "jianren":
				foe.tals[famJianRen] = newTalent(levelID(famJianRen, 10))
				setParam(foe.tals[famJianRen], 0, 100)
				setParam(foe.tals[famJianRen], 1, 50)
				want = 590
			}
			b := faqBattle(u, god, foe)
			b.enter(u)
			if foe.hp != 1000-want || god.hp != 680 {
				t.Fatalf("enemy HP=%d god HP=%d expected damage=%d cost=320", foe.hp, god.hp, want)
			}
			if mode == "magic_shield" && foe.mshield != 1000 {
				t.Fatal("physical hit consumed magic shield")
			}
			cmds, err := DecodeRecord(b.w.b)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "hudun" && !hasCmd(cmds, CmdActDo, int64(foe.tals[famHuDun].id), -1, -1) {
				t.Fatal("missing shield skill activation")
			}
			if mode == "qijia" && !hasCmd(cmds, CmdActDo, int64(foe.tals[famQiJia].id), -1, -1) {
				t.Fatal("missing qijia activation")
			}
		})
	}
}

func TestTianGangTJExtraTargetPhysicalAndScaling(t *testing.T) {
	for lv := 1; lv <= 5; lv++ {
		u := newUnit(tjf(fu(1, 100, 1000, 1, levelID(famTianGangJianQi, 4)), lv), 0)
		god := newUnit(raced(fu(2, 0, 10000, 1), raceGod), 0)
		a := newUnit(fu(101, 0, 10000, 1, levelID(famHuDun, 10)), 1)
		z := newUnit(fu(102, 0, 10000, 1, levelID(famHuDun, 10)), 1)
		for _, x := range []*unit{a, z} {
			setParam(x.tals[famHuDun], 0, 100)
			setParam(x.tals[famHuDun], 1, 50)
			x.shield = 100
			x.mshield = 5000
		}
		b := faqBattle(u, god, a, z)
		b.enter(u)
		lossA, lossZ := 10000-a.hp, 10000-z.hp
		primary, extra := int64(3100), int64(320*lv-100)
		if !((lossA == primary && lossZ == extra) || (lossA == extra && lossZ == primary)) {
			t.Fatalf("TJ level %d damage=%d/%d expected=%d/%d", lv, lossA, lossZ, primary, extra)
		}
		if god.hp != 6800 || a.mshield != 5000 || z.mshield != 5000 {
			t.Fatal("wrong health cost or shield type")
		}
	}
}

func TestTianGangSelfHealthCostBypassesDefenses(t *testing.T) {
	for _, hp := range []int64{1000, 100} {
		u := newUnit(raced(fu(1, 100, 1000, 1, levelID(famTianGangJianQi, 4)), raceGod), 0)
		u.hp = hp
		u.shield = 10000
		u.mshield = 10000
		benchGod := newUnit(raced(fu(6, 0, 100000, 1), raceGod), 0)
		foe := newUnit(fu(101, 0, 10000, 1), 1)
		b := faqBattle(u, benchGod, foe)
		b.setStatus(u, stImmune, 0, 2, false, 0, 0)
		b.enter(u)
		if u.hp != max(hp-320, 0) || foe.hp != 9360 || benchGod.hp != 100000 {
			t.Fatalf("self cost or bench selection: self=%d foe=%d bench=%d", u.hp, foe.hp, benchGod.hp)
		}
	}
}
