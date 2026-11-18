package battle

import (
	"math/rand/v2"
	"testing"
)

// TestFeixianShieldAbsorb：飞仙4（a=50,b=100）——先吸敌方法盾、再吸本方法盾，总量不超过 50% 上限，
// 按 100% 转成自身物理结界。
func TestFeixianShieldAbsorb(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, levelID(famFeiXian, 4)), 0)
	ally := newUnit(fu(2, 100, 1000, 1), 0)
	foe := newUnit(fu(101, 100, 1000, 1), 1)
	foe.mshield, ally.mshield = 300, 300
	b := faqBattle(u, ally, foe)
	b.feixian(u, u.tals[famFeiXian])
	if foe.mshield != 0 || ally.mshield != 100 {
		t.Fatalf("absorb order: foe=%d ally=%d, want 0/100", foe.mshield, ally.mshield)
	}
	if u.shield != 500 {
		t.Fatalf("converted physical shield=%d, want 500", u.shield)
	}
}

// TestFeixianShieldConvertRate：飞仙1（a=20,b=40）——预算 200，按 40% 转成物理结界 = 80。
func TestFeixianShieldConvertRate(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, levelID(famFeiXian, 1)), 0)
	foe := newUnit(fu(101, 100, 1000, 1), 1)
	foe.mshield = 300
	b := faqBattle(u, foe)
	b.feixian(u, u.tals[famFeiXian])
	if foe.mshield != 100 || u.shield != 80 {
		t.Fatalf("foe=%d shield=%d, want 100/80", foe.mshield, u.shield)
	}
}

// TestFeixianNeedsEnterAlly：概率 = 入阵护法数 × c%，没有入阵队友时恒为 0，不会触发神之力/遁入。
func TestFeixianNeedsEnterAlly(t *testing.T) {
	for seed := uint64(0); seed < 100; seed++ {
		u := newUnit(fu(1, 100, 1000, 1, levelID(famFeiXian, 4)), 0)
		ally := newUnit(fu(2, 100, 1000, 1), 0)
		bench := newUnit(fu(6, 0, 1000, 1), 0)
		foe := newUnit(fu(101, 100, 1000, 1), 1)
		b := faqBattle(u, ally, bench, foe)
		b.rng = rand.New(rand.NewPCG(seed, seed+1))
		b.feixian(u, u.tals[famFeiXian])
		if b.skills["飞仙"] != 0 || ally.att != 100 || ally.dunru {
			t.Fatalf("seed %d: triggered without an enter ally", seed)
		}
	}
}

// TestFeixianChanceCap：飞仙4 的 25%×入阵数 在 4 名入阵队友时到 100%，每回合必触发并遁入。
func TestFeixianChanceCap(t *testing.T) {
	for seed := uint64(0); seed < 30; seed++ {
		u := newUnit(fu(1, 100, 1000, 1, levelID(famFeiXian, 4)), 0)
		a2 := newUnit(fu(2, 100, 900, 1, levelID(famPanTaoYan, 4)), 0)
		a3 := newUnit(fu(3, 100, 800, 1, levelID(famPanTaoYan, 4)), 0)
		a4 := newUnit(fu(4, 100, 700, 1, levelID(famPanTaoYan, 4)), 0)
		a5 := newUnit(fu(5, 100, 600, 1, levelID(famPanTaoYan, 4)), 0) // 血最低，应被选中
		bench := newUnit(fu(6, 0, 1000, 1), 0)
		foe := newUnit(fu(101, 100, 1000, 1), 1)
		b := faqBattle(u, a2, a3, a4, a5, bench, foe)
		b.rng = rand.New(rand.NewPCG(seed, seed+1))
		b.feixian(u, u.tals[famFeiXian])
		if b.skills["飞仙"] == 0 {
			t.Fatalf("seed %d: 4 enter allies should reach 100%% probability", seed)
		}
		if !bench.front() || a5.front() {
			t.Fatalf("seed %d: lowest-HP enter ally should 遁入 (bench front=%v a5 front=%v)", seed, bench.front(), a5.front())
		}
	}
}

// TestFeixianChanceCapClamp：飞仙1 每名入阵 10%、上限 40%——9 名入阵队友时仍是 40%，不是 90%。
func TestFeixianChanceCapClamp(t *testing.T) {
	const n = 400
	trig := 0
	for seed := uint64(0); seed < n; seed++ {
		u := newUnit(fu(1, 100, 1000, 1, levelID(famFeiXian, 1)), 0)
		units := []*unit{u}
		for i := 0; i < 4; i++ { // 前阵入阵护法
			units = append(units, newUnit(fu(2+i, 100, int32(900-10*i), 1, levelID(famPanTaoYan, 4)), 0))
		}
		for i := 0; i < 5; i++ { // 后阵入阵护法也计入入阵数
			units = append(units, newUnit(fu(6+i, 0, 500, 1, levelID(famPanTaoYan, 4)), 0))
		}
		foe := newUnit(fu(101, 100, 1000, 1), 1)
		b := faqBattle(append(units, foe)...)
		b.rng = rand.New(rand.NewPCG(seed, seed+1))
		b.feixian(u, u.tals[famFeiXian])
		if b.skills["飞仙"] != 0 {
			trig++
		}
	}
	if trig < 120 || trig > 200 { // 期望 400×40% = 160
		t.Fatalf("triggered %d/%d, want ~160 (40%% cap)", trig, n)
	}
}

// TestFeixianNoBenchGanlu：没有可用后阵时不遁入——神之力/神之护保留，并按激发概率触发甘露1。
func TestFeixianNoBenchGanlu(t *testing.T) {
	const n = 400
	trig, revived := 0, 0
	for seed := uint64(0); seed < n; seed++ {
		u := newUnit(tjf(fu(1, 100, 1000, 1, levelID(famFeiXian, 4)), 5), 0) // 激发 5：y=50%
		low := newUnit(fu(2, 100, 1000, 1, levelID(famPanTaoYan, 4)), 0)     // 入阵护法，前阵血最低
		dead := newUnit(fu(3, 0, 1000, 1), 0)
		dead.hp = 0
		foe := newUnit(fu(101, 100, 1000, 1), 1)
		b := faqBattle(u, low, dead, foe)
		b.rng = rand.New(rand.NewPCG(seed, seed+1))
		b.feixian(u, u.tals[famFeiXian])
		if b.skills["飞仙"] == 0 {
			continue // 25% 概率没过
		}
		trig++
		if !low.front() {
			t.Fatalf("seed %d: 没有后阵可去，不应换位", seed)
		}
		if low.att <= low.baseAtt || low.maxHP <= low.baseHP {
			t.Fatalf("seed %d: 未遁入时应保留神之力/神之护 (att %d/%d maxHP %d/%d)",
				seed, low.att, low.baseAtt, low.maxHP, low.baseHP)
		}
		if dead.alive() {
			revived++
			if dead.has(stNoSoul) == nil {
				t.Fatalf("seed %d: 甘露复活后应带锢魂", seed)
			}
			if dead.hp != dead.maxHP*20/100 {
				t.Fatalf("seed %d: 甘露应复活 20%% 血，got %d/%d", seed, dead.hp, dead.maxHP)
			}
		}
	}
	t.Logf("飞仙触发 %d/%d 次，其中甘露1 复活 %d 次", trig, n, revived)
	if trig == 0 || revived == 0 {
		t.Fatalf("触发 %d 次、甘露 %d 次，分支未被覆盖", trig, revived)
	}
}
