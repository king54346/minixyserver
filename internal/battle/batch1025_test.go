package battle

import (
	"math/rand/v2"
	"testing"
)

// TestTianGangPrefersVulnerable：天罡剑气优先打不处于无敌 / 暴走的敌人（无敌的燃灯不再白挨）。
func TestTianGangPrefersVulnerable(t *testing.T) {
	for seed := uint64(0); seed < 20; seed++ {
		u := newUnit(fu(1, 10, 1000, 1, levelID(famTianGangJianQi, 4)), 0)
		god := newUnit(raced(fu(2, 0, 1000, 1), raceGod), 0)
		x := newUnit(fu(101, 1, 100000, 1), 1)
		y := newUnit(fu(102, 1, 100000, 1), 1)
		b := faqBattle(u, god, x, y)
		b.rng = rand.New(rand.NewPCG(seed, seed))
		b.setStatus(x, stImmune, 0, 1, false, 0, 0)
		b.enter(u)
		if y.maxHP-y.hp != 2*god.maxHP*32/100 || god.maxHP-god.hp != god.maxHP*32/100 {
			t.Fatalf("seed %d: vulnerable foe took %d, immune took %d", seed, y.maxHP-y.hp, x.maxHP-x.hp)
		}
	}
}

// TestFacing3：对面三位 = 对位及其左右，位置锁定（1 号位只打 1、2 号，对位空着也不改选）。
func TestFacing3(t *testing.T) {
	u := newUnit(fu(1, 10, 1000, 1), 0)
	var foes []*unit
	for i := 1; i <= 5; i++ {
		foes = append(foes, newUnit(fu(100+i, 10, 1000, 1), 1))
	}
	b := faqBattle(append([]*unit{u}, foes...)...)
	if got := b.facing3(u); len(got) != 2 || got[0] != foes[0] || got[1] != foes[1] {
		t.Fatalf("slot 1 facing %v", got)
	}
	foes[0].hp = 0
	if got := b.facing3(u); len(got) != 1 || got[0] != foes[1] {
		t.Fatalf("slot 1 facing with opposite dead %v", got)
	}
	u.Pos = 3
	if got := b.facing3(u); len(got) != 3 || got[0] != foes[2] {
		t.Fatalf("slot 3 facing %v", got)
	}
}

// TestHanbingStiffLasts：攻击蛟魔王被寒冰甲僵化——这次行动结束不清，下次行动后才清。
func TestHanbingStiffLasts(t *testing.T) {
	u := newUnit(fu(1, 100, 100000, 1), 0)
	x := newUnit(fu(101, 10, 100000, 1, levelID(famHanBingJia, 4)), 1)
	setParam(x.tals[famHanBingJia], 1, 100)
	b := faqBattle(u, x)
	b.act(u)
	if u.has(stStiff) == nil {
		t.Fatal("stiff cleared at the end of the action that got it")
	}
	setParam(x.tals[famHanBingJia], 1, 0) // 下一次不再僵化
	b.act(u)
	if u.has(stStiff) != nil {
		t.Fatal("stiff not cleared after the next action")
	}
}

// TestStiffBlocksShenyou：僵化中阵亡，神佑等死亡神通发动不了。
func TestStiffBlocksShenyou(t *testing.T) {
	src := newUnit(fu(101, 100, 1000, 1), 1)
	u := newUnit(fu(1, 100, 1000, 1, levelID(famShenYou, 10)), 0)
	setParam(u.tals[famShenYou], 0, 100)
	b := faqBattle(src, u)
	b.addStatus(u, stStiff, 0, 1, true, 5001, slotDebuff)
	b.hurt(src, u, 1, 5000, true)
	if u.alive() {
		t.Fatal("shenyou revived a stiff unit")
	}
}

// TestQuzhuBinds：驱逐换上来的护法被捆缚，下次行动跳过。
func TestQuzhuBinds(t *testing.T) {
	x := newUnit(fu(101, 10, 1000, 1), 1)
	y := newUnit(fu(106, 10, 1000, 1), 1)
	b := faqBattle(newUnit(fu(1, 10, 1000, 1), 0), x, y)
	b.quzhu(x, y)
	if !y.front() || y.has(stBind) == nil {
		t.Fatalf("swapped-in front %v bound %v", y.front(), y.has(stBind) != nil)
	}
}

// TestQianKunXiuHideAlone：乾坤袖的隐身单独判定——捆缚没中也能隐身。
func TestQianKunXiuHideAlone(t *testing.T) {
	u := newUnit(fu(1, 10, 1000, 1, levelID(famQianKunXiu, 4)), 0)
	setParam(u.tals[famQianKunXiu], 0, 0)
	setParam(u.tals[famQianKunXiu], 1, 100)
	var foes []*unit
	for i := 1; i <= 4; i++ {
		foes = append(foes, newUnit(fu(100+i, int32(100*i), 1000, 1), 1))
	}
	b := faqBattle(append([]*unit{u}, foes...)...)
	b.enter(u)
	if foes[0].has(stHidden) == nil || foes[1].has(stHidden) == nil || foes[3].has(stBind) != nil {
		t.Fatalf("hidden %v %v, bound %v", foes[0].has(stHidden) != nil, foes[1].has(stHidden) != nil, foes[3].has(stBind) != nil)
	}
}

// TestDoubleYuBing：自带 + 套装两个驭兵各自判定（同突袭）。
func TestDoubleYuBing(t *testing.T) {
	u := newUnit(fu(1, 1000, 100000, 1, levelID(famYuBing, 5), levelID(famYuBing, 3)), 0)
	x := newUnit(fu(101, 10, 1000000, 1), 1)
	for _, k := range u.talAll(famYuBing) {
		setParam(k, 0, 100)
	}
	b := faqBattle(u, x)
	one := u.baseAtt * int64(u.tals[famYuBing].param(1)) / 100 // 主条目一个的加值（初始攻击基准）
	b.beforeAttack(u, x)
	if len(u.talAll(famYuBing)) != 2 || u.fired[famYuBing] <= one {
		t.Fatalf("yubing fired %d, single %d", u.fired[famYuBing], one)
	}
}

// 同档固定按站位顺序，雷影庇护共用相同规则。
func TestDunrangRandom(t *testing.T) {
	u := newUnit(fu(1, 10, 1000, 1, levelID(famDunRang, 10)), 0)
	lei := newUnit(fu(6, 10, 100000, 1, levelID(famLeiYingBiHu, 4)), 0)
	b7 := newUnit(fu(7, 10, 1000, 1), 0)
	b8 := newUnit(fu(8, 10, 1000, 1), 0)
	src := newUnit(fu(101, 10, 1000, 1), 1)
	b := faqBattle(u, b8, lei, b7, src)
	seen := map[*unit]bool{}
	for i := 0; i < 60; i++ {
		if to := b.dunrangTo(0); to == nil || to == u {
			t.Fatalf("dunrang picked %v", to)
		} else {
			seen[to] = true
		}
	}
	if len(seen) != 2 || seen[lei] {
		t.Fatalf("雷震子 should stay back while other bench units exist: %v", seen)
	}
	// 只有雷震子一名可用后阵时才选他。
	b7.hp, b8.hp = 0, 0
	if b.dunrangTo(0) != lei {
		t.Fatal("雷震子 must be picked when he is the only bench unit")
	}
	// 雷影庇护代承后，只有一名后阵可用时就换上该护法。
	for i := 0; i < 40; i++ {
		u2 := newUnit(fu(1, 10, 1000, 1, levelID(famDunRang, 10)), 0)
		lei2 := newUnit(fu(6, 10, 100000, 1, levelID(famLeiYingBiHu, 4)), 0)
		src2 := newUnit(fu(101, 10, 1000, 1), 1)
		b2 := faqBattle(u2, lei2, src2)
		if !b2.leiying(src2, u2, 5000, true) || u2.front() || !lei2.front() || lei2.leiying != 1 {
			t.Fatalf("leiying swap: u front %v lei front %v", u2.front(), lei2.front())
		}
	}
}

// TestRampageOnRevive：带暴走神通的护法被复活后当场暴走，本回合无敌（到回合结束清除），不等下回合。
func TestRampageOnRevive(t *testing.T) {
	for _, pos := range []int{1, 6} {
		u := newUnit(raced(fu(pos, 100, 1000, 1, levelID(famBuQu, 4)), raceBeast), 0)
		b := faqBattle(u, newUnit(fu(101, 10, 1000, 1), 1))
		for attempt := range 2 {
			u.hp = 0
			b.w.b = nil
			b.relive(u, 1, u.maxHP)
			cmds := mustDecode(t, b.w.b)
			if !u.raged || !b.invincible(u) || len(cmds) < 2 || cmds[0].ID != CmdRelive ||
				cmds[1].ID != CmdActDo || cmds[1].Magic != int64(levelID(famBuQu, 4)) {
				t.Fatalf("position %d revival %d: expected immediate rampage, got %+v", pos, attempt+1, cmds)
			}
			b.tickStatus(u, false)
			if b.invincible(u) {
				t.Fatal("immune should end with the round")
			}
		}
	}
}

// TestQuzhuMidAttackKeepsAtt：攻击中带着突袭的临时加攻被驱逐到后阵，再入阵攻击仍是初始值，不被多扣一次临时加攻
// （用户报：3000 攻击的斗战胜佛被驱逐再入阵变 600 多）。
func TestQuzhuMidAttackKeepsAtt(t *testing.T) {
	u := newUnit(fu(4, 3000, 100000, 1, levelID(famTuXi, 10)), 0)
	bn := newUnit(fu(6, 10, 1000, 1), 0)
	x := newUnit(fu(104, 10, 1000000, 1), 1)
	b := faqBattle(u, bn, x)
	temp0, fired0, epoch0 := u.tempAtt, u.fired, u.tempEpoch
	b.tempUp(u, u.tals[famTuXi], 1500) // 攻击中：突袭 +1500
	b.quzhu(u, bn)                     // 攻击中被驱逐（如对方反击带驱逐）
	b.endAction(u, temp0, fired0, epoch0, atkMain)
	b.swap(bn, u) // 再入阵
	if u.att != 3000 {
		t.Fatalf("att after re-enter %d, want 3000", u.att)
	}
}

// TestChongFengTempCleared：冲锋（攻击后清除）的加攻在行动结束时扣回；行动中被驱逐到后阵的，再入阵仍是初始攻击。
func TestChongFengTempCleared(t *testing.T) {
	u := newUnit(fu(1, 3000, 100000, 1, levelID(famChongFeng, 10)), 0)
	setParam(u.tals[famChongFeng], 0, 100)
	x := newUnit(fu(101, 10, 10000000, 1), 1)
	b := faqBattle(u, x)
	b.act(u)
	if u.att != 3000 || u.tempAtt != 0 {
		t.Fatalf("after act att %d temp %d, want 3000 / 0", u.att, u.tempAtt)
	}

	v := newUnit(fu(1, 3000, 100000, 1, levelID(famChongFeng, 10)), 0)
	setParam(v.tals[famChongFeng], 0, 100)
	bn := newUnit(fu(6, 10, 1000, 1), 0)
	y := newUnit(fu(101, 10, 10000000, 1, levelID(famQuZhu, 10)), 1)
	b2 := faqBattle(v, bn, y)
	temp0, fired0, epoch0 := v.tempAtt, v.fired, v.tempEpoch
	b2.tempUp(v, v.tals[famChongFeng], 600) // 出手前冲锋加攻
	b2.quzhu(v, bn)                         // 行动中被驱逐
	b2.endAction(v, temp0, fired0, epoch0, atkFollow)
	b2.swap(bn, v)
	if v.att != 3000 {
		t.Fatalf("re-enter att %d, want 3000", v.att)
	}
}

// TestCangJianYuHua：藏剑于花（回合开始 & 天将受击）只把仙剑分配给前阵己方护法；
// 携带藏剑于花的护法攻击后，前阵所有护法携带的仙剑按各自持剑者 50% 攻击力造成 1 次伤害，且其他前阵护法的仙剑不消散清零。
func TestCangJianYuHua(t *testing.T) {
	u := newUnit(tjf(fu(1, 1000, 100000, 1, levelID(famCangJianYuHua, 4)), 5), 0)
	ally := newUnit(fu(2, 2000, 100000, 1), 0)
	bench := newUnit(fu(6, 3000, 100000, 1), 0)
	foe := newUnit(fu(101, 100, 1000000, 1), 1)
	b := faqBattle(u, ally, bench, foe)
	for i := 0; i < 30; i++ {
		u.swords, ally.swords, bench.swords = 0, 0, 0
		b.roundStart(u)
		b.onHit(foe, u, 100, atkMain)
		if bench.swords != 0 {
			t.Fatalf("bench unit got %d swords from 藏剑于花", bench.swords)
		}
	}

	u.swords, u.swordKind, u.swordSet = 1, 0, true
	ally.swords, ally.swordKind, ally.swordSet = 2, 0, true // 前阵队友带 2 柄道剑（每柄 2000/2 = 1000 伤害）
	bench.swords, bench.swordKind, bench.swordSet = 3, 0, true
	hp0 := foe.hp
	b.attackAction(u, foe, 100, atkMain)
	// u 普攻 1000 + u 自身 1 柄剑 500 + 前阵队友 ally 的 2 柄仙剑 2×1000 = 3500（后阵 bench 的剑不出手），且两人仙剑均不消失
	if got := hp0 - foe.hp; got != 1000+500+2*1000 {
		t.Fatalf("foe took %d, want 3500", got)
	}
	if u.swords != 1 || ally.swords != 2 {
		t.Fatalf("swords u=%d ally=%d, want 1 and 2 (must not disappear after attack)", u.swords, ally.swords)
	}
}
