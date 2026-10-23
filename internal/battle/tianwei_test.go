package battle

import "testing"

// TestTianweiInvincible：天威降临打敌我全体（含武则天自己），无敌的单位（包括武则天自己）不掉血。
func TestTianweiInvincible(t *testing.T) {
	src := newUnit(fu(1, 1000, 100000, 1, levelID(famTianWeiJiangLin, 4)), 0)
	x := newUnit(fu(101, 100, 10000, 1), 1)
	y := newUnit(fu(102, 100, 10000, 1), 1)
	b := faqBattle(src, x, y)
	b.setStatus(y, stImmune, 0, 1, false, 5321, slotBuff)
	b.setStatus(src, stImmune, 0, 1, false, 5321, slotBuff)
	b.attackAction(src, x, 100, atkMain)
	if y.hp != 10000 || src.hp != 100000 || x.hp != 10000-1000-400 {
		t.Fatalf("hp self %d x %d y %d", src.hp, x.hp, y.hp)
	}
}
