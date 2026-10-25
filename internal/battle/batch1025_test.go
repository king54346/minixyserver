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
