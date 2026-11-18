package battle

import "testing"

// TestLeiyingRespectsQijia：气甲把物理伤害封顶到不致命时，雷影不应触发（2026-11-16 调整）。
func TestLeiyingRespectsQijia(t *testing.T) {
	foe := newUnit(fu(101, 0, 100000, 1), 1)
	lei := newUnit(fu(1, 0, 100000, 1, levelID(famLeiYingBiHu, 4)), 0)
	bench := newUnit(fu(7, 0, 1000, 1), 0)
	// 气甲10：物理伤害封顶 4% 上限（100×4% = 4），500 点伤害不再致命。
	tgt := newUnit(fu(2, 0, 100, 1, levelID(famDunRang, 10), levelID(famQiJia, 10)), 0)
	b := faqBattle(lei, tgt, bench, foe)
	b.hurt(foe, tgt, 1, 500, true)
	if !tgt.alive() || tgt.hp != 96 {
		t.Fatalf("qijia target: alive=%v hp=%d want 96", tgt.alive(), tgt.hp)
	}
	if lei.leiying != 0 {
		t.Fatalf("雷影 should not trigger when 气甲 keeps the hit non-lethal, count=%d", lei.leiying)
	}
}

// TestLeiyingDunrangTjReflect：雷影触发的遁让同样结算遁让激发的返伤（2026-11-16 调整）。
func TestLeiyingDunrangTjReflect(t *testing.T) {
	foe := newUnit(fu(101, 0, 1000, 1), 1)
	lei := newUnit(fu(1, 0, 100000, 1, levelID(famLeiYingBiHu, 4)), 0)
	tgt := newUnit(tjf(fu(2, 0, 100, 1, levelID(famDunRang, 10)), 5), 0) // 100 血，500 伤害致命；遁让激发 5 级 → x=30%
	bench := newUnit(fu(7, 0, 1000, 1), 0)
	b := faqBattle(lei, tgt, bench, foe)
	if !b.leiying(foe, tgt, 500, true) {
		t.Fatal("lightning protection did not activate")
	}
	if got := 1000 - foe.hp; got != 150 {
		t.Fatalf("遁让激发返伤 = %d, want 150 (500×30%%)", got)
	}
}

// TestLeiyingCarriesCappedDamage：气甲封顶后，雷震子代承的是封顶后的数值，不是原始伤害（2026-11-16 调整）。
func TestLeiyingCarriesCappedDamage(t *testing.T) {
	foe := newUnit(fu(101, 0, 100000, 1), 1)
	lei := newUnit(fu(1, 0, 100000, 1, levelID(famLeiYingBiHu, 4)), 0)
	// 上限 1000、气甲10 封顶 4%（=40），当前 30 血：5000 封顶后是 40，仍然致命，但只该代承 40。
	tgt := newUnit(fu(2, 0, 1000, 1, levelID(famDunRang, 10), levelID(famQiJia, 10)), 0)
	tgt.hp = 30
	bench := newUnit(fu(7, 0, 1000, 1), 0)
	b := faqBattle(lei, tgt, bench, foe)
	b.hurt(foe, tgt, 1, 5000, true)
	if lei.leiying != 1 {
		t.Fatalf("protection did not trigger, count=%d", lei.leiying)
	}
	if got := 100000 - lei.hp; got != 40 {
		t.Fatalf("carrier took %d, want 40 (气甲封顶值)", got)
	}
	if !tgt.alive() || tgt.hp != 30 {
		t.Fatalf("target: alive=%v hp=%d", tgt.alive(), tgt.hp)
	}
}

// TestLeiyingHPBonusSurvivesRevive：雷影天匠的生命上限加成在复活重置后保留（2026-11-16 调整）。
func TestLeiyingHPBonusSurvivesRevive(t *testing.T) {
	lei := newUnit(tjf(fu(1, 0, 1000, 1, levelID(famLeiYingBiHu, 4)), 5), 0)
	b := faqBattle(lei)
	b.battleStart()
	if lei.maxHP != 2000 || lei.hp != 2000 {
		t.Fatalf("battleStart: maxHP=%d hp=%d want 2000", lei.maxHP, lei.hp)
	}
	lei.hp = 0
	b.reliveInitial(lei, levelID(famLeiYingBiHu, 4), 100)
	if lei.maxHP != 2000 || lei.hp != 2000 {
		t.Fatalf("after revive: maxHP=%d hp=%d want 2000", lei.maxHP, lei.hp)
	}
}
