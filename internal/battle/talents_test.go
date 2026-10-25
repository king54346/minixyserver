package battle

// 各神通族的单元测试：构造双方 Fighter，Simulate 后用 Replay 校验战报自洽，并断言神通效果。

import (
	"math/rand/v2"
	"testing"
)

// Batch1 八族单测：每族构造双方 Fighter，Simulate 后用 Replay 校验战报自洽，
// 再断言该神通的效果出现且数值正确。神通 id 见 gamedata/data/talent.csv。

func simCheck(t *testing.T, t1, t2 []*Fighter, seed uint64) (Result, []Cmd) {
	t.Helper()
	r1, r2 := CloneTeam(t1), CloneTeam(t2) // Simulate 会改站位，回放用开战前的复制
	res := Simulate(t1, t2, rand.New(rand.NewPCG(seed, 1)))
	cmds, err := DecodeRecord(res.Record)
	if err != nil {
		t.Fatal(err)
	}
	if win, err := Replay(r1, r2, res.Record); err != nil {
		t.Fatal(err)
	} else if win != res.Win {
		t.Fatalf("replay win=%v, result %v", win, res.Win)
	}
	return res, cmds
}

// simUntilSeed 切种子直到指定神通触发（有概率的神通用），返回触发时的结果。
func simUntilSeed(t *testing.T, t1, t2 []*Fighter, f family) (Result, []Cmd) {
	t.Helper()
	for seed := uint64(0); seed < 200; seed++ {
		a, b := CloneTeam(t1), CloneTeam(t2)
		res, cmds := simCheck(t, a, b, seed)
		if res.Skills[f.String()] > 0 {
			return res, cmds
		}
	}
	t.Fatalf("skill %s never triggered in 200 seeds", f)
	return Result{}, nil
}

func subHPTo(cmds []Cmd, pos int) []int64 {
	var out []int64
	for _, c := range cmds {
		if c.ID == CmdSubHP {
			for _, x := range c.Targets {
				if x[0] == int64(pos) {
					out = append(out, x[1])
				}
			}
		}
	}
	return out
}

func hasCmd(cmds []Cmd, id int, magic int64, pos int64, val int64) bool {
	for _, c := range cmds {
		if c.ID != id || (magic >= 0 && c.Magic != magic) {
			continue
		}
		for _, x := range c.Targets {
			if (pos < 0 || x[0] == pos) && (val < 0 || x[1] == val) {
				return true
			}
		}
	}
	return false
}

// TestB1Powan 破妄10（21711→21720，param 80）：攻击前 80% 概率把对面三位
// 上限恢复初始值。敌方带蟠桃宴4（20954，+20% 上限）补位入阵 1000→1200，
// 破妄命中后应以 CHG_MAXHP op2 还原 200。
func TestB1Powan(t *testing.T) {
	t1 := []*Fighter{{Pos: 1, Att: 20, HP: 500, MaxHP: 500, Speed: 10, Talents: []int32{21720}}}
	t2 := []*Fighter{{Pos: 106, Att: 5, HP: 1000, MaxHP: 1000, Speed: 1, Talents: []int32{20954}}} // 补位入阵
	res, cmds := simUntilSeed(t, t1, t2, famPoWang)
	if res.Skills[famPoWang.String()] == 0 {
		t.Fatal("powan not counted")
	}
	if !hasCmd(cmds, CmdChgMaxHP, 21720, 101, -200) {
		t.Fatalf("no CHG_MAXHP -200 to 101 by 21720: %+v", cmds)
	}
}

// TestB1Miefa 灭法10（21710，param 100）：目标有仙衣时伤害 +100%，
// 且 100% 清除无敌。敌方仙衣1（20331）+ 光阴碎4（21424，开场无敌），
// 我方 100 攻出手应打出 200（无敌若未清除则为 0）。
func TestB1Miefa(t *testing.T) {
	t1 := []*Fighter{{Pos: 1, Att: 100, HP: 500, MaxHP: 500, Speed: 10, Talents: []int32{21710}}}
	t2 := []*Fighter{{Pos: 101, Att: 10, HP: 500, MaxHP: 500, Speed: 1, Talents: []int32{20331, 21424}}}
	res, cmds := simCheck(t, t1, t2, 3)
	if res.Skills[famMieFa.String()] == 0 {
		t.Fatal("miefa not counted")
	}
	if hasCmd(cmds, CmdAttChange, 21710, -1, -1) { // 伤害提高在结算时乘，不改攻击（用户确认）
		t.Fatalf("灭法 changed attack: %+v", cmds)
	}
	got := subHPTo(cmds, 101)
	if len(got) == 0 || got[0] != 200 {
		t.Fatalf("first SUB_HP to 101 = %v, want 200 (immune cleared, +100%% dmg)", got)
	}
}

// TestB1Ganlu 甘露4（21354，param 80）：入阵随机复活一名已死友方，
// 生命为上限 80% 且带锢魂。前阵摆满 5 人，载体后阵待命；第一回合敌方
// 打死对位三人，第二回合初载体补位入阵，复活其中一具（上限 10 → 8 血）。
func TestB1Ganlu(t *testing.T) {
	t1 := []*Fighter{
		{Pos: 1, Att: 5, HP: 10, MaxHP: 10, Speed: 1},
		{Pos: 2, Att: 5, HP: 10, MaxHP: 10, Speed: 1},
		{Pos: 3, Att: 5, HP: 10, MaxHP: 10, Speed: 1},
		{Pos: 4, Att: 5, HP: 500, MaxHP: 500, Speed: 1},
		{Pos: 5, Att: 5, HP: 500, MaxHP: 500, Speed: 1},
		{Pos: 6, Att: 5, HP: 500, MaxHP: 500, Speed: 1, Talents: []int32{21354}},
	}
	t2 := []*Fighter{
		{Pos: 101, Att: 100, HP: 500, MaxHP: 500, Speed: 10},
		{Pos: 102, Att: 100, HP: 500, MaxHP: 500, Speed: 10},
		{Pos: 103, Att: 100, HP: 500, MaxHP: 500, Speed: 10},
	}
	res, cmds := simCheck(t, t1, t2, 5)
	if res.Skills[famGanLu.String()] == 0 {
		t.Fatal("ganlu not counted")
	}
	if !hasCmd(cmds, CmdRelive, 21354, -1, 8) {
		t.Fatalf("no RELIVE 8hp by 21354: %+v", cmds)
	}
}

// TestB1Guaxiang 卦象4（20854，param 4;40）：蓄力满 4 后攻击时给全体友方
// 物理结界（每人 40% 自身上限）。4 名敌方先手各打一下（10 伤），
// 蓄满后释放：1000 上限 → 400 点结界。
func TestB1Guaxiang(t *testing.T) {
	t1 := []*Fighter{{Pos: 1, Att: 50, HP: 1000, MaxHP: 1000, Speed: 1, Talents: []int32{20854}}}
	t2 := []*Fighter{
		{Pos: 101, Att: 10, HP: 200, MaxHP: 200, Speed: 10},
		{Pos: 102, Att: 10, HP: 200, MaxHP: 200, Speed: 10},
		{Pos: 103, Att: 10, HP: 200, MaxHP: 200, Speed: 10},
		{Pos: 104, Att: 10, HP: 200, MaxHP: 200, Speed: 10},
	}
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famGuaXiang.String()] == 0 {
		t.Fatal("guaxiang not counted")
	}
	if !hasCmd(cmds, CmdShield, 1, 1, 400) { // op1 加盾，无招式号
		t.Fatalf("no SHIELD +400 to pos 1: %+v", cmds)
	}
}

// TestB1Daozhishi 道之始4（20974，param 60）：入阵时我方全体回 60% 上限。
// 前阵摆满，载体后阵待命；第一回合敌方打残 slot2（70→40），第二回合初
// 载体补位入阵，slot2 回 60 点。
func TestB1Daozhishi(t *testing.T) {
	t1 := []*Fighter{
		{Pos: 1, Att: 5, HP: 10, MaxHP: 10, Speed: 1},
		{Pos: 2, Att: 5, HP: 70, MaxHP: 100, Speed: 1},
		{Pos: 3, Att: 5, HP: 500, MaxHP: 500, Speed: 1},
		{Pos: 4, Att: 5, HP: 500, MaxHP: 500, Speed: 1},
		{Pos: 5, Att: 5, HP: 500, MaxHP: 500, Speed: 1},
		{Pos: 6, Att: 5, HP: 500, MaxHP: 500, Speed: 1, Talents: []int32{20974}},
	}
	t2 := []*Fighter{
		{Pos: 101, Att: 100, HP: 500, MaxHP: 500, Speed: 10},
		{Pos: 102, Att: 30, HP: 500, MaxHP: 500, Speed: 10},
	}
	res, cmds := simCheck(t, t1, t2, 11)
	if res.Skills[famDaoZhiShi.String()] == 0 {
		t.Fatal("daozhishi not counted")
	}
	if !hasCmd(cmds, CmdAddHP, 15158, 2, 60) {
		t.Fatalf("no ADD_HP 60 to pos 2 by 道之始 (15158): %+v", cmds)
	}
}

// TestB1Baicao 百草4（20864，param 5;40）：蓄力满 5 后攻击时回复全体友方
// 40% 上限。5 名敌方先手各打 10（共缺 50 血），释放后应回满 50。
func TestB1Baicao(t *testing.T) {
	t1 := []*Fighter{{Pos: 1, Att: 50, HP: 1000, MaxHP: 1000, Speed: 1, Talents: []int32{20864}}}
	t2 := []*Fighter{
		{Pos: 101, Att: 10, HP: 200, MaxHP: 200, Speed: 10},
		{Pos: 102, Att: 10, HP: 200, MaxHP: 200, Speed: 10},
		{Pos: 103, Att: 10, HP: 200, MaxHP: 200, Speed: 10},
		{Pos: 104, Att: 10, HP: 200, MaxHP: 200, Speed: 10},
		{Pos: 105, Att: 10, HP: 200, MaxHP: 200, Speed: 10},
	}
	res, cmds := simCheck(t, t1, t2, 13)
	if res.Skills[famBaiCao.String()] == 0 {
		t.Fatal("baicao not counted")
	}
	if !hasCmd(cmds, CmdAddHP, 20864, 1, 50) {
		t.Fatalf("no ADD_HP 50 to pos 1 by 20864: %+v", cmds)
	}
}

// TestB1Guangyinsui 光阴碎4（21424，param 10;100）：入阵战意 +10，
// 自身及相邻 100% 获得无敌到回合结束。第一回合敌方应免伤（SUB_HP 0）。
func TestB1Guangyinsui(t *testing.T) {
	t1 := []*Fighter{{Pos: 1, Att: 50, HP: 200, MaxHP: 200, Speed: 10}}
	t2 := []*Fighter{{Pos: 106, Att: 10, HP: 500, MaxHP: 500, Speed: 1, Talents: []int32{21424}}} // 补位入阵
	res, cmds := simCheck(t, t1, t2, 17)
	if res.Skills[famGuangYinSui.String()] == 0 {
		t.Fatal("guangyinsui not counted")
	}
	if !hasCmd(cmds, CmdZhanyi, 1, 0, 10) { // op1 加战意，敌方 +10
		t.Fatalf("no ZHANYI +10 for side 2: %+v", cmds)
	}
	got := subHPTo(cmds, 101)
	if len(got) == 0 || got[0] != 0 {
		t.Fatalf("first SUB_HP to 101 = %v, want 0 (immune)", got)
	}
}

// TestB1Shuizhiyuan 水之元4（20964，param 60;48;5001）：入阵对敌方全体
// 造成 60% 攻击的水伤（100 攻 → 60），48% 概率僵化（BUFF 5001）。
func TestB1Shuizhiyuan(t *testing.T) {
	t1 := []*Fighter{{Pos: 6, Att: 100, HP: 500, MaxHP: 500, Speed: 10, Talents: []int32{20964}}} // 补位入阵
	mk := func() ([]*Fighter, []*Fighter) {
		return CloneTeam(t1), []*Fighter{
			{Pos: 101, Att: 10, HP: 500, MaxHP: 500, Speed: 1},
			{Pos: 102, Att: 10, HP: 500, MaxHP: 500, Speed: 1},
		}
	}
	var res Result
	var cmds []Cmd
	for seed := uint64(0); seed < 200; seed++ {
		a, b := mk()
		res, cmds = simCheck(t, a, b, seed)
		if res.Skills[famShuiZhiYuan.String()] > 0 && hasCmd(cmds, CmdBuff, 5001, -1, -1) {
			break
		}
		if seed == 199 {
			t.Fatal("shuizhiyuan stiff never applied in 200 seeds")
		}
	}
	n := 0
	for _, c := range cmds {
		if c.ID == CmdSubHP && c.Magic == 15157 {
			for _, x := range c.Targets {
				if x[1] != 60 {
					t.Fatalf("shuizhiyuan SUB_HP = %d, want 60", x[1])
				}
				n++
			}
		}
	}
	if n != 2 {
		t.Fatalf("shuizhiyuan hit %d targets, want 2", n)
	}
}

// Batch2 五族单测：Simulate 后用 Replay 校验战报自洽，再断言效果与数值。
// helper（simCheck / simUntilSeed / subHPTo / hasCmd）见 talents_b1_test.go。
// 蓄力三族用同一挂法：载体前阵站 1 位挨打蓄力（受击 +1/回合），第 5 回合释放。

// 蓄力载体队：载体 1 位（200 攻 / 5000 血 / 慢速），2-5 位 5 攻血包；
// 敌方 101 号 2000 血挨载体对位普攻（4 回合 800），其余杂兵。
func chargeCarrier(talentID int32) ([]*Fighter, []*Fighter) {
	mk := func(pos int, att, hp int32) *Fighter {
		return &Fighter{Pos: pos, Att: att, HP: hp, MaxHP: hp, Speed: 1}
	}
	t1 := []*Fighter{
		{Pos: 1, Att: 200, HP: 5000, MaxHP: 5000, Speed: 1, Talents: []int32{talentID}},
		mk(2, 5, 5000), mk(3, 5, 5000), mk(4, 5, 5000), mk(5, 5, 5000),
	}
	mke := func(pos int, att, hp int32) *Fighter {
		return &Fighter{Pos: pos, Att: att, HP: hp, MaxHP: hp, Speed: 10}
	}
	t2 := []*Fighter{
		mke(101, 20, 2000),
		mke(102, 20, 400), mke(103, 20, 400), mke(104, 20, 400), mke(105, 20, 400),
	}
	return t1, t2
}

func countCmd(cmds []Cmd, id int, magic int64, val int64) int {
	n := 0
	for _, c := range cmds {
		if c.ID != id || (magic >= 0 && c.Magic != magic) {
			continue
		}
		for _, x := range c.Targets {
			if val < 0 || x[1] == val {
				n++
			}
		}
	}
	return n
}

// TestB2Xuanyuan 轩辕剑4（20874，4;100）：第 5 回合释放，以 100% 攻击三连击
// （200×3），之后普攻照常。101 号 400 血：两连击打死，第三击改打当前对位。
func TestB2Xuanyuan(t *testing.T) {
	t1, t2 := chargeCarrier(20874)
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famXuanYuanJian.String()] == 0 {
		t.Fatal("xuanyuanjian not counted")
	}
	if !hasCmd(cmds, CmdActDo, 20874, -1, -1) {
		t.Fatalf("no ACT_DO 20874: %+v", cmds)
	}
	if n := countCmd(cmds, CmdSubHP, attackNormal, 200); n < 3 {
		t.Fatalf("SUB_HP normal 200 x %d, want >= 3 (triple + normal attack)", n)
	}
}

// TestB2Huabing 哗兵4（21324，4;200）：第 5 回合释放，敌方最高攻者（103 号
// 300 攻）流失 600 生命（非物理伤害，招式号 21324）。
func TestB2Huabing(t *testing.T) {
	t1, t2 := chargeCarrier(21324)
	t2[2].Att = 300
	t2[2].HP, t2[2].MaxHP = 5000, 5000
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famHuaBing.String()] == 0 {
		t.Fatal("huabing not counted")
	}
	if !hasCmd(cmds, CmdSubHP, 21324, 103, 600) {
		t.Fatalf("no SUB_HP 600 to 103 by 21324: %+v", cmds)
	}
}

// TestB2Qiankunpo 乾坤破4（21404，4;30;10）：第 5 回合释放，以 30% 攻击（60）
// 对当前生命最低者连射 10 次；102 号 300 血（挨 4 回合血包普攻剩 280）吃 5 箭
// 阵亡，剩下 5 箭打次低者，仍是每箭 60。
func TestB2Qiankunpo(t *testing.T) {
	t1, t2 := chargeCarrier(21404)
	t2[1].HP, t2[1].MaxHP = 300, 300
	for i := 2; i < 5; i++ {
		t2[i].HP, t2[i].MaxHP = 5000, 5000
	}
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famQianKunPo.String()] == 0 {
		t.Fatal("qiankunpo not counted")
	}
	if !hasCmd(cmds, CmdActDo, 21404, -1, -1) {
		t.Fatalf("no ACT_DO 21404: %+v", cmds)
	}
	// 官方战报：每一射是 SUB_HP(乾坤破)，不是普攻
	if n := countCmd(cmds, CmdSubHP, 21404, 60); n < 10 {
		t.Fatalf("SUB_HP 21404 60 x %d, want >= 10 (c=10 volley, may fire twice in 30 rounds)", n)
	}
}

// TestB2ZhuxianSwords 诛仙阵4（20984，param 4）：入阵携 4 柄仙剑分给本方
// （5 人取 4），每人至多 1 柄：SWORD_EARTH ×4；攻击后剑伤照布剑机制打出。
func TestB2ZhuxianSwords(t *testing.T) {
	mk := func(pos int) *Fighter {
		f := &Fighter{Pos: pos, Att: 100, HP: 2000, MaxHP: 2000, Speed: 1}
		if pos == 1 { // 放在后阵 6 号位，第 1 回合补到 1 号位入阵
			f.Pos, f.Talents = 6, []int32{20984}
		}
		return f
	}
	t1 := []*Fighter{mk(1), mk(2), mk(3), mk(4), mk(5)}
	t2 := []*Fighter{{Pos: 101, Att: 10, HP: 10000, MaxHP: 10000, Speed: 1}}
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famZhuXianZhen.String()] == 0 {
		t.Fatal("zhuxianzhen not counted")
	}
	// SWORD_EARTH/SWORD_ATTACK 指令无 targets（剑数在 Actor 后字节里），按 ID 计数。
	countID := func(id int) int {
		n := 0
		for _, c := range cmds {
			if c.ID == id {
				n++
			}
		}
		return n
	}
	if n := countID(CmdSwordEarth) + countID(28) + countID(29) + countID(30) + countID(39); n != 4 { // 添剑命令按仙剑属性
		t.Fatalf("add-sword x %d, want 4", n)
	}
	if n := countID(CmdSwordAttack); n == 0 {
		t.Fatalf("no SWORD_ATTACK after zhuxianzhen swords")
	}
}

// TestB2ZhuxianAtt 诛仙阵12系（120984，param 20）：入阵本方全体永久 +20%
// 攻击（100 攻 → +20，无攻击上限概念，见 talents_b2.go 注释）。
func TestB2ZhuxianAtt(t *testing.T) {
	mk := func(pos int) *Fighter {
		f := &Fighter{Pos: pos, Att: 100, HP: 2000, MaxHP: 2000, Speed: 1}
		if pos == 3 { // 放在后阵 6 号位，第 1 回合补到 3 号位入阵
			f.Pos, f.Talents = 6, []int32{120984}
		}
		return f
	}
	t1 := []*Fighter{mk(1), mk(2), mk(3), mk(4), mk(5)}
	t2 := []*Fighter{{Pos: 101, Att: 10, HP: 10000, MaxHP: 10000, Speed: 1}}
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famZhuXianZhen.String()] == 0 {
		t.Fatal("zhuxianzhen(12) not counted")
	}
	for pos := int64(1); pos <= 5; pos++ {
		if !hasCmd(cmds, CmdAttChange, 120984, pos, 20) {
			t.Fatalf("no ATT_CHANGE +20 to %d by 120984: %+v", pos, cmds)
		}
	}
}

// TestB2Qunyao 群夭诀4（21344，80;5554;48）：载体第 1 回合被杀，敌方三体带
// 早夭（BUFF 5260）；同回合友方普攻命中敌方后，其他带早夭者各分担 50（招式号
// 0 的 SUB_HP）；载体 48% 概率 2 回合后复活 32% 上限（50 → 16）。
func TestB2Qunyao(t *testing.T) {
	mkTeam := func() ([]*Fighter, []*Fighter) {
		t1 := []*Fighter{
			{Pos: 1, Att: 10, HP: 50, MaxHP: 50, Speed: 1, Talents: []int32{21344}},
			{Pos: 2, Att: 100, HP: 5000, MaxHP: 5000, Speed: 5},
		}
		t2 := []*Fighter{
			{Pos: 101, Att: 500, HP: 5000, MaxHP: 5000, Speed: 10},
			{Pos: 102, Att: 10, HP: 5000, MaxHP: 5000, Speed: 2},
			{Pos: 103, Att: 10, HP: 5000, MaxHP: 5000, Speed: 2},
			{Pos: 104, Att: 10, HP: 5000, MaxHP: 5000, Speed: 2},
		}
		return t1, t2
	}
	var res Result
	var cmds []Cmd
	found := false
	for seed := uint64(0); seed < 200; seed++ {
		t1, t2 := mkTeam()
		res, cmds = simCheck(t, t1, t2, seed)
		if res.Skills[famQunYaoJue.String()] > 0 && hasCmd(cmds, CmdRelive, -1, 1, 16) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("qunyao trigger + revive not seen in 200 seeds")
	}
	if n := countCmd(cmds, CmdBuff, 5260, -1); n < 3 {
		t.Fatalf("BUFF 5260 x %d, want >= 3 (zaoyao on 3 foes)", n)
	}
	if n := countCmd(cmds, CmdSubHP, 0, 50); n < 2 {
		t.Fatalf("shared SUB_HP 50 (magic 0) x %d, want >= 2", n)
	}
}

// Batch3 八族单测：Simulate 后用 Replay 校验战报自洽，再断言效果与数值。
// helper（simCheck / subHPTo / hasCmd / countCmd）见 talents_b1_test.go 与 talents_b2_test.go。

func dieCount(cmds []Cmd, pos int64) int {
	n := 0
	for _, c := range cmds {
		if c.ID != CmdDie {
			continue
		}
		for _, x := range c.Targets {
			if pos < 0 || x[0] == pos {
				n++
			}
		}
	}
	return n
}

// rampageCarrier：暴走载体（残血 10%、200 攻、慢速）对位敌方 101（50 攻）。
func rampageCarrier(famID int32) ([]*Fighter, []*Fighter) {
	t1 := []*Fighter{{Pos: 1, Att: 200, HP: 100, MaxHP: 1000, Speed: 1, Talents: []int32{famID}}}
	t2 := []*Fighter{{Pos: 101, Att: 50, HP: 5000, MaxHP: 5000, Speed: 10}}
	return t1, t2
}

// TestB3Moling 魔灵4（21054）：开场濒危暴走，本回合免伤（敌方首击 SUB_HP 0），
// 获赠神通直到战斗结束——赠品[3]避阵10（20450）每回合开始必定治疗（ADD_HP）。
func TestB3Moling(t *testing.T) {
	t1, t2 := rampageCarrier(21054)
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famMoLing.String()] == 0 {
		t.Fatal("moling not counted")
	}
	if res.Skills[famBiZhen.String()] == 0 {
		t.Fatal("gift bizhen not granted (multi-gift failed)")
	}
	got := subHPTo(cmds, 1)
	if len(got) == 0 || got[0] != 0 {
		t.Fatalf("first SUB_HP to 1 = %v, want 0 (rampage immune)", got)
	}
	if !hasCmd(cmds, CmdAddHP, 20450, 1, 200) {
		t.Fatalf("no ADD_HP 200 to 1 by 20450 (gift bizhen): %+v", cmds)
	}
}

// TestB3Jingyue 静岳4（21064）：开场濒危暴走，赠品[3]愈合10（20650）回合结束
// 回复 40% 上限（1000 → 400）。
func TestB3Jingyue(t *testing.T) {
	t1, t2 := rampageCarrier(21064)
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famJingYue.String()] == 0 {
		t.Fatal("jingyue not counted")
	}
	if res.Skills[famYuHe.String()] == 0 {
		t.Fatal("gift yuhe not granted (multi-gift failed)")
	}
	if !hasCmd(cmds, CmdAddHP, 20650, 1, 400) {
		t.Fatalf("no ADD_HP 400 to 1 by 20650 (gift yuhe): %+v", cmds)
	}
}

// TestB3Yinren 隐忍5（21025）：开场濒危暴走，赠品[3]反击10（20700）在受击后
// 还击——切种子直到“反击”演出出现（载体无天生反击，出现即证明获赠）。
func TestB3Yinren(t *testing.T) {
	found := false
	for seed := uint64(0); seed < 200; seed++ {
		t1 := []*Fighter{{Pos: 1, Att: 100, HP: 400, MaxHP: 4000, Speed: 1, Talents: []int32{21025}}}
		t2 := []*Fighter{{Pos: 101, Att: 50, HP: 20000, MaxHP: 20000, Speed: 10}}
		res, _ := simCheck(t, t1, t2, seed)
		if res.Skills[famYinRen.String()] > 0 && res.Skills[famFanJi.String()] > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("yinren rampage + gift fanji never seen in 200 seeds")
	}
}

// TestB3Yinyang 阴阳乱4（21434）：开场濒危暴走，赠品[2]愈合5（20645）回合结束
// 回复 20% 上限（1000 → 200）。
func TestB3Yinyang(t *testing.T) {
	t1, t2 := rampageCarrier(21434)
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famYinYangLuan.String()] == 0 {
		t.Fatal("yinyangluan not counted")
	}
	if res.Skills[famYuHe.String()] == 0 {
		t.Fatal("gift yuhe5 not granted (multi-gift failed)")
	}
	if !hasCmd(cmds, CmdAddHP, 20645, 1, 200) {
		t.Fatalf("no ADD_HP 200 to 1 by 20645 (gift yuhe5): %+v", cmds)
	}
}

// TestB3Haihun 还魂10（20510，70;100）：载体首回合被杀，70% 概率 2 回合后
// 复活满血（50 → RELIVE 50）。载体另带血包队友撑住战斗，否则首杀即判负、
// 返死来不及结算。切种子直到复活出现。
func TestB3Haihun(t *testing.T) {
	mkTeam := func() ([]*Fighter, []*Fighter) {
		t1 := []*Fighter{
			{Pos: 1, Att: 10, HP: 50, MaxHP: 50, Speed: 1, Talents: []int32{20510}},
			{Pos: 2, Att: 10, HP: 5000, MaxHP: 5000, Speed: 1},
		}
		t2 := []*Fighter{{Pos: 101, Att: 500, HP: 20000, MaxHP: 20000, Speed: 10}}
		return t1, t2
	}
	var cmds []Cmd
	found := false
	for seed := uint64(0); seed < 200; seed++ {
		t1, t2 := mkTeam()
		res, c := simCheck(t, t1, t2, seed)
		cmds = c
		if res.Skills[famHuanHun.String()] > 0 && hasCmd(c, CmdRelive, -1, 1, 50) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("haihun trigger + revive not seen in 200 seeds")
	}
	_ = cmds
}

// TestB3Bumie 不灭躯1（21151，10;1;5402）：载体残血 40/1000 首回合被杀（敌 40 攻），
// 满血复活（RELIVE 1000）攻击 +10%，3 回合结束时自杀（同位 DIE ×2），且只复活一次。
func TestB3Bumie(t *testing.T) {
	t1 := []*Fighter{{Pos: 1, Att: 100, HP: 40, MaxHP: 1000, Speed: 1, Talents: []int32{21151}}}
	t2 := []*Fighter{{Pos: 101, Att: 40, HP: 10000, MaxHP: 10000, Speed: 10}}
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famBuMieQu.String()] == 0 {
		t.Fatal("bumiequ not counted")
	}
	if !hasCmd(cmds, CmdRelive, 21151, 1, 1000) {
		t.Fatalf("no RELIVE 1000 to 1 by 21151: %+v", cmds)
	}
	if !hasCmd(cmds, CmdAttChange, 21151, 1, 10) {
		t.Fatalf("no ATT_CHANGE +10 to 1 by 21151: %+v", cmds)
	}
	if n := dieCount(cmds, 1); n != 2 {
		t.Fatalf("DIE to 1 x %d, want 2 (killed + doom suicide)", n)
	}
	if n := countCmd(cmds, CmdRelive, -1, -1); n != 1 {
		t.Fatalf("RELIVE x %d, want 1 (no second revive)", n)
	}
}

// TestB3Guixu 归墟阵1（21161，15;5551;12）：载体首回合被杀，敌方全体受 15%
// 攻击的土伤（200 → 30）；捆缚（BUFF 5231）与复活（RELIVE 80）切种子验证。
func TestB3Guixu(t *testing.T) {
	mkTeam := func() ([]*Fighter, []*Fighter) {
		t1 := []*Fighter{
			{Pos: 1, Att: 200, HP: 40, MaxHP: 1000, Speed: 1, Talents: []int32{21161}},
			{Pos: 2, Att: 10, HP: 8000, MaxHP: 8000, Speed: 1},
		}
		t2 := []*Fighter{
			{Pos: 101, Att: 40, HP: 5000, MaxHP: 5000, Speed: 10},
			{Pos: 102, Att: 10, HP: 5000, MaxHP: 5000, Speed: 2},
			{Pos: 103, Att: 10, HP: 5000, MaxHP: 5000, Speed: 2},
		}
		return t1, t2
	}
	t1, t2 := mkTeam()
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famGuiXuZhen.String()] == 0 {
		t.Fatal("guixuzhen not counted")
	}
	for _, pos := range []int64{101, 102, 103} {
		if !hasCmd(cmds, CmdSubHP, 15160, pos, 30) {
			t.Fatalf("no SUB_HP 30 to %d by 归墟阵 (15160): %+v", pos, cmds)
		}
	}
	bindFound := false
	for seed := uint64(0); seed < 200; seed++ {
		a, b := mkTeam()
		_, c := simCheck(t, a, b, seed)
		if countCmd(c, CmdBuff, 5231, -1) > 0 {
			bindFound = true
			break
		}
	}
	if !bindFound {
		t.Fatal("guixuzhen bind (BUFF 5231) never seen in 200 seeds")
	}
	reviveFound := false
	for seed := uint64(0); seed < 200; seed++ {
		a, b := mkTeam()
		_, c := simCheck(t, a, b, seed)
		if hasCmd(c, CmdRelive, -1, 1, 80) {
			reviveFound = true
			break
		}
	}
	if !reviveFound {
		t.Fatal("guixuzhen revive (RELIVE 80) never seen in 200 seeds")
	}
}

// TestB3Guiyuan 归元1（21461，10;12）：朱雀首回合被杀 → ACT_DO(归元 → 自己) → CHG_FIGHTER(火之卵 54004，
// 生命 1000×12% = 120、攻击 200×10% = 20) → 锢魂；朱雀死时不放火法。火之卵每次行动放火之魂法术 17022（20 点、
// 敌方全体），从不普攻；火之卵死亡时放火之魂神通 21471（官方战报 session-02）。
func TestB3Guiyuan(t *testing.T) {
	t1 := []*Fighter{{Pos: 1, Att: 200, HP: 40, MaxHP: 1000, Speed: 1, Talents: []int32{21461}}}
	t2 := []*Fighter{
		{Pos: 101, Att: 40, HP: 5000, MaxHP: 5000, Speed: 10},
		{Pos: 102, Att: 10, HP: 5000, MaxHP: 5000, Speed: 2},
	}
	res, cmds := simCheck(t, t1, t2, 7)
	if res.Skills[famGuiYuan.String()] == 0 {
		t.Fatal("guiyuan not counted")
	}
	chg := -1
	for i, c := range cmds {
		if c.ID == CmdChgFighter {
			chg = i
			if c.Actor != 1 || c.Magic != eggShape || c.Targets[0][1] != 120 {
				t.Fatalf("CHG_FIGHTER %+v", c)
			}
			if p := cmds[i-1]; p.ID != CmdActDo || p.Magic != 21461 || p.Targets[0][0] != 1 {
				t.Fatalf("before CHG_FIGHTER %+v", p)
			}
		}
		if c.ID == CmdSubHP && c.Magic == 21461 {
			t.Fatal("朱雀 death should not cast fire")
		}
		if chg >= 0 && c.ID == CmdActDo && c.Actor == 1 && c.Magic <= attackDodge {
			t.Fatalf("egg physical attack %+v", c)
		}
	}
	if chg < 0 || !hasCmd(cmds, CmdSubHP, eggMagic, 101, 20) || !hasCmd(cmds, CmdBuff, 5141, 1, -1) {
		t.Fatalf("no egg / egg nova / 锢魂: %+v", cmds)
	}
	if !hasCmd(cmds, CmdSubHP, eggDeathTalent, 101, -1) { // 火之卵自带疯魔10，攻击会涨
		t.Fatalf("egg death 火之魂 missing: %+v", cmds)
	}
}
