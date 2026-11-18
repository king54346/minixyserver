package battle

import "testing"

func TestHuaxueIndependentlyChecksYuanxing(t *testing.T) {
	for _, mode := range []string{"normal", "ghost_axe", "miss", "untransformed", "stiff"} {
		t.Run(mode, func(t *testing.T) {
			u := newUnit(fu(1, 100, 1000, 1, levelID(famHuaXue, 10)), 0)
			target := newUnit(fu(103, 0, 10000, 1), 1)
			guard := newUnit(fu(102, 0, 10000, 1, levelID(famYuanXing, 10)), 1)
			guard.yuanxingActive = true
			setParam(u.tals[famHuaXue], 0, 100)
			b := faqBattle(u, target, guard)
			switch mode {
			case "ghost_axe":
				b.setStatus(u, stGhostAxe, 100, 2, false, 5635, slotBuff)
			case "miss":
				guard.lead.dodge = 100
			case "untransformed":
				guard.yuanxingActive = false
			case "stiff":
				b.setStatus(guard, stStiff, 0, 2, false, 5001, slotDebuff)
			}
			b.attackAction(u, target, 100, atkMain)
			wantTarget, wantGuard := int64(10000), int64(8900)
			switch mode {
			case "ghost_axe":
				wantTarget, wantGuard = 9900, 9000
			case "miss":
				wantTarget, wantGuard = 10000, 10000
			case "untransformed", "stiff":
				wantTarget, wantGuard = 8900, 10000
			}
			if target.hp != wantTarget || guard.hp != wantGuard {
				t.Fatalf("%s original=%d protector=%d expected=%d/%d", mode, target.hp, guard.hp, wantTarget, wantGuard)
			}
		})
	}
}

func TestHuaxueProtectionRetainsLifeLossAndCurrentAttackCap(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, levelID(famHuaXue, 10)), 0)
	u.att = 200 // Current attack, not the initial 100.
	u.atkLanded = true
	setParam(u.tals[famHuaXue], 0, 100)
	target := newUnit(fu(103, 0, 100000, 1), 1)
	guard := newUnit(fu(102, 0, 100000, 1, levelID(famYuanXing, 10)), 1)
	guard.yuanxingActive = true
	guard.shield = 50000
	guard.mshield = 50000
	b := faqBattle(u, target, guard)
	b.setStatus(guard, stImmune, 0, 2, false, 0, 0)
	b.attackEffects(u, target, 1)
	if target.hp != 100000 || guard.hp != 98000 || guard.shield != 50000 || guard.mshield != 50000 {
		t.Fatal("guarded life loss must use current attack cap and bypass protector's shields/immunity")
	}
	cmds, err := DecodeRecord(b.w.b)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCmd(cmds, CmdSubHP, 20210, 102, 2000) || !hasCmd(cmds, CmdActDo, attackGuard, 102, -1) {
		t.Fatal("missing protected damage or guard return record")
	}
}
