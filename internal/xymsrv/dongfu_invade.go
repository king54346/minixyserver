package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 魔潮入侵（差事页「魔潮入侵」、内务「魔潮」→ view/PartyAdminiView.lua CPartyMoChaoAskBoxView、
// view/PartyMonsterFightView.lua、view/PartyView.lua refreshMonster）：
//   - 三个入口：
//     a. 野生山妖由 [人界门]（monster）入侵，30 级及以下玩家与之战斗获得 60% 攻击力 / 血量加成；
//     b. 山妖头目由 [地界门]（monster_leader）入侵，40 级及以下玩家与之战斗获得 30% 攻击力 / 血量加成；
//     c. 山妖督军由 [天界门]（monster_governor）入侵，所有玩家均以真实数据与之战斗。
//   - [魔潮入侵] 持续半小时（1800 秒），所有洞府成员于此半小时内可各进行 10 场战斗。
//   - 共 150 名守卫（野生山妖 50 + 山妖头目 50 + 山妖督军 50），战斗内每击杀 1 个山妖（战斗内护法），
//     都将减少剩余的山妖数量；玩家所有死亡的护法均会保留（复活每 1 个护法消耗 2 元宝，前 10 个免费）。
//   - 只有杀死了全部的野生山妖、山妖头目、山妖督军（150 名守卫），玩家才可以与终极 Boss [山妖大王]（monster_boss）战斗；
//     所有的 [山妖大王] 均由 3 个阶段构成，同样分为 [人界]、[地界]、[天界]，符合条件的玩家战斗也将获得数值加成；
//     山妖大王的血量将被记录，供玩家车轮战（猴将军 24301–24303 / 机关人 24304–24306）。
//   - 每天可以用洞府元宝开一次魔潮（1 级洞府 300 元宝、2 级 600 元宝、3 级 900 元宝，即 300×洞府等级）；
//     洞府升级的时候会触发魔潮，如果洞府升级魔潮失败了，洞府就升级失败。
//   - 所有参与的玩家将获得高额奖励，总贡献较高的玩家将额外获得元宝及稀有奖励。

const (
	invadeDuration  = 1800
	invadeGuardEach = 50
	invadeMaxTimes  = 10
	invadeFreeLive  = 10
	invadeLiveGold  = 2
	// 魔潮最高重数。客户端管理页的规则文案写死「魔潮最高为20层」（view/PartyAdminiView.lua），
	// 每级洞府开放 4 重（1级 1-4 重 … 5 级满级 1-20 重）——等级只决定怪物强度与消耗，
	// 层数由 invadeLevelCap 按洞府等级换算。
	invadeMaxLevel = 20
)

// invadeFrameParts 是魔潮入侵前三名额外获得的卡牌边框碎片个数（第 1/2/3 名）。
var invadeFrameParts = [3]int{5, 3, 2}

// invadeFramePartItems 返回该名次额外奖励的边框碎片（道具号 → 个数）。
// 只有前 3 名、且本次有贡献（contri > 0）才给；按个数发、不指定款，随机取 10443–10449，
// 同款重复会累加。seed 一般用 uid：同一个玩家的结果可复现，便于测试。
func invadeFramePartItems(rank, contri int, seed uint64) map[string]int32 {
	if rank >= 3 || contri <= 0 {
		return nil
	}
	out := map[string]int32{}
	rng := rand.New(rand.NewPCG(seed, uint64(rank*31+7)))
	for _, typ := range framePartTypes(rng, invadeFrameParts[rank]) {
		out["WP"+itoa(typ)]++
	}
	return out
}

// invadeLevelCap 是当前洞府等级下可开启的最高重数：4×洞府等级，封顶 invadeMaxLevel（20）。
// 早先是写死的 6，5 级洞府永远开不出第 7 重，和客户端「最高20层」的说明矛盾。
func invadeLevelCap(dfLv int32) int {
	return max(1, min(invadeMaxLevel, int(dfLv)*4))
}

var invadeHours = []int{12, 18, 21}

// invadeMinute 是时段的分钟数：客户端把三个选项写死成 12:02 / 18:02 / 21:02，
// 所以「12 点」这个时段实际是 12:02 开始。
const invadeMinute = 2

// invadeNow 是取当前时间的钩子（测试用来固定「现在」，好让时段判断可复现）。
var invadeNow = time.Now

// invadeSlotStart 把「选择的时段」换算成今天的具体时间戳；该时段已经过了就返回提示。
// 客户端给的只有小时（open_hour），分钟固定为 invadeMinute。
func invadeSlotStart(hour int, now time.Time) (int64, string) {
	y, mo, d := now.Date()
	at := time.Date(y, mo, d, hour, invadeMinute, 0, 0, now.Location())
	if !at.After(now) {
		return 0, fmt.Sprintf("%d:%02d 这个时段已经过了，请选择更晚的时段。", hour, invadeMinute)
	}
	return at.Unix(), ""
}

type DFInvade struct {
	Users     map[int32]*DFInvadeUser `json:"users,omitempty"`
	Day       string                  `json:"day"`
	Level     int                     `json:"level"`
	OpenHour  int                     `json:"open_hour"`
	Start     int64                   `json:"start"`
	Finish    int64                   `json:"finish,omitempty"` // 全部清除的时刻
	Gov       int                     `json:"gov"`              // 山妖督军（天界门）剩余数
	Leader    int                     `json:"leader"`           // 山妖头目（地界门）剩余数
	Monster   int                     `json:"monster"`          // 野生山妖（人界门）剩余数
	BossKind  int                     `json:"boss_kind"`        // 0 猴将军（金刚猴 24301–24303） 1 机关人（24304–24306）
	BossStage int                     `json:"boss_stage"`       // 1 人界 2 地界 3 天界
	BossHP    int64                   `json:"boss_hp"`
	BossMax   int64                   `json:"boss_max"`
	BossKiller int32                  `json:"boss_killer,omitempty"` // 对山妖大王最后一击的人（官方 50226 只给他）
	Seed      uint64                  `json:"seed"`
	Icons     [3]int32                `json:"icons"`              // 督军 / 头目 / 野生山妖的形象
	Upgrade   bool                    `json:"upgrade,omitempty"`  // 是否由洞府升级触发
	FailLog   bool                    `json:"fail_log,omitempty"` // 升级魔潮超时失败是否已记录
	Rewarded  bool                    `json:"rewarded,omitempty"`
}

type DFInvadeUser struct {
	Contri int   `json:"contri"`
	Times  int   `json:"times"`
	Relive int   `json:"relive"`
	Dead   []int `json:"dead,omitempty"`
	Last   int64 `json:"last,omitempty"` // 上次挑战的时刻（洞府争夺的挑战间隔）
}

// cd 是洞府争夺里距离下次能挑战还有几秒。
func (u *DFInvadeUser) cd(now int64) int64 { return max(0, u.Last+gankUserCD-now) }

func (iv *DFInvade) stage() int {
	if iv.BossStage < 1 {
		return 1
	}
	if iv.BossStage > 3 {
		return 3
	}
	return iv.BossStage
}

func (iv *DFInvade) bossCA() int32 {
	return int32(24300 + (iv.BossKind%2)*3 + iv.stage())
}

func (iv *DFInvade) initBossStage(stage int) {
	iv.BossStage = stage
	iv.BossHP = 0
	for _, f := range invadeBoss(iv.bossCA(), iv.Level) {
		iv.BossHP += int64(f.HP)
	}
	iv.BossMax = iv.BossHP
}

// status 是 S_SEND_DONGFU_JOB_INFO 的 invade_status：
// 4 等待、0 进行中、1 胜利、2 失败、3 无。
func (iv *DFInvade) status(now int64) int {
	switch {
	case iv == nil || iv.Day != today() && now >= iv.Start+invadeDuration:
		return 3
	case now < iv.Start:
		return 4
	case iv.BossHP <= 0 && iv.stage() >= 3 && iv.Gov+iv.Leader+iv.Monster == 0:
		return 1
	case now < iv.Start+invadeDuration:
		return 0
	}
	return 2
}

func (df *Dongfu) checkInvadeTimeout(now int64) {
	iv := df.Invade
	if iv != nil && iv.status(now) == 2 && iv.Upgrade && !iv.FailLog {
		iv.FailLog = true
		df.log("未能及时击退魔潮入侵，洞府升级失败")
	}
}

func (df *Dongfu) startInvade(level, hour int, start int64, upgrade bool) *DFInvade {
	if level < 1 {
		level = 1
	}
	iv := &DFInvade{
		Day:      today(),
		Level:    level,
		OpenHour: hour,
		Start:    start,
		Seed:     uint64(time.Now().UnixNano()),
		Gov:      invadeGuardEach,
		Leader:   invadeGuardEach,
		Monster:  invadeGuardEach,
		BossKind: (level - 1) % 2,
		Upgrade:  upgrade,
	}
	iv.initBossStage(1)
	for k := range 3 {
		iv.monsters(k, invadeGuardEach)
	}
	df.Invade = iv
	return iv
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

func invadeBoss(ca int32, level int) []*battle.Fighter {
	return battle.EnemyTeam(gamedata.GateMode{CA: ca, Param: 100 * max(1, level)})
}

// invadeBuffPct：人界（stage 1）30 级及以下加成 60%，地界（stage 2）40 级及以下加成 30%，天界（stage 3）无加成。
func invadeBuffPct(grade, stage int) int {
	switch stage {
	case 1:
		if grade <= 30 {
			return 60
		}
	case 2:
		if grade <= 40 {
			return 30
		}
	}
	return 0
}

// monsters 是 kind（0 山妖督军 1 山妖头目 2 野生山妖）的一队：5 / 4 / 3 星护法，每场最多上阵 5 名（不超过剩余数量）。
func (iv *DFInvade) monsters(kind, left int) []*battle.Fighter {
	rng := rand.New(rand.NewPCG(iv.Seed+uint64(kind*1000+left), 7))
	star := 5 - kind
	n := min(5, max(1, left))
	grade := min(100, 25+(2-kind)*10+5*max(1, iv.Level))
	var team []*battle.Fighter
	for slot := 1; slot <= n; slot++ {
		if t := slPick(star, rng); t != 0 {
			if x := petFighter(t, grade, slot, 2+max(1, iv.Level)); x != nil {
				team = append(team, x)
			}
		}
	}
	if kind < 3 && iv.Icons[kind] == 0 && len(team) > 0 {
		iv.Icons[kind] = team[0].Icon
	}
	return team
}

func (srv *Server) invadeJob(df *Dongfu, m xym.Msg) {
	now := nowUnix()
	df.checkInvadeTimeout(now)
	iv := df.Invade
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
			iv.monsters(k, invadeGuardEach)
		}
	}
	return xym.Msg{
		"monster_left":          iv.Monster,
		"monster_leader_left":   iv.Leader,
		"monster_governor_left": iv.Gov,
		"monster_boss_hp":       iv.BossHP,
		"monster_boss_max_hp":   iv.BossMax,
		"challenge_times":       max(0, invadeMaxTimes-u.Times),
		"free_relive_times":     max(0, invadeFreeLive-u.Relive),
		"die_list":              die,
		"monster_icon":          iv.Icons[2],
		"monster_leader_icon":   iv.Icons[1],
		"monster_governor_icon": iv.Icons[0],
		"monster_boss_ca":       iv.bossCA(),
	}
}

func onOpenInvadeLevel(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		list := []xym.Msg{}
		for l := 1; l <= invadeLevelCap(df.Lv); l++ {
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
	opened := false
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		now := nowUnix()
		df.checkInvadeTimeout(now)
		gold := 300 * df.Lv // 1 级洞府 300 元宝、2 级 600 元宝、3 级 900 元宝
		ok := false
		switch {
		case !isAdmin(me.Position):
			s.sendMsg("只有府主和洞主可以开启魔潮入侵。")
		case level < 1 || level > invadeLevelCap(df.Lv):
			s.sendMsg("魔潮等级不正确（当前洞府最多 " + itoa(invadeLevelCap(df.Lv)) + " 重）。")
		case df.Invade != nil && (df.Invade.status(now) == 0 || df.Invade.status(now) == 4):
			s.sendMsg("当前已有正在进行的魔潮入侵。")
		case df.InvadeDay == today() || df.Invade != nil && df.Invade.Day == today() && !df.Invade.Upgrade:
			s.sendMsg("魔潮入侵每天只能开启一次。")
		case df.Gold < gold:
			s.sendMsg("洞府元宝不足，开启需要 " + dfNum(gold) + " 洞府元宝。")
		default:
			if !containsInt(invadeHours, hour) {
				s.sendMsg("开启时段不正确。")
				break
			}
			// 官方：先选「下一次魔潮开启时间」，到点才开，不是点了就开。
			start, slotMsg := invadeSlotStart(hour, invadeNow())
			if slotMsg != "" {
				s.sendMsg(slotMsg)
				break
			}
			df.Gold -= gold
			df.InvadeDay = today()
			df.startInvade(level, hour, start, false)
			df.log(s.role.Name + " 开启了 " + dfNum(int32(level)) + " 级魔潮入侵")
			s.srv.pushDongfuEvent(df, dfPushInvade, "本洞府已开启魔潮入侵，可前往「事件」参战。")
			ok = true
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO", s.dfJobInfo(p, df, me))
		opened = ok
		return ok
	})
	if opened {
		// 排期成功后立刻刷新主界面「事件」，让魔潮那条马上出现（等待态显示「距开始」倒计时）。
		// 放在 dfMine 外面：sendEventList 会取洞府锁，锁里再取会死锁；
		// 用 sendEventList 而不是 pushEventList——handler 已经持有 p.mu，后者会重复加锁。
		s.sendEventList(p)
	}
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
		df.checkInvadeTimeout(nowUnix())
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
	var stage int
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		now := nowUnix()
		df.checkInvadeTimeout(now)
		iv := df.Invade
		switch {
		case !ok || iv == nil || iv.status(now) != 0:
			s.sendMsg("魔潮入侵还没有开始或已经结束。")
		case iv.user(s.role.UID).Times >= invadeMaxTimes:
			s.sendMsg("本次魔潮入侵你的10场战斗次数已用完。")
		case kind == 0 && iv.Gov == 0 || kind == 1 && iv.Leader == 0 || kind == 2 && iv.Monster == 0:
			s.sendMsg("该魔妖已被清除！！")
		case kind == 3 && iv.Gov+iv.Leader+iv.Monster > 0:
			s.sendMsg("只有杀死了全部的野生山妖、山妖头目、山妖督军，才可以与山妖大王战斗！")
		case kind == 3:
			boss, stage = true, iv.stage()
			team2 = invadeBoss(iv.bossCA(), iv.Level)
			for _, f := range team2 { // Boss 血量全洞府共享
				f.HP, f.MaxHP = int32(min(iv.BossHP, int64(f.MaxHP))), int32(iv.BossMax)
			}
		default:
			stage = 3 - kind // 2 野生山妖(人界=1) 1 山妖头目(地界=2) 0 山妖督军(天界=3)
			left := map[int]int{0: iv.Gov, 1: iv.Leader, 2: iv.Monster}[kind]
			team2 = iv.monsters(kind, left)
		}
		if team2 != nil {
			dead = append(dead, iv.user(s.role.UID).Dead...)
		} else if iv != nil {
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
	if pct := invadeBuffPct(s.role.Grade, stage); pct > 0 {
		for _, f := range team1 {
			f.Att = f.Att * int32(100+pct) / 100
			baseHP := int32(int64(f.HP-f.PanelSetHP) * int64(100+pct) / 100)
			f.HP = int32(int64(f.HP) * int64(100+pct) / 100)
			f.PanelSetHP = f.HP - baseHP
			f.MaxHP = f.HP
		}
	}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), 0}, battle.Formation{}, s.actRng())
	alive, _ := battle.Survivors(r1, r2, res.Record)
	dmg := int64(0)
	killed := 0
	if boss {
		dmg = recordDamage(res.Record, r2)
	} else {
		for _, f := range r2 {
			if !alive[f.Pos] {
				killed++
			}
		}
		if res.Win {
			killed = len(r2)
		}
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
			if res.Win {
				iv.BossHP = 0
			} else {
				iv.BossHP = max(0, iv.BossHP-dmg)
			}
			if dmg > 0 {
				u.Contri += max(1, int(dmg/10000))
			}
			if iv.BossHP <= 0 {
				if iv.stage() < 3 {
					iv.initBossStage(iv.stage() + 1)
				} else if iv.BossKiller == 0 {
					// 终阶段把妖王打死的人：官方 50226「BOSS击败」奖励（五星飞升护法）只给他。
					iv.BossKiller = s.role.UID
				}
			}
		} else if killed > 0 {
			var dec int
			switch kind {
			case 0:
				dec = min(iv.Gov, killed)
				iv.Gov -= dec
			case 1:
				dec = min(iv.Leader, killed)
				iv.Leader -= dec
			case 2:
				dec = min(iv.Monster, killed)
				iv.Monster -= dec
			}
			u.Contri += dec * invadeContri[kind]
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
	if res.Win || boss && dmg > 0 || !boss && killed > 0 {
		typ = 2
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(typ, nil))
	if finished != nil {
		s.srv.invadeReward(finished)
	}
}

// invadeRankItems 是官方「立秋魔潮排行」奖励（客户端 auto/reward.lua 50212–50218），
// 按贡献名次（0 起）取道具。**已去掉无字天书碎片（WP11154）**——用户要求不发碎片，
// 缺的那部分用元宝/银两补（见 invadeMailSpec）。
func invadeRankItems(rank int) map[string]int32 {
	switch {
	case rank == 0: // 50212 排行1
		return map[string]int32{"GPZFS2": 3, "WP10442": 5, "WP10440": 20, "WP10130": 10}
	case rank < 3: // 50213 排行2-3
		return map[string]int32{"GPZFS2": 2, "WP10442": 3, "WP10440": 15, "WP10130": 10}
	case rank < 10: // 50214 排行4-10
		return map[string]int32{"GPZFS2": 2, "WP10442": 1, "WP10440": 15, "WP10130": 10}
	case rank < 20: // 50215 排行11-20
		return map[string]int32{"GPZFS2": 1, "WP10442": 1, "WP10440": 15, "WP10130": 5}
	case rank < 50: // 50216 排行21-50
		return map[string]int32{"GPZFS2": 1, "WP10442": 1, "WP10440": 10, "WP10130": 5}
	case rank < 100: // 50217 排行51-100
		return map[string]int32{"GPZFS2": 1, "WP10442": 1, "WP10440": 10, "WP10130": 5}
	default: // 50218 排行101-200
		return map[string]int32{"GPZFS2": 1, "WP10442": 1, "WP10440": 10}
	}
}

// invadeMailSpec 组装单个参与者的魔潮奖励邮件：
//   - 50227 魔潮完成奖励：金宝箱(WP10002)×10 + 黄金钥匙(WP10003)×10，所有参与者都有；
//   - 50212–50218 排行奖励：按贡献名次（贡献 0 的不给），已去掉无字天书碎片；
//   - 元宝 200×重数、银两 50万×重数（所有参与者）；前 3 名再各加 (3-名次)×200×重数 元宝；
//   - 50226 BOSS击败：五星飞升护法(GPFSDZ5)×1，只给对山妖大王最后一击的人。
//
// 不发神魂：官方表 soul 字段全为空（早先按领土战的习惯塞过，已删）。
func invadeMailSpec(level, rank, contri int, upgraded bool, newLv int32, bossKiller bool) MailSpec {
	level = max(1, level)
	gold, cash := int32(200*level), int32(500000*level)
	items := map[string]int32{"WP10002": 10, "WP10003": 10} // 50227
	text := "洞府成功击退了魔潮入侵，这是所有参与成员的高额奖励。"
	if upgraded {
		text = "洞府成功击退了魔潮入侵并升到了 " + dfNum(newLv) + " 级，这是你的奖励。"
	}
	if contri > 0 {
		for k, v := range invadeRankItems(rank) {
			items[k] += v
		}
		if rank < 3 {
			gold += int32((3 - rank) * 200 * level)
		}
		text += "你的总贡献名列前茅，额外获得了元宝及稀有奖励！"
	}
	if bossKiller {
		items["GPFSDZ5"] = 1
		text += "你完成了对山妖大王的最后一击，额外获得五星飞升护法！"
	}
	return MailSpec{Title: "魔潮入侵", Text: text, Gold: gold, Cash: cash, Items: items}
}

// invadeReward：魔潮全部清除，所有参与者按官方奖励表发奖（见 invadeMailSpec）；
// 若为洞府升级触发的魔潮，则洞府成功升 1 级。
func (srv *Server) invadeReward(df *Dongfu) {
	type rankEntry struct {
		uid    int32
		contri int
	}
	var ranks []rankEntry
	killer := int32(0)
	level := 1
	upgraded := false
	newLv := int32(0)
	srv.dongfu.do(func() bool {
		iv := df.Invade
		killer = iv.BossKiller
		level = max(1, iv.Level)
		for uid, u := range iv.Users {
			if m := df.member(uid); m != nil {
				m.contribute(int32(max(20, u.Contri)))
				ranks = append(ranks, rankEntry{uid: uid, contri: u.Contri})
			}
		}
		if iv.Upgrade && df.Lv < dfMaxLv {
			df.Lv++
			upgraded = true
			newLv = df.Lv
			df.log("魔潮入侵被成功击退，洞府升到了 " + dfNum(df.Lv) + " 级")
		} else {
			df.log("魔潮入侵被成功击退")
		}
		return true
	})
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].contri != ranks[j].contri {
			return ranks[i].contri > ranks[j].contri
		}
		return ranks[i].uid < ranks[j].uid
	})
	winList := []xym.Msg{}
	for i := 0; i < min(3, len(ranks)); i++ {
		name, icon, _ := srv.roleBrief(ranks[i].uid)
		winList = append(winList, xym.Msg{"usernum": ranks[i].uid, "vip_icon_ts": 0, "name": name, "icon": icon})
	}
	for rank, r := range ranks {
		spec := invadeMailSpec(level, rank, r.contri, upgraded, newLv, killer != 0 && r.uid == killer)
		for k, v := range invadeFramePartItems(rank, r.contri, uint64(r.uid)) {
			spec.Items[k] += v
		}
		srv.SendMail(r.uid, spec)
		if sess := srv.sessionOf(r.uid); sess != nil {
			sess.SendExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_WIN_LIST", xym.Msg{"list": winList})
			if upgraded {
				var info xym.Msg
				srv.dongfu.do(func() bool {
					if me := df.member(r.uid); me != nil {
						info = srv.dfInfoMsg(df, me)
					}
					return false
				})
				if info != nil {
					sess.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", info)
				}
			}
		}
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
		freeLeft := max(0, invadeFreeLive-u.Relive)
		freeUsed := min(n, freeLeft)
		cost := int32((n - freeUsed) * invadeLiveGold)
		if cost > 0 && !s.spend(0, cost) {
			s.SendExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO", s.invadeInfo(df))
			return false
		}
		u.Relive += n
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
		level, ca := 0, int32(0)
		if iv := df.Invade; iv != nil {
			level, ca = iv.Level, iv.bossCA()
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
