package battle

import "testing"

func TestZhuangjiDoesNotTriggerAfterMiss(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, levelID(famZhuangJi, 10)), 0)
	target := newUnit(fu(103, 0, 10000, 1), 1)
	guard := newUnit(fu(102, 0, 10000, 1, levelID(famYuanXing, 10)), 1)
	guard.yuanxingActive = true
	guard.lead.dodge = 100
	setParam(u.tals[famZhuangJi], 0, 100)
	b := faqBattle(u, target, guard)
	b.attackAction(u, target, 100, atkMain)
	if target.hp != 10000 || guard.hp != 10000 || u.hp != 1000 {
		t.Fatalf("missed attack triggered collision: target=%d guard=%d self=%d", target.hp, guard.hp, u.hp)
	}
	cmds, err := DecodeRecord(b.w.b)
	if err != nil {
		t.Fatal(err)
	}
	if hasCmd(cmds, CmdSubHP, 20490, -1, -1) {
		t.Fatal("collision must not appear after a miss")
	}
	if !hasCmd(cmds, CmdActDo, attackGuard, 102, -1) {
		t.Fatal("protector must return to its own slot after the guard animation")
	}
}

func TestZhuangjiYuanxingActivationAndBypass(t *testing.T) {
	for _, mode := range []string{"active", "untransformed", "stiff", "dead", "bench", "distant", "ghost_axe"} {
		t.Run(mode, func(t *testing.T) {
			u := newUnit(fu(1, 100, 1000, 1, levelID(famZhuangJi, 10)), 0)
			target := newUnit(fu(103, 0, 10000, 1), 1)
			guard := newUnit(fu(102, 0, 10000, 1, levelID(famYuanXing, 10)), 1)
			guard.yuanxingActive = true
			setParam(u.tals[famZhuangJi], 0, 100)
			b := faqBattle(u, target, guard)
			switch mode {
			case "untransformed":
				guard.yuanxingActive = false
			case "stiff":
				b.setStatus(guard, stStiff, 0, 2, false, 5001, slotDebuff)
			case "dead":
				guard.hp = 0
			case "bench":
				guard.Pos = 106
			case "distant":
				guard.Pos = 101
			case "ghost_axe":
				b.setStatus(u, stGhostAxe, 100, 2, false, 5635, slotBuff)
			}
			before := guard.hp
			b.attackAction(u, target, 100, atkMain)
			switch mode {
			case "active":
				if target.hp != 10000 || guard.hp != 9500 {
					t.Fatalf("active protection: %d/%d", target.hp, guard.hp)
				}
			case "ghost_axe":
				if target.hp != 9900 || guard.hp != 9600 {
					t.Fatalf("only main attack bypasses protection: %d/%d", target.hp, guard.hp)
				}
			default:
				if target.hp != 9500 || guard.hp != before {
					t.Fatalf("ineligible protector intercepted collision: %d/%d", target.hp, guard.hp)
				}
			}
		})
	}
}

func TestZhuangjiProtectorUsesOwnPhysicalShield(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, levelID(famZhuangJi, 10)), 0)
	target := newUnit(fu(103, 0, 10000, 1), 1)
	guard := newUnit(fu(102, 0, 10000, 1, levelID(famYuanXing, 10)), 1)
	target.shield = 5000
	guard.yuanxingActive = true
	guard.shield = 200
	guard.mshield = 9000
	setParam(u.tals[famZhuangJi], 0, 100)
	b := faqBattle(u, target, guard)
	b.attackAction(u, target, 100, atkMain)
	if target.hp != 10000 || target.shield != 5000 || guard.hp != 9700 || guard.shield != 0 || guard.mshield != 9000 || u.hp != 800 {
		t.Fatal("collision must use protector's physical shield and keep the attacker's self-cost")
	}
}
