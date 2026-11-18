package battle

import "testing"

func TestTeammateOnlyEffectsExcludeOwnerAndBench(t *testing.T) {
	owner := newUnit(fuh(1, 100, 500, 1000, 1, levelID(famHunShou, 10), levelID(famJianGe, 10)), 0)
	front := newUnit(fuh(2, 100, 100, 1000, 1, levelID(famJianGe, 10)), 0)
	back := newUnit(fuh(6, 100, 100, 1000, 1, levelID(famJianGe, 10)), 0)
	b := faqBattle(owner, front, back)
	b.onHunShou(owner, owner.tal(famHunShou))
	if owner.hp != 500 || owner.mshield != 0 || front.hp <= 100 || front.mshield == 0 || back.hp != 100 || back.mshield != 0 {
		t.Fatal("soul guard affected wrong teammate scope")
	}
	front.charge = chargeCap(chargeTalent(front)) // no eligible front-row recipient
	k := owner.tal(famJianGe)
	setParam(k, 2, 100)
	owner.charge = 1
	b.releaseCharge(owner, k)
	if back.charge != 0 {
		t.Fatal("sword song transferred to a back-row unit")
	}
}
