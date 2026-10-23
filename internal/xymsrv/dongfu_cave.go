package xymsrv

import (
	"sort"
	"strconv"

	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

// 灵穴争夺（内务「战争」灵穴页、差事「灵穴争夺」→ view/PartyLingXueView.lua、view/PartyQianXiListView.lua）：
//   - 洞府先迁徙到洞天（C_GET_AVAIL_STATION_LIST → S_SEND_AVAIL_STATION_LIST{station_id, need_lv, dongfu_num}，
//     C_MOVE_DONGFU{station_id} 后等 S_SEND_DONGFU_INFO；组织页「迁徙」洞府 2 级起），洞天 = 世界地图 1–8，
//     地图上 15 个关卡就是灵穴（view 用 map/%02d/%03d.png、TaskInfo[station×100+gate]）。
//   - C_GET_CAVE_GAME_INFO → S_SEND_CAVE_GAME_OPEN（参战中）/ S_SEND_CAVE_GAME_NONE；按钮 C_OPEN_CAVE_GAME / C_QUIT_CAVE_GAME。
//   - 地图 C_GET_CAVE_GAME_STATION_INFO → S_SEND_CAVE_GAME_STATION_INFO{gate_list[gate, dongfu_icon 占领者,
//     is_my_gate, gate_pos_list[pos, occupy_type]], target_gate_list}；关卡 C_GET/REFRESH_CAVE_GAME_GATE_INFO{gate} →
//     S_SEND_CAVE_GAME_GATE_INFO{occupy_dongfu_name, need_gold 0 免费 / 1 花元宝 / 2 不可, gate_pos_list[守位玩家]}；
//     抢位 C_START_CAVE_FIGHT{gate, gate_pos}。「占领场景中的关卡获得灵脉积分，战斗失败也有奖励」。
//   - 成员 / 排名：C_GET_CAVE_GAME_USER_LIST、C_GET_CAVE_GAME_DONGFU_RANK。
//
// 估计：每个灵穴 5 个守位，开局由野怪（论道假玩家阵容）占着；一个洞府占到 3 个守位即占领该灵穴；目标灵穴是
// 5 / 10 / 15 号（分值 3，其余 1）；每次抢位成功洞府积分 + 分值、个人积分 +1，失败个人 +0 但给 5000 银两；
// 每人每天 10 次，次数用完每次 20 元宝；占领的灵穴每个让洞府灵脉 +1；洞天需要洞府等级 2 / 2 / 2 / 2 / 3 / 3 / 4 / 4。

const (
	cavePositions = 5
	caveGates     = 15
	caveDaily     = 10
	caveExtraGold = 20
	caveLoseCash  = 5000
)

var caveTargets = []int{5, 10, 15}
var caveNeedLv = []int32{0, 2, 2, 2, 2, 3, 3, 4, 4}

// DFCave 是一个洞天的灵穴（Dongfus.Caves[洞天号]）。
type DFCave struct {
	Gates  map[int]*[cavePositions]DFCavePos `json:"gates"`
	Scores map[int32]int                     `json:"scores,omitempty"` // 洞府号 -> 积分
}

// DFCavePos 是一个守位：UID 为守位玩家（论道假玩家号 = 野怪），Dongfu 为其洞府（0 = 野怪）。
type DFCavePos struct {
	UID    int32 `json:"uid"`
	Dongfu int32 `json:"dongfu,omitempty"`
}

// DFCaveUser 是成员的灵穴数据（Dongfu.CaveUsers）。
type DFCaveUser struct {
	Day   string `json:"day"`
	Times int    `json:"times"`
	Score int    `json:"score"`
}

func (d *Dongfus) cave(station int32) *DFCave {
	if d.Caves == nil {
		d.Caves = map[int32]*DFCave{}
	}
	c := d.Caves[station]
	if c == nil {
		c = &DFCave{Gates: map[int]*[cavePositions]DFCavePos{}, Scores: map[int32]int{}}
		for g := 1; g <= caveGates; g++ {
			var ps [cavePositions]DFCavePos
			for k := range ps {
				ps[k].UID = int32(ldFakeUIDBase + 50 + (int(station)*7+g*3+k)%50)
			}
			c.Gates[g] = &ps
		}
		d.Caves[station] = c
	}
	if c.Scores == nil {
		c.Scores = map[int32]int{}
	}
	return c
}

// owner 是占领该灵穴的洞府（同一洞府 ≥3 个守位；0 = 无）。
func caveOwner(ps *[cavePositions]DFCavePos) int32 {
	n := map[int32]int{}
	for _, x := range ps {
		if x.Dongfu != 0 {
			n[x.Dongfu]++
			if n[x.Dongfu] >= 3 {
				return x.Dongfu
			}
		}
	}
	return 0
}

// caveHeld 是洞府在本洞天占领的灵穴数（灵脉加成）。调用方持锁。
func (d *Dongfus) caveHeld(df *Dongfu) int32 {
	if df.Station == 0 || d.Caves[df.Station] == nil {
		return 0
	}
	n := int32(0)
	for _, ps := range d.Caves[df.Station].Gates {
		if caveOwner(ps) == df.ID {
			n++
		}
	}
	return n
}

func (df *Dongfu) caveUser(uid int32) *DFCaveUser {
	if df.CaveUsers == nil {
		df.CaveUsers = map[int32]*DFCaveUser{}
	}
	u := df.CaveUsers[uid]
	if u == nil {
		u = &DFCaveUser{}
		df.CaveUsers[uid] = u
	}
	if u.Day != today() {
		u.Day, u.Times = today(), 0
	}
	return u
}

func (srv *Server) caveJob(df *Dongfu, p *Player, m xym.Msg) {
	if df.Station == 0 || !df.CaveOn {
		return
	}
	m["cave_game_times"] = max(0, caveDaily-df.caveUser(p.UID).Times)
}

func (s *Session) caveInfo(df *Dongfu) {
	d := s.srv.dongfu
	if df.Station == 0 || !df.CaveOn {
		s.SendExt("DONGFU_PTO.S_SEND_CAVE_GAME_NONE", xym.Msg{"win_dongfu_icon": 0, "win_dongfu_name": "", "total_lingqi": 0,
			"total_gold": 0, "station_id": df.Station})
		return
	}
	c := d.cave(df.Station)
	n := 0
	for _, o := range d.List {
		if o.Station == df.Station {
			n++
		}
	}
	targets := []xym.Msg{}
	for _, g := range caveTargets {
		targets = append(targets, xym.Msg{"gate": g})
	}
	s.SendExt("DONGFU_PTO.S_SEND_CAVE_GAME_OPEN", xym.Msg{"total_lingqi": df.CaveLingqi, "total_gold": 0, "dongfu_num": n,
		"score": c.Scores[df.ID], "rank": s.caveRank(c, df.ID), "station_id": df.Station, "target_gate_list": targets})
}

func (s *Session) caveRank(c *DFCave, id int32) int {
	rank := 1
	for o, sc := range c.Scores {
		if o != id && sc > c.Scores[id] {
			rank++
		}
	}
	return rank
}

func (s *Session) caveStation(df *Dongfu) {
	c := s.srv.dongfu.cave(df.Station)
	gates := []xym.Msg{}
	for g := 1; g <= caveGates; g++ {
		ps := c.Gates[g]
		owner := caveOwner(ps)
		icon := int32(0)
		if o := s.srv.dongfu.List[owner]; o != nil {
			icon = o.Icon
		}
		pl := []xym.Msg{}
		for k, x := range ps {
			t := 2 // 他人 / 野怪
			if x.Dongfu == df.ID {
				t = 1
			}
			pl = append(pl, xym.Msg{"pos": k + 1, "occupy_type": t})
		}
		gates = append(gates, xym.Msg{"gate": g, "dongfu_icon": icon, "is_my_gate": b2i(owner == df.ID), "gate_pos_list": pl})
	}
	targets := []xym.Msg{}
	for _, g := range caveTargets {
		targets = append(targets, xym.Msg{"gate": g})
	}
	s.SendExt("DONGFU_PTO.S_SEND_CAVE_GAME_STATION_INFO", xym.Msg{"station_id": df.Station, "target_gate_list": targets,
		"gate_list": gates, "last_win_dongfu_icon": 0})
}

func (s *Session) caveGate(df *Dongfu, p *Player, gate int) xym.Msg {
	d := s.srv.dongfu
	c := d.cave(df.Station)
	ps := c.Gates[gate]
	pl := []xym.Msg{}
	if ps != nil {
		for k, x := range ps {
			name, icon := "", int32(0)
			dfName, dfIcon := "野外妖怪", int32(0)
			if isFakeUID(x.UID) {
				f := fakeFoeCached(x.UID)
				name, icon = f.Name, f.Icon
			} else {
				name, icon, _ = s.srv.roleBrief(x.UID)
			}
			if o := d.List[x.Dongfu]; o != nil {
				dfName, dfIcon = o.Name, o.Icon
			}
			pl = append(pl, xym.Msg{"pos": k + 1, "dongfu_name": dfName, "dongfu_icon": dfIcon, "server_name": "细水长流",
				"name": name, "usernum": x.UID, "vip_icon_ts": 0, "fight_point": 0})
			_ = icon
		}
	}
	owner := ""
	if ps != nil {
		if o := d.List[caveOwner(ps)]; o != nil {
			owner = o.Name
		}
	}
	need := 0
	if df.caveUser(p.UID).Times >= caveDaily {
		need = 1
	}
	return xym.Msg{"gate": gate, "occupy_dongfu_name": owner, "need_gold": need, "gate_pos_list": pl}
}

func onCaveFight(s *Session, p *Player, m xym.Msg) {
	gate, pos := int(m["gate"].(int64)), int(m["gate_pos"].(int64))-1
	d := s.srv.dongfu
	var occupant int32
	ok := false
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		switch {
		case df.Station == 0 || !df.CaveOn:
			s.sendMsg("洞府还没有参加灵穴争夺。")
		case gate < 1 || gate > caveGates || pos < 0 || pos >= cavePositions:
			s.sendMsg("灵穴不存在。")
		case d.cave(df.Station).Gates[gate][pos].Dongfu == df.ID:
			s.sendMsg("这个守位已经是本洞府的了。")
		case df.caveUser(s.role.UID).Times >= caveDaily && !s.spend(0, caveExtraGold):
		default:
			ok, occupant = true, d.cave(df.Station).Gates[gate][pos].UID
		}
		if !ok && df.Station != 0 {
			s.SendExt("DONGFU_PTO.S_SEND_CAVE_GAME_GATE_INFO", s.caveGate(df, p, gate))
		}
		return false
	})
	if !ok {
		return
	}
	var foe *ldFoe
	if isFakeUID(occupant) {
		foe = fakeFoeCached(occupant)
	} else {
		foe = s.srv.realFoe(occupant, p)
	}
	team1, _ := p.dyTeam(nil)
	if foe == nil || len(foe.Team) == 0 || len(team1) == 0 {
		s.retMsg("对方正忙，请稍后再试。")
		return
	}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(foe.Team)
	res := s.simulate(p, team1, battle.CloneTeam(foe.Team), [2]int32{p.leaderSkill(), foe.Leader}, foe.Zf, s.actRng())
	s.fightRecord(strconv.FormatInt(recordSeq.Add(1), 10), foe.Name, foe.Grade, r1, r2, res.Record)
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		u := df.caveUser(s.role.UID)
		u.Times++
		c := d.cave(df.Station)
		if res.Win && c.Gates[gate][pos].UID == occupant {
			c.Gates[gate][pos] = DFCavePos{UID: s.role.UID, Dongfu: df.ID}
			pts := 1
			for _, g := range caveTargets {
				if g == gate {
					pts = 3
				}
			}
			c.Scores[df.ID] += pts
			u.Score++
			df.CaveLingqi += int32(pts) * 10
			me.contribute(1)
		}
		p.track(evDongfu, 1)
		s.SendExt("DONGFU_PTO.S_SEND_CAVE_GAME_GATE_INFO", s.caveGate(df, p, gate))
		return true
	})
	if res.Win {
		s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(2, nil))
		return
	}
	d2 := &bagDelta{}
	p.giveCode("WP20010", caveLoseCash, s.actRng(), d2) // 失败也有奖励：银两
	p.saveLog(s)
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(3, s.sendDelta(p, d2)))
}

func init() {
	Handle("DONGFU_PTO.C_GET_AVAIL_STATION_LIST", func(s *Session, p *Player, m xym.Msg) {
		d := s.srv.dongfu
		list := []xym.Msg{}
		d.do(func() bool {
			for st := int32(1); st <= 8; st++ {
				n := 0
				for _, o := range d.List {
					if o.Station == st {
						n++
					}
				}
				list = append(list, xym.Msg{"station_id": st, "need_lv": caveNeedLv[st], "dongfu_num": n})
			}
			return false
		})
		s.SendExt("DONGFU_PTO.S_SEND_AVAIL_STATION_LIST", xym.Msg{"list": list})
	})
	Handle("DONGFU_PTO.C_MOVE_DONGFU", func(s *Session, p *Player, m xym.Msg) {
		st := int32(m["station_id"].(int64))
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			ok := false
			switch {
			case !isAdmin(me.Position):
				s.sendMsg("只有府主和洞主可以迁徙洞府。")
			case st < 1 || st > 8:
				s.sendMsg("洞天不存在。")
			case df.Lv < caveNeedLv[st]:
				s.sendMsg("洞府等级不足。")
			default:
				df.Station, df.CaveOn = st, true
				df.log(s.role.Name + " 把洞府迁徙到了 " + caveName(st))
				ok = true
			}
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
			return ok
		})
	})
	Handle("DONGFU_PTO.C_GET_CAVE_GAME_INFO", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool { s.caveInfo(df); return false })
	})
	Handle("DONGFU_PTO.C_OPEN_CAVE_GAME", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			switch {
			case !isAdmin(me.Position):
				s.sendMsg("只有府主和洞主可以报名灵穴争夺。")
			case df.Station == 0:
				s.sendMsg("请先把洞府迁徙到洞天（组织页「迁徙」）。")
			default:
				df.CaveOn = true
			}
			s.caveInfo(df)
			return true
		})
	})
	Handle("DONGFU_PTO.C_QUIT_CAVE_GAME", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			if isAdmin(me.Position) {
				df.CaveOn = false
			}
			s.caveInfo(df)
			return true
		})
	})
	Handle("DONGFU_PTO.C_GET_CAVE_GAME_STATION_INFO", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			if df.Station == 0 {
				s.sendMsg("请先把洞府迁徙到洞天。")
				s.SendExt("DONGFU_PTO.S_SEND_CAVE_GAME_STATION_INFO", xym.Msg{"station_id": 0, "target_gate_list": []xym.Msg{},
					"gate_list": []xym.Msg{}, "last_win_dongfu_icon": 0})
				return false
			}
			s.caveStation(df)
			return true
		})
	})
	gateInfo := func(s *Session, p *Player, m xym.Msg) {
		gate := int(m["gate"].(int64))
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			if df.Station == 0 {
				s.sendMsg("请先把洞府迁徙到洞天。")
				return false
			}
			s.SendExt("DONGFU_PTO.S_SEND_CAVE_GAME_GATE_INFO", s.caveGate(df, p, gate))
			return true
		})
	}
	Handle("DONGFU_PTO.C_GET_CAVE_GAME_GATE_INFO", gateInfo)
	Handle("DONGFU_PTO.C_REFRESH_CAVE_GAME_GATE_INFO", gateInfo)
	Handle("DONGFU_PTO.C_START_CAVE_FIGHT", onCaveFight)
	Handle("DONGFU_PTO.C_GET_CAVE_GAME_USER_LIST", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			list := []xym.Msg{}
			for _, x := range df.Members {
				u := df.caveUser(x.UID)
				name, icon, _ := s.srv.roleBrief(x.UID)
				list = append(list, xym.Msg{"usernum": x.UID, "vip_icon_ts": 0, "icon": icon, "name": name,
					"fight_times": u.Times, "score": u.Score})
			}
			s.SendExt("DONGFU_PTO.S_SEND_CAVE_GAME_USER_LIST", xym.Msg{"list": list})
			return true
		})
	})
	Handle("DONGFU_PTO.C_GET_CAVE_GAME_DONGFU_RANK", func(s *Session, p *Player, m xym.Msg) {
		d := s.srv.dongfu
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			list := []xym.Msg{}
			if df.Station != 0 {
				c := d.cave(df.Station)
				type row struct {
					id    int32
					score int
				}
				var rows []row
				for id, sc := range c.Scores {
					rows = append(rows, row{id, sc})
				}
				sort.Slice(rows, func(i, j int) bool { return rows[i].score > rows[j].score })
				for i, r := range rows {
					if o := d.List[r.id]; o != nil {
						list = append(list, xym.Msg{"pos": i + 1, "icon": o.Icon, "name": o.Name, "server_name": "细水长流", "score": r.score})
					}
				}
			}
			s.SendExt("DONGFU_PTO.S_SEND_CAVE_GAME_DONGFU_RANK", xym.Msg{"list": list})
			return false
		})
	})
}

var caveNames = []string{"桃花源", "东海岩洞", "鬼哭洞", "无心谷", "飞炎洞府", "水幕洞天", "山腹空穴", "盘丝洞", "蜃楼洞天"}

func caveName(st int32) string {
	if st >= 0 && int(st) < len(caveNames) {
		return caveNames[st]
	}
	return ""
}
