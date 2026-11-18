package battle

import "testing"

func TestTemporaryAttackFamiliesUseInitialAttack(t *testing.T) {
	families := []family{famTuXi, famBeiShui, famShiQiang, famYuBing, famQuLei, famTaLang, famPoYan, famDaoHuo, famTaChao, famQuMo, famZhuXian, famDouShou, famShouXing, famTingWei}
	for _, fam := range families {
		t.Run(fam.String(), func(t *testing.T) {
			u := newUnit(fu(1, 1000, 1000, 1, levelID(fam, 10)), 0)
			u.att, u.tempAtt, u.tempBase = 2000, 600, 600
			foe := newUnit(fu(101, 100, 10000, 1), 1)
			foe.hp = 5000
			for _, r := range raceBane {
				if r.fam == fam {
					foe.Race = r.race
				}
			}
			k := u.tals[fam]
			setParam(k, 0, 100)
			if len(k.Param) > 1 {
				setParam(k, 1, 50)
			}
			b := faqBattle(u, foe)
			b.beforeAttack(u, foe)
			if u.att != 2500 || u.tempAtt != 1100 { // 加值=初始攻击 1000 的 50%（不吃当前的临时加成）
				t.Fatalf("initial attack increase not used: att=%d temp=%d", u.att, u.tempAtt)
			}
			b.endAction(u, 600, nil, u.tempEpoch, atkFollow)
			if u.att != 2000 || u.tempAtt != 600 {
				t.Fatal("wrong temporary increase removed")
			}
		})
	}
}

func TestRaceTemporaryAttackUsesInitialRaceSum(t *testing.T) {
	for _, tc := range []struct {
		fam  family
		race int
	}{{famMoSha, raceGhost}, {famShouYao, raceBeast}} {
		u := newUnit(raced(fu(1, 1000, 1000, 1, levelID(tc.fam, 10)), tc.race), 0)
		mate := newUnit(raced(fu(2, 1000, 1000, 1), tc.race), 0)
		u.att, u.tempAtt, u.tempBase = 2000, 600, 600
		mate.att = 3000 // 当前攻击被抬高，初始攻击仍 1000——加成应按初始值算
		k := u.tals[tc.fam]
		setParam(k, 0, 100)
		setParam(k, 1, 50)
		foe := newUnit(fu(101, 0, 10000, 1), 1)
		b := faqBattle(u, mate, foe)
		b.beforeAttack(u, foe)
		if u.att != 3000 {
			t.Fatalf("%s race sum must be initial (1000+1000)*50%%: att=%d", tc.fam, u.att)
		}
	}
}

func TestJianGeAndChongFengUseInitialAttack(t *testing.T) {
	u := newUnit(fu(1, 1000, 1000, 1, levelID(famJianGe, 4)), 0)
	u.att, u.tempAtt, u.tempBase = 2000, 600, 600
	u.charge = 1
	k := u.tals[famJianGe]
	setParam(k, 1, 50)
	setParam(k, 2, 0)
	b := faqBattle(u, newUnit(fu(103, 0, 10000, 1), 1))
	b.releaseCharge(u, k)
	if u.att != 2500 {
		t.Fatal("sword song must use initial attack")
	}
	v := newUnit(fu(1, 1000, 1000, 1, levelID(famChongFeng, 10)), 0)
	v.att, v.tempAtt, v.tempBase = 2000, 600, 600
	x := newUnit(fu(103, 0, 10000, 1), 1)
	k = v.tals[famChongFeng]
	setParam(k, 0, 100)
	setParam(k, 1, 10)
	b = faqBattle(v, x)
	b.chongFengTarget(v, x)
	if v.att != 2300 { // 初始攻击 1000×10%×距离3=300
		t.Fatal("charge must use initial attack")
	}
}
