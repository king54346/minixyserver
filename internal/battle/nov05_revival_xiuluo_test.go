package battle

import (
	"math/rand/v2"
	"testing"
	"turnrpg/internal/gamedata"
)

func TestHuiSaoTenTriggersForEveryLuanWuHit(t *testing.T) {
	for seed := uint64(0); seed < 40; seed++ {
		u := newUnit(fu(2, 100, 100000, 1, 20260, 20065), 0)
		x := newUnit(fu(102, 0, 100000, 1), 1)
		left := newUnit(fu(101, 0, 100000, 1), 1)
		right := newUnit(fu(103, 0, 100000, 1), 1)
		b := faqBattle(u, x, left, right)
		b.rng = rand.New(rand.NewPCG(seed, 1))
		setParam(u.tals[famLuanWu], 0, 100)
		b.attackAction(u, x, 100, atkMain)
		attacks, sweeps := 0, 0
		for _, c := range mustDecode(t, b.w.b) {
			if c.ID == CmdActDo && c.Actor == u.Pos {
				if c.Magic == attackNormal || c.Magic == attackCrit {
					attacks++
				}
				if c.Magic == 20260 {
					sweeps++
				}
			}
		}
		if attacks != 3 || sweeps != 3 {
			t.Fatalf("seed %d: attacks=%d sweeps=%d", seed, attacks, sweeps)
		}
	}
}

func TestOrdinaryResurrectionRestoresInitialState(t *testing.T) {
	for _, source := range []string{"ganlu", "magic", "yiming", "guangyin"} {
		t.Run(source, func(t *testing.T) {
			u := newUnit(fu(1, 10, 1000, 1), 0)
			x := newUnit(fu(2, 100, 1000, 1, 20376, 22074, 21524), 0)
			foe := newUnit(fu(101, 0, 100000, 1), 1)
			b := faqBattle(u, x, foe)
			x.hp = 0
			x.att = 450
			x.maxHP = 2500
			x.charge = 4
			x.xiuluo = 7
			b.setStatus(x, stZhanhu, 50, 1, true, 5262, slotBuff)
			x.tempAtt = 25
			wantHP := int64(800)
			switch source {
			case "ganlu":
				b.ganlu(u, newTalent(21354))
			case "magic":
				// Exercise the actual resurrection spell dispatch, with a deterministic target.
				m := &gamedata.MagicInfo{SP: [3]int{5, 80, 0}, Target: 1, Times: 1}
				b.cast(u, &castable{id: 13502, lv: 1, left: 1, MagicInfo: m})
			case "yiming":
				k := newTalent(levelID(famYiMing, 4))
				wantHP = 1000 * int64(k.param(0)) / 100
				b.onYiMing(u, k)
			case "guangyin":
				k := newTalent(21424)
				k.applyTJ(5)
				wantHP = 1000
				b.onGuangYinSui(u, k)
			}
			if !x.alive() || x.att != 100 || x.maxHP != 1000 || x.charge != 0 || x.has(stZhanhu) != nil || x.tempAtt != 0 {
				t.Fatalf("state hp=%d att=%d max=%d charge=%d", x.hp, x.att, x.maxHP, x.charge)
			}
			if x.hp != wantHP {
				t.Fatalf("hp=%d want=%d", x.hp, wantHP)
			}
			if x.xiuluo != 8 {
				t.Fatalf("death skill resurrection should retain points and add event: %d", x.xiuluo)
			}
		})
	}
}

func TestGhostAxeFiveAndKillRevival(t *testing.T) {
	u := newUnit(fu(1, 100, 10000, 1), 0)
	dead := newUnit(fu(2, 1, 1000, 1), 0)
	x := newUnit(fu(101, 1, 10000, 1), 1)
	b := faqBattle(u, dead, x)
	b.round = 3
	dead.hp = 0
	dead.eaten = true
	b.setStatus(dead, stNoSoul, 0, maxRounds, false, 5141, slotDebuff)
	b.applyBuff(u, u, 5635)
	if s := u.has(stGhostAxe); s == nil || s.val != 50 || s.byAction || s.left != 1 {
		t.Fatal("ghost axe 5 does not grant 50% round-long bypass")
	}
	x.hp = 1
	b.attackAction(u, x, 100, atkMain) // Use the real kill hook, not a direct grant call.
	if dead.eaten || dead.has(stNoSoul) != nil || dead.reviveMark != 5151 || dead.revivePct != 30 || dead.reviveAt != 5 {
		t.Fatalf("ghost axe revival state: %+v", dead)
	}
	b.tickStatus(u, false)
	if u.has(stGhostAxe) != nil {
		t.Fatal("ghost axe survived round end")
	}
}

func TestXiuluoPursuitStopsAfterKillEvenIfTargetRevives(t *testing.T) {
	for _, killed := range []bool{false, true} {
		u := newUnit(fu(1, 100, 10000, 1, 22074), 0)
		x := newUnit(fu(101, 0, 1, 1, levelID(famShenYou, 10)), 1)
		other := newUnit(fu(102, 0, 100000, 1), 1)
		b := faqBattle(u, x, other)
		b.round = 2
		u.xiuluo = 10
		u.tals[famXiuLuoYinShen].applyTJ(5)
		setParam(x.tals[famShenYou], 0, 100)
		if killed {
			u.lastKill = true
			b.followUps(u, x, 0)
			if u.xiuluo != 10 {
				t.Fatal("pursued after prior kill")
			}
			continue
		}
		// Guaranteed first pursuit kills, and guaranteed ShenYou revives the target.
		// The now-living target must not let the loop consume another point.
		b.followUps(u, x, 0)
		if u.xiuluo != 9 || !x.alive() { // 神佑不属于死亡技能，只消耗本次追击的修罗点。
			t.Fatalf("points=%d alive=%v deaths=%d", u.xiuluo, x.alive(), x.deaths)
		}
	}
}

func TestGhostAxeFiveBypassesBothKindsOfProtection(t *testing.T) {
	for _, fam := range []family{famYuanXing, famShouWei} {
		bypasses := 0
		for seed := uint64(0); seed < 400; seed++ {
			u := newUnit(fu(2, 100, 100000, 1), 0)
			x := newUnit(fu(102, 0, 100000, 1), 1)
			guard := newUnit(fu(101, 0, 100000, 1, levelID(fam, 10)), 1)
			guard.yuanxingActive = true
			if fam == famShouWei {
				setParam(guard.tals[fam], 0, 100)
				x.hp = 40000 // Satisfy both guard eligibility conditions before testing bypass.
			}
			b := faqBattle(u, x, guard)
			b.rng = rand.New(rand.NewPCG(seed, 1))
			b.applyBuff(u, u, 5635)
			b.attack(u, x, 100, atkFollow)
			if u.atkVictim == x {
				bypasses++
			}
		}
		if bypasses < 150 || bypasses > 250 {
			t.Fatalf("family %d bypassed %d/400", fam, bypasses)
		}
	}
}

func TestXiuluoPointsAccumulateWhileSealedAndFromStiffDeath(t *testing.T) {
	ashura := newUnit(fu(1, 10, 1000, 1, 22074), 0)
	ashura.hp = 0
	ashura.eaten = true
	dead := newUnit(fu(102, 1, 1000, 1, 21524), 1)
	src := newUnit(fu(2, 2000, 10000, 1), 0)
	b := faqBattle(ashura, dead, src)
	b.setStatus(dead, stStiff, 0, 1, true, 0, 0)
	b.hurt(src, dead, 1, 2000, true)
	if ashura.xiuluo != 1 {
		t.Fatalf("sealed ashura missed death-skill carrier's death: %d", ashura.xiuluo)
	}
	b.reliveInitial(dead, 1, 100)
	if ashura.xiuluo != 2 {
		t.Fatalf("missed revival event: %d", ashura.xiuluo)
	}
}
