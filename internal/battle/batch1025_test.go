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
	one := u.atkBase() * int64(u.tals[famYuBing].param(1)) / 100 // 主条目一个的加值
	b.beforeAttack(u, x)
	if len(u.talAll(famYuBing)) != 2 || u.fired[famYuBing] <= one {
		t.Fatalf("yubing fired %d, single %d", u.fired[famYuBing], one)
	}
}

// TestDunrangLeftToRight：遁让与后阵从左到右第一个存活护法交换；雷影庇护代承时跳过雷震子本人。
func TestDunrangLeftToRight(t *testing.T) {
	u := newUnit(fu(1, 10, 1000, 1, levelID(famDunRang, 10)), 0)
	lei := newUnit(fu(6, 10, 100000, 1, levelID(famLeiYingBiHu, 4)), 0)
	b7 := newUnit(fu(7, 10, 1000, 1), 0)
	b8 := newUnit(fu(8, 10, 1000, 1), 0)
	src := newUnit(fu(101, 10, 1000, 1), 1)
	b := faqBattle(u, b8, lei, b7, src)
	if to := b.dunrangTo(0, nil); to != lei {
		t.Fatalf("dunrang picked slot %d, want 6", to.slot())
	}
	if !b.leiying(src, u, 5000, true) || u.front() || !b7.front() {
		t.Fatalf("leiying swap: u front %v, slot-7 front %v", u.front(), b7.front())
	}
}

// TestRampageOnRevive：带暴走神通的护法被复活后当场暴走，本回合无敌（到回合结束清除），不等下回合。
func TestRampageOnRevive(t *testing.T) {
	u := newUnit(raced(fu(1, 100, 1000, 1, levelID(famBuQu, 4)), raceBeast), 0)
	b := faqBattle(u, newUnit(fu(101, 10, 1000, 1), 1))
	u.hp = 0
	b.relive(u, 1, 500)
	if !u.raged || !b.invincible(u) {
		t.Fatalf("raged %v invincible %v", u.raged, b.invincible(u))
	}
	b.tickStatus(u, false) // 回合结束
	if b.invincible(u) {
		t.Fatal("immune should end with the round")
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
