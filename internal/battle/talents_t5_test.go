package battle

// T5 补的神通族：每族构造双方 Fighter，Simulate 后 Replay 校验战报自洽，再断言该族的效果与数值。
// 有概率的族在种子 0-199 里找一场满足断言的（同时要求该族被演出过，fam 为空则不要求）。

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"turnrpg/internal/gamedata"
)

func fu(pos int, att, hp int32, speed int, tals ...int32) *Fighter {
	return &Fighter{Pos: pos, Att: att, HP: hp, MaxHP: hp, Speed: speed, Talents: tals}
}

// fuh 是带伤的单位（hp < maxHP）。
func fuh(pos int, att, hp, maxHP int32, speed int, tals ...int32) *Fighter {
	f := fu(pos, att, hp, speed, tals...)
	f.MaxHP = maxHP
	return f
}

func raced(f *Fighter, race int) *Fighter {
	f.Race = race
	return f
}

type famCase struct {
	t1, t2 []*Fighter
	fam    family // 要求被演出的神通族（0 = 不要求）
	check  func(res Result, cmds []Cmd) string
}

func runFamCase(t *testing.T, i int, c famCase) {
	t.Helper()
	last := "never triggered"
	for seed := uint64(0); seed < 200; seed++ {
		res, cmds := simCheck(t, CloneTeam(c.t1), CloneTeam(c.t2), seed)
		if c.fam != 0 && res.Skills[c.fam.String()] == 0 {
			continue
		}
		if last = c.check(res, cmds); last == "" {
			return
		}
	}
	t.Fatalf("case %d (%v): %s", i, c.fam, last)
}

// want 把一串 (条件, 说明) 变成 check 的返回值：第一个不成立的说明。
func want(conds ...any) string {
	for i := 0; i+1 < len(conds); i += 2 {
		if !conds[i].(bool) {
			return conds[i+1].(string)
		}
	}
	return ""
}

func contains(l []int64, v int64) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func ok(Result, []Cmd) string { return "" }

func anyCmd(cmds []Cmd, id int) bool {
	for _, c := range cmds {
		if c.ID == id {
			return true
		}
	}
	return false
}

func TestT5Families(t *testing.T) {
	cases := []famCase{
		// ---- 攻击前 ----
		// 驱魔10 攻击魔族 +75% 初始攻击
		{fam: famQuMo,
			t1: []*Fighter{fu(1, 100, 1000, 10, 20100)}, t2: []*Fighter{raced(fu(101, 1, 10000, 1), raceGhost)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdAttChange, 20100, 1, 75), "no +75", contains(subHPTo(c, 101), 175), "no 175 hit")
			}},
		// 诛仙10 攻击仙族
		{fam: famZhuXian,
			t1: []*Fighter{fu(1, 100, 1000, 10, 20080)}, t2: []*Fighter{raced(fu(101, 1, 10000, 1), raceGod)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 20080, 1, 75), "no +75") }},
		// 斗兽10 攻击兽族
		{fam: famDouShou,
			t1: []*Fighter{fu(1, 100, 1000, 10, 20110)}, t2: []*Fighter{raced(fu(101, 1, 10000, 1), raceBeast)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 20110, 1, 75), "no +75") }},
		// 兽性10 攻击人族
		{fam: famShouXing,
			t1: []*Fighter{fu(1, 100, 1000, 10, 20090)}, t2: []*Fighter{raced(fu(101, 1, 10000, 1), raceHuman)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 20090, 1, 75), "no +75") }},
		// 以命演天4 神之护3 + 战呼3 给最高攻
		{fam: famYiMingYanTian,
			t1: []*Fighter{fu(1, 10, 1000, 1, 22098), fu(2, 50, 1000, 1)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdBuff, 5213, 2, -1), "no buff 5213", hasCmd(c, CmdChgMaxHP, 5213, 2, 90), "no +90 max",
					hasCmd(c, CmdBuff, 5263, 2, -1), "no buff 5263 on top att", hasCmd(c, CmdAttChange, 0, 2, 15), "no +30% att")
			}},
		// 九尾仙车4 狐尾 + 咆哮
		{fam: famJiuWeiXianChe,
			t1: []*Fighter{fu(1, 10, 1000, 1, 22094), fu(2, 0, 1000, 1)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdBuff, 5644, 2, -1), "no buff 5644", hasCmd(c, CmdChgMaxHP, 5213, 2, 90), "no +9% max",
					hasCmd(c, CmdZhanyi, -1, -1, 5), "no +5 zy")
			}},
		// 摄魂术4 借死者神通
		{fam: famSheHunShu,
			t1: []*Fighter{fu(1, 10, 100000, 1, 21804), fu(2, 0, 10, 1, 20330, 20320)},
			t2: []*Fighter{fu(101, 0, 100000, 1), fu(102, 100, 100000, 10)},
			check: func(r Result, _ []Cmd) string {
				return want(r.Skills[famHuDun.String()]+r.Skills[famShanBi.String()] > 0, "borrowed talent never fired")
			}},
		// ---- 攻击后 / 受击 ----
		// 负重4 相邻 50% 物理 + 嘲讽4
		{fam: famFuZhong,
			t1: []*Fighter{fu(2, 100, 100000, 10, 21764)},
			t2: []*Fighter{fu(101, 1, 100000, 1), fu(102, 1, 100000, 1), fu(103, 1, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 21764, -1, 50), "no 50 splash", hasCmd(c, CmdBuff, 5604, 102, -1), "no taunt on target")
			}},
		// 贪吃4 相邻 100% 物理并回血
		{fam: famTanChi,
			t1: []*Fighter{fuh(2, 100, 500, 100000, 10, 21774)},
			t2: []*Fighter{fu(101, 1, 100000, 1), fu(102, 1, 100000, 1), fu(103, 1, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 21774, -1, 100), "no 100 splash", hasCmd(c, CmdAddHP, 21774, 2, -1), "no self heal")
			}},
		// 象形4 捆缚并吸 10% 攻击
		{fam: famXiangXing,
			t1: []*Fighter{fu(1, 100, 1000, 10, 21554)}, t2: []*Fighter{fu(101, 50, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdBuff, 5231, 101, -1), "no bind", hasCmd(c, CmdAttChange, 21554, 101, -5), "no -5",
					hasCmd(c, CmdAttChange, 21554, 1, 5), "no +5")
			}},
		// 狮吼4 混乱攻击者相邻并吸 8% 上限
		{fam: famShiHou,
			t1: []*Fighter{fu(1, 0, 1000, 1), fu(2, 10, 1000, 10), fu(3, 0, 1000, 1)},
			t2: []*Fighter{fu(102, 1, 100000, 1, 21674)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdChgMaxHP, 21674, -1, -80), "no -80 max", hasCmd(c, CmdBuff, 5051, -1, -1), "no confuse")
			}},
		// 天威降临4 敌我全体追加 40%
		{fam: famTianWeiJiangLin,
			t1: []*Fighter{fu(1, 100, 1000, 10, 22202), fu(2, 0, 1000, 1)},
			t2: []*Fighter{fu(101, 0, 100000, 1), fu(102, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 22202, 2, 40), "ally not hit", hasCmd(c, CmdSubHP, 22202, 102, 40), "foe not hit")
			}},
		// 狂龙4 咆哮再触发
		{fam: famKuangLong,
			t1: []*Fighter{fu(1, 10, 1000, 1, 20220, 22186)}, t2: []*Fighter{fu(101, 0, 100000, 1)}, check: ok},
		// 横冲直撞4 击败后追击
		{fam: famHengChongZhiZhuang,
			t1: []*Fighter{fu(1, 1000, 1000, 10, 22206)}, t2: []*Fighter{fu(101, 0, 10, 1), fu(102, 0, 100000, 1)}, check: ok},
		// 火炮之术4 弹丸 24% 初始攻击
		{fam: famHuoPaoZhiShu,
			t1: []*Fighter{fu(1, 100, 1000, 1, 22158), fu(2, 0, 1000, 1, 20050)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdSubHP, 22158, 101, 24), "no 24 pellet") }},
		// ---- 受到伤害 ----
		// 不侵10 抵抗捆缚
		{fam: famBuQin,
			t1: []*Fighter{fu(1, 100, 1000, 10, 21554)}, t2: []*Fighter{fu(101, 1, 100000, 1, 20040)},
			check: func(_ Result, c []Cmd) string { return want(!hasCmd(c, CmdBuff, 5231, 101, -1), "bind landed") }},
		// 分水10 水伤等值反弹
		{fam: famFenShui,
			t1: []*Fighter{fu(6, 100, 1000, 1, 21784)}, t2: []*Fighter{fu(101, 1, 100000, 1, 20410)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdSubHP, 20410, 1, 80), "no 80 reflect") }},
		// 寒冰甲4 免水伤并僵化施法者
		{fam: famHanBingJia,
			t1: []*Fighter{fu(6, 100, 1000, 1, 21784)}, t2: []*Fighter{fu(101, 1, 100000, 1, 21544)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 21784, 101, 0), "water not nullified", hasCmd(c, CmdBuff, 5001, 1, -1), "caster not stiff")
			}},
		// 五指山4 受攻击 -24% 与随机负面
		{fam: famWuZhiShan,
			t1: []*Fighter{fu(1, 100, 1000, 10), fu(2, 100, 1000, 10)}, t2: []*Fighter{fu(101, 1, 100000, 1, 21494)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdSubHP, attackNormal, 101, 76), "no 76 hit") }},
		// 水易伤10 龙跃 80 → 280
		{t1: []*Fighter{fu(6, 100, 1000, 1, 21784)}, t2: []*Fighter{fu(101, 1, 100000, 1, 130120)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdSubHP, 21784, 101, 280), "no 280") }},
		// 返璞归真4 减伤 70% 并转移 40%
		{fam: famFanPuGuiZhen,
			t1: []*Fighter{fu(1, 0, 1000, 1, 21904)}, t2: []*Fighter{fu(101, 100, 100000, 10)},
			check: func(_ Result, c []Cmd) string {
				got := subHPTo(c, 1)
				return want(len(got) > 0 && got[0] == 30, fmt.Sprintf("first hit %v, want 30", got),
					hasCmd(c, CmdSubHP, 21904, 101, 40), "no 40 transfer")
			}},
		// 月之暗面4 触发了但暴走护法都不满足条件（生命 ≤25%）时获得神通
		{fam: famYueZhiAnMian,
			t1: []*Fighter{fu(1, 10, 100000, 1, 22054), fuh(2, 0, 100, 100000, 1, 21031)}, t2: []*Fighter{fu(101, 10, 100000, 10)},
			check: func(r Result, _ []Cmd) string {
				return want(r.Skills[famHuDun.String()] > 0, "granted shield talent never fired")
			}},
		// 月之暗面4 把 70% 伤害以生命流失转给后阵的暴走护法（6 号位优先于前阵）
		{fam: famYueZhiAnMian,
			t1: []*Fighter{fu(1, 10, 100000, 1, 22054), fu(2, 0, 100000, 1, 21031), fu(3, 0, 100000, 1), fu(4, 0, 100000, 1),
				fu(5, 0, 100000, 1), fu(6, 0, 100000, 1, 21031)},
			t2: []*Fighter{fu(101, 1000, 100000, 10)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 22054, 6, 700), "no 700 transfer to slot 6")
			}},
		// 雷影庇护4 代遁让护法承受致命伤
		{fam: famLeiYingBiHu,
			t1: []*Fighter{fu(1, 0, 100000, 1, 22198), fu(2, 0, 10, 1, 20310), fu(3, 0, 100000, 1), fu(4, 0, 100000, 1),
				fu(5, 0, 100000, 1), fu(6, 0, 1000, 1)}, // 前阵站满，MOVE 只能来自遁让换位
			t2: []*Fighter{fu(102, 100, 100000, 10)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 22198, 1, 100), "carrier did not take 100", anyCmd(c, CmdMove), "no swap")
			}},
		// 万仙来朝4 阵眼伤害变生命流失（跳过水易伤）
		{t1: []*Fighter{fu(1, 100, 1000, 10, 20750), fu(2, 0, 1000, 1, 22004)},
			t2: []*Fighter{fu(101, 0, 100000, 1, 130120)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 20750, 101, 30), "no raw 30", hasCmd(c, CmdSubHP, 20750, 101, 105), "no 105 (vuln)")
			}},
		// ---- 蓄力 ----
		// 六艺4
		{fam: famLiuYi, t1: chargeT1(21514), t2: chargeT2(), check: ok},
		// 刃舞4 挥扫 80%×200
		{fam: famRenWu, t1: chargeT1(21534), t2: chargeT2(),
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdSubHP, 21534, -1, 160), "no 160 sweep") }},
		// 剑歌4 每层 +16% 当前攻击
		{fam: famJianGe, t1: chargeT1(21684), t2: chargeT2(),
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 21684, 1, 32), "no +32") }},
		// 枪出游龙4 160% 二连击
		{fam: famQiangChuYouLong, t1: chargeT1(22142), t2: chargeT2(),
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdSubHP, -1, -1, 320), "no 320 hit") }},
		// 脱帽退敌4 每层蓄力减伤 20%
		{t1: chargeT1(21778), t2: chargeT2(),
			check: func(_ Result, c []Cmd) string {
				got := subHPTo(c, 1)
				return want(contains(got, 16) && contains(got, 12), fmt.Sprintf("hits on carrier %v, want 16 and 12", got))
			}},
		// 龙虎仙丹4 物理结界 + 续命丹
		{fam: famLongHuXianDan, t1: chargeT1(22014), t2: chargeT2(),
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdShield, 1, 1, 4000), "no 4000 shield", hasCmd(c, CmdBuff, 5611, -1, -1), "no buff 5611")
			}},
		// ---- 回合开始 ----
		// 猎命4 暴走获赠守卫/闪避/反击/狂热
		{fam: famLieMing, t1: rampT1(21504), t2: rampT2(),
			check: func(r Result, _ []Cmd) string {
				return want(r.Skills[famShanBi.String()]+r.Skills[famFanJi.String()]+r.Skills[famKuangRe.String()]+r.Skills[famShouWei.String()] > 0, "gifts never fired")
			}},
		// 舞兮江歌4 暴走获赠先攻/背水/愈合/灵佑
		{fam: famWuXiJiangGe, t1: rampT1(22138), t2: rampT2(),
			check: func(r Result, _ []Cmd) string {
				return want(r.Skills[famXianGong.String()]+r.Skills[famBeiShui.String()]+r.Skills[famYuHe.String()]+r.Skills[famLingYou.String()] > 0, "gifts never fired")
			}},
		// 水之形4 吸取更高攻者 20%
		{fam: famShuiZhiXing,
			t1: []*Fighter{fu(1, 50, 10000, 1, 21754)}, t2: []*Fighter{fu(101, 100, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdAttChange, 21754, 101, -20), "no -20", hasCmd(c, CmdAttChange, 21754, 1, 20), "no +20")
			}},
		// 魔龙4 吸取敌方法术结界
		{fam: famMoLong,
			t1: []*Fighter{fu(1, 100, 100000, 1, 21694)}, t2: []*Fighter{fu(101, 10, 100000, 10, 21305)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdMagicShield, 2, 101, 2), "no -2 from foe", hasCmd(c, CmdMagicShield, 1, 1, 2), "no +2 self")
			}},
		// 乌江之殇4 扣左右 33%（2%×剩余 9 回合 + 15%）当前生命换结界 + 战呼2
		{fam: famWuJiangZhiShang,
			t1: []*Fighter{fu(1, 0, 1000, 1), fu(2, 10, 1000, 1, 22064), fu(3, 0, 1000, 1)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 22064, 1, 330), "no 330 cost", hasCmd(c, CmdShield, 1, 1, 330), "no 330 shield",
					hasCmd(c, CmdBuff, 5262, 1, -1), "no buff 5262")
			}},
		// 修罗隐身4 两次死亡技死亡后隐身
		{fam: famXiuLuoYinShen,
			t1:    []*Fighter{fu(1, 10, 100000, 1, 22074), fu(2, 0, 10, 1, 20510), fu(3, 0, 10, 1, 20510)},
			t2:    []*Fighter{fu(102, 100, 100000, 10), fu(103, 100, 100000, 10)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdBuff, 5111, 1, -1), "no buff 5111") }},
		// 十日圣母4 献祭 999，逆势护法每回合 +239 上限
		{fam: famShiRiShengMu,
			t1: []*Fighter{fu(1, 0, 1000, 1, 22162), fu(2, 0, 1000, 1, 20790)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 22162, 1, 999), "no sacrifice", hasCmd(c, CmdChgMaxHP, 22162, 2, 239), "no +239")
			}},
		// 藏剑于花4 召唤仙剑
		{fam: famCangJianYuHua,
			t1: []*Fighter{fu(1, 10, 1000, 1, 22146)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string { return want(anyCmd(c, CmdSwordEarth), "no SWORD_EARTH") }},
		// 疯狂4 获得疯魔 80% 攻击
		{fam: famFengKuang,
			t1: []*Fighter{fu(1, 100, 1000, 1, 22194), fu(2, 10, 1000, 1, 20060)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 22194, 1, 80), "no +80") }},
		// 魔欲入体4 元形时额外吸血
		{fam: famMoYuRuTi,
			t1: []*Fighter{raced(fu(1, 10, 1000, 1, 22166), raceGhost), raced(fu(2, 10, 1000, 1, 20180), raceGhost),
				raced(fu(3, 0, 1000, 1), raceGhost), raced(fu(4, 0, 1000, 1), raceGhost)},
			t2:    []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdSubHP, 22166, -1, -1), "no drain") }},
		// 元形10：ACT_DO 只指向自己；左右吸血合在一条 SUB_HP（两个目标）；自己挂 BUFF 5371（官方战报）
		{fam: famYuanXing,
			t1: []*Fighter{fu(1, 10, 1000, 1), fu(2, 10, 1000, 1, 20180), fu(3, 10, 1000, 1)},
			t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				selfOnly, twoTargets := false, false
				for _, x := range c {
					if x.ID == CmdActDo && x.Magic == 20180 {
						selfOnly = len(x.Targets) == 1 && x.Targets[0][0] == 2
					}
					if x.ID == CmdSubHP && x.Magic == 20180 && len(x.Targets) == 2 {
						twoTargets = true
					}
				}
				return want(selfOnly, "ACT_DO not self-only", twoTargets, "drain not in one SUB_HP", hasCmd(c, CmdBuff, 5371, 2, -1), "no buff 5371")
			}},
		// 竭力咒4：死亡时 ACT_DO 指向所有敌方，各挂 BUFF 5484 并再降 5% 攻击（官方战报）
		{fam: famJieLiZhou,
			t1: []*Fighter{fu(1, 0, 10, 1, 21114)},
			t2: []*Fighter{fu(101, 100, 100000, 10), fu(102, 100, 100000, 10)},
			check: func(_ Result, c []Cmd) string {
				all := false
				for _, x := range c {
					if x.ID == CmdActDo && x.Magic == 21114 {
						all = len(x.Targets) == 2
					}
				}
				return want(all, "ACT_DO not on all foes", hasCmd(c, CmdBuff, 5484, 101, -1), "no buff 5484",
					hasCmd(c, CmdAttChange, 5484, 101, -1), "no 5% drop")
			}},
		// ---- 回合结束 ----
		// 龙威4 吸友军 140 生命换法术结界
		{fam: famLongWei,
			t1: []*Fighter{fuh(1, 100, 500, 1000, 1, 21484), fu(2, 0, 1000, 1)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 21484, 2, 140), "no 140 drain", hasCmd(c, CmdAddHP, 21484, 1, 140), "no heal",
					hasCmd(c, CmdMagicShield, 1, 2, 140), "no shield")
			}},
		// 审死4 偶数回合处决队友
		{fam: famShenSi,
			t1: []*Fighter{fuh(1, 10, 100, 1000, 1, 21654), fuh(2, 50, 500, 1000, 1)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 21654, 2, 500), "no execution", hasCmd(c, CmdAddHP, 21654, 1, 240), "no 240 heal",
					hasCmd(c, CmdSubHP, 21654, 101, 24), "no 24 earth")
			}},
		// 命数由我4 剩余回合 × 20% 目标攻击
		{fam: famMingShuYouWo,
			t1: []*Fighter{fu(1, 10, 1000, 1, 22024)}, t2: []*Fighter{fu(101, 10, 100000, 1)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdSubHP, 22024, 101, 18), "no 18") }},
		// 钉头七箭4 七箭各 20%
		{fam: famDingTouQiJian,
			t1: []*Fighter{fu(1, 100, 1000, 1, 22084)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(countCmd(c, CmdSubHP, 22084, 20) >= 7, "fewer than 7 arrows")
			}},
		// 飞仙4 入阵护法获得神之力7
		{fam: famFeiXian,
			t1: []*Fighter{fu(1, 10, 1000, 1, 22034), fu(2, 100, 1000, 1, 20974)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 5207, 2, 21), "no +21") }},
		// 飞仙4 增益后遁入后阵并撤销增益（神之力 / 神之护只在前阵存活期间有效）
		{fam: famFeiXian,
			t1: []*Fighter{fu(1, 10, 1000, 1, 22034), fu(2, 100, 1000, 1, 20974), fu(3, 0, 5000, 1), fu(4, 0, 5000, 1),
				fu(5, 0, 5000, 1), fu(6, 0, 1000, 1)},
			t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdAttChange, 5207, 2, 21), "no +21", anyCmd(c, CmdMove), "no retreat MOVE",
					hasCmd(c, CmdAttChange, -1, 6, -21), "神之力 not reverted on bench")
			}},
		// 天斩4 追击血池目标
		{fam: famTianZhan,
			t1: []*Fighter{fu(1, 100, 100000, 10, 21750), fu(2, 100, 100000, 1, 22182)},
			t2: []*Fighter{fu(101, 0, 100000, 1), fu(102, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, magicFireHurt, -1, 37), "no 37 blood pool", hasCmdID(c, CmdBloodPoolAtt), "no BLOODPOOL_ATT", hasCmdID(c, CmdBloodPoolIn), "no BLOODPOOL_IN", hasCmd(c, CmdSubHP, 22182, -1, 60), "no 60 chase",
					hasCmd(c, CmdSubHP, 22182, -1, 30), "no 30 splash")
			}},
		// ---- 入阵（开场在前阵不触发，载体放在后阵 6 / 106 号位，第 1 回合开始补位入阵）----
		// 援护4 三体 40% 水伤 + 全体回 16%
		{fam: famYuanHu,
			t1: []*Fighter{fu(6, 100, 1000, 1, 50214), fuh(2, 0, 500, 1000, 1)}, t2: []*Fighter{fu(101, 1, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 50214, 101, 40), "no 40", hasCmd(c, CmdAddHP, 50214, 2, 160), "no 160 heal")
			}},
		// 龙跃4 全体 80% 水伤并混乱
		{fam: famLongYue,
			t1: []*Fighter{fu(6, 100, 1000, 1, 21784)}, t2: []*Fighter{fu(101, 1, 100000, 1), fu(102, 1, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 21784, 102, 80), "no 80", hasCmd(c, CmdBuff, 5051, -1, -1), "no confuse")
			}},
		// 破除禁锢4 回复 70% 上限
		{fam: famPoChuJinGu,
			t1: []*Fighter{fu(6, 10, 1000, 1, 22102), fuh(2, 0, 100, 1000, 1)}, t2: []*Fighter{fu(101, 1, 100000, 1)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAddHP, 22102, 2, 700), "no 700") }},
		// 紫青剑4 补位时消耗剑意
		{fam: famZiQingJian,
			t1: []*Fighter{fu(1, 0, 10, 1), fu(2, 0, 100000, 1), fu(3, 0, 100000, 1), fu(4, 0, 100000, 1),
				fu(5, 0, 100000, 1), fu(6, 100, 1000, 1, 21734)}, t2: []*Fighter{fu(101, 100, 100000, 10)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 21734, 1, 16), "no +16") }},
		// 天罡剑气4 耗仙族 32% 上限，敌受两倍
		{fam: famTianGangJianQi,
			t1: []*Fighter{fu(6, 10, 1000, 1, 22190), raced(fu(2, 0, 1000, 1), raceGod)}, t2: []*Fighter{fu(101, 1, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 22190, 2, 320), "no 320 cost", hasCmd(c, CmdSubHP, 22190, 101, 640), "no 640")
			}},
		// 噬心龙咒4 夺取入阵者 24% 攻击与气血
		{fam: famShiXinLongZhou,
			t1: []*Fighter{fu(6, 100, 1000, 1)}, t2: []*Fighter{fu(101, 1, 100000, 1, 22218)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdAttChange, 22218, 1, -24), "no -24", hasCmd(c, CmdSubHP, 22218, 1, 240), "no 240")
			}},
		// 特定弱化15 敌方攻击 -50%
		{fam: famTeDingRuoHua,
			t1: []*Fighter{fu(1, 10, 1000, 1, 29015)}, t2: []*Fighter{fu(101, 100, 100000, 1)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 29015, 101, -50), "no -50") }},
		// ---- 死亡 ----
		// 芭蕉扇4 燃烧中立即 2 次结算（狂怒焰5 的燃烧5：上限 10%）
		{fam: famBaJiaoShan,
			t1: []*Fighter{fu(1, 10, 10, 1, 21145), fu(2, 1000, 10, 1, 21524), fu(3, 0, 100000, 1)},
			t2: []*Fighter{fu(101, 100, 1000, 10), fu(102, 100, 1000, 10)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 21524, 101, 200), "no 200 burst (燃烧5 = 10% ×2)")
			}},
		// 芭蕉扇单次结算不超过自身攻击（攻击 30：每次 30，2 次 60）
		{fam: famBaJiaoShan,
			t1: []*Fighter{fu(1, 10, 10, 1, 21145), fu(2, 30, 10, 1, 21524), fu(3, 0, 100000, 1)},
			t2: []*Fighter{fu(101, 100, 1000, 10), fu(102, 100, 1000, 10)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 21524, 101, 60), "no 60 capped burst")
			}},
		// 生死别4 对位三体流失 25% 上限，低血跳过行动
		{fam: famShengSiBie,
			t1: []*Fighter{fuh(1, 10, 10, 1000, 1, 21414), fu(3, 0, 100000, 1)},
			t2: []*Fighter{fu(101, 100, 300, 10), fu(102, 100, 1000, 10)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 21414, 101, 250), "no 250", hasCmd(c, CmdBuff, 5231, 101, -1), "no bind on low",
					!hasCmd(c, CmdBuff, 5231, 102, -1), "bind on healthy")
			}},
		// 情殇4 吸血化身白骨魔
		{fam: famQingShang,
			t1: []*Fighter{fuh(1, 10, 10, 1000, 1, 21724), fu(3, 0, 1000, 1)}, t2: []*Fighter{fu(101, 100, 1000, 10)},
			check: func(r Result, c []Cmd) string {
				return want(hasCmd(c, CmdChgFighter, baiguShape, 1, 480), "no baigu transform 480", !hasCmd(c, CmdRelive, 21724, 1, -1), "relive sent",
					r.Skills["magic:"+gamedata.Magics[baiguMagic].Name] > 0, "no baigu magic")
			}},
		// 诸佛消散4 (攻×4+上限) 平摊
		{fam: famZhuFuXiaoSan,
			t1: []*Fighter{fuh(1, 10, 10, 100, 1, 21794), fu(3, 0, 100000, 1)},
			t2: []*Fighter{fu(101, 100, 1000, 10), fu(102, 100, 1000, 10)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdSubHP, 21794, 101, 70), "no 70", hasCmd(c, CmdSubHP, 21794, 102, 70), "no 70 on 102")
			}},
		// 精卫填海4 最高攻两人遗志 +30%
		{fam: famJingWeiTianHai,
			t1: []*Fighter{fu(1, 10, 10, 1, 22150), fu(2, 50, 10000, 1), fu(3, 40, 10000, 1), fu(4, 30, 10000, 1)},
			t2: []*Fighter{fu(101, 100, 1000, 10)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdAttChange, 22150, 2, 15), "no +15", hasCmd(c, CmdAttChange, 22150, 3, 12), "no +12",
					!hasCmd(c, CmdAttChange, 22150, 4, -1), "third got it")
			}},
		// 不灭法身4 满血复活上限 +50%
		{fam: famBuMieFaShen,
			t1: []*Fighter{fuh(1, 10, 10, 100, 1, 22044), fu(3, 0, 100000, 1)}, t2: []*Fighter{fu(101, 100, 1000, 10)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdChgMaxHP, 22044, 1, 50), "no +50", hasCmd(c, CmdRelive, 22044, 1, 150), "no relive 150")
			}},
		// 烈焰吞噬4 吞噬尸体：亡者攻击 / 上限的 20%
		{fam: famLieYanTunShi,
			t1: []*Fighter{fu(1, 1000, 1000, 10, 22154)}, t2: []*Fighter{fu(101, 500, 1000, 1), fu(102, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string {
				return want(hasCmd(c, CmdChgMaxHP, 22154, 1, 200), "no +200", hasCmd(c, CmdAttChange, 22154, 1, 100), "no +100",
					hasCmd(c, CmdBuff, 5141, 101, -1), "corpse lacks buff 5141")
			}},
		// ---- 事件 ----
		// 铸剑4 入阵技首次触发全体 +20%
		{fam: famZhuJian,
			t1: []*Fighter{fu(1, 100, 1000, 1, 22210), fu(6, 0, 1000, 1, 20974)}, t2: []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 22210, 1, 20), "no +20") }},
		// 天地灵气4 额外攻击
		{fam: famTianDiLingQi,
			t1: []*Fighter{fu(1, 100, 1000, 1, 22214), fu(6, 0, 1000, 1, 20974)}, t2: []*Fighter{fu(101, 0, 100000, 1)}, check: ok},
		// 天魔觉醒4 死亡技护法死亡 +25%
		{fam: famTianMoJueXing,
			t1: []*Fighter{fu(1, 100, 100000, 1, 22134), fu(2, 0, 10, 1, 20510)}, t2: []*Fighter{fu(102, 100, 100000, 10)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 22134, 1, 25), "no +25") }},
		// 藏剑于身4 人族加攻时 +4%
		{fam: famCangJianYuShen,
			t1:    []*Fighter{fu(1, 100, 1000, 1, 22174), raced(fu(2, 10, 1000, 1, 20280), raceHuman)},
			t2:    []*Fighter{fu(101, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 22174, 1, 4), "no +4") }},
		// 回梦4 友方回到后阵 +60% 初始生命
		{fam: famHuiMeng,
			t1: []*Fighter{fu(1, 0, 1000, 1, 22178), fu(2, 0, 1000, 1, 20310), fu(3, 0, 100000, 1), fu(4, 0, 100000, 1),
				fu(5, 0, 100000, 1), fu(6, 0, 1000, 1)},
			t2:    []*Fighter{fu(102, 0, 100000, 1), fu(106, 1000, 100000, 10, 21784)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdChgMaxHP, 22178, 1, 600), "no +600") }},
		// 绝命追猎4 分击护法攻击后 +6%
		{fam: famJueMingZhuiLie,
			t1:    []*Fighter{fu(1, 100, 1000, 1, 22170), fu(2, 10, 1000, 1, 20770)},
			t2:    []*Fighter{fu(101, 0, 100000, 1), fu(102, 0, 100000, 1)},
			check: func(_ Result, c []Cmd) string { return want(hasCmd(c, CmdAttChange, 22170, 1, 6), "no +6") }},
	}
	for i, c := range cases {
		t.Run(fmt.Sprintf("%02d_%d", i, c.fam), func(t *testing.T) { runFamCase(t, i, c) })
	}
}

func chargeT1(id int32) []*Fighter { t1, _ := chargeCarrier(id); return t1 }
func chargeT2() []*Fighter         { _, t2 := chargeCarrier(0); return t2 }
func rampT1(id int32) []*Fighter   { t1, _ := rampageCarrier(id); return t1 }
func rampT2() []*Fighter           { _, t2 := rampageCarrier(0); return t2 }

// TestT5Vulnerability：五种易伤把对应属性伤害变为 a%（10 级 350%），其他属性不受影响。
func TestT5Vulnerability(t *testing.T) {
	for kind, id := range []int32{130150, 130130, 130120, 130110, 130140} { // 道 土 水 火 雷
		for k2 := range 5 {
			b := &battle{rng: rand.New(rand.NewPCG(1, 1)), skills: map[string]int{}}
			u := newUnit(fu(1, 100, 1000, 1), 0)
			x := newUnit(fu(101, 1, 10000, 1, id), 1)
			b.units = [2][]*unit{{u}, {x}}
			b.propertyHurt(u, x, 0, 100, k2)
			want := int64(100)
			if k2 == kind {
				want = 350
			}
			if got := 10000 - x.hp; got != want {
				t.Fatalf("talent %d kind %d: took %d, want %d", id, k2, got, want)
			}
		}
	}
}

func hasCmdID(c []Cmd, id int) bool {
	for _, x := range c {
		if x.ID == id {
			return true
		}
	}
	return false
}
