package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 日常玩法（解忧、缘魂、秘境、摇钱树、宝卷）共用的小工具。

func today() string { return time.Now().Format("2006-01-02") }

func (s *Session) actRng() *rand.Rand {
	return rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(s.role.UID)+7))
}

// attrVal 是 S_ATTR_UPDATE 里 name 字段的当前值。
func attrVal(ro *Role, name string) any {
	switch name {
	case "cash":
		return ro.Cash
	case "gold":
		return ro.Gold
	case "tpoint":
		return ro.TPoint
	case "hpoint":
		return ro.HPoint
	case "soulexp":
		return ro.SoulExp
	case "friend_point":
		return ro.FriendPoint
	case "grade":
		return ro.Grade
	case "exp":
		return ro.Exp
	case "next_exp":
		return nextExp(ro.Grade)
	}
	return 0
}

// roleUpdate 在存档上检查并修改角色（fn 返回 false 表示余额不足、不改），改成功后发 S_ATTR_UPDATE（names 列出的字段）。
func (s *Session) roleUpdate(fn func(ro *Role) bool, names ...string) bool {
	ok := false
	role, err := s.srv.roles.Update(s.role.UID, func(ro *Role) bool {
		ok = fn(ro)
		return ok
	})
	if err != nil {
		s.logErr("save role", err)
		return false
	}
	if role != nil {
		s.role = role
	}
	if ok && len(names) > 0 {
		attr := make([]xym.Msg, 0, len(names))
		for _, n := range names {
			attr = append(attr, xym.Msg{"name": n, "val": attrVal(role, n)})
		}
		s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": attr})
	}
	return ok
}

// rewardMsg 是 S_REWARD_MSG（各数值为 0，调用方再填）。
func rewardMsg(typ int, list []xym.Msg) xym.Msg {
	if list == nil {
		list = []xym.Msg{}
	}
	return xym.Msg{"cash": 0, "mexp": 0, "pexp": 0, "ling": 0, "gold": 0, "soul": 0, "type": typ, "item": 0,
		"no_fight_again": 1, "star": 0, "list": list}
}

// petFighter 是一张卡在 grade 级时的敌方单位（站位 100 + slot；神通取各槽第 tal 级，0 起，超出取最高）。
func petFighter(t int32, grade, slot, tal int) *battle.Fighter {
	pi := gamedata.Pets[t]
	if pi == nil {
		return nil
	}
	var tals []int32
	for _, l := range pi.Talents {
		if len(l) > 0 {
			tals = append(tals, l[min(tal, len(l)-1)])
		}
	}
	att, hp := pi.Att(grade), pi.HP(grade)
	return &battle.Fighter{ID: int32(900000 + slot), Pos: 100 + slot, Name: pi.Name, Att: att, HP: hp, MaxHP: hp,
		Speed: pi.Speed, Icon: t, Race: pi.Race, Grade: grade, Talents: tals, Magic: pi.Magic, MagicLv: max(1, min(10, grade/10)),
		SkillFull: len(tals) > 0 && tal >= maxTalentLen(pi)-1} // 神通都满级：战报里显示神通满徽标
}

// randStar 按权重（1–5 星）抽一个星级。
func randStar(w [5]int, rng *rand.Rand) int {
	total := 0
	for _, x := range w {
		total += x
	}
	r := rng.IntN(total)
	for i, x := range w {
		if r -= x; r < 0 {
			return i + 1
		}
	}
	return 1
}

// recordParams 是 S_FIGHT_RECORD.otherparam 里战斗内护法详情要的三项（格式照官方战报与 war/netwar.lua）：
//   - 4 卡牌：「站位:等级:天生法术:法术等级:生效神通…」，| 分隔（详情里只点亮列出的神通）；
//   - 8 法术书：「站位#法术号 等级 精修buff 精修次数 天将阶:…」（精修 buff = 法术表 jingxiu_buff，没精修为 0）；
//   - 9 装备：「站位#装备位 装备号 强化等级 宝石道具号 天将阶:…」。
func recordParams(teams ...[]*battle.Fighter) []xym.Msg {
	var cards, books, suits []string
	for _, team := range teams {
		for _, f := range team {
			c := fmt.Sprintf("%d:%d:%d:%d", f.Pos, max(f.Grade, 1), f.Magic, max(f.MagicLv, 1))
			for _, t := range f.Talents {
				c += fmt.Sprintf(":%d", t)
			}
			cards = append(cards, c)
			var bs []string
			for i, b := range f.Books {
				tj := 0
				if i < len(f.BookTJ) {
					tj = f.BookTJ[i]
				}
				jxBuff, jx := 0, 0
				if i < len(f.BookJX) && f.BookJX[i] > 0 {
					jx = f.BookJX[i]
					if m := gamedata.Magics[b[0]]; m != nil {
						jxBuff = int(m.JXBuff)
					}
				}
				bs = append(bs, fmt.Sprintf("%d %d %d %d %d", b[0], b[1], jxBuff, jx, tj))
			}
			if len(bs) > 0 {
				books = append(books, fmt.Sprintf("%d#%s", f.Pos, strings.Join(bs, ":")))
			}
			var ss []string
			for _, s := range f.Suits {
				ss = append(ss, fmt.Sprintf("%d %d %d %d %d", s[0], s[1], s[2], s[4], s[3]))
			}
			if len(ss) > 0 {
				suits = append(suits, fmt.Sprintf("%d#%s", f.Pos, strings.Join(ss, ":")))
			}
		}
	}
	out := []xym.Msg{{"idx": 4, "value": strings.Join(cards, "|")}}
	var powers []string
	for _, team := range teams {
		for _, f := range team {
			if n := battle.PowerFull(f); n > 0 {
				powers = append(powers, fmt.Sprintf("%d:%d", f.Pos, n))
			}
		}
	}
	if len(powers) > 0 { // 满蓄力层数（客户端默认 4）
		out = append([]xym.Msg{{"idx": 1, "value": strings.Join(powers, "|")}}, out...)
	}
	if len(books) > 0 {
		out = append(out, xym.Msg{"idx": 8, "value": strings.Join(books, "|")})
	}
	if len(suits) > 0 {
		out = append(out, xym.Msg{"idx": 9, "value": strings.Join(suits, "|")})
	}
	return out
}

// maxTalentLen 是护法三个神通槽里最长的等级数。
func maxTalentLen(pi *gamedata.PetInfo) int {
	n := 0
	for _, l := range pi.Talents {
		n = max(n, len(l))
	}
	return n
}

// fightRecord 发 S_FIGHT_RECORD。对方是玩家阵容（有神通满的卡）时 w2 填对方威望：客户端在 w2 为 0 时把对方
// 所有卡的 skillfull 清零（war/netwar.lua，与我方 w1 同理），金卡左上角的神通满徽标就出不来。
func (s *Session) fightRecord(rid, name2 string, level2 int, team1, team2 []*battle.Fighter, record []byte) {
	s.fightRecordW(rid, name2, level2, 0, team1, team2, record)
}

// fightRecordW 同 fightRecord，w2 是对方玩家的威望（prestigeOf；0 = 不是玩家 / 不知道，按等级估计）。
// 之前对方一律按「等级 × 10」估计，满级玩家永远是 500（用户报）。
func (s *Session) fightRecordW(rid, name2 string, level2, w2 int, team1, team2 []*battle.Fighter, record []byte) {
	full := false
	for _, f := range team2 {
		full = full || f.SkillFull
	}
	if w2 <= 0 && full {
		w2 = max(level2, 1) * 10 // 估计：同 prestige 的口径（等级 × 10）
	}
	s.SendExt("FIGHT_PTO.S_FIGHT_RECORD", xym.Msg{
		"canop": 14, "rid": rid,
		"w1": s.prestige(), "level1": s.role.Grade, "name1": s.role.Name, "team1": teamMsgs(team1),
		"w2": w2, "level2": level2, "name2": name2, "team2": teamMsgs(team2),
		"otherparam": recordParams(team1, team2), "record": record,
	})
}

// prestige 是战报里我方的威望（S_FIGHT_RECORD.w1，战斗界面顶上显示）。客户端在 w1 为 0 时把我方所有卡的
// skillfull 清零（war/netwar.lua），神通满的徽标就出不来。官方威望来自成就（本服没有），估计为
// 人物等级 × 10 + 通关过的关卡数。
func (s *Session) prestige() int {
	return prestigeOf(s.role.Grade, s.player)
}

// prestigeOf：人物等级 × 10 + p 通关过的关卡数（p 可为 nil）。
func prestigeOf(grade int, p *Player) int {
	n := grade * 10
	if p != nil {
		for _, pr := range p.Prog {
			if pr != nil {
				for _, t := range pr.Gates {
					if t > 0 {
						n++
					}
				}
			}
		}
	}
	return n
}
