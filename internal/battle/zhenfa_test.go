package battle

import (
	"math/rand/v2"
	"testing"

	"turnrpg/internal/gamedata"
)

// TestZhenfaDamage：风扬阵五阶阵位 4（10 级：造成物/法伤害 +1000+20×10）打蛇蟠阵五阶（1 级：受到法术伤害
// −1200−24，物理不减）：普攻多 1200；最低 0；阵眼阵亡后不再加减。
func TestZhenfaDamage(t *testing.T) {
	u := newUnit(fu(4, 1000, 100000, 1), 0)
	eye := newUnit(fu(3, 10, 100000, 0), 0) // 风扬阵阵眼在阵位 4：u 自己就是阵眼
	x := newUnit(fu(101, 10, 100000, 0), 1)
	xeye := newUnit(fu(102, 10, 100000, 0), 1) // 蛇蟠阵阵眼在阵位 2
	b := faqBattle(u, eye, x, xeye)
	b.setupZhenfa([2]Formation{{Type: 3, Stair: 5, Lv: [5]int{10, 10, 10, 10, 10}}, {Type: 8, Stair: 5}})
	if got := b.hurt(u, x, 1, 1000, true); got != 1000+1000+20*10 {
		t.Fatalf("physical %d", got)
	}
	if got := b.hurt(u, x, 1, 3000, false); got != 3000+1200-1224 {
		t.Fatalf("magic %d", got)
	}
	if got := b.hurt(u, x, 1, 10, false); got != 0 { // 10 + 1200 − 1224 → 0
		t.Fatalf("small magic %d", got)
	}
	if got := b.hurt(x, u, 1, 100, false); got != 100 {
		t.Fatalf("snake att on wind %d", got)
	}
	xeye.hp = 0 // 敌方阵眼阵亡：蛇蟠阵的减免失效
	if got := b.hurt(u, x, 1, 3000, false); got != 3000+1200 {
		t.Fatalf("magic after eye died %d", got)
	}
}

// TestZhenfaStats：天覆阵阵位 5 先攻 −1；云垂阵五阶阵位 4 初始生命 +600+12×等级、阵位 1 失去神通 20031 族。
func TestZhenfaStats(t *testing.T) {
	lose := gamedata.Talents[20031]
	if lose == nil {
		t.Skip("no talent 20031")
	}
	a := newUnit(fu(1, 100, 1000, 5, 20031), 0)
	d := newUnit(fu(4, 100, 1000, 5), 0)
	x := newUnit(fu(105, 100, 1000, 5), 1)
	b := faqBattle(a, d, x)
	b.setupZhenfa([2]Formation{{Type: 4, Stair: 5, Lv: [5]int{0, 0, 0, 3, 0}}, {Type: 1, Stair: 1}})
	if d.maxHP != 1000+600+36 || d.hp != d.maxHP || d.MaxHP != int32(d.maxHP) || d.Speed != 6 {
		t.Fatalf("pos4 maxHP %d hp %d speed %d", d.maxHP, d.hp, d.Speed)
	}
	if a.tal(family(lose.FamilyID)) != nil {
		t.Fatal("pos1 talent kept")
	}
	if x.Speed != 4 {
		t.Fatalf("tianfu pos5 speed %d", x.Speed)
	}
	// 云垂阵的 kezhi 里有天覆（1），天覆的没有云垂：我方云垂不被克，敌方天覆被克
	if b.zf[0].countered || !b.zf[1].countered {
		t.Fatalf("counter flags %v %v", b.zf[0].countered, b.zf[1].countered)
	}
}

// TestZhenfaEyeSkill：阵眼技在指定回合结束时释放（地载阵五阶：2/5/8 回合 100%）；被克制时不放；战报带阵法参数。
func TestZhenfaEyeSkill(t *testing.T) {
	run := func(foe Formation) (bool, Result) {
		t1 := []*Fighter{fu(1, 10, 1000000, 0), fu(2, 10, 1000000, 0), fu(3, 10, 1000000, 0)}
		t2 := []*Fighter{fu(101, 10, 1000000, 0)}
		res := SimulateWith(t1, t2, Options{Formations: [2]Formation{{Type: 2, Stair: 5}, foe}}, rand.New(rand.NewPCG(1, 1)))
		cmds, err := DecodeRecord(res.Record)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cmds {
			if c.ID == CmdActDo && c.Actor == 2 && c.Magic == 14508 {
				return true, res
			}
		}
		return false, res
	}
	if ok, res := run(Formation{}); !ok || res.ZfParam != "1:2:5:1:1:1:1:1|2:0:1" { // 敌方没阵法也要写（客户端取 zhenfa[2]）
		t.Fatalf("eye skill %v param %q", ok, res.ZfParam)
	}
	if ok, res := run(Formation{Type: 1, Stair: 1}); ok || res.ZfParam != "1:2:5:1:1:1:1:1|2:1:1:1:1:1:1:1" { // 天覆克地载
		t.Fatalf("countered eye skill %v param %q", ok, res.ZfParam)
	}
}

// TestZhenfaFollowsPos：固定加成跟着阵位走——云垂阵阵位 4（初始生命 +600+12×等级、先攻 +1）的护法换到后阵后
// 收回（生命按比例保留），换上来的护法得到；阵位 1 的失去神通只在该阵位生效。
func TestZhenfaFollowsPos(t *testing.T) {
	lose := gamedata.Talents[20031]
	a := newUnit(fu(4, 100, 1000, 5), 0)
	c := newUnit(fu(6, 100, 1000, 5), 0)
	x := newUnit(fu(101, 100, 1000, 5), 1)
	b := faqBattle(a, c, x)
	b.setupZhenfa([2]Formation{{Type: 4, Stair: 5, Lv: [5]int{1, 1, 1, 1, 1}}})
	if a.maxHP != 1612 || a.Speed != 6 || c.maxHP != 1000 {
		t.Fatalf("setup a %d/%d c %d", a.maxHP, a.Speed, c.maxHP)
	}
	b.swap(a, c)
	if a.maxHP != 1000 || a.hp != 1000 || a.Speed != 5 || c.maxHP != 1612 || c.hp != 1612 || c.Speed != 6 {
		t.Fatalf("after swap a %d/%d/%d c %d/%d/%d", a.maxHP, a.hp, a.Speed, c.maxHP, c.hp, c.Speed)
	}
	if lose != nil {
		d := newUnit(fu(1, 100, 1000, 5, 20031), 0)
		b.units[0] = append(b.units[0], d)
		b.zfSync(d)
		if d.tal(family(lose.FamilyID)) != nil {
			t.Fatal("pos1 talent not suppressed")
		}
		d.Pos = 7
		b.zfSync(d)
		if d.tal(family(lose.FamilyID)) == nil {
			t.Fatal("talent still suppressed on bench")
		}
	}
	cmds, err := DecodeRecord(b.w.b)
	if err != nil || len(cmds) == 0 {
		t.Fatal(err)
	}
}
