// xymrec 查看服务器存下的战报文件（xymserver -records 目录里的 *.rec，见 internal/xymsrv/records.go）：
// 双方名字、阵容（站位、形象、生命、攻击、先攻、种族），再逐条列出战报命令，最后跑一遍血量回放自洽检查。
//
//	go run ./cmd/xymrec 113245_20022_7.rec
package main

import (
	"fmt"
	"os"
	"strings"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

var cmdNames = map[int]string{
	battle.CmdActDo: "ACT_DO", battle.CmdSubHP: "SUB_HP", battle.CmdAddHP: "ADD_HP", battle.CmdDie: "DIE",
	battle.CmdRelive: "RELIVE", battle.CmdBuff: "BUFF", battle.CmdTurn: "TURN", battle.CmdMove: "MOVE",
	battle.CmdWarStart: "WAR_START", battle.CmdWarEnd: "WAR_END", battle.CmdZhanyi: "ZHANYI",
	battle.CmdAttChange: "ATT_CHANGE", battle.CmdRmBuff: "RM_BUFF", battle.CmdShield: "SHIELD",
	battle.CmdChgMaxHP: "CHG_MAXHP", battle.CmdAddPower: "ADD_POWER", battle.CmdChgFighter: "CHG_FIGHTER",
	battle.CmdBloodPoolIn: "BLOODPOOL_IN", battle.CmdBloodPoolAtt: "BLOODPOOL_ATT", battle.CmdDitingCount: "DITING",
	battle.CmdXiuluoCount: "XIULUO", battle.CmdActionEnd: "ACTION_END", battle.CmdMagicShield: "MAGIC_SHIELD",
	battle.CmdMoveToEmpty: "MOVE_TO_EMPTY", battle.CmdPinXue: "PINXUE", battle.CmdSwordEarth: "SWORD(土)", 28: "SWORD(雷)", 29: "SWORD(火)",
	30: "SWORD(水)", 39: "SWORD(道)", battle.CmdSwordAttack: "SWORD_ATTACK",
}

// magicName 是招式号的说明（神通 / 法术 / BUFF，查不到就空）。
func magicName(id int64) string {
	if t := gamedata.Talents[int32(id)]; t != nil && t.Family != "" {
		return t.Family
	}
	if m := gamedata.Magics[int32(id)]; m != nil {
		return m.Name
	}
	if b := gamedata.Buffs[int32(id)]; b != nil {
		return b.Name
	}
	return ""
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: xymrec <file.rec>")
		os.Exit(2)
	}
	body, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, spec, m, err := xym.DecodeExt(xym.S2C, body)
	if err != nil || spec.Name != "S_FIGHT_RECORD" {
		fmt.Fprintln(os.Stderr, "not a fight record:", err)
		os.Exit(1)
	}
	fmt.Printf("rid %v\n", m["rid"])
	var teams [2][]*battle.Fighter
	for side, k := range []string{"1", "2"} {
		fmt.Printf("== %v（等级 %v 威望 %v）\n", m["name"+k], m["level"+k], m["w"+k])
		for _, x := range m["team"+k].([]xym.Msg) {
			f := &battle.Fighter{Pos: int(num(x["pos"])), Icon: int32(num(x["icon"])), HP: int32(num(x["hp"])),
				MaxHP: int32(num(x["hp"])), Att: int32(num(x["att"]))}
			teams[side] = append(teams[side], f)
			name := ""
			if p := gamedata.Pets[f.Icon]; p != nil {
				name = p.Name
			}
			fmt.Printf("  pos %3d  %-6s(%d)  hp %d  att %d  sp %v  race %v  skillfull %v\n",
				f.Pos, name, f.Icon, f.HP, f.Att, x["sp"], x["race"], x["skillfull"])
		}
	}
	if op, ok := m["otherparam"]; ok {
		fmt.Printf("otherparam %v\n", op)
	}
	rec := m["record"].([]byte)
	cmds, derr := battle.DecodeRecord(rec)
	turn := 0
	for i, c := range cmds {
		if c.ID == battle.CmdTurn {
			turn++
			fmt.Printf("---- 第 %d 回合\n", turn)
			continue
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%4d %-13s", i, cmdNames[c.ID])
		if c.Actor != 0 {
			fmt.Fprintf(&sb, " actor %d", c.Actor)
		}
		if c.Magic != 0 {
			fmt.Fprintf(&sb, " magic %d", c.Magic)
			if n := magicName(c.Magic); n != "" {
				fmt.Fprintf(&sb, "「%s」", n)
			}
		}
		if len(c.Targets) > 0 {
			fmt.Fprintf(&sb, " %v", c.Targets)
		}
		fmt.Println(sb.String())
	}
	if derr != nil {
		fmt.Println("解析错误:", derr)
	}
	if _, err := battle.Replay(teams[0], teams[1], rec); err != nil {
		fmt.Println("回放检查:", err)
	} else {
		fmt.Println("回放检查: OK")
	}
}

func num(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}
