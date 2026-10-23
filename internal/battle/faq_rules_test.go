package battle

import (
	"math/rand/v2"
	"testing"
)

// 图鉴 FAQ 的共性规则（生命流失、法术结界免疫、守卫 1 点、气甲对所有物理伤害）。

func faqBattle(us ...*unit) *battle {
	b := &battle{rng: rand.New(rand.NewPCG(1, 1)), skills: map[string]int{}, round: 1}
	for _, u := range us {
		b.units[u.side] = append(b.units[u.side], u)
	}
	return b
}

// TestLifeLossPierces：生命流失（化血等）不受无敌 / 诈死 / 结界影响，也不触发蓄力、坚韧；普通伤害被无敌挡住。
func TestLifeLossPierces(t *testing.T) {
	src := newUnit(fu(101, 100, 1000, 1), 1)
	u := newUnit(fu(1, 100, 1000, 1, 20439, 21514), 0) // 坚韧9、六艺（蓄力）
	b := faqBattle(src, u)
	b.setStatus(u, stImmune, 0, 1, false, 0, 0)
	u.mshield = 500
	if got := b.hurt(src, u, 1, 300, false); got != 0 || u.hp != 1000 {
		t.Fatalf("无敌挡普通伤害: got %d hp %d", got, u.hp)
	}
	u.charge = 0
	u.mshield = 500
	att := u.att
	if got := b.loseHP(src, u, 20210, 300); got != 300 || u.hp != 700 || u.mshield != 500 || u.charge != 0 || u.att != att {
		t.Fatalf("化血穿无敌: got %d hp %d mshield %d charge %d att %d", got, u.hp, u.mshield, u.charge, u.att)
	}
	b.clearStatus(u, stImmune)
	b.setStatus(u, stZhasi, 0, 1, false, zhasiBuff, slotBuff) // 诈死后本回合
	if b.hurt(src, u, 1, 300, true) != 0 || b.loseHP(src, u, 20210, 100) != 100 {
		t.Fatal("诈死：普通伤害免疫、生命流失照扣")
	}
}

// TestMagicShieldResists：带法术结界时免疫负面持续状态（FAQ；官方战报 5861 次只有 3 次例外）。
func TestMagicShieldResists(t *testing.T) {
	x := newUnit(fu(1, 100, 1000, 1), 0)
	b := faqBattle(x)
	x.mshield = 1
	if b.addStatus(x, stStiff, 0, 1, true, 5001, slotDebuff) {
		t.Fatal("法术结界没挡住僵化")
	}
	x.mshield = 0
	if !b.addStatus(x, stStiff, 0, 1, true, 5001, slotDebuff) {
		t.Fatal("没结界也上不了僵化")
	}
}

// TestGuardSharesOne：守卫代为承受时原目标仍受 1 点（官方战报 duobao:49）；气甲对分击等非攻击物理伤害同样封顶。
func TestGuardSharesOne(t *testing.T) {
	_, cmds := simCheck(t, []*Fighter{fuh(1, 0, 100, 100000, 1), fu(2, 0, 1000000, 1, 20759)},
		[]*Fighter{fu(101, 1000, 1000000, 10)}, 1)
	if !hasCmd(cmds, CmdSubHP, attackNormal, 1, 1) {
		t.Fatal("guarded target did not take 1")
	}
	src := newUnit(fu(101, 100, 1000, 1), 1)
	q := newUnit(fu(1, 100, 1000, 1, 20345), 0) // 气甲5：物理伤害不超过上限 24%
	b := faqBattle(src, q)
	if got := b.hurt(src, q, 20769, 900, true); got != 240 {
		t.Fatalf("气甲 on 分击: %d", got)
	}
}

// TestGanluRevivesBenched：甘露入阵复活的是被它补位换到后阵的死者（官方战报 lundao:71 RELIVE 后阵 6 / 7 号位）。
func TestGanluRevivesBenched(t *testing.T) {
	_, cmds := simCheck(t, []*Fighter{fuh(1, 0, 1, 1000, 1), fu(2, 0, 100000, 1), fu(3, 0, 100000, 1), fu(4, 0, 100000, 1),
		fu(5, 0, 100000, 1), fu(6, 0, 100000, 1, 21354)}, []*Fighter{fu(101, 100, 1000000, 10)}, 1)
	if !hasCmd(cmds, CmdRelive, 21354, -1, -1) {
		t.Fatal("甘露 did not revive the benched corpse")
	}
}

// TestHuiLieBuff：会猎命中后给目标挂 BUFF 506x（官方战报 2087：BUFF 5065），重复触发不叠加。
func TestHuiLieBuff(t *testing.T) {
	_, cmds := simCheck(t, []*Fighter{fu(1, 10, 100000, 10, 20640)}, []*Fighter{fu(101, 10, 100000, 1)}, 1)
	if !hasCmd(cmds, CmdBuff, 5070, 101, -1) {
		t.Fatal("会猎 buff not applied")
	}
}

// TestTianWeiCharges：天威降临打到我方蓄力护法也蓄力；普攻被闪避照样触发。
func TestTianWeiCharges(t *testing.T) {
	_, cmds := simCheck(t, []*Fighter{fu(1, 100, 100000, 10, 22202), fu(2, 0, 100000, 1, 21514)},
		[]*Fighter{fu(101, 0, 100000, 1, 20319)}, 1) // 对面闪避9
	if !hasCmd(cmds, CmdAddPower, 1, 2, -1) {
		t.Fatal("ally did not charge from 天威降临")
	}
	for i, c := range cmds {
		if c.ID == CmdSubHP && c.Magic == attackDodge {
			for _, d := range cmds[i:] {
				if d.ID == CmdActDo && d.Magic == 22202 {
					return
				}
				if d.ID == CmdActDo && d.Magic == attackNormal {
					break
				}
			}
		}
	}
	t.Fatal("天威降临 not triggered after a dodge")
}

// TestPoisonNoStack：同种燃烧再挂只保留一个（取高），换高等级时先去掉旧图标。
func TestPoisonNoStack(t *testing.T) {
	x := newUnit(fu(1, 100, 1000, 1), 0)
	b := faqBattle(x)
	b.addPoison(x, poison{pct: 2, left: maxRounds, buff: 5021, burn: true})
	b.addPoison(x, poison{pct: 6, left: maxRounds, buff: 5023, burn: true})
	b.addPoison(x, poison{pct: 4, left: maxRounds, buff: 5022, burn: true})
	if len(x.poison) != 1 || x.poison[0].pct != 6 || x.poison[0].buff != 5023 {
		t.Fatalf("poison %+v", x.poison)
	}
	if _, ok := x.buffSlot[5021]; ok {
		t.Fatal("old burn icon not removed")
	}
}

// TestClearAllDebuffs：清除特效移除所有负面状态（毒砂、敌方锢魂、中毒、僵化、会猎），本方给的锢魂不动。
func TestClearAllDebuffs(t *testing.T) {
	x := newUnit(fu(1, 100, 1000, 1), 0)
	src := newUnit(fu(101, 100, 1000, 1), 1)
	b := faqBattle(x, src)
	b.applyBuff(src, x, 5035) // 毒砂5
	b.applyBuff(src, x, 5065) // 会猎5
	b.applyBuff(src, x, 5135) // 毒化5
	b.addStatus(x, stNoSoul, 0, maxRounds, false, 5141, slotDebuff)
	b.addStatus(x, stStiff, 0, 1, true, 5001, slotDebuff)
	b.clearAllDebuffs(x)
	if x.dusha != 0 || x.hurtUp != 0 || len(x.poison) != 0 || x.has(stNoSoul) != nil || x.has(stStiff) != nil {
		t.Fatalf("left: dusha %d hurtUp %d poison %d status %v", x.dusha, x.hurtUp, len(x.poison), x.status)
	}
	b.setStatus(x, stNoSoul, 0, maxRounds, false, 5141, slotDebuff) // 甘露复活后的锢魂
	b.clearAllDebuffs(x)
	if x.has(stNoSoul) == nil {
		t.Fatal("own 锢魂 cleared")
	}
}

// TestDunRangNotWhenInvincible：无敌时遁让不发动（不受伤也就不必换位）；没无敌照常换到后阵。
func TestDunRangNotWhenInvincible(t *testing.T) {
	src := newUnit(fu(101, 100, 100000, 1), 1)
	x := newUnit(fu(1, 100, 10000, 1, 20310), 0)
	bn := newUnit(fu(6, 100, 1000, 1), 0)
	b := faqBattle(src, x, bn)
	b.setStatus(x, stImmune, 0, 1, false, 5321, slotBuff)
	b.propertyHurt(src, x, 15151, 2500, 2)
	if x.Pos != 1 {
		t.Fatal("遁让 fired while invincible")
	}
	b.clearStatus(x, stImmune)
	for i := 0; i < 3 && x.Pos == 1; i++ { // 80% 概率
		b.propertyHurt(src, x, 15151, 2500, 2)
	}
	if x.Pos == 1 {
		t.Fatal("遁让 did not fire without 无敌")
	}
}

// TestZhasiClearsAtRoundEnd：诈死的免疫状态（BUFF 5411）回合结束时清除，下一回合照常受伤。
func TestZhasiClearsAtRoundEnd(t *testing.T) {
	src := newUnit(fu(101, 100, 1000, 1), 1)
	u := newUnit(fu(1, 100, 1000, 1), 0)
	b := faqBattle(src, u)
	b.setStatus(u, stZhasi, 0, 1, false, zhasiBuff, slotBuff)
	if b.hurt(src, u, 1, 300, true) != 0 {
		t.Fatal("not immune")
	}
	b.tickStatus(u, false) // 回合结束
	if _, ok := u.buffSlot[zhasiBuff]; ok || b.hurt(src, u, 1, 300, true) != 300 {
		t.Fatal("诈死 not cleared at round end")
	}
}

// TestLeiYingFromBench：雷震子在后阵也代遁让护法承受致命伤并让它遁入后阵；被僵化时不发动。
func TestLeiYingFromBench(t *testing.T) {
	src := newUnit(fu(101, 1000, 100000, 1), 1)
	dr := newUnit(fu(1, 0, 100, 1, 20310), 0)    // 带遁让，100 血
	lz := newUnit(fu(6, 0, 100000, 1, 22198), 0) // 雷震子在后阵
	bn := newUnit(fu(7, 0, 1000, 1), 0)          // 后阵候补
	b := faqBattle(src, dr, lz, bn)
	b.hurt(src, dr, 1, 500, true)
	if !dr.alive() || dr.Pos == 1 || lz.hp != 100000-500 || lz.leiying != 1 {
		t.Fatalf("leiying: dr alive %v pos %d lz hp %d", dr.alive(), dr.Pos, lz.hp)
	}
	dr2 := newUnit(fu(2, 0, 100, 1, 20310), 0)
	b.units[0] = append(b.units[0], dr2)
	b.setStatus(lz, stStiff, 0, 1, true, 5001, slotDebuff)
	b.hurt(src, dr2, 1, 500, true)
	if dr2.alive() {
		t.Fatal("stiff 雷震子 still protected")
	}
}
