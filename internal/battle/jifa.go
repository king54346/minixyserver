package battle

import "turnrpg/internal/gamedata"

// 神通激发（天将）：5 星卡穿有缘的 5 星法术书 / 衣服 / 武器并升了天将阶，对应槽的神通获得额外效果，
// 等级 0–5 由服务端算好放在 Fighter.TalentTJ（见 xymsrv/tianjiang.go petTJ）。
//
// 额外效果的文字是 talent 表的 tjdesc，参数是 tjparam（gamedata.TalentInfo.TJParam），变量与客户端
// logic/talent_mgr.lua getTianjiangDesc 一致：
//
//	x = a×0.1×等级   y = b×0.1×等级   z = 25 + x   w = a×等级   v = b×等级
//
// 例：回梦 tjparam 100、5 级 → x = 50，「增加生命效果提升 50%」，与官方战报 ×1.5 相符；烈焰吞噬 200 → ×2。
// 各族的额外效果写在该族原来的代码旁边（搜 tjOn）；只提升触发概率的族统一在 newTalent 里改参数（tjChance）。

// tjOn 报告该神通是否激发（等级 > 0；枪出游龙、疯狂等没有 tjparam 也有激发效果）。
func (k *talent) tjOn() bool { return k.tj > 0 }

func (k *talent) tjParam(i int) float64 {
	if i < len(k.TJParam) {
		return k.TJParam[i]
	}
	return 0
}

// tjX / tjY / tjZ / tjW / tjV 是 tjdesc 里的 $x $y $z $w $v（未激发为 0）。
func (k *talent) tjX() float64 { return k.tjVar(0, 0.1) }
func (k *talent) tjY() float64 { return k.tjVar(1, 0.1) }
func (k *talent) tjZ() float64 { return 25 + k.tjX() }
func (k *talent) tjW() float64 { return k.tjVar(0, 1) }
func (k *talent) tjV() float64 { return k.tjVar(1, 1) }

func (k *talent) tjVar(i int, f float64) float64 {
	if !k.tjOn() {
		return 0
	}
	return float64(int(k.tjParam(i)*f*float64(k.tj)*10)) / 10 // 客户端取一位小数（math.floor(…×10)/10）
}

// tjPct 是 $x 取整，用作百分比概率 / 加成。
func (k *talent) tjPct() int { return int(k.tjX()) }

// tjBoost 按 $x% 放大 n（「效果提升 $x%」一类）。
func (k *talent) tjBoost(n int64) int64 {
	if x := k.tjX(); x > 0 {
		return n + int64(float64(n)*x/100)
	}
	return n
}

// tjBoost2 按 $y% 放大 n。
func (k *talent) tjBoost2(n int64) int64 {
	if y := k.tjY(); y > 0 {
		return n + int64(float64(n)*y/100)
	}
	return n
}

// levelID 是 f 族第 lv 级（1 起，超出按最高级）的神通号：基础系各级神通号连续（如暴击 20121–20130）。
func levelID(f family, lv int) int32 {
	lv = max(lv, 1)
	id := int32(f) + int32(lv) - 1
	for id > int32(f) {
		if ti := gamedata.Talents[id]; ti != nil && ti.FamilyID == int32(f) {
			return id
		}
		id--
	}
	return int32(f)
}

// tjChance 是激发只提升触发概率的族：族 → 概率所在的参数下标（激发后该参数加 $x）。
var tjChance = map[family]int{
	famXianGong: 0, famLuanWu: 0, famQuLei: 0, famTaLang: 0, famPoYan: 0, famDaoHuo: 0, famXueRuo: 1, famHuaXue: 0,
	famKuangRe: 0, famLianJi: 0, famQuZhu: 0, famShanBi: 0, famZhuangJi: 0, famShenYou: 0, famHuZhen: 0, famFanJi: 0,
	famChongFeng: 0, famHuiLie: 0, famTaChao: 0, famLiJian: 2,
}

// applyTJ 设置神通的激发等级；只提升概率的族直接把参数改大（复制一份，不动共享的表）。
func (k *talent) applyTJ(lv int) {
	k.tj = lv
	if i, ok := tjChance[k.fam]; ok && k.tjOn() && i < len(k.Param) {
		p := append([]float64(nil), k.Param...)
		p[i] += k.tjX()
		info := *k.TalentInfo
		info.Param = p
		k.TalentInfo = &info
	}
}
