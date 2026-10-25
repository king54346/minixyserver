package battle

import (
	"fmt"
	"slices"

	"turnrpg/internal/gamedata"
)

// 阵法：
//   - 阵位加成：五个前阵阵位各有加成，数值 = 基础 + 阵位等级 × 每级（先攻只取基础，失去神通是神通号）。
//   - 固定加成跟着阵位走（zfSync）：初始攻击 / 初始生命上限 / 先攻 / 失去神通给当前站在该阵位的护法，换位、补位、
//     回后阵时重算（离开阵位就收回）；生命上限变化时按比例保留当前生命（存活的至少 1 点，不触发回复神通）；攻击和
//     生命的加减同时改初始值。阵眼阵亡不影响固定加成。
//   - 伤害加减：阵眼存活时，出手者所在阵位的「造成物理 / 法术伤害增加」与受击者所在阵位的「受到物理 / 法术伤害减少」
//     在队长技之后、其他减伤和护盾之前结算（最低 0）。生命流失不受影响。
//   - 阵眼技：阵眼阵位上存活的护法，在本方阵法没被敌方阵法克制、自身没被麻痹 / 捆缚时，于指定回合结束（其他回合
//     结束效果之后，先我方后敌方）按概率释放阵眼法术（不需要战意、不过法术自身的发动几率）；有 eye_buff 时对法术的
//     实际目标逐个按 eye_buff_rate 附加该 buff。克制只影响阵眼技。
//   - 战报：S_FIGHT_RECORD.otherparam idx 11「方:阵法:阶:阵位1等级:…:阵位5等级」，两方用 | 分隔（方 1 我方 2 敌方，
//     war/war.lua initZhenFaInfo），客户端据此显示阵法图标、克制和阵眼阵亡变灰。

// Formation 是一方使用的阵法（Type 0 = 不用）。阵位等级 1–10。
type Formation struct {
	Type, Stair int
	Lv          [5]int
}

type zfSide struct {
	Formation
	info      *gamedata.ZhenfaInfo
	countered bool // 被敌方阵法克制：不能放阵眼技
}

func (f Formation) info() *gamedata.ZhenfaInfo {
	if f.Type == 0 {
		return nil
	}
	return gamedata.ZhenfaOf(f.Type, max(f.Stair, 1))
}

func (f Formation) lv(pos int) int { return max(f.Lv[pos-1], 1) }

// zfParam 是战报 otherparam idx 11 的值（双方都没用阵法时为空）。只要有一方用阵法就两方都写：没用的一方写
// 「方:0:1」——客户端 CWar:getZhenFaInfo 查敌方站位时直接取 self.zhenfa[2].zhenfa_type，缺了敌方那段会 Lua 报错、
// 战斗卡住（线上事故 2026-10-21）。
func zfParam(fs [2]Formation) string {
	if fs[0].info() == nil && fs[1].info() == nil {
		return ""
	}
	parts := make([]string, 2)
	for side, f := range fs {
		if f.info() == nil {
			parts[side] = fmt.Sprintf("%d:0:1", side+1)
			continue
		}
		parts[side] = fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d:%d", side+1, f.Type, max(f.Stair, 1), f.lv(1), f.lv(2), f.lv(3), f.lv(4), f.lv(5))
	}
	return parts[0] + "|" + parts[1]
}

// setupZhenfa：开战（WAR_START 之后、开场神通之前）记下双方阵法并给前阵护法固定加成。
func (b *battle) setupZhenfa(fs [2]Formation) {
	for side, f := range fs {
		if info := f.info(); info != nil {
			other := fs[1-side]
			b.zf[side] = &zfSide{Formation: f, info: info, countered: other.info() != nil && gamedata.Counters(other.Type, f.Type)}
		}
	}
	for side := range b.units {
		for _, u := range b.units[side] {
			b.zfSync(u)
		}
	}
}

// zfAt 是 side 一方阵位 pos 上 kind 加成的数值（没有为 0）。
func (b *battle) zfAt(side, pos, kind int) int64 {
	z := b.zf[side]
	if z == nil || pos < 1 || pos > 5 {
		return 0
	}
	var n int64
	for _, a := range z.info.PosAdd[pos-1] {
		if a.Kind == kind {
			if kind == gamedata.ZfLoseTal {
				return int64(a.Per)
			}
			n += int64(a.Value(z.lv(pos)))
		}
	}
	return n
}

// zfSync 让 u 的阵法固定加成与它现在的阵位一致（在后阵 / 阵亡则收回）。
func (b *battle) zfSync(u *unit) {
	var want [3]int64
	var lost family
	if u.alive() && u.front() {
		pos := u.slot()
		want = [3]int64{b.zfAt(u.side, pos, gamedata.ZfAtt), b.zfAt(u.side, pos, gamedata.ZfHP), b.zfAt(u.side, pos, gamedata.ZfSpeed)}
		if id := b.zfAt(u.side, pos, gamedata.ZfLoseTal); id != 0 {
			if ti := gamedata.Talents[int32(id)]; ti != nil {
				lost = family(ti.FamilyID)
			}
		}
	}
	u.zfLost = lost
	if !u.alive() {
		return // 阵亡的不再演出，复活后回到阵位时再同步
	}
	if d := want[0] - u.zfFixed[0]; d != 0 {
		got := b.changeAtt(u, 0, d)
		u.baseAtt += got
		u.zfFixed[0] += got
	}
	if d := want[2] - u.zfFixed[2]; d != 0 {
		u.Speed += int(d)
		u.zfFixed[2] = want[2]
	}
	if d := want[1] - u.zfFixed[1]; d != 0 {
		prev := u.maxHP
		newMax := max(prev+d, 1)
		d = newMax - prev
		hp := min(max(u.hp*newMax/max(prev, 1), 1), newMax)
		if d > 0 {
			b.zfMaxHP(u, d)
		}
		if dh := hp - u.hp; dh != 0 {
			cmd, n := CmdAddHP, dh
			if dh < 0 {
				cmd, n = CmdSubHP, -dh
			}
			u.hp = hp
			b.hpCmd(cmd, 0, u, n)
		}
		if d < 0 {
			b.zfMaxHP(u, d)
		}
		u.MaxHP += int32(d) // 初始生命上限同样加减
		u.zfFixed[1] += d
	}
}

// zfMaxHP 改生命上限并输出 CHG_MAXHP（不动当前生命，由 zfSync 按比例处理）。
func (b *battle) zfMaxHP(u *unit, d int64) {
	op, n := 1, d
	if d < 0 {
		op, n = 2, -d
	}
	u.maxHP += d
	b.w.u8(CmdChgMaxHP)
	b.w.u32(0)
	b.w.u8(op)
	b.w.u8(1)
	b.w.u8(u.Pos)
	b.w.u32(n)
}

// zfEye 是 side 的阵眼护法（阵眼阵位上存活的前阵护法；没有返回 nil）。
func (b *battle) zfEye(side int) *unit {
	z := b.zf[side]
	if z == nil {
		return nil
	}
	for _, x := range b.frontOf(side) {
		if x.slot() == z.info.Eye {
			return x
		}
	}
	return nil
}

// zfAdjust：阵眼存活时按阵位加减伤害（src 造成的物理 / 法术伤害加，t 受到的减），最低 0。
func (b *battle) zfAdjust(src, t *unit, n int64, physical bool) int64 {
	if src == nil || src.side == t.side || n <= 0 || b.zf == [2]*zfSide{} {
		return n
	}
	add, sub := gamedata.ZfMagicAdd, gamedata.ZfMagicSub
	if physical {
		add, sub = gamedata.ZfPhysicAdd, gamedata.ZfPhysicSub
	}
	if src.front() && b.zfEye(src.side) != nil {
		n += b.zfAt(src.side, src.slot(), add)
	}
	if t.front() && b.zfEye(t.side) != nil {
		n -= b.zfAt(t.side, t.slot(), sub)
	}
	return max(n, 0)
}

// zfEyeSkill：回合结束时双方（先我方后敌方）阵眼技判定。胜负照常在下一回合开始时判定（这里不能写 WAR_END）。
func (b *battle) zfEyeSkill() {
	for side, z := range b.zf {
		if z == nil || z.countered || !slices.Contains(z.info.EyeRounds, b.round) || b.over {
			continue
		}
		eye := b.zfEye(side)
		m := gamedata.Magics[z.info.EyeSkill]
		if eye == nil || m == nil || eye.has(stSilence) != nil || eye.has(stBind) != nil || !b.chance(z.info.EyeRate) {
			continue
		}
		targets := b.cast(eye, &castable{id: z.info.EyeSkill, lv: max(z.info.EyeLv, 1), left: 1, MagicInfo: m})
		if z.info.EyeBuff != 0 {
			for _, t := range targets {
				if t.alive() && eye.alive() && b.chance(z.info.EyeBuffRate) {
					b.applyBuff(eye, t, z.info.EyeBuff)
				}
			}
		}
	}
}
