package xymsrv

import (
	"math/rand/v2"
	"strconv"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 魔潮入侵（差事页「魔潮入侵」、内务「魔潮」→ view/PartyAdminiView.lua CPartyMoChaoAskBoxView、
// view/PartyMonsterFightView.lua、view/PartyView.lua refreshMonster）：
//   - 府主 / 洞主选等级和时间（12:02 / 18:02 / 21:02）开启：消耗洞府元宝 洞府等级×120 + 魔潮等级×30、元气 洞府等级×100，
//     魔潮持续 30 分钟，每天只能开启一次。C_OPEN_MONSTER_INVADE{level, open_hour}；
//     等级列表 C_OPEN_MONSTER_INVADE_LEVEL_INFO → S_SEND_MONSTER_INVADE_LEVEL_INFO{current_level, list[level], last_open_hour}。
//   - S_SEND_DONGFU_JOB_INFO 的 invade_status：4 等待（invade_open_left_time 倒计时到 invade_open_hour:02）、
//     0 进行中（invade_left_time）、1 胜利（invade_finish_time）、2 失败、3 无；剩余怪数 invade_monster_*。
//   - 战斗界面 C_MONSTER_INVADE_INFO → S_SEND_MONSTER_INVADE_INFO：统领 / 首领 / 魔妖剩余数、Boss 血量（全洞府共享），
//     本人挑战次数、免费复活次数、阵亡站位 die_list；C_MONSTER_INVADE_FIGHT{type: monster_governor / monster_leader /
//     monster / monster_boss}；复活 C_MONSTER_INVADE_RELIVE_FIGHTER{pos} / _ALL_FIGHTER；
//     参与名单 C_GET_MONSTER_INVADE_USER_LIST → S_SEND_MONSTER_INVADE_USER_LIST{level, list[…contribute…], monster_boss_ca}。
//   - Boss 是 party_mgr.BossInfo 的阵型 24301–24306（猴将军 / 机关人 人 / 地 / 天境，fight_npc 31201–31206）。
//
// 估计：魔潮等级 1–6 对应 Boss 阵型 24300+等级（参数 100×等级）；统领 / 首领 / 魔妖 数量 1+等级/2、2+等级、6+2×等级，
// 由 5 / 4 / 3 星护法组成（等级 40+10×魔潮等级）；Boss 可随时挑战；开启时已过当天时间点则立即开始；
// 免费复活每天 3 次，之后每次 20 元宝；贡献 = 魔妖 1 / 首领 3 / 统领 5、Boss 每 1 万伤害 1 点，胜利后按贡献发
// 洞府贡献，全体参与者另得 元宝 20×魔潮等级（邮件）。

const (
	invadeDuration = 1800
	invadeFreeLive = 3
	invadeLiveGold = 20
	invadeMaxLevel = 6
)

var invadeHours = []int{12, 18, 21}

type DFInvade struct {
	Day      string                  `json:"day"`
	Level    int                     `json:"level"`
	OpenHour int                     `json:"open_hour"`
	Start    int64                   `json:"start"`
	Finish   int64                   `json:"finish,omitempty"` // 全部清除的时刻
	Gov      int                     `json:"gov"`
	Leader   int                     `json:"leader"`
	Monster  int                     `json:"monster"`
	BossHP   int64                   `json:"boss_hp"`
	BossMax  int64                   `json:"boss_max"`
	Users    map[int32]*DFInvadeUser `json:"users,omitempty"`
	Rewarded bool                    `json:"rewarded,omitempty"`
	Seed     uint64                  `json:"seed"`
	Icons    [3]int32                `json:"icons"` // 统领 / 首领 / 魔妖的形象
}

type DFInvadeUser struct {
	Contri int   `json:"contri"`
	Times  int   `json:"times"`
	Relive int   `json:"relive"`
	Dead   []int `json:"dead,omitempty"`
}

// status 是 S_SEND_DONGFU_JOB_INFO 的 invade_status。
func (iv *DFInvade) status(now int64) int {
	switch {
	case iv == nil || iv.Day != today() && now >= iv.Start+invadeDuration:
		return 3
	case now < iv.Start:
		return 4
	case iv.BossHP <= 0 && iv.Gov+iv.Leader+iv.Monster == 0:
		return 1
	case now < iv.Start+invadeDuration:
		return 0
	}
	return 2
}

func (iv *DFInvade) user(uid int32) *DFInvadeUser {
	if iv.Users == nil {
		iv.Users = map[int32]*DFInvadeUser{}
	}
	u := iv.Users[uid]
	if u == nil {
		u = &DFInvadeUser{}
		iv.Users[uid] = u
	}
	return u
}

func invadeBoss(level int) []*battle.Fighter {
	team := battle.EnemyTeam(gamedata.GateMode{CA: int32(24300 + level), Param: 100 * level})
	return team
}

// invadeMonsters 是 kind（0 统领 1 首领 2 魔妖）的一队：5 / 4 / 3 星护法，阵容随 seed 固定。
func (iv *DFInvade) monsters(kind, nth int) []*battle.Fighter {
	rng := rand.New(rand.NewPCG(iv.Seed+uint64(kind*1000+nth), 7))
	star := 5 - kind
	n := 5 - kind
	grade := min(100, 40+10*iv.Level)
	var team []*battle.Fighter
	for slot := 1; slot <= n; slot++ {
		if t := slPick(star, rng); t != 0 {
			if x := petFighter(t, grade, slot, 2+iv.Level); x != nil {
				team = append(team, x)
			}
		}
	}
	if kind < 3 && nth == 0 && len(team) > 0 {
		iv.Icons[kind] = team[0].Icon
	}
	return team
}

func (srv *Server) invadeJob(df *Dongfu, m xym.Msg) {
	iv := df.Invade
	now := nowUnix()
	st := iv.status(now)
	m["invade_status"] = st
	if st == 3 {
		return
	}
	m["invade_open_hour"] = iv.OpenHour
	m["invade_monster_left"], m["invade_monster_leader_left"], m["invade_monster_governor_left"] = iv.Monster, iv.Leader, iv.Gov
	m["invade_monster_boss_hp"] = iv.BossHP
	switch st {
	case 4:
		m["invade_open_left_time"] = iv.Start - now
	case 0:
		m["invade_left_time"] = iv.Start + invadeDuration - now
	case 1:
		m["invade_finish_time"] = iv.Finish - iv.Start
	}
}

func (s *Session) invadeInfo(df *Dongfu) xym.Msg {
	iv := df.Invade
	u := iv.user(s.role.UID)
	die := []xym.Msg{}
	for _, pos := range u.Dead {
		die = append(die, xym.Msg{"pos": pos})
	}
	if iv.Icons[0] == 0 {
		for k := range 3 {
			iv.monsters(k, 0)
		}
	}
	return xym.Msg{"monster_left": iv.Monster, "monster_leader_left": iv.Leader, "monster_governor_left": iv.Gov,
		"monster_boss_hp": iv.BossHP, "monster_boss_max_hp": iv.BossMax, "challenge_times": u.Times,
		"free_relive_times": max(0, invadeFreeLive-u.Relive), "die_list": die, "monster_icon": iv.Icons[2],
		"monster_leader_icon": iv.Icons[1], "monster_governor_icon": iv.Icons[0], "monster_boss_ca": 24300 + iv.Level}
}

func onOpenInvadeLevel(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		list := []xym.Msg{}
		for l := 1; l <= min(invadeMaxLevel, int(df.Lv)+1); l++ {
			list = append(list, xym.Msg{"level": l})
		}
		cur, hour := 1, 12
		if df.Invade != nil {
			cur, hour = df.Invade.Level, df.Invade.OpenHour
		}
		s.SendExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_LEVEL_INFO", xym.Msg{"current_level": cur, "list": list, "last_open_hour": hour})
		return false
	})
}

func onOpenInvade(s *Session, p *Player, m xym.Msg) {
	level, hour := int(m["level"].(int64)), int(m["open_hour"].(int64))
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		gold, yuanqi := df.Lv*120+int32(level)*30, df.Lv*100
		ok := false
		switch {
		case !isAdmin(me.Position):
			s.sendMsg("只有府主和洞主可以开启魔潮入侵。")
		case level < 1 || level > min(invadeMaxLevel, int(df.Lv)+1):
			s.sendMsg("魔潮等级不正确。")
		case df.Invade != nil && df.Invade.Day == today():
			s.sendMsg("魔潮入侵每天只能开启一次。")
		case df.Gold < gold || df.Yuanqi < yuanqi:
			s.sendMsg("洞府元宝或元气不足。")
		default:
			if !containsInt(invadeHours, hour) {
				hour = 12
			}
			now := time.Now()
			start := time.Date(now.Year(), now.Month(), now.Day(), hour, 2, 0, 0, now.Location()).Unix()
			if start < now.Unix() {
				start = now.Unix() // 今天的时间点已过：立即开始
			}
			df.Gold -= gold
			df.Yuanqi -= yuanqi
			iv := &DFInvade{Day: today(), Level: level, OpenHour: hour, Start: start, Seed: uint64(now.UnixNano()),
				Gov: 1 + level/2, Leader: 2 + level, Monster: 6 + 2*level}
			for _, f := range invadeBoss(level) {
				iv.BossHP += int64(f.HP)
			}
			iv.BossMax = iv.BossHP
			df.Invade = iv
			df.log(s.role.Name + " 开启了 " + dfNum(int32(level)) + " 级魔潮入侵")
			ok = true
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO", s.dfJobInfo(p, df, me))
		return ok
	})
}

func containsInt(l []int, x int) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}

func onInvadeInfo(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		if df.Invade == nil {
			s.SendExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO", xym.Msg{"monster_boss_hp": 0, "die_list": []xym.Msg{}})
			return false
		}
		s.SendExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO", s.invadeInfo(df))
		return true
	})
}

var invadeKinds = map[string]int{"monster_governor": 0, "monster_leader": 1, "monster": 2, "monster_boss": 3}
var invadeContri = [4]int{5, 3, 1, 0}

func onInvadeFight(s *Session, p *Player, m xym.Msg) {
	kind, ok := invadeKinds[m["type"].(string)]
	var team2 []*battle.Fighter
	var dead []int
	var boss bool
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		iv := df.Invade
		switch {
		case !ok || iv.status(nowUnix()) != 0:
			s.sendMsg("魔潮入侵还没有开始或已经结束。")
		case kind == 0 && iv.Gov == 0 || kind == 1 && iv.Leader == 0 || kind == 2 && iv.Monster == 0:
			s.sendMsg("该魔妖已被清除！！")
		case kind == 3:
			boss, team2 = true, invadeBoss(iv.Level)
			for _, f := range team2 { // Boss 血量全洞府共享
				f.HP, f.MaxHP = int32(min(iv.BossHP, int64(f.MaxHP))), int32(iv.BossMax)
			}
		default:
			left := map[int]int{0: iv.Gov, 1: iv.Leader, 2: iv.Monster}[kind]
			team2 = iv.monsters(kind, left)
		}
		if team2 != nil {
			dead = append(dead, iv.user(s.role.UID).Dead...)
		} else {
			s.SendExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO", s.invadeInfo(df))
		}
		return false
	})
	if team2 == nil {
		return
	}
	for _, f := range team2 {
		f.Pos = 100 + f.Pos%100
	}
	team1, _ := p.dyTeam(dead)
	if len(team1) == 0 {
		s.sendMsg("出战护法全部阵亡，请先复活。")
		return
	}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), 0}, battle.Formation{}, s.actRng())
	alive, _ := battle.Survivors(r1, r2, res.Record)
	dmg := int64(0)
	if boss {
		dmg = recordDamage(res.Record, r2)
	}
	s.fightRecord(strconv.FormatInt(recordSeq.Add(1), 10), "魔潮", 50, r1, r2, res.Record)
	var finished *Dongfu
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		iv := df.Invade
		if iv == nil || iv.status(nowUnix()) != 0 {
			return false
		}
		u := iv.user(s.role.UID)
		u.Times++
		for _, f := range r1 {
			if !alive[f.Pos] {
				u.Dead = append(u.Dead, f.Pos)
			}
		}
		if boss {
			iv.BossHP = max(0, iv.BossHP-dmg)
			u.Contri += int(dmg / 10000)
		} else if res.Win {
			switch kind {
			case 0:
				iv.Gov = max(0, iv.Gov-1)
			case 1:
				iv.Leader = max(0, iv.Leader-1)
			case 2:
				iv.Monster = max(0, iv.Monster-1)
			}
			u.Contri += invadeContri[kind]
		}
		if iv.status(nowUnix()) == 1 && !iv.Rewarded {
			iv.Finish, iv.Rewarded = nowUnix(), true
			finished = df
		}
		p.track(evDongfu, 1)
		s.SendExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO", s.invadeInfo(df))
		return true
	})
	typ := 3
	if res.Win || boss && dmg > 0 {
		typ = 2
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(typ, nil))
	if finished != nil {
		s.srv.invadeReward(finished)
	}
}

// invadeReward：魔潮全部清除，参与者按贡献得洞府贡献，并邮件发元宝。
func (srv *Server) invadeReward(df *Dongfu) {
	var users []int32
	level := 0
	srv.dongfu.do(func() bool {
		iv := df.Invade
		level = iv.Level
		for uid, u := range iv.Users {
			if m := df.member(uid); m != nil {
				m.contribute(int32(u.Contri))
				users = append(users, uid)
			}
		}
		df.log("魔潮入侵被成功击退")
		return true
	})
	for _, uid := range users {
		srv.SendMail(uid, MailSpec{Title: "魔潮入侵", Text: "洞府成功击退了魔潮入侵，这是你的奖励。", Gold: int32(20 * level)})
	}
}

// recordDamage 是战报里对 team2 造成的总扣血（Boss 共享血量用）。
func recordDamage(rec []byte, team2 []*battle.Fighter) int64 {
	cmds, err := battle.DecodeRecord(rec)
	if err != nil {
		return 0
	}
	pos := map[int64]bool{}
	for _, f := range team2 {
		pos[int64(f.Pos)] = true
	}
	var n int64
	for _, c := range cmds {
		if c.ID == battle.CmdSubHP {
			for _, t := range c.Targets {
				if pos[t[0]] {
					n += t[1]
				}
			}
		}
	}
	return n
}

func (s *Session) invadeRelive(pos int) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		iv := df.Invade
		if iv == nil {
			return false
		}
		u := iv.user(s.role.UID)
		n := len(u.Dead)
		if pos > 0 {
			n = 0
			for _, d := range u.Dead {
				if d == pos {
					n = 1
				}
			}
		}
		if n == 0 {
			s.SendExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO", s.invadeInfo(df))
			return false
		}
		if u.Relive >= invadeFreeLive && !s.spend(0, int32(invadeLiveGold*n)) {
			s.SendExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO", s.invadeInfo(df))
			return false
		}
		u.Relive++
		if pos > 0 {
			var keep []int
			for _, d := range u.Dead {
				if d != pos {
					keep = append(keep, d)
				}
			}
			u.Dead = keep
		} else {
			u.Dead = nil
		}
		s.SendExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO", s.invadeInfo(df))
		return true
	})
}

func onInvadeUserList(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		list := []xym.Msg{}
		level, ca := 0, 0
		if iv := df.Invade; iv != nil {
			level, ca = iv.Level, 24300+iv.Level
			for uid, u := range iv.Users {
				name, icon, _ := s.srv.roleBrief(uid)
				list = append(list, xym.Msg{"usernum": uid, "vip_icon_ts": 0, "name": name, "icon": icon,
					"contribute": u.Contri, "relive_times": u.Relive, "fight_point": 0})
			}
		}
		s.SendExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_USER_LIST", xym.Msg{"level": level, "list": list, "monster_boss_ca": ca})
		return false
	})
}

func init() {
	Handle("DONGFU_PTO.C_OPEN_MONSTER_INVADE_LEVEL_INFO", onOpenInvadeLevel)
	Handle("DONGFU_PTO.C_OPEN_MONSTER_INVADE", onOpenInvade)
	Handle("DONGFU_PTO.C_MONSTER_INVADE_INFO", onInvadeInfo)
	Handle("DONGFU_PTO.C_MONSTER_INVADE_FIGHT", onInvadeFight)
	Handle("DONGFU_PTO.C_MONSTER_INVADE_RELIVE_FIGHTER", func(s *Session, p *Player, m xym.Msg) {
		s.invadeRelive(int(m["pos"].(int64)))
	})
	Handle("DONGFU_PTO.C_MONSTER_INVADE_RELIVE_ALL_FIGHTER", func(s *Session, p *Player, m xym.Msg) { s.invadeRelive(0) })
	Handle("DONGFU_PTO.C_GET_MONSTER_INVADE_USER_LIST", onInvadeUserList)
}
