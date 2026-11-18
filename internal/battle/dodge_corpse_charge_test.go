package battle

import "testing"

func TestYuanxingDodgeUsesProtector(t *testing.T) {
	attacker := newUnit(fu(1, 100, 1000, 1), 0)
	target := newUnit(fu(102, 0, 1000, 1), 1)
	guard := newUnit(fu(101, 0, 1000, 1, levelID(famYuanXing, 10)), 1)
	guard.yuanxingActive = true
	target.lead.dodge = 100
	b := faqBattle(attacker, target, guard)
	b.attackAction(attacker, target, 100, atkMain)
	if target.hp != 1000 || guard.hp != 900 {
		t.Fatalf("original target dodge intercepted the hit: target=%d protector=%d", target.hp, guard.hp)
	}
	guard.lead.dodge = 100
	b.w.b = nil
	b.attackAction(attacker, target, 100, atkMain)
	cmds := mustDecode(t, b.w.b)
	if !hasCmd(cmds, CmdSubHP, attackDodge, 101, 0) || hasCmd(cmds, CmdSubHP, attackDodge, 102, 0) || guard.hp != 900 {
		t.Fatalf("protector dodge or return animation failed: %+v", cmds)
	}
}

func TestNoSoulGlyphAfterDeath(t *testing.T) {
	for _, buff := range []int32{5141, 0} {
		attacker := newUnit(fu(1, 100, 1000, 1), 0)
		target := newUnit(fu(101, 0, 100, 1), 1)
		b := faqBattle(attacker, target)
		b.setStatus(target, stNoSoul, 0, maxRounds, false, buff, slotDebuff)
		b.w.b = nil
		b.hurt(attacker, target, attackNormal, 100, true)
		cmds := mustDecode(t, b.w.b)
		died := false
		shown := false
		for _, c := range cmds {
			if c.ID == CmdDie {
				died = true
			}
			if died && c.ID == CmdBuff && c.Magic == 5141 {
				shown = true
			}
		}
		if !shown || target.has(stNoSoul) == nil {
			t.Fatalf("missing corpse glyph: %+v", cmds)
		}
	}
}

func TestHumanChargePreservedAfterExpulsion(t *testing.T) {
	human := newUnit(raced(fu(1, 100, 1000, 1, levelID(famLiuYi, 4)), raceHuman), 0)
	bench := newUnit(fu(6, 100, 1000, 1), 0)
	b := faqBattle(human, bench)
	b.setCharge(human, 2)
	b.w.b = nil
	b.quzhu(human, bench)
	cmds := mustDecode(t, b.w.b)
	if human.front() || human.charge != 2 || len(cmds) == 0 || cmds[0].ID != CmdMove || !hasCmd(cmds, CmdAddPower, 1, 6, 2) {
		t.Fatalf("charge lost after move: charge=%d commands=%+v", human.charge, cmds)
	}
}
