package battle

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func qjUnit(pos int, att, hp int32, side int, tals ...int32) *unit {
	return newUnit(fu(pos, att, hp, 5, tals...), side)
}

// TestQjBossHard：坚硬只扣 1 点（生命流失照扣）；种族易伤：来自该族的伤害 ×200%。
func TestQjBossHard(t *testing.T) {
	src := qjUnit(1, 100, 1000, 0)
	src.Race = raceHuman
	hard := qjUnit(101, 10, 1000, 1, qjJianYing)
	vuln := qjUnit(102, 10, 1000, 1, 130167)
	b := faqBattle(src, hard, vuln)
	b.qjBossStart()
	b.hurt(src, hard, 1, 300, true)
	b.hurt(src, vuln, 1, 300, true)
	if hard.hp != 999 || vuln.hp != 400 {
		t.Fatalf("坚硬 %d（应 999），易伤 %d（应 400）", hard.hp, vuln.hp)
	}
	b.loseHP(src, hard, 1, 100)
	if hard.hp != 899 {
		t.Fatalf("生命流失不受坚硬影响: %d", hard.hp)
	}
}

// TestQjBossShenliJibai：小怪「给予神力」死后满血复活并给对方神之力；首领「击败」死亡时小怪全灭且不再复活。
func TestQjBossShenliJibai(t *testing.T) {
	me := qjUnit(1, 100, 1000, 0)
	minion := qjUnit(101, 10, 500, 1, qjGeiShenLi)
	boss := qjUnit(103, 10, 500, 1, qjJiBai)
	b := faqBattle(me, minion, boss)
	b.actor = me
	b.hurt(me, minion, 1, 500, true)
	if !minion.alive() || minion.hp != 500 || me.att <= 100 {
		t.Fatalf("给予神力：minion hp %d, me att %d", minion.hp, me.att)
	}
	b.hurt(me, boss, 1, 500, true)
	if minion.alive() {
		t.Fatal("击败：首领死后小怪应全灭且不再复活")
	}
}

// TestQjBossSplitTransform：孙悟空分裂（PET_CLONE 到前阵空位，战报回放通过）；蝴蝶死后变身火人。
func TestQjBossSplitTransform(t *testing.T) {
	t1 := []*Fighter{fu(1, 3000, 100000, 9), fu(2, 3000, 100000, 9)}
	t2 := []*Fighter{fu(103, 10, 20000, 1, qjFenLie1, qjFenLie1+1, qjFenLie3)}
	r1, r2 := CloneTeam(t1), CloneTeam(t2)
	res := SimulateWith(t1, t2, Options{}, rand.New(rand.NewPCG(1, 1)))
	cmds, _ := DecodeRecord(res.Record)
	clones := 0
	for _, c := range cmds {
		if c.ID == CmdPetClone {
			clones++
		}
	}
	if clones != 3 {
		t.Fatalf("分裂 %d 次，应 3 次", clones)
	}
	if _, err := Replay(r1, r2, res.Record); err != nil {
		t.Fatal(err)
	}

	huoren := &Fighter{Name: "火人", Icon: 55022, Race: raceGhost, Speed: 8, Att: 900, HP: 5000, MaxHP: 5000, Talents: []int32{130134}}
	me := qjUnit(1, 100, 1000, 0)
	boss := qjUnit(103, 10, 500, 1, qjBianHuoRen)
	b := faqBattle(me, boss)
	b.qjNPCs = map[int32]*Fighter{36225: huoren}
	b.hurt(me, boss, 1, 500, true)
	if !boss.alive() || boss.maxHP != 5000 || boss.att != 900 || boss.Icon != 55022 {
		t.Fatalf("变身火人：alive %v maxHP %d att %d icon %d", boss.alive(), boss.maxHP, boss.att, boss.Icon)
	}
}

// TestQjBossZibaoRevive：自爆（第 a 回合开始，伤害后自己阵亡）；救活队友（每 a 回合结束）。
func TestQjBossZibaoRevive(t *testing.T) {
	me := qjUnit(1, 100, 10000, 0)
	bomber := qjUnit(101, 100, 1000, 1, qjZiBao) // 第 10 回合自爆，100% 攻击
	b := faqBattle(me, bomber)
	b.round = 10
	b.qjRoundStart(bomber)
	if bomber.alive() || me.hp != 9900 {
		t.Fatalf("自爆：bomber alive %v, me hp %d", bomber.alive(), me.hp)
	}
	mate := qjUnit(102, 10, 1000, 1)
	boss := qjUnit(103, 10, 1000, 1, qjJiuHuo) // 每 3 回合结束复活，100%
	b2 := faqBattle(me, mate, boss)
	b2.hurt(me, mate, 1, 1000, true)
	b2.round = 3
	b2.qjRoundEnd(boss)
	if !mate.alive() || mate.hp != 1000 {
		t.Fatalf("救活队友：mate hp %d", mate.hp)
	}
}

// TestQjBossOverflow：无敌首领只能靠「溢出」掉血：给予神通让我方带溢出，溢出的治疗令首领流失生命。
func TestQjBossOverflow(t *testing.T) {
	me := qjUnit(1, 100, 1000, 0)
	boss := qjUnit(103, 10, 100000, 1, qjWuDi, qjGeiShenTong)
	b := faqBattle(me, boss)
	b.w.b = nil
	b.qjBossStart()
	cmds, err := DecodeRecord(b.w.b) // 给予神通：ACT_DO 之后 ADD_TEMP_TALENT（首领 → 我方），客户端飘出神通名
	if err != nil || !slices.ContainsFunc(cmds, func(c Cmd) bool {
		return c.ID == CmdAddTempTalent && c.Actor == boss.Pos && c.Targets[0][0] == int64(me.Pos)
	}) {
		t.Fatalf("给予神通没有演出: %v %+v", err, cmds)
	}
	b.hurt(me, boss, 1, 5000, true)
	if boss.hp != 100000 {
		t.Fatalf("无敌首领被打掉了血: %d", boss.hp)
	}
	b.heal(me, 1, 500) // 满血时治疗 500 全部溢出：流失 = 500 / 1000 × 100 × 150% = 75
	if boss.hp != 100000-75 {
		t.Fatalf("溢出：boss hp %d", boss.hp)
	}
}

// TestTianMoCount：天魔觉醒每提升一次，天女魃头像上的计数（XIULUO_COUNT）跟着 +1。
func TestTianMoCount(t *testing.T) {
	tnb := qjUnit(1, 100, 1000, 0, levelID(famTianMoJueXing, 4))
	dead := qjUnit(101, 100, 1000, 1, levelID(famKuangNuYan, 1))
	b := faqBattle(tnb, dead)
	b.w.b = nil
	b.deathSkillEvent(dead)
	b.deathSkillEvent(dead)
	cmds, err := DecodeRecord(b.w.b)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, c := range cmds {
		if c.ID == CmdXiuluoCount && c.Actor == tnb.Pos {
			got = append(got, c.Magic)
		}
	}
	if !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("计数 %v", got)
	}
}
