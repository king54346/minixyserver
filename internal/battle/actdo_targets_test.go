package battle

import (
	"math/rand/v2"
	"testing"

	"turnrpg/internal/gamedata"
)

// TestActDoTargets：技能动作（ACT_DO）的目标照官方战报（session-01/02 全部 ACT_DO 按目标分类）：
//   - selfActFamilies 只指向施放者自己（指向队友时客户端不播入阵 / 回合技动画，如蟠桃宴、道之始）；
//   - 伤敌的死亡技 / 蓄力技指向敌方（指向死者自己时没有动画，如同归诀）；
//   - 仙衣、气甲、火之魂官方有动作，之前没发。
func TestActDoTargets(t *testing.T) {
	foeAct := map[family]bool{famTongGuiJue: true, famQunYaoJue: true, famWangYuZhou: true, famLuoWang: true, famGuMao: true,
		famYiXiang: true, famQianKunPo: true, famYuanHu: true, famHuoZhiHun: true}
	mustShow := map[family]bool{famXianYi: true, famQiJia: true, famHuoZhiHun: true}
	var ids []int32
	for id := range gamedata.Talents {
		if id < 100000 {
			ids = append(ids, id)
		}
	}
	seen := map[family]bool{}
	rng := rand.New(rand.NewPCG(5, 5))
	for iter := 0; iter < 2500; iter++ {
		mk := func(side int) []*Fighter {
			var out []*Fighter
			for s := 1; s <= 8; s++ {
				hp := int32(500 + rng.IntN(5000))
				f := &Fighter{Pos: s + side*enemyBase, Att: int32(50 + rng.IntN(500)), HP: hp, MaxHP: hp,
					Speed: rng.IntN(10), Race: 1 + rng.IntN(4), Grade: 50}
				for range 3 {
					f.Talents = append(f.Talents, ids[rng.IntN(len(ids))])
				}
				out = append(out, f)
			}
			return out
		}
		res := Simulate(mk(0), mk(1), rand.New(rand.NewPCG(uint64(iter), 2)))
		cmds, err := DecodeRecord(res.Record)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cmds {
			ti := gamedata.Talents[int32(c.Magic)]
			if c.ID != CmdActDo || ti == nil {
				continue
			}
			f := family(ti.FamilyID)
			seen[f] = true
			self := len(c.Targets) == 1 && c.Targets[0][0] == int64(c.Actor)
			foe := len(c.Targets) > 0
			for _, x := range c.Targets {
				foe = foe && x[0]/100 != int64(c.Actor)/100
			}
			switch {
			case selfActFamilies[f] && !self:
				t.Fatalf("%s ACT_DO targets %v, want self", ti.Family, c.Targets)
			case foeAct[f] && !foe:
				t.Fatalf("%s ACT_DO targets %v (actor %d), want foes", ti.Family, c.Targets, c.Actor)
			}
		}
	}
	for f := range mustShow {
		if !seen[f] {
			t.Errorf("%v never showed ACT_DO", f)
		}
	}
}
