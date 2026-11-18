package battle

import "testing"

func TestXiuluoFollowChargeIndependentOfFirstAttack(t *testing.T) {
	u := newUnit(fu(1, 1000, 1000000, 1, levelID(famChongFeng, 10)), 0)
	x := newUnit(fu(101, 1, 10000000, 1), 1)
	b := faqBattle(u, x)
	k := u.tals[famChongFeng]
	setParam(k, 0, 0)
	b.act(u) // 首击不触发冲锋。
	before := x.hp
	setParam(k, 0, 100)
	setParam(k, 1, 1000)
	b.attackAction(u, x, 100, atkXiuluo)
	if before-x.hp < 5000 {
		t.Fatalf("pursuit failed its independent charge roll: damage=%d", before-x.hp)
	}
	if u.att != 1000 || u.tempAtt != 0 {
		t.Fatal("pursuit charge attack leaked after attack")
	}
}
