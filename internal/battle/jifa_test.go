package battle

import "testing"

// tjf 是带激发等级的单位：每个神通同一激发等级。
func tjf(f *Fighter, lv int) *Fighter {
	f.TalentTJ = make([]int, len(f.Talents))
	for i := range f.TalentTJ {
		f.TalentTJ[i] = lv
	}
	return f
}

// 变量与客户端 getTianjiangDesc 一致：x = a×0.1×等级 等。
func TestJifaVars(t *testing.T) {
	k := newTalent(22178) // 回梦4，tjparam 100
	k.applyTJ(5)
	if k.tjX() != 50 || k.tjZ() != 75 || k.tjW() != 500 {
		t.Fatalf("回梦 lv5: x=%v z=%v w=%v", k.tjX(), k.tjZ(), k.tjW())
	}
	k = newTalent(22178)
	k.applyTJ(0)
	if k.tjOn() || k.tjX() != 0 || k.tjBoost(100) != 100 {
		t.Fatal("level 0 must be off")
	}
	// 只提升概率的族：参数直接加 $x，且不改共享的表
	k = newTalent(20050) // 先攻10，tjparam 40 → x = 20
	before := k.Param[0]
	k.applyTJ(5)
	if k.Param[0] != before+20 || newTalent(20050).Param[0] != before {
		t.Fatalf("先攻 chance %v → %v (shared %v)", before, k.Param[0], newTalent(20050).Param[0])
	}
	if got := levelID(famBaoJi, 10); got != 20130 {
		t.Fatalf("暴击10 = %d", got)
	}
	if got := levelID(famBaoJi, 99); got != 20130 {
		t.Fatalf("level clamp = %d", got)
	}
}

// 回梦激发 5 级：增加生命效果 ×1.5（官方战报 9495 = 10551×90%）。
func TestJifaHuimeng(t *testing.T) {
	t1 := []*Fighter{tjf(fu(1, 0, 1000, 1, 22178), 5), fu(2, 0, 1000, 1, 20310), fu(3, 0, 100000, 1), fu(4, 0, 100000, 1),
		fu(5, 0, 100000, 1), fu(6, 0, 1000, 1)}
	t2 := []*Fighter{fu(102, 0, 100000, 1), fu(106, 1000, 100000, 10, 21784)} // 龙跃补位入阵
	runFamCase(t, 0, famCase{fam: famHuiMeng, t1: t1, t2: t2,
		check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdChgMaxHP, 22178, 1, 900), "no +900") }})
}

// 烈焰吞噬激发 5 级：吞噬效果 ×2（官方战报：亡者攻击 / 上限的 40%）。
func TestJifaLieyan(t *testing.T) {
	t1 := []*Fighter{tjf(fu(1, 1000, 1000, 10, 22154), 5)}
	t2 := []*Fighter{fu(101, 500, 1000, 1), fu(102, 0, 100000, 1)}
	runFamCase(t, 0, famCase{fam: famLieYanTunShi, t1: t1, t2: t2,
		check: func(_ Result, c []Cmd) string {
			return want(hasCmd(c, CmdChgMaxHP, 22154, 1, 400), "no +400", hasCmd(c, CmdAttChange, 22154, 1, 200), "no +200")
		}})
}

// 坚韧10 激发（tjparam 20 → x = 10）：触发后上限 +10% 并回复同量（官方战报：坚韧后 CHG_MAXHP + ADD_HP）。
func TestJifaJianren(t *testing.T) {
	t1 := []*Fighter{fu(1, 100, 1000, 10)}
	t2 := []*Fighter{tjf(fu(101, 100, 100000, 1, 20440), 5)}
	runFamCase(t, 0, famCase{fam: famJianRen, t1: t1, t2: t2,
		check: func(_ Result, c []Cmd) string {
			return want(hasCmd(c, CmdChgMaxHP, 20440, 101, 10000), "no +10000 max")
		}})
}

// 猎命激发（30;80）：暴走线放宽到 z = 40%，触发后回复 y = 40% 上限。
func TestJifaRampage(t *testing.T) {
	t1 := []*Fighter{tjf(fuh(1, 10, 350, 1000, 1, 21504), 5)}
	t2 := []*Fighter{fu(101, 1, 100000, 10)}
	runFamCase(t, 0, famCase{fam: famLieMing, t1: t1, t2: t2,
		check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAddHP, 21504, 1, 400), "no 400 heal") }})
	// 未激发时 35% 血不到 25% 线，不暴走
	res, _ := simCheck(t, []*Fighter{fuh(1, 10, 350, 1000, 1, 21504)}, CloneTeam(t2), 1)
	if res.Skills[famLieMing.String()] != 0 {
		t.Fatal("unawakened 猎命 raged at 35%")
	}
}

// 五指山激发（150 → x = 75）：对每个被施加负面状态的护法造成 75% 攻击的土属性伤害。
func TestJifaWuzhishan(t *testing.T) {
	t1 := []*Fighter{tjf(fu(1, 100, 100000, 1, 21494), 5)}
	t2 := []*Fighter{fu(101, 0, 100000, 1), fu(102, 0, 100000, 1)}
	runFamCase(t, 0, famCase{fam: famWuZhiShan, t1: t1, t2: t2,
		check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdSubHP, 21494, -1, 75), "no 75 earth") }})
}

// 魔煞激发（50 → x = 25）：攻击后保留攻击加值的 25%。
func TestJifaMoshaKeep(t *testing.T) {
	t1 := []*Fighter{tjf(raced(fu(1, 100, 100000, 10, 20570), raceGhost), 5)} // 魔煞10
	t2 := []*Fighter{fu(101, 0, 100000, 1)}
	runFamCase(t, 0, famCase{fam: famMoSha, t1: t1, t2: t2,
		check: func(_ Result, c []Cmd) string {
			for _, x := range c {
				if x.ID == CmdAttChange && x.Magic == 0 && len(x.Targets) > 0 && x.Targets[0][0] == 1 && x.Targets[0][1] < 0 {
					for _, y := range c {
						if y.ID == CmdAttChange && y.Magic == 20570 && len(y.Targets) > 0 {
							if up := y.Targets[0][1]; -x.Targets[0][1] == up-up*25/100 {
								return ""
							}
						}
					}
				}
			}
			return "temp attack not partly kept"
		}})
}
