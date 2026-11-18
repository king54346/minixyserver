package battle

import (
	"strconv"
	"testing"
	"turnrpg/internal/gamedata"
)

func TestInnateMultiHitSpellsResolveAsDao(t *testing.T) {
	for _, tc := range []struct {
		petID, magicID int32
		hits           int64
	}{
		{23020, 15415, 3},
		{24030, 15416, 3},
		{25036, 15417, 3},
		{43021, 15418, 6},
		{44031, 15419, 6},
		{45036, 15420, 6},
	} {
		t.Run(strconv.Itoa(int(tc.magicID)), func(t *testing.T) {
			pet := gamedata.Pets[tc.petID]
			m := gamedata.Magics[tc.magicID]
			if pet == nil || pet.Magic != tc.magicID || m == nil || m.Kind != 0 {
				t.Fatal("expected this guardian's innate spell to be Dao")
			}
			f := fu(1, 100, 10000, 1)
			f.Magic, f.MagicLv = pet.Magic, 5
			caster := newUnit(f, 0)
			caster.att = 200
			xianYi := levelID(famXianYi, 10)
			huDun := levelID(famHuDun, 10)
			foe := newUnit(fu(101, 0, 10000, 1, xianYi, huDun), 1)
			setParam(foe.tals[famXianYi], 0, 100)
			setParam(foe.tals[famXianYi], 1, 50)
			foe.shield, foe.mshield = 10000, 10
			b := faqBattle(caster, foe)
			b.round, b.zy[0] = m.CondRound[0], m.CondZY
			b.castMagic(caster)

			perHit := caster.att * int64(float64(m.SP[1])+float64(m.SP[2])/10*5) / 100
			wantHP := int64(10010) - tc.hits*(perHit-perHit/2)
			if foe.hp != wantHP || foe.shield != 10000 || foe.mshield != 0 {
				t.Fatalf("HP=%d want %d, physical shield=%d, spell shield=%d", foe.hp, wantHP, foe.shield, foe.mshield)
			}
			cmds := mustDecode(t, b.w.b)
			if !hasCmd(cmds, CmdActDo, int64(xianYi), 101, -1) ||
				hasCmd(cmds, CmdActDo, int64(huDun), 101, -1) ||
				!hasCmd(cmds, CmdSubHP, int64(tc.magicID), 101, perHit-perHit/2) {
				t.Fatal("wrong reactions or damage attribution in battle record")
			}
		})
	}
}

func TestHigherZhanhuReplacesLower(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1), 0)
	b := faqBattle(u)
	b.zhanhu(u, 5262)
	b.zhanhu(u, 5263)
	b.zhanhu(u, 5262)
	if u.att != 130 || u.has(stZhanhu).buff != 5263 {
		t.Fatalf("expected only 战呼3, attack=%d status=%+v", u.att, u.has(stZhanhu))
	}
	b.tickStatus(u, true)
	if u.att != 100 || u.has(stZhanhu) != nil {
		t.Fatalf("expired 战呼 did not restore attack: %d", u.att)
	}
}

func TestFenJiDoesNotHitOnMiss(t *testing.T) {
	id := levelID(famFenJi, 4)
	u := newUnit(fu(1, 100, 1000, 1, id), 0)
	target := newUnit(fu(101, 0, 1000, 1), 1)
	second := newUnit(fu(102, 0, 1000, 9), 1)
	target.lead.dodge = 100
	b := faqBattle(u, target, second)
	b.attackAction(u, target, 100, atkMain)
	if second.hp != 1000 || hasCmd(mustDecode(t, b.w.b), CmdSubHP, int64(id), 102, -1) {
		t.Fatal("missed main hit still dealt split damage")
	}
}

func TestMultiHitSpellUsesDaoAndCurrentAttack(t *testing.T) {
	m := gamedata.Magics[12505]
	caster := newUnit(fu(1, 100, 10000, 1), 0)
	caster.att = 200
	foe := newUnit(fu(101, 0, 10000, 1, levelID(famXianYi, 10), levelID(famHuDun, 10)), 1)
	setParam(foe.tals[famXianYi], 0, 100)
	setParam(foe.tals[famXianYi], 1, 50)
	foe.shield = 10000
	b := faqBattle(caster, foe)
	b.cast(caster, &castable{id: 12505, lv: 5, left: 1, MagicInfo: m})
	perHit := caster.att * int64(float64(m.SP[1])+float64(m.SP[2])/10*5) / 100
	expected := int64(10000) - 3*(perHit-perHit/2)
	if foe.hp != expected || foe.shield != 10000 {
		t.Fatalf("dao multi-hit HP=%d want %d, physical shield=%d", foe.hp, expected, foe.shield)
	}
	cmds := mustDecode(t, b.w.b)
	if !hasCmd(cmds, CmdActDo, int64(levelID(famXianYi, 10)), 101, -1) || hasCmd(cmds, CmdActDo, int64(levelID(famHuDun, 10)), 101, -1) {
		t.Fatal("wrong spell reaction")
	}
	caster.Grade = 100
	if magicPower(caster, m.Star) != caster.att {
		t.Fatal("normal spell should use current attack below its cap")
	}
}

func TestZhuFuQiJiaEachTarget(t *testing.T) {
	id := int32(21794)
	qi := levelID(famQiJia, 10)
	caster := newUnit(fu(1, 1000, 3000, 1, id), 0)
	caster.hp = 0
	first := newUnit(fu(101, 0, 1000, 1, qi), 1)
	second := newUnit(fu(102, 0, 2000, 1, qi), 1)
	for _, u := range []*unit{first, second} {
		setParam(u.tals[famQiJia], 1, 10)
	}
	second.shield = 50
	b := faqBattle(caster, first, second)
	b.deathMore(caster)
	cmds := mustDecode(t, b.w.b)
	if first.hp != 900 || second.hp != 1850 || second.shield != 0 ||
		!hasCmd(cmds, CmdActDo, int64(qi), 101, -1) || !hasCmd(cmds, CmdActDo, int64(qi), 102, -1) {
		t.Fatalf("QiJia must resolve and display individually: %+v", cmds)
	}
}

func TestZhuFuPhysicalResistanceEachTarget(t *testing.T) {
	const id = 21794
	caster := newUnit(fu(1, 100, 500, 1, id), 0)
	caster.hp = 0
	first := newUnit(fu(101, 0, 1000, 1), 1)
	second := newUnit(fu(102, 0, 1000, 1), 1)
	third := newUnit(fu(103, 0, 1000, 1), 1)
	first.lead.nres = 10
	second.lead.nres = 50
	third.lead.nres = 25
	third.shield = 25
	b := faqBattle(caster, first, second, third)
	b.deathMore(caster)
	cmds := mustDecode(t, b.w.b)
	if len(cmds) == 0 || cmds[0].ID != CmdActDo || cmds[0].Magic != id ||
		len(cmds[0].Targets) != 3 || cmds[0].Targets[0][0] != 101 ||
		cmds[0].Targets[1][0] != 102 || cmds[0].Targets[2][0] != 103 {
		t.Fatalf("expected one cast animation targeting all three defenders: %+v", cmds)
	}
	if !hasCmd(cmds, CmdSubHP, id, 101, 270) ||
		!hasCmd(cmds, CmdSubHP, id, 102, 150) ||
		!hasCmd(cmds, CmdSubHP, id, 103, 200) {
		t.Fatalf("each share should use its own physical resistance and shield: %+v", cmds)
	}
}

func TestTianWeiDaoTriggersRunze(t *testing.T) {
	u := newUnit(fu(1, 1000, 10000, 1, levelID(famTianWeiJiangLin, 4)), 0)
	foe := newUnit(fu(101, 100, 10000, 1, levelID(famRunZe, 10)), 1)
	setParam(foe.tals[famRunZe], 0, 100)
	b := faqBattle(u, foe)
	b.selfAfterAttack(u, foe, 0)
	cmds := mustDecode(t, b.w.b)
	if !hasCmd(cmds, CmdSubHP, 22202, 101, 400) || !hasCmd(cmds, CmdActDo, int64(foe.tals[famRunZe].id), -1, -1) {
		t.Fatalf("dao TianWei should damage and trigger Runze: %+v", cmds)
	}
}
