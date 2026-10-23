package xymsrv

import (
	"slices"
	"strconv"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

// 洞府争夺（内务「战争」→ view/PartyFightDetailView.lua 争夺 / 反击名单，view/PartyLueDuoFightView.lua 界门战斗）：
//   - 府主 / 洞主在争夺名单（C_GET_GANK_LIST → S_SEND_GANK_LIST{list, gank_times}）里选目标 C_GANK_DONGFU{dongfu_id}，
//     回 S_SEND_GANK_FIGHT_INFO 打开界门战斗；反击名单 C_GET_REVENGE_LIST / C_REVENGE_DONGFU（xtype 2），
//     「燃烧怒焰」C_REVENGE_ADD 花 50 洞府元气加 3% 战斗加成（持续到本周日，只作用于洞府玩法）。
//   - 进攻方攻击对方三个界门（守卫）C_START_GANK_FIGHT{type}，赢一场该界门援军 −1；防守方援助 C_START_GANK_HELP_FIGHT
//     赢一场援军 +1。「争夺成功将夺得目标洞府的灵气」。
//   - 差事页事件（S_SEND_DONGFU_JOB_INFO.gank_*）：status 1 进行中、2 进攻方胜、3 防守方胜；side 1 进攻 2 防守；
//     「攻击对方界门可减少援军数 / 援助己方界门可增加援军数」。C_GANK_PROTECT 停战（btnStopFight）。
//
// 本服：除了真实洞府，再给 5 个野外洞府当目标（守卫是论道假玩家的阵容），保证单人也能玩。
// 估计：争夺持续 30 分钟；每个界门援军 3；三个界门援军都打光即进攻方胜，夺走对方灵气池 20%（野外洞府 300×等级）；
// 每天首次争夺免费，之后每次 100 洞府元宝，最多 5 次；停战花 100×等级 洞府元宝，当天不能被争夺；
// 阵亡护法本场不能再出战，复活每次 20 元宝（前 3 次免费）；燃烧怒焰最多 +15%。

const (
	gankDuration = 1800
	gankGuardNum = 3
	gankMaxTimes = 5
	gankNPCBase  = 800001
	gankNPCCount = 5
)

var gankNPCNames = []string{"幽冥古洞", "落霞仙府", "青丘狐穴", "黑风山洞", "盘丝妖窟"}

type DFGank struct {
	Target int32                   `json:"target"` // 对方洞府号（野外洞府 800001 起）
	Side   int                     `json:"side"`   // 1 进攻 2 防守
	Type   int                     `json:"type"`   // 1 争夺 2 反击
	Start  int64                   `json:"start"`
	End    int64                   `json:"end,omitempty"` // 分出胜负的时刻
	Winner int                     `json:"winner,omitempty"`
	Num    [3]int                  `json:"num"` // 防守方三个界门的援军（只记在防守方 / 进攻野外时记在进攻方）
	Users  map[int32]*DFInvadeUser `json:"users,omitempty"`
	Stolen int32                   `json:"stolen,omitempty"`
}

// DFWar 是洞府的争夺记录（Dongfu.War）。
type DFWar struct {
	Gank    *DFGank `json:"gank,omitempty"`
	Day     string  `json:"day,omitempty"`
	Times   int     `json:"times,omitempty"`
	Protect string  `json:"protect,omitempty"` // 停战的日期
	Revenge int     `json:"revenge,omitempty"` // 燃烧怒焰加成 %
	RevWeek string  `json:"rev_week,omitempty"`
	Enemies []int32 `json:"enemies,omitempty"` // 争夺过本洞府的洞府（反击名单）
}

func (df *Dongfu) war() *DFWar {
	if df.War == nil {
		df.War = &DFWar{}
	}
	w := df.War
	if w.Day != today() {
		w.Day, w.Times = today(), 0
	}
	return w
}

func isGankNPC(id int32) bool { return id >= gankNPCBase && id < gankNPCBase+gankNPCCount }

// gankStatus：1 进行中 2 进攻方胜 3 防守方胜（0 = 没有）。
func (g *DFGank) status(now int64) int {
	switch {
	case g == nil:
		return 0
	case g.Winner != 0:
		return g.Winner
	case now < g.Start+gankDuration:
		return 1
	}
	return 3
}

func (g *DFGank) user(uid int32) *DFInvadeUser {
	if g.Users == nil {
		g.Users = map[int32]*DFInvadeUser{}
	}
	u := g.Users[uid]
	if u == nil {
		u = &DFInvadeUser{}
		g.Users[uid] = u
	}
	return u
}

// defenderGank 是存援军数的那份记录（进攻野外洞府时就是进攻方自己的）。调用方持有洞府锁。
func (d *Dongfus) defenderGank(df *Dongfu) *DFGank {
	g := df.War.Gank
	if g == nil || g.Side == 2 || isGankNPC(g.Target) {
		return g
	}
	if o := d.List[g.Target]; o != nil && o.War != nil && o.War.Gank != nil {
		return o.War.Gank
	}
	return g
}

type gankTarget struct {
	id                  int32
	name                string
	icon, lv, num, size int32
}

func (d *Dongfus) gankTargetOf(id int32) *gankTarget {
	if isGankNPC(id) {
		i := id - gankNPCBase
		return &gankTarget{id: id, name: gankNPCNames[i], icon: 6 + i, lv: 1 + i, num: 20 + 3*i, size: 25 + 5*i}
	}
	if o := d.List[id]; o != nil {
		return &gankTarget{id: id, name: o.Name, icon: o.Icon, lv: o.Lv, num: int32(len(o.Members)), size: o.size()}
	}
	return nil
}

func (t *gankTarget) msg() xym.Msg {
	return xym.Msg{"dongfu_id": t.id, "name": t.name, "server_name": "细水长流", "member_num": t.num, "size": t.size,
		"icon": t.icon, "lv": t.lv, "rank": 0}
}

func (s *Session) gankList(df *Dongfu) {
	d := s.srv.dongfu
	list := []xym.Msg{}
	for _, o := range d.sorted(func(o *Dongfu) bool { return o.ID != df.ID && (o.War == nil || o.War.Protect != today()) }) {
		list = append(list, d.gankTargetOf(o.ID).msg())
	}
	for i := range gankNPCCount {
		list = append(list, d.gankTargetOf(gankNPCBase+int32(i)).msg())
	}
	s.SendExt("DONGFU_PTO.S_SEND_GANK_LIST", xym.Msg{"list": list, "gank_times": df.war().Times})
}

// gankJob 填差事页的争夺事件。
func (srv *Server) gankJob(df *Dongfu, m xym.Msg) {
	if df.War == nil || df.War.Gank == nil {
		return
	}
	g := df.War.Gank
	now := nowUnix()
	st := g.status(now)
	if st == 0 || g.Winner == 0 && now > g.Start+gankDuration+86400 {
		return
	}
	t := srv.dongfu.gankTargetOf(g.Target)
	if t == nil {
		return
	}
	m["gank_status"], m["gank_side"], m["gank_type"] = st, g.Side, g.Type
	m["gank_dongfu_icon"], m["gank_dongfu_name"], m["gank_server_name"] = t.icon, t.name, "细水长流"
	if st == 1 {
		m["gank_left_time"] = g.Start + gankDuration - now
	} else if g.End > 0 {
		m["gank_finish_time"] = g.End - g.Start
	}
	m["gank_revenge_add"] = df.war().Revenge
}

// guardFoe 是界门守卫 i 的阵容：真实洞府用守卫成员的出战阵容，空缺或野外洞府用论道假玩家。
func (srv *Server) guardFoe(target int32, i int, me *Player) *ldFoe {
	var uid int32
	srv.dongfu.do(func() bool {
		if o := srv.dongfu.List[target]; o != nil {
			uid = o.Guards[i]
		}
		return false
	})
	if uid != 0 {
		if f := srv.realFoe(uid, me); f != nil && len(f.Team) > 0 {
			return f
		}
	}
	n := int(target%100)*3 + i
	return fakeFoeCached(int32(ldFakeUIDBase + 40 + n%60))
}

func (s *Session) gankInfo(p *Player, df *Dongfu) xym.Msg {
	d := s.srv.dongfu
	g := df.War.Gank
	dg := d.defenderGank(df)
	u := g.user(s.role.UID)
	guards := []xym.Msg{}
	target := g.Target
	if g.Side == 2 {
		target = df.ID
	}
	for i, t := range dfGuardTypes {
		m := xym.Msg{"usernum": 0, "vip_icon_ts": 0, "icon": 0, "type": t, "name": "守卫", "num": dg.Num[i], "max_num": gankGuardNum}
		if o := d.List[target]; o != nil && o.Guards[i] != 0 {
			name, icon, _ := s.srv.roleBrief(o.Guards[i])
			m["usernum"], m["icon"], m["name"] = o.Guards[i], icon, name
		} else {
			f := fakeFoeCached(int32(ldFakeUIDBase + 40 + (int(target%100)*3+i)%60))
			m["icon"], m["name"] = f.Icon, f.Name
		}
		guards = append(guards, m)
	}
	die := []xym.Msg{}
	for _, pos := range u.Dead {
		die = append(die, xym.Msg{"pos": pos})
	}
	return xym.Msg{"status": g.status(nowUnix()), "side": g.Side, "guard": guards, "lingqi": df.Lingqi,
		"challenge_times": u.Times, "challenge_cd": 0, "free_relive_times": max(0, invadeFreeLive-u.Relive), "die_list": die}
}

func (s *Session) startGank(p *Player, target int32, typ int) {
	d := s.srv.dongfu
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		w := df.war()
		t := d.gankTargetOf(target)
		var o *Dongfu
		if !isGankNPC(target) {
			o = d.List[target]
		}
		cost := int32(0)
		if w.Times > 0 {
			cost = 100
		}
		fail := func(msg string) bool {
			s.sendMsg(msg)
			if df.War.Gank != nil && df.War.Gank.status(nowUnix()) == 1 {
				s.SendExt("DONGFU_PTO.S_SEND_GANK_FIGHT_INFO", s.gankInfo(p, df))
			}
			return false
		}
		switch {
		case !isAdmin(me.Position):
			return fail("只有府主和洞主可以发起争夺。")
		case w.Gank != nil && w.Gank.status(nowUnix()) == 1:
			s.SendExt("DONGFU_PTO.S_SEND_GANK_FIGHT_INFO", s.gankInfo(p, df))
			return false
		case t == nil || target == df.ID:
			return fail("目标洞府不存在。")
		case o != nil && (o.War != nil && o.War.Gank != nil && o.War.Gank.status(nowUnix()) == 1 || o.War != nil && o.War.Protect == today()):
			return fail("对方正处于争夺或停战中。")
		case w.Times >= gankMaxTimes:
			return fail("今天的争夺次数已用完。")
		case df.Gold < cost:
			return fail("洞府元宝不足。")
		}
		df.Gold -= cost
		w.Times++
		now := nowUnix()
		w.Gank = &DFGank{Target: target, Side: 1, Type: typ, Start: now, Num: [3]int{gankGuardNum, gankGuardNum, gankGuardNum}}
		if o != nil {
			ow := o.war()
			ow.Gank = &DFGank{Target: df.ID, Side: 2, Type: typ, Start: now, Num: [3]int{gankGuardNum, gankGuardNum, gankGuardNum}}
			ow.Enemies = addUniq(ow.Enemies, df.ID)
			o.log("洞府「" + df.Name + "」对本洞府发起了争夺")
		}
		df.log(s.role.Name + " 对「" + t.name + "」发起了争夺")
		s.SendExt("DONGFU_PTO.S_SEND_GANK_FIGHT_INFO", s.gankInfo(p, df))
		return true
	})
}

// gankFight：进攻（help=false）或援助（help=true）第 i 个界门。
func (s *Session) gankFight(p *Player, typ string, help bool) {
	i := slices.Index(dfGuardTypes, typ)
	d := s.srv.dongfu
	var target int32
	var dead []int
	var ok bool
	var rev int
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		g := df.war().Gank
		switch {
		case i < 0 || g == nil || g.status(nowUnix()) != 1:
			s.sendMsg("争夺已经结束。")
		case help != (g.Side == 2):
			s.sendMsg("不能这样做。")
		case !help && d.defenderGank(df).Num[i] == 0:
			s.sendMsg("该守卫已被击破！")
		case help && d.defenderGank(df).Num[i] >= gankGuardNum:
			s.sendMsg("该界门援军已满。")
		default:
			ok, target, dead, rev = true, g.Target, append(dead, g.user(s.role.UID).Dead...), df.war().Revenge
			if help {
				target = df.ID
			}
		}
		if !ok && g != nil {
			s.SendExt("DONGFU_PTO.S_SEND_GANK_FIGHT_INFO", s.gankInfo(p, df))
		}
		return false
	})
	if !ok {
		return
	}
	var foe *ldFoe
	if help { // 援助：和进攻方的同位守卫（或野外的守卫）打
		var attacker int32
		s.dfMine(func(df *Dongfu, me *DFMember) bool { attacker = df.War.Gank.Target; return false })
		foe = s.srv.guardFoe(attacker, i, p)
	} else {
		foe = s.srv.guardFoe(target, i, p)
	}
	team1, _ := p.dyTeam(dead)
	if len(team1) == 0 {
		s.sendMsg("出战护法全部阵亡，请先复活。")
		return
	}
	for _, f := range team1 { // 燃烧怒焰
		f.Att += f.Att * int32(rev) / 100
	}
	team2 := battle.CloneTeam(foe.Team)
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), foe.Leader}, foe.Zf, s.actRng())
	alive, _ := battle.Survivors(r1, r2, res.Record)
	s.fightRecord(strconv.FormatInt(recordSeq.Add(1), 10), foe.Name, foe.Grade, r1, r2, res.Record)
	var stolen struct {
		from *Dongfu
		n    int32
	}
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		g := df.War.Gank
		if g == nil || g.status(nowUnix()) != 1 {
			return false
		}
		u := g.user(s.role.UID)
		u.Times++
		for _, f := range r1 {
			if !alive[f.Pos] {
				u.Dead = append(u.Dead, f.Pos)
			}
		}
		dg := d.defenderGank(df)
		if res.Win {
			u.Contri++
			me.contribute(2)
			if help {
				dg.Num[i] = min(gankGuardNum, dg.Num[i]+1)
			} else {
				dg.Num[i] = max(0, dg.Num[i]-1)
				if dg.Num == [3]int{} { // 三个界门都打光：进攻方胜，夺灵气
					now := nowUnix()
					g.Winner, g.End = 2, now
					n := 300 * (1 + (g.Target - gankNPCBase))
					if o := d.List[g.Target]; o != nil {
						o.accrueLingqi()
						n = o.Lingqi / 5
						o.Lingqi -= n
						if og := o.War.Gank; og != nil {
							og.Winner, og.End = 2, now
						}
						stolen.from = o
					}
					g.Stolen = n
					df.accrueLingqi()
					df.Lingqi = min(df.lingqiMax(), df.Lingqi+n)
					stolen.n = n
					df.log("争夺「" + d.gankTargetOf(g.Target).name + "」成功，夺得灵气 " + dfNum(n))
				}
			}
		}
		p.track(evDongfu, 1)
		s.SendExt("DONGFU_PTO.S_SEND_GANK_FIGHT_INFO", s.gankInfo(p, df))
		return true
	})
	if stolen.from != nil {
		s.srv.dongfu.do(func() bool { stolen.from.log("被争夺，损失灵气 " + dfNum(stolen.n)); return true })
	}
	typ2 := 3
	if res.Win {
		typ2 = 2
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(typ2, nil))
}

func onGankFightInfo(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		if df.war().Gank == nil {
			s.sendMsg("当前没有争夺。")
			return false
		}
		s.SendExt("DONGFU_PTO.S_SEND_GANK_FIGHT_INFO", s.gankInfo(p, df))
		return false
	})
}

func onGankUserList(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		list := []xym.Msg{}
		if g := df.war().Gank; g != nil {
			for uid, u := range g.Users {
				name, icon, _ := s.srv.roleBrief(uid)
				list = append(list, xym.Msg{"usernum": uid, "vip_icon_ts": 0, "name": name, "icon": icon,
					"contribute": u.Contri, "kill_num": u.Contri, "relive_times": u.Relive})
			}
		}
		s.SendExt("DONGFU_PTO.S_SEND_GANK_USER_LIST", xym.Msg{"list": list})
		return false
	})
}

func onGankRelive(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		g := df.war().Gank
		if g == nil {
			return false
		}
		u := g.user(s.role.UID)
		if len(u.Dead) > 0 && (u.Relive < invadeFreeLive || s.spend(0, int32(invadeLiveGold*len(u.Dead)))) {
			u.Relive++
			u.Dead = nil
		}
		s.SendExt("DONGFU_PTO.S_SEND_GANK_FIGHT_INFO", s.gankInfo(p, df))
		return true
	})
}

func onGankProtect(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		w := df.war()
		cost := 100 * df.Lv
		ok := false
		switch {
		case !isAdmin(me.Position):
			s.sendMsg("只有府主和洞主可以停战。")
		case w.Protect == today():
			s.sendMsg("今天已经停战了。")
		case df.Gold < cost:
			s.sendMsg("洞府元宝不足，停战需要 " + dfNum(cost) + "。")
		default:
			df.Gold -= cost
			w.Protect = today()
			s.sendMsg("停战成功，今天其他洞府不能对本洞府发起争夺。")
			ok = true
		}
		s.gankList(df)
		return ok
	})
}

func (s *Session) revengeList(df *Dongfu) {
	d := s.srv.dongfu
	w := df.war()
	if wk := weekKey(time.Now()); w.RevWeek != wk {
		w.RevWeek, w.Revenge = wk, 0
	}
	list := []xym.Msg{}
	for _, id := range w.Enemies {
		if t := d.gankTargetOf(id); t != nil {
			list = append(list, t.msg())
		}
	}
	s.SendExt("DONGFU_PTO.S_SEND_REVENGE_LIST", xym.Msg{"total_add": w.Revenge, "list": list})
}

func init() {
	Handle("DONGFU_PTO.C_GET_GANK_LIST", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool { s.gankList(df); return false })
	})
	Handle("DONGFU_PTO.C_GANK_DONGFU", func(s *Session, p *Player, m xym.Msg) { s.startGank(p, int32(m["dongfu_id"].(int64)), 1) })
	Handle("DONGFU_PTO.C_REVENGE_DONGFU", func(s *Session, p *Player, m xym.Msg) { s.startGank(p, int32(m["dongfu_id"].(int64)), 2) })
	Handle("DONGFU_PTO.C_GANK_FIGHT_INFO", onGankFightInfo)
	Handle("DONGFU_PTO.C_START_GANK_FIGHT", func(s *Session, p *Player, m xym.Msg) { s.gankFight(p, m["type"].(string), false) })
	Handle("DONGFU_PTO.C_START_GANK_HELP_FIGHT", func(s *Session, p *Player, m xym.Msg) { s.gankFight(p, m["type"].(string), true) })
	Handle("DONGFU_PTO.C_GANK_USER_LIST", onGankUserList)
	Handle("DONGFU_PTO.C_GANK_RELIVE_ALL_FIGHTER", onGankRelive)
	Handle("DONGFU_PTO.C_GANK_PROTECT", onGankProtect)
	Handle("DONGFU_PTO.C_GET_REVENGE_LIST", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool { s.revengeList(df); return true })
	})
	Handle("DONGFU_PTO.C_REVENGE_ADD", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			w := df.war()
			ok := false
			switch {
			case w.Revenge >= 15:
				s.sendMsg("燃烧怒焰已达上限。")
			case df.Yuanqi < 50:
				s.sendMsg("洞府元气不足。")
			default:
				df.Yuanqi -= 50
				w.Revenge += 3
				ok = true
			}
			s.revengeList(df)
			return ok
		})
	})
}
