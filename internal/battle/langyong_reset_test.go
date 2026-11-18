package battle

import "testing"

func TestLangYongRestoresInitialAttackDuringAction(t *testing.T) {
	dragon := newUnit(tjf(fu(1, 1, 1000, 1, levelID(famLangYong, 4)), 5), 0)
	target := newUnit(fu(101, 100, 1000, 1, levelID(famTuXi, 10)), 1)
	b := faqBattle(dragon, target)
	epoch := target.tempEpoch
	b.tempUp(target, target.tals[famTuXi], 200)
	b.setStatus(target, stZhanhu, 50, 1, true, 5265, slotBuff)
	b.changeAtt(target, 5265, 50)
	b.enter(dragon)
	if target.att != 100 || target.baseAtt != 100 {
		t.Fatalf("浪涌后攻击=%d，初始攻击=%d", target.att, target.baseAtt)
	}
	b.endAction(target, 0, nil, epoch, atkMain)
	b.clearStatus(target, stZhanhu)
	if target.att != 100 {
		t.Fatalf("清除旧加攻后攻击=%d，预期100", target.att)
	}
}
