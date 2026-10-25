package battle

import (
	"strconv"

	"turnrpg/internal/gamedata"
)

// 神通按触发时机分文件实现（引擎本身：battle.go 回合流程、attack_action.go 攻击行为、effects.go 扣血 / 加攻等基本效果、
// battle_status.go 状态、swap.go 换位 / 驱逐、swords.go 仙剑、targets.go 选目标）：
//
//	talents_attack.go  攻击前 / 攻击后 / 受击（beforeAttack、selfAfterAttack、attackEffects、followUps、onHit；流程见 attack_action.go）
//	talents_round.go   回合开始 / 回合结束、暴走（roundStart、roundEnd、rampage）
//	talents_enter.go   入阵（enter）
//	talents_open.go    开场（battleStart、afterOpening、火炮之术装弹）
//	talents_death.go   死亡、复活倒计时（died、pendingRevive）
//	talents_charge.go  蓄力满后释放（releaseCharge）
//	talents_hurt.go    受到伤害时：属性易伤 / 减免、攻击减伤、伤害转移与代承（propertyTaken、attackTaken、beforeHurt）
//	talents_event.go   由别的单位的事件触发：首次发动死亡 / 入阵技、死亡技护法死亡或复活、人族加攻、回到后阵
//
// 族名、参数、描述见 gamedata/data/talent.csv 与 talentset.csv；param(i) 对应描述里的 $a、$b……
// 描述与官方战报不符时以战报为准（代码注释里记录了这类校准）。

// family 是神通族号（常量见 families.go）：该族最小的神通号，即 gamedata.TalentInfo.FamilyID。
// 族名只在数据里（talent.csv 的 fname），代码一律按族号判断。
type family int32

// String 是族名（日志 / 测试输出用）。
func (f family) String() string {
	if ti := gamedata.Talents[int32(f)]; ti != nil {
		return ti.Family
	}
	return strconv.Itoa(int(f))
}

// implemented 是已实现的神通族（覆盖率报告用，见 TestTalentCoverage）。
var implemented = map[family]bool{}

// Implemented 报告神通 ti 所在的族是否已实现。
func Implemented(ti *gamedata.TalentInfo) bool { return ti != nil && implemented[family(ti.FamilyID)] }

// register 登记已实现的神通族（各 talents_*.go 在 init 里登记自己的）。
func register(fs ...family) {
	for _, f := range fs {
		implemented[f] = true
	}
}

// 引擎流程里直接处理的族：attack_action.go 的 attack / guardFor、effects.go 的 hurt 等（闪避、暴击、减伤、护卫……）
// 与 battle_skill.go 的施法。
func init() {
	register(
		famShanBi, famBaoJi, famHuDun, famJianRen, famXiXue, famZhaSi, famFenJi, famShouWei, famXianGong, famQiJia, famChongFeng, famRunZe, famJiuYuan, famDunRang,
	)
}

// tempUp 是「攻击时有概率提升攻击力（攻击后清除）」一类：演出、加攻，攻击后在 act 里还原。
func (b *battle) tempUp(u *unit, t *talent, n int64) {
	if n <= 0 {
		return
	}
	b.show(u, t.id, u)
	b.inTempUp = true // 临时加攻不触发藏剑于身（attRaised）
	got := b.changeAtt(u, t.id, n)
	b.inTempUp = false
	u.tempAtt += got // 按实际加的扣回（坚韧护法封顶时比 n 小）
	if u.fired == nil {
		u.fired = map[family]int64{}
	}
	u.fired[t.fam] += n
}

// keepTemp：魔煞 / 兽妖激发——攻击后保留该族攻击加值的 $x%（返回保留量）。
func (b *battle) keepTemp(u *unit) int64 {
	var keep int64
	for _, f := range []family{famMoSha, famShouYao} {
		if k := u.tals[f]; k != nil && k.tjOn() && u.fired[f] > 0 {
			keep += int64(float64(u.fired[f]) * k.tjX() / 100)
		}
	}
	return min(keep, u.tempAtt)
}

// tuxiTJ：本次行动发动了激发的突袭（不会触发对方的先攻、逆势）。
func (u *unit) tuxiTJ() bool {
	k := u.tals[famTuXi]
	return k != nil && k.tjOn() && u.fired[famTuXi] > 0
}

// raceCount 是 side 一方某种族的护法数（图鉴 FAQ：「种族数量统计包括前后阵，也包括已阵亡护法。超出4名时取前4名」），
// 仙尊、仙帝、人圣、人皇、魔煞、魔主、兽妖、兽王用。
func (b *battle) raceCount(side, race int) int64 {
	return countRace(b.units[side], race)
}

func countRace(us []*unit, race int) int64 {
	n := 0
	for _, x := range us {
		if x.Race == race {
			n++
		}
	}
	return int64(min(n, 4))
}

// fxMagics：这几族扣血 / 回血时官方战报用专门的特效法术号（magic.csv model 210000「入阵-浪涌」「死亡-狂怒焰」…），
// 客户端按它播特效；用神通号则只有数字没有特效。
var fxMagics = map[family]int32{famTianBian: 15153, famLangYong: 15154, famKuangLei: 15155, famXunDian: 15156,
	famShuiZhiYuan: 15157, famDaoZhiShi: 15158, famKuangNuYan: 15159, famGuiXuZhen: 15160, famGuMao: 15161}

func fxMagic(k *talent) int32 {
	if id, ok := fxMagics[k.fam]; ok {
		return id
	}
	return k.id
}
