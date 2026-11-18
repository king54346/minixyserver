package battle

import (
	"testing"

	"turnrpg/internal/gamedata"
)

func TestTuomaoReducesAllDamageExceptLifeLoss(t *testing.T) {
	for _, kind := range []string{"physical", "property", "talent", "loss"} {
		t.Run(kind, func(t *testing.T) {
			u := newUnit(fu(1, 100, 1000, 1), 0)
			x := newUnit(fu(101, 10, 1000, 1, 21778), 1)
			x.charge = 3
			b := faqBattle(u, x)
			switch kind {
			case "property":
				b.propertyHurt(u, x, 10006, 100, 0)
			case "loss":
				b.loseHP(u, x, 20210, 100)
			default:
				b.hurt(u, x, 1, 100, kind == "physical")
			}
			want := int64(960)
			if kind == "loss" {
				want = 900
			}
			if x.hp != want {
				t.Fatalf("hp=%d want=%d", x.hp, want)
			}
		})
	}
}

func TestShenweiDamagesNeighborsWithoutOpposite(t *testing.T) {
	u := newUnit(fu(2, 100, 1000, 1, 20934), 0)
	left := newUnit(fu(101, 0, 1000, 1), 1)
	right := newUnit(fu(103, 0, 1000, 1), 1)
	b := faqBattle(u, left, right)
	b.enter(u)
	if left.hp != 920 || right.hp != 920 {
		t.Fatalf("left=%d right=%d", left.hp, right.hp)
	}
}

func TestFirstStrikeHitsHiddenAttackSource(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1), 0)
	x := newUnit(fu(101, 100, 1000, 1, levelID(famXianGong, 10)), 1)
	setParam(x.tals[famXianGong], 0, 100)
	b := faqBattle(u, x)
	b.setStatus(u, stHidden, 0, 1, false, hiddenBuff, slotBuff)
	b.attackAction(u, x, 100, atkMain)
	cmds := mustDecode(t, b.w.b)
	pre, incoming := -1, -1
	for i, c := range cmds {
		if c.ID == CmdSubHP && len(c.Targets) > 0 {
			if c.Targets[0][0] == 1 && c.Targets[0][1] > 0 {
				pre = i
			}
			if c.Targets[0][0] == 101 && c.Targets[0][1] > 0 {
				incoming = i
			}
		}
	}
	if pre < 0 || incoming <= pre {
		t.Fatalf("先攻伤害=%d 原攻击=%d", pre, incoming)
	}
}

func TestDodgeBlocksQuzhu(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, levelID(famQuZhu, 10)), 0)
	x := newUnit(fu(101, 0, 1000, 1), 1)
	bench := newUnit(fu(106, 0, 1000, 1), 1)
	x.lead.dodge = 100
	b := faqBattle(u, x, bench)
	b.attackAction(u, x, 100, atkMain)
	if !x.front() || bench.front() {
		t.Fatal("闪避后被驱逐")
	}
}

func TestGuardianCanDodgeSharedAttack(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1), 0)
	x := newUnit(fuh(101, 0, 100, 1000, 1), 1)
	g := newUnit(fu(102, 0, 1000, 1, levelID(famShouWei, 10), levelID(famShanBi, 10)), 1)
	setParam(g.tals[famShouWei], 0, 100)
	setParam(g.tals[famShanBi], 0, 100)
	b := faqBattle(u, x, g)
	b.attackAction(u, x, 100, atkMain)
	if g.hp != 1000 || x.hp != 99 || !hasCmd(mustDecode(t, b.w.b), CmdSubHP, attackDodge, 102, 0) {
		t.Fatalf("守卫生命=%d 被保护者生命=%d", g.hp, x.hp)
	}
}

func TestProtectedTargetCanStillDodge(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1), 0)
	x := newUnit(fuh(101, 0, 100, 1000, 1), 1)
	g := newUnit(fu(102, 0, 1000, 1, levelID(famShouWei, 10)), 1)
	x.lead.dodge = 100
	setParam(g.tals[famShouWei], 0, 100)
	b := faqBattle(u, x, g)
	b.attackAction(u, x, 100, atkMain)
	if x.hp != 100 || g.hp != 1000 {
		t.Fatalf("原目标闪避后生命=%d 守卫生命=%d", x.hp, g.hp)
	}
}

func TestShenyouDoesNotTriggerDeathSkillEvent(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1), 0)
	x := newUnit(fu(101, 10, 1000, 1, levelID(famShenYou, 10)), 1)
	smith := newUnit(fu(102, 10, 1000, 1, levelID(famZhuJian, 4)), 1)
	asura := newUnit(fu(103, 10, 1000, 1, levelID(famXiuLuoYinShen, 4)), 1)
	setParam(x.tals[famShenYou], 0, 100)
	b := faqBattle(u, x, smith, asura)
	b.hurt(u, x, 1, 2000, true)
	if !x.alive() || smith.att != 10 || asura.xiuluo != 0 {
		t.Fatalf("神佑存活=%v 铸剑攻击=%d 修罗点=%d", x.alive(), smith.att, asura.xiuluo)
	}
}

func TestHengchongTJRetainsAttack(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, 22206), 0)
	x := newUnit(fu(101, 0, 10000, 1), 1)
	u.tals[famHengChongZhiZhuang].tj = 5
	b := faqBattle(u, x)
	b.attackAction(u, x, 100, atkMain)
	b.attackAction(u, x, 100, atkMain)
	if u.att != 121 {
		t.Fatalf("两次加攻后=%d want=121", u.att)
	}
}

func TestDragonRestoresTemporaryPeakOnEntry(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, 22218, 20120), 0)
	bench := newUnit(fu(6, 0, 1000, 1), 0)
	x := newUnit(fu(101, 0, 10000, 1), 1)
	b := faqBattle(u, bench, x)
	b.attackAction(u, x, 100, atkMain)
	if u.att != 100 || u.peakAtt != 150 {
		t.Fatalf("攻击后=%d 最高=%d", u.att, u.peakAtt)
	}
	b.swap(u, bench)
	b.swap(u, bench)
	if u.att != 150 {
		t.Fatalf("入阵后=%d", u.att)
	}
}

func TestAOERunzeBeforeNextTargetAndIce(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1), 0)
	first := newUnit(fu(101, 100, 1000, 1, 21300), 1)
	second := newUnit(fu(102, 0, 1000, 1), 1)
	second.hp = 1
	b := faqBattle(u, first, second)
	m := &gamedata.MagicInfo{Kind: 2, Target: 7, Star: 5, Hurt: [2]float64{1000, 0}, Buff: 5001, BuffRate: [2]float64{100, 0}}
	b.cast(u, &castable{id: 15100, lv: 1, left: 1, MagicInfo: m})
	if !second.alive() || first.has(stStiff) == nil {
		t.Fatalf("后续目标生命=%d 首目标冰封=%v", second.hp, first.has(stStiff) != nil)
	}
	heal, hit := -1, -1
	for i, c := range mustDecode(t, b.w.b) {
		if len(c.Targets) == 0 || c.Targets[0][0] != 102 {
			continue
		}
		if c.ID == CmdAddHP && c.Magic == 21300 && heal < 0 {
			heal = i
		}
		if c.ID == CmdSubHP && c.Magic == 15100 {
			hit = i
		}
	}
	if heal < 0 || hit <= heal {
		t.Fatalf("治疗命令=%d 后续伤害命令=%d", heal, hit)
	}
}

func TestFeixianSharesWholeBonusBeforeRetreat(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1, 22034), 0)
	dragon := newUnit(fu(2, 100, 1000, 1, 22218), 0)
	bench := newUnit(fu(6, 100, 1000, 1), 0)
	b := faqBattle(u, dragon, bench)
	k := u.tals[famFeiXian]
	k.tj = 1
	setParam(k, 0, 0)
	setParam(k, 2, 100)
	b.changeAtt(dragon, 1, 100)
	b.changeMaxHP(dragon, 1, 1000)
	dragon.shield, dragon.mshield = 100, 200
	b.feixian(u, k)
	// 神之力按基础攻击另加21，神之护另加420，完整增益为121和1420。
	if dragon.front() || u.att != 116 || u.maxHP != 1191 || u.shield != 13 || u.mshield != 27 {
		t.Fatalf("退阵=%v 前阵攻击=%d 上限=%d 结界=%d/%d", !dragon.front(), u.att, u.maxHP, u.shield, u.mshield)
	}
}

func TestTalangCannotKillImmuneThroughLifesteal(t *testing.T) {
	u := newUnit(fu(1, 1000, 1000, 1, levelID(famTaLang, 10), 20270, 20289), 0)
	x := newUnit(fu(101, 0, 100, 1, 21291), 1)
	u.hp = 1
	u.tals[famXiXue].tj = 5
	setParam(u.tals[famTaLang], 0, 100)
	setParam(u.tals[famLianJi], 0, 100)
	b := faqBattle(u, x)
	b.setStatus(x, stImmune, 0, 1, false, 5321, slotBuff)
	b.attackAction(u, x, 100, atkMain)
	if x.hp != 100 || x.maxHP != 100 || x.has(stImmune) == nil || x.has(stStiff) == nil || u.hp != 1 {
		t.Fatalf("目标=%d/%d 无敌=%v 僵化=%v 吸血者=%d", x.hp, x.maxHP, x.has(stImmune) != nil, x.has(stStiff) != nil, u.hp)
	}
}
