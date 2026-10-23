package battle

import (
	"math/rand/v2"
	"testing"
)

// TestBuffSlots：每种 buff 在单位身上各占一个槽位（客户端按槽位记 buff，同槽位再挂会留下删不掉的旧特效）；
// 同一种再挂复用原槽位；移除按 buff 号找回槽位；同一状态换了来源时先移除旧特效。
func TestBuffSlots(t *testing.T) {
	b := &battle{rng: rand.New(rand.NewPCG(1, 1)), skills: map[string]int{}}
	u := newUnit(fu(1, 100, 1000, 1), 0)
	b.units = [2][]*unit{{u}, nil}
	b.setStatus(u, stImmune, 0, 1, false, 5321, slotBuff) // 光阴碎：无敌到本回合结束
	b.setStatus(u, stNoSoul, 0, maxRounds, false, 5402, slotBuff)
	b.buff(u, 5321, 1)
	s1, s2 := u.buffSlot[5321], u.buffSlot[5402]
	if s1 == s2 || s1 < 2 || s2 < 2 {
		t.Fatalf("slots %d %d", s1, s2)
	}
	b.w.b = nil
	b.tickStatus(u, false) // 回合结束：无敌到期
	want := []byte{CmdRmBuff, byte(u.Pos), byte(s1)}
	if string(b.w.b) != string(want) || u.buffSlot[5402] != s2 {
		t.Fatalf("rm %v want %v", b.w.b, want)
	}
	// 免伤换来源（无 buff 号）：旧特效先拿掉
	b.setStatus(u, stImmune, 0, 1, false, 5321, slotBuff)
	s3 := u.buffSlot[5321]
	b.w.b = nil
	b.setStatus(u, stImmune, 0, 1, false, 0, 0)
	if want := []byte{CmdRmBuff, byte(u.Pos), byte(s3)}; string(b.w.b) != string(want) {
		t.Fatalf("replace %v want %v", b.w.b, want)
	}
}

// TestLieyanNuhou：离火兽烈焰怒吼（目标规则 17，target_param 20061 乱舞）只给本方带乱舞的护法挂火附魔。
func TestLieyanNuhou(t *testing.T) {
	caster := fu(1, 1000, 100000, 10, 20061)
	caster.Magic, caster.MagicLv = 15408, 10
	_, cmds := simCheck(t, []*Fighter{caster, fu(2, 1000, 100000, 9), fu(3, 1000, 100000, 8, 20065)},
		[]*Fighter{fu(101, 1, 1000000, 1)}, 1)
	got := map[int64]bool{}
	for _, c := range cmds {
		if c.ID == CmdBuff && c.Magic == 5651 {
			for _, x := range c.Targets {
				got[x[0]] = true
			}
		}
	}
	if !got[1] || !got[3] || got[2] {
		t.Fatalf("火附魔 targets %v", got)
	}
}

// TestFireEnchant：火附魔——攻击伤害低于初始攻击时，差值的 val% 转为火属性生命流失（SUB_HP 10002）。
func TestFireEnchant(t *testing.T) {
	b := &battle{rng: rand.New(rand.NewPCG(1, 1)), skills: map[string]int{}}
	u := newUnit(fu(1, 1000, 1000, 1), 0)
	x := newUnit(fu(101, 1, 100000, 1), 1)
	b.units = [2][]*unit{{u}, {x}}
	b.setStatus(u, stFireEnchant, 100, 1, true, 5651, slotBuff)
	b.w.b = nil
	b.fireEnchant(u, x, 300)
	cmds, err := DecodeRecord(b.w.b)
	if err != nil || len(cmds) != 1 || cmds[0].ID != CmdSubHP || cmds[0].Magic != magicFireHurt || cmds[0].Targets[0][1] != 700 {
		t.Fatalf("enchant %+v %v", cmds, err)
	}
	b.w.b = nil
	b.fireEnchant(u, x, 1200) // 伤害已超过初始攻击：不追加
	if len(b.w.b) != 0 {
		t.Fatal("extra hit above base attack")
	}
}

// TestMaxHPCutSubHP：上限降到当前生命以下时，CHG_MAXHP 之后补 SUB_HP 扣掉超出部分（客户端不会自己压血量）；
// 当前生命低于新上限时不扣。
func TestMaxHPCutSubHP(t *testing.T) {
	b := &battle{rng: rand.New(rand.NewPCG(1, 1)), skills: map[string]int{}}
	x := newUnit(fu(1, 100, 1000, 1), 0)
	b.units = [2][]*unit{{x}, nil}
	b.changeMaxHP(x, 21716, -300)
	cmds, err := DecodeRecord(b.w.b)
	if err != nil || len(cmds) != 2 || cmds[0].ID != CmdChgMaxHP || cmds[1].ID != CmdSubHP ||
		cmds[1].Magic != 21716 || cmds[1].Targets[0][1] != 300 || x.hp != 700 {
		t.Fatalf("cut %+v hp %d", cmds, x.hp)
	}
	x.hp = 100
	b.w.b = nil
	b.changeMaxHP(x, 21716, -300)
	if cmds, _ := DecodeRecord(b.w.b); len(cmds) != 1 || x.hp != 100 {
		t.Fatalf("below new max %+v hp %d", cmds, x.hp)
	}
}
