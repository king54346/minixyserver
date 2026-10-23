package battle

import (
	"math/rand/v2"
	"sort"
	"testing"

	"turnrpg/internal/gamedata"
)

// TestStressRandomTalents：随机 8v8、每人 3 个神通（T5 补的族及与之联动的族），Simulate 后 Replay 自洽、不 panic。
func TestStressRandomTalents(t *testing.T) {
	var ids []int32
	newFam := map[family]bool{}
	for _, f := range []family{famDunRang, famNiShi, famXianGong, famFenJi, famRanHun, famHuanHun, famYuanXing, famFengMo, famZhenYan, famXianDi, famKuangNuYan, famBuQu, famShouWei} {
		newFam[f] = true
	}
	for _, f := range append(append(append(append(deathFamilies, enterFamilies...), chargeTalents...), rampageTalents...), raceFams...) {
		newFam[f] = true
	}
	for id, ti := range gamedata.Talents {
		if f := family(ti.FamilyID); newFam[f] || t5Families[f] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	rng := rand.New(rand.NewPCG(9, 9))
	for iter := 0; iter < 3000; iter++ {
		mk := func(side int) []*Fighter {
			var out []*Fighter
			for s := 1; s <= 8; s++ {
				hp := int32(500 + rng.IntN(5000))
				f := &Fighter{Pos: s + side*enemyBase, Att: int32(50 + rng.IntN(500)), HP: hp, MaxHP: hp,
					Speed: rng.IntN(10), Race: 1 + rng.IntN(4), Grade: 50}
				for range 3 {
					f.Talents = append(f.Talents, ids[rng.IntN(len(ids))])
					f.TalentTJ = append(f.TalentTJ, rng.IntN(6)) // 激发等级 0–5
				}
				out = append(out, f)
			}
			return out
		}
		a, b := mk(0), mk(1)
		ra, rb := CloneTeam(a), CloneTeam(b)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("iter %d panic %v\n%v\n%v", iter, r, talentsOf(ra), talentsOf(rb))
				}
			}()
			res := SimulateLeaders(a, b, [2]int32{90001 + int32(rng.IntN(183)), 90001 + int32(rng.IntN(183))}, rand.New(rand.NewPCG(uint64(iter), 2)))
			if _, err := Replay(ra, rb, res.Record); err != nil {
				t.Fatalf("iter %d: %v", iter, err)
			}
		}()
	}
}

var raceFams = []family{famQuMo, famZhuXian, famDouShou, famShouXing}

// t5Families 是 T5 补的族（压力测试取样用）。
var t5Families = map[family]bool{}

func init() {
	for _, f := range []family{famWanXianLaiChao, famBuQin, famBuMieFaShen, famWuJiangZhiShang, famJiuWeiXianChe, famWuZhiShan, famYiMingYanTian, famXiuLuoYinShen, famLiuYi, famRenWu, famFenShui, famJianGe, famShiRiShengMu, famMingShuYouWo, famShiXinLongZhou, famHuiMeng, famTuYiShang, famTianDiLingQi, famTianWeiJiangLin, famTianZhan, famTianGangJianQi, famTianMoJueXing, famShenSi, famHanBingJia, famQingShang, famYuanHu, famSheHunShu, famYueZhiAnMian, famQiangChuYouLong, famHengChongZhiZhuang, famShuiZhiXing, famShuiYiShang, famHuoYiShang, famHuoPaoZhiShu, famLieYanTunShi, famTeDingRuoHua, famKuangLong, famShiHou, famLieMing, famShengSiBie, famFengKuang, famPoChuJinGu, famJingWeiTianHai, famZiQingJian, famJueMingZhuiLie, famTuoMaoTuiDi, famWuXiJiangGe, famBaJiaoShan, famCangJianYuHua, famCangJianYuShen, famZhuFuXiaoSan, famXiangXing, famFuZhong, famTanChi, famFanPuGuiZhen, famDaoYiShang, famDingTouQiJian, famZhuJian, famLeiYingBiHu, famLeiYiShang, famFeiXian, famMoYuRuTi, famMoLong, famLongWei, famLongHuXianDan, famLongYue} {
		t5Families[f] = true
	}
}

func talentsOf(fs []*Fighter) [][]int32 {
	var out [][]int32
	for _, f := range fs {
		out = append(out, f.Talents)
	}
	return out
}
