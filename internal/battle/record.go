package battle

import (
	"encoding/binary"
	"fmt"
	"strings"

	"turnrpg/internal/gamedata"
)

// Cmd 是解码后的一条战报命令（格式见 client war/netwar.lua，与 Simulate 写出的一致）。
type Cmd struct {
	ID      int
	Actor   int        // ACT_DO 出手位 / MOVE 起点 / RM_BUFF 位置 / ZHANYI 方
	Magic   int64      // 招式号 / op / WAR_END 胜负（1 胜）
	Targets [][2]int64 // (位置, 数值)
}

// DecodeRecord 解析战报，要求逐字节读完；遇到未知命令或越界返回错误。
func DecodeRecord(b []byte) (out []Cmd, err error) {
	o := 0
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("battle: record truncated at %d of %d", o, len(b))
		}
	}()
	u8 := func() int64 { o++; return int64(b[o-1]) }
	u32 := func() int64 { o += 4; return int64(binary.LittleEndian.Uint32(b[o-4:])) }
	list := func(withNum func() int64) [][2]int64 {
		n := int(u8())
		l := make([][2]int64, n)
		for i := range l {
			l[i][0] = u8()
			if withNum != nil {
				l[i][1] = withNum()
			}
		}
		return l
	}
	for o < len(b) {
		c := Cmd{ID: int(u8())}
		switch c.ID {
		case CmdWarStart, CmdWarEnd:
			c.Magic = u8()
		case CmdTurn, CmdActionEnd, CmdPinXue:
		case CmdActDo:
			c.Actor = int(u8())
			c.Magic = u32()
			c.Targets = list(nil)
		case CmdSubHP, CmdAddHP, CmdRelive:
			c.Magic = u32()
			c.Targets = list(u32)
		case CmdDie:
			c.Targets = list(nil)
		case CmdBuff:
			c.Magic = u32()
			c.Targets = list(func() int64 { u8(); return u8() })
		case CmdMove, CmdMoveToEmpty:
			c.Actor = int(u8())
			c.Magic = u8()
		case CmdSwordEarth, 28, 29, 30, 39: // 添剑（按属性）
			c.Actor = int(u8())
		case CmdSwordAttack: // Targets = [[目标, 剑数]]
			c.Actor = int(u8())
			tp := u8()
			c.Targets = [][2]int64{{tp, u8()}}
		case CmdZhanyi:
			c.Actor = int(u8())
			c.Magic = u8()
			c.Targets = [][2]int64{{0, u8()}}
		case CmdAttChange:
			c.Magic = u32()
			c.Targets = list(func() int64 {
				op := u8()
				n := u32()
				if op == 2 {
					return -n
				}
				return n
			})
		case CmdRmBuff:
			c.Actor = int(u8())
			u8()
		case CmdShield, CmdMagicShield:
			c.Magic = u8()
			c.Targets = list(u32)
		case CmdAddPower:
			c.Magic = u8()
			c.Targets = list(u8)
		case CmdChgFighter: // Actor = 站位，Magic = 形象，Targets = [[站位, 生命]]
			c.Actor = int(u8())
			c.Magic = u32()
			hp := u32()
			u32() // 攻击
			u8()  // 先攻
			u8()  // full
			u8()  // 种族
			u8()  // power
			c.Targets = [][2]int64{{int64(c.Actor), hp}}
		case CmdBloodPoolIn, CmdBloodPoolAtt: // Actor = 站位，Magic = 量
			c.Actor = int(u8())
			c.Magic = u32()
		case CmdDitingCount:
			c.Actor = int(u8())
		case CmdXiuluoCount: // Actor = 站位，Magic = 点数
			c.Actor = int(u8())
			c.Magic = u8()
		case CmdChgMaxHP:
			c.Magic = u32()
			op := u8()
			c.Targets = list(func() int64 {
				n := u32()
				if op == 2 {
					return -n
				}
				return n
			})
		default:
			return out, fmt.Errorf("battle: unexpected cmd %d at %d", c.ID, o-1)
		}
		out = append(out, c)
	}
	if o != len(b) {
		return out, fmt.Errorf("battle: record overrun: %d of %d", o, len(b))
	}
	return out, nil
}

// Replay 按命令回放血量做自洽检查：不能负血、DIE 恰在归零时、阵亡单位不能普攻 / 施法（死亡触发的
// 神通演出除外，官方战报同样如此）。返回战报里的胜负；出错时错误信息带上出错前后的命令和双方单位。
func Replay(team1, team2 []*Fighter, rec []byte) (bool, error) {
	win, _, err := replay(team1, team2, rec)
	return win, err
}

// Survivors 回放战报，返回开战时各站位（Fighter.Pos）上的单位战斗结束时是否活着（跟随换位）。
func Survivors(team1, team2 []*Fighter, rec []byte) (map[int]bool, error) {
	_, alive, err := replay(team1, team2, rec)
	return alive, err
}

func replay(team1, team2 []*Fighter, rec []byte) (bool, map[int]bool, error) {
	cmds, err := DecodeRecord(rec)
	if err != nil {
		return false, nil, err
	}
	if len(cmds) == 0 || cmds[0].ID != CmdWarStart || cmds[len(cmds)-1].ID != CmdWarEnd {
		return false, nil, fmt.Errorf("battle: record must start with WAR_START and end with WAR_END")
	}
	all := append(append([]*Fighter(nil), team1...), team2...)
	hp, maxHP := map[int64]int64{}, map[int64]int64{}
	who := map[int64]int{} // 当前站位 -> 开战时站位
	for _, f := range all {
		hp[int64(f.Pos)], maxHP[int64(f.Pos)] = int64(f.HP), int64(f.MaxHP)
		who[int64(f.Pos)] = f.Pos
	}
	fail := func(i int, format string, args ...any) (bool, map[int]bool, error) {
		var sb strings.Builder
		fmt.Fprintf(&sb, "battle: cmd %d: "+format, append([]any{i}, args...)...)
		for j := max(0, i-8); j <= i && j < len(cmds); j++ {
			fmt.Fprintf(&sb, "\n%4d %+v", j, cmds[j])
		}
		for _, f := range all {
			fmt.Fprintf(&sb, "\nunit %d %s hp=%d talents=%v magic=%d", f.Pos, f.Name, f.HP, f.Talents, f.Magic)
		}
		return false, nil, fmt.Errorf("%s", sb.String())
	}
	for i, c := range cmds {
		switch c.ID {
		case CmdActDo:
			if hp[int64(c.Actor)] <= 0 && gamedata.Talents[int32(c.Magic)] == nil {
				return fail(i, "dead unit %d acts", c.Actor)
			}
		case CmdSubHP:
			for _, x := range c.Targets {
				if x[1] > hp[x[0]] {
					return fail(i, "pos %d loses %d of %d hp", x[0], x[1], hp[x[0]])
				}
				hp[x[0]] -= x[1]
				if hp[x[0]] == 0 && (i+1 >= len(cmds) || cmds[i+1].ID != CmdDie) {
					return fail(i, "pos %d at 0 hp without DIE", x[0])
				}
			}
		case CmdAddHP:
			for _, x := range c.Targets {
				if hp[x[0]] <= 0 || hp[x[0]]+x[1] > maxHP[x[0]] {
					return fail(i, "bad heal %v (hp %d/%d)", x, hp[x[0]], maxHP[x[0]])
				}
				hp[x[0]] += x[1]
			}
		case CmdRelive:
			for _, x := range c.Targets {
				hp[x[0]] = x[1]
			}
		case CmdChgFighter: // 变身：满血的新单位
			for _, x := range c.Targets {
				hp[x[0]], maxHP[x[0]] = x[1], x[1]
			}
		case CmdChgMaxHP: // 客户端只改上限；降到当前生命以下时随后有 SUB_HP 扣掉超出部分
			for _, x := range c.Targets {
				maxHP[x[0]] += x[1]
			}
		case CmdMove, CmdMoveToEmpty: // 交换两个站位
			a, z := int64(c.Actor), c.Magic
			hp[a], hp[z] = hp[z], hp[a]
			maxHP[a], maxHP[z] = maxHP[z], maxHP[a]
			wa, oka := who[a]
			wz, okz := who[z]
			delete(who, a)
			delete(who, z)
			if oka {
				who[z] = wa
			}
			if okz {
				who[a] = wz
			}
		case CmdDie:
			for _, x := range c.Targets {
				if hp[x[0]] != 0 {
					return fail(i, "DIE %d with %d hp", x[0], hp[x[0]])
				}
			}
		}
	}
	alive := map[int]bool{}
	for pos, orig := range who {
		alive[orig] = hp[pos] > 0
	}
	return cmds[len(cmds)-1].Magic == 1, alive, nil
}

// CloneTeam 复制一队：同一队打多场、或回放时要对照开战前的初值时用。
func CloneTeam(team []*Fighter) []*Fighter {
	out := make([]*Fighter, len(team))
	for i, f := range team {
		c := *f
		out[i] = &c
	}
	return out
}
