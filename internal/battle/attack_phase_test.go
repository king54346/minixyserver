package battle

import "testing"

func TestPoWangPrecedesChargeRelease(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind atkKind
	}{{"normal", atkMain}, {"first-strike", atkPre}, {"counter", atkCounter}} {
		t.Run(tc.name, func(t *testing.T) {
			u := newUnit(raced(fu(1, 100, 1000, 1, 21711, int32(famFeiShi)), raceHuman), 0)
			x := newUnit(fu(101, 0, 1000, 1), 1)
			x.hp, x.maxHP = 2000, 2000
			setParam(u.tals[famPoWang], 0, 100)
			charge := u.tals[famFeiShi]
			setParam(charge, 1, 50)
			u.charge = chargeNeed(charge)
			b := faqBattle(u, x)
			if tc.kind == atkMain {
				b.act(u)
			} else {
				b.attackAction(u, x, 100, tc.kind)
			}
			powang, release := 0, 0
			for i, cmd := range mustDecode(t, b.w.b) {
				if cmd.ID != CmdActDo {
					continue
				}
				switch cmd.Magic {
				case 21711:
					if powang != 0 {
						t.Fatal("破妄重复结算")
					}
					powang = i + 1
				case int64(famFeiShi):
					release = i + 1
				}
			}
			if powang == 0 || release <= powang || x.maxHP != 1000 || x.hp != 400 || u.charge != 0 {
				t.Fatalf("破妄=%d 飞石=%d hp=%d max=%d charge=%d", powang, release, x.hp, x.maxHP, u.charge)
			}
		})
	}
}

func TestMieFaBreaksInvincibilityOnJinShen(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, levelID(famMieFa, 10)), 0)
	x := newUnit(fu(101, 0, 1000, 1, int32(famJinShenBoss)), 1)
	b := faqBattle(u, x)
	b.setStatus(x, stImmune, 0, 1, false, 5321, slotBuff)
	b.attackAction(u, x, 100, atkMain)
	if b.invincible(x) || x.hp != 800 {
		t.Fatalf("灭法未清除金身目标的无敌或伤害未加成，hp=%d", x.hp)
	}
}
