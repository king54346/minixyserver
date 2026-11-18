package xymsrv

import (
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 洞府领土争夺（DONGFU_TERRITORY_PTO）：两个洞府的真实玩家对战。
//   - 报名以洞府为单位（C_TERRITORY_SIGN.vs_dongfu_id = 要打的对手洞府），同一对洞府共用一场；
//     先报名的一方是攻方（side 0，出生点 X），对手是守方（side 1，出生点 Y）；
//   - 地图 8 个战场 × 16 个据点，邻接表来自客户端数据（gamedata/territory.json）；
//   - MOVE 按邻接移动并占点（擂台除外，必须打赢擂主）；每 10 秒按双方据点权重给分
//     （出生点 1、怪物 2、擂台 3、仙阵 4、祭坛 5、妖王 10）；
//   - KILL_MONSTER / KILL_BOSS 先打一场 PVE，赢了才扣妖物、加分、占点；
//   - 擂台：擂主是真人玩家（territory_arena.go），走进去自动开战，赢了易主；
//   - EXCHANGE_SCORE：奇异仙阵 50 仙气 → 30 胜利点；胜利点满 50 自动折成洞府神魂；
//   - 战场 15 分钟结束，按双方胜利点判胜负并结算奖励。

const (
	terrDuration       = 900 // 一场 15 分钟
	terrTick           = 10  // 每 10 秒结算一次据点分
	terrExchangeNeed   = 50  // 结算：50 胜利点 = 1 个神魂
	terrXianqiExchange = 50  // 奇异仙阵：50 仙气换 30 胜利点（每场一次）
	terrSoulItem       = 11286
	terrStation        = 1
	terrEventInterval  = 300 // 场景事件每 5 分钟一次（规则页：战场内每隔 5 分钟所有关卡触发各自的关卡事件）
	terrLeaveBonusMin  = 20  // 官方：胜利点 ≥20 的离场玩家也能拿到战场奖励
)

var terrWeight = map[string]int{"spawn": 1, "monster": 2, "arena": 3, "array": 4, "altar": 5, "boss": 10}

type terrGateState struct{ Owner int } // -1 中立、0 攻方、1 守方

// terrMob 是一个据点上的妖物/妖王状态。
type terrMob struct {
	left int
	max  int
	at   time.Time // 下次补满时刻
	// 妖王血量继承（官方规则：「妖王血量将被继承…当前血量恢复上限的20%，降低初始值的8%」）。
	// hpRate / capRate 都是相对「初始上限」的百分比：100 = 满血 / 初始上限。
	hpRate  int
	capRate int
}

type terrPlayer struct {
	UID    int32
	Name   string
	Side   int
	Gate   string
	Score  int // 胜利点
	Xianqi int // 仙气
	Lineup int
	AtkLv  int // 攻击祭坛修炼等级 0-5
	HpLv   int // 防守祭坛修炼等级 0-5
	Souls     int  // 本场已折算的神魂（每 50 胜利点 1 个）
	Wins      int  // 本场战胜擂主的次数（S_TERRITORY_OPEN_MAIN.win_times）
	Exchanged bool // 官方：仙阵兑换 50 仙气→30 胜利点，每个玩家每场只能一次
	// Team 是「当前阵容」的快照（我方站位 1-9）。别的玩家来驱逐/挑战时用它当守方，
	// 这样不用跨会话去读对方的 Player（那会和对方自己的战斗并发改 FightNo）。
	Team []*battle.Fighter
	// Prestige 是威望快照（战报里对方的 w2）。威望要算关卡进度，也在 Player 上，
	// 同样不能跨会话去读，所以随阵容快照一起存。
	Prestige int
}

type terrBattle struct {
	mu      sync.Mutex
	srv     *Server
	id      int64
	station int
	start   time.Time
	over    bool
	order   []string
	kind    map[string]string
	adj     map[string][]string
	gates   map[string]*terrGateState
	players map[int32]*terrPlayer
	scores  [2]int
	mobs    map[string]*terrMob // 每个怪物据点独立刷怪（key = gate key）
	name    map[string]string   // 据点中文名（擂台档次靠它区分人界/地界/天界）
	ord     map[string]int      // 据点在 order 里的序号（生成稳定的「守方玩家」uid）
	owners  map[string]*terrOwner // 各擂台的擂主（真实玩家）
	eventAt time.Time           // 下一次场景事件的时刻（每 5 分钟一次）
	// 两个洞府的真实对战：dfA 是发起方（攻方，side 0），dfB 是对手（守方，side 1）。
	dfA, dfB     int32
	nameA, nameB string
}

var terrMgr struct {
	mu    sync.Mutex
	seq   int64
	open  map[string]*terrBattle // key = terrPairKey(station, 两个洞府 id)
	byUID map[int32]*terrBattle
}

func init() {
	terrMgr.byUID = map[int32]*terrBattle{}
	terrMgr.open = map[string]*terrBattle{}
	terrHandle()
}

// terrPairKey 是两个洞府之间的战场 key（与攻守方向无关，同一对洞府只有一场）。
func terrPairKey(station int, id1, id2 int32) string {
	lo, hi := id1, id2
	if lo > hi {
		lo, hi = hi, lo
	}
	return fmt.Sprintf("%d:%d-%d", station, lo, hi)
}

func terrHandle() {
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_LOGIN_OPEN_MAIN", func(s *Session, p *Player, m xym.Msg) { s.terrOpen(p) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_OPEN_MAIN", func(s *Session, p *Player, m xym.Msg) { s.terrOpen(p) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_PING_GAME", func(s *Session, p *Player, m xym.Msg) { s.terrOpen(p) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_AVAIL_VS_DONGFU_LIST", func(s *Session, p *Player, m xym.Msg) {
		slog.Info("territory", "uid", s.role.UID, "op", "avail_list")
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_AVAIL_VS_DONGFU_LIST", xym.Msg{
			"min_gold": 0, "max_gold": 0, "status": 0, "list": s.terrFoeList(),
		})
	})
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", func(s *Session, p *Player, m xym.Msg) { s.terrSign(p, m) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN_GROUP", func(s *Session, p *Player, m xym.Msg) { s.terrSign(p, m) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_MOVE", func(s *Session, p *Player, m xym.Msg) { s.terrMove(p, m) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_TRANSMIT", func(s *Session, p *Player, m xym.Msg) { s.terrMove(p, m) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_KILL_MONSTER", func(s *Session, p *Player, m xym.Msg) { s.terrKill(p, "monster") })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_KILL_BOSS", func(s *Session, p *Player, m xym.Msg) { s.terrKill(p, "boss") })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_EXCHANGE_SCORE", func(s *Session, p *Player, m xym.Msg) { s.terrExchange(p) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_RANK", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_RANK", xym.Msg{"list": s.terrRankList()})
	})
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_RECORD_LIST", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_RECORD_LIST", xym.Msg{"list": []xym.Msg{}})
	})
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_PLAY_RECORD", func(s *Session, p *Player, m xym.Msg) { s.retMsg("战报已失效。") })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_MONSTER_INFO", func(s *Session, p *Player, m xym.Msg) { s.terrMonsterInfo() })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_LINEUP_INFO", func(s *Session, p *Player, m xym.Msg) { s.terrLineup(p, 0) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_CHANGE_LINEUP", func(s *Session, p *Player, m xym.Msg) {
		no, _ := m["lineup_no"].(int64)
		s.terrLineup(p, int(no))
	})
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_LINEUP_DIE_LIST", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_LINEUP_DIE_LIST", xym.Msg{"xianqi": 0, "list": []xym.Msg{}})
	})
	for _, name := range []string{"C_TERRITORY_RELIVE_ALL_PETS", "C_TERRITORY_RELIVE_POS_PET", "C_TERRITORY_FORCE_RELIVE_ALL_PETS"} {
		Handle("DONGFU_TERRITORY_PTO."+name, func(s *Session, p *Player, m xym.Msg) { s.terrLineup(p, 0) })
	}
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_OWNER_OR_MATCH_USER", func(s *Session, p *Player, m xym.Msg) { s.terrChallengeList(0) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_MATCH_USER", func(s *Session, p *Player, m xym.Msg) { s.terrChallengeList(0) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_CHALLENGE_USER_LIST", func(s *Session, p *Player, m xym.Msg) {
		typ, _ := m["type"].(int64)
		s.terrChallengeList(int(typ))
	})
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_GET_EJECT_USER_LIST", func(s *Session, p *Player, m xym.Msg) { s.terrEjectList() })
	// 挑战擂台擂主：与敌方真实玩家打一场，赢了才占点、易主并给奖励。
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_CHALLENGE_USER", func(s *Session, p *Player, m xym.Msg) { s.terrArenaChallenge(p, m) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_DUEL", func(s *Session, p *Player, m xym.Msg) { s.terrArenaChallenge(p, m) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_EJECT_USER", func(s *Session, p *Player, m xym.Msg) { s.terrEject(p, m) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_ADD_PROP", func(s *Session, p *Player, m xym.Msg) { s.terrAddProp(p) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_SAY", func(s *Session, p *Player, m xym.Msg) { s.terrSay(p, m) })
	Handle("DONGFU_TERRITORY_PTO.C_TERRITORY_LEAVE_GAME", func(s *Session, p *Player, m xym.Msg) { s.terrLeave() })
}

// terrDongfu 是玩家所在的洞府与成员记录（没有洞府时都返回 nil）。
// Dongfus.of 要求调用方持锁，这里用 do 包一层。
func (s *Session) terrDongfu() (*Dongfu, *DFMember) {
	var df *Dongfu
	var mem *DFMember
	s.srv.dongfu.do(func() bool {
		df, mem = s.srv.dongfu.of(s.role.UID)
		return false
	})
	return df, mem
}

// terrDongfuByID 按洞府号取洞府。
func (s *Session) terrDongfuByID(id int32) *Dongfu {
	var out *Dongfu
	s.srv.dongfu.do(func() bool {
		out = s.srv.dongfu.List[id]
		return false
	})
	return out
}

// terrFoeList 是「可挑战的洞府」列表：除自己洞府外的所有洞府。
func (s *Session) terrFoeList() []xym.Msg {
	mine, _ := s.terrDongfu()
	out := []xym.Msg{}
	s.srv.dongfu.do(func() bool {
		ids := make([]int32, 0, len(s.srv.dongfu.List))
		for id := range s.srv.dongfu.List {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		pos := 0
		for _, id := range ids {
			df := s.srv.dongfu.List[id]
			if df == nil || (mine != nil && df.ID == mine.ID) {
				continue
			}
			pos++
			out = append(out, xym.Msg{"id": df.ID, "name": df.Name, "server_name": "", "lv": df.Lv,
				"rank": pos, "icon": df.Icon, "member_num": len(df.Members), "size": 30})
		}
		return false
	})
	return out
}

// terrRankList 是战场双方的洞府排行（我方的名字 + 对手的名字）。
func (s *Session) terrRankList() []xym.Msg {
	out := []xym.Msg{}
	if b := s.terrBattle(); b != nil {
		b.mu.Lock()
		a, z := b.nameA, b.nameB
		b.mu.Unlock()
		out = append(out, xym.Msg{"pos": 1, "icon": 0, "name": a, "server_name": "", "member_num": 0, "size": 30})
		out = append(out, xym.Msg{"pos": 2, "icon": 0, "name": z, "server_name": "", "member_num": 0, "size": 30})
		return out
	}
	if mine, _ := s.terrDongfu(); mine != nil {
		out = append(out, xym.Msg{"pos": 1, "icon": mine.Icon, "name": mine.Name, "server_name": "",
			"member_num": len(mine.Members), "size": 30})
	}
	return out
}

func (b *terrBattle) kindOf(key string) string { return b.kind[key] }

func (b *terrBattle) spawnKey(side int) string {
	for _, k := range b.order {
		if k == "X" && side == 0 || k == "Y" && side == 1 {
			return k
		}
	}
	return b.order[0]
}

func newTerrBattle(station int, srv *Server, a, z *Dongfu) *terrBattle {
	st := gamedata.TerritoryStationByID(station)
	if st == nil {
		st = &gamedata.TerritoryStation{ID: station}
	}
	terrMgr.mu.Lock()
	terrMgr.seq++
	id := terrMgr.seq
	terrMgr.mu.Unlock()
	b := &terrBattle{srv: srv, id: id, station: station, start: time.Now(), kind: map[string]string{}, mobs: map[string]*terrMob{},
		adj: map[string][]string{}, gates: map[string]*terrGateState{}, players: map[int32]*terrPlayer{},
		name: map[string]string{}, ord: map[string]int{}, owners: map[string]*terrOwner{}}
	b.eventAt = b.start.Add(terrEventInterval * time.Second)
	b.dfA, b.nameA = a.ID, a.Name
	b.dfB, b.nameB = z.ID, z.Name
	byID := map[int]string{}
	for i, g := range st.Gates {
		b.order = append(b.order, g.Key)
		b.kind[g.Key] = g.Kind
		b.name[g.Key] = g.Name
		b.ord[g.Key] = i + 1
		b.gates[g.Key] = &terrGateState{Owner: g.Owner}
		switch g.Kind {
		case "monster":
			b.mobs[g.Key] = &terrMob{left: 4, max: 4, at: time.Now().Add(5 * time.Minute)}
		case "boss":
			b.mobs[g.Key] = &terrMob{left: 1, max: 1, at: time.Now().Add(5 * time.Minute), hpRate: 100, capRate: 100}
		}
		byID[g.ID] = g.Key
	}
	for _, e := range st.Edges {
		a, z := byID[e.A], byID[e.B]
		if a == "" || z == "" {
			continue
		}
		b.adj[a] = append(b.adj[a], z)
		b.adj[z] = append(b.adj[z], a)
	}
	go b.loop()
	return b
}

func (b *terrBattle) loop() {
	t := time.NewTicker(terrTick * time.Second)
	defer t.Stop()
	for range t.C {
		b.mu.Lock()
		if b.over {
			b.mu.Unlock()
			return
		}
		// 场景事件：官方规则「战场内每隔 5 分钟，所有关卡均会触发各自的关卡事件」。
		// 事件里会补满各据点的妖物/妖王，并返回一条要广播的公告（解锁后再发，避免持锁发消息）。
		var eventMsg xym.Msg
		if !time.Now().Before(b.eventAt) {
			eventMsg = b.fireEventLocked()
			b.eventAt = b.eventAt.Add(terrEventInterval * time.Second)
		}
		if time.Since(b.start) >= terrDuration*time.Second {
			b.over = true
			b.mu.Unlock()
			b.closeAll()
			return
		}
		var gain [2]int
		for _, k := range b.order {
			if g := b.gates[k]; g != nil && g.Owner >= 0 {
				gain[g.Owner] += terrWeight[b.kind[k]]
			}
		}
		b.scores[0] += gain[0]
		b.scores[1] += gain[1]
		var credits []int32
		var crossed []int32
		for uid, pl := range b.players {
			old := pl.Score
			pl.Score += gain[pl.Side]
			if n := terrAccrue(pl); n > 0 {
				credits = append(credits, uid)
			}
			_ = old
			if pl.Score >= terrExchangeNeed || pl.Xianqi >= terrXianqiExchange {
				crossed = append(crossed, uid)
			}
		}
		b.mu.Unlock()
		if eventMsg != nil {
			b.broadcast(eventMsg)
		}
		for _, uid := range credits {
			if sess := b.srv.sessionOf(uid); sess != nil && sess.player != nil {
				n := 0
				b.mu.Lock()
				if pl := b.players[uid]; pl != nil {
					n = pl.Souls
					pl.Souls = 0
				}
				b.mu.Unlock()
				if n > 0 {
					sess.player.df().GodSoul += int32(n)
					sess.SendExt("ETC_PTO.S_SEND_TOP_MSG", xym.Msg{"msg": fmt.Sprintf("神魂 +%d（已加到洞府神魂）", n)})
					sess.player.saveLog(sess)
				}
			}
		}
		for _, uid := range crossed {
			if sess := b.srv.sessionOf(uid); sess != nil {
				sess.terrSendOpen(b)
			}
		}
	}
}

// terrAccrue 每 10 秒结算一次：没用掉的仙气先按 50→30 折胜利点（每场一次），
// 再把满 50 的胜利点折成神魂（洞府神魂值），保证玩家在战场里就能看到到账，不依赖撤退/结束。
func terrAccrue(pl *terrPlayer) int {
	if pl == nil {
		return 0
	}
	n := pl.Score / terrExchangeNeed
	pl.Score -= n * terrExchangeNeed
	pl.Souls += n
	return n
}

// terrAutoExchange 在结算前把没用掉的仙气按官方规则折算：50 仙气 → 30 胜利点。
// 官方是「每个玩家只能兑换一次」，所以这里也只折一次（多余的仙气本场作废）。
func terrAutoExchange(pl *terrPlayer) bool {
	if pl == nil || pl.Exchanged || pl.Xianqi < terrXianqiExchange {
		return false
	}
	pl.Xianqi -= terrXianqiExchange
	pl.Score += 30
	pl.Exchanged = true
	return true
}

// terrSettle 按胜利点结算神魂：每 50 点换 1 个（直接加洞府神魂），战场结束/离场时自动发。
func terrSettle(sess *Session, pl *terrPlayer) {
	if sess == nil || sess.player == nil || pl == nil || pl.Score < terrExchangeNeed {
		return
	}
	n := pl.Score / terrExchangeNeed
	pl.Score -= n * terrExchangeNeed // 已折算的部分扣掉，离场奖励按剩下的胜利点判定
	sess.player.df().GodSoul += int32(n)
	// 战场界面上奖励弹窗会被战斗界面挡掉，再发一条顶部飘字，确保玩家看得到结算。
	sess.SendExt("ETC_PTO.S_SEND_TOP_MSG", xym.Msg{"msg": fmt.Sprintf("领土战结算：神魂 +%d（胜利点 %d）", n, pl.Score)})
	sess.player.saveLog(sess)
}

// terrEndBonus 是战场结束的胜负奖励（规则页「所有参与玩家均将获得战场奖励，胜负不同」）：
// 胜方在场玩家 +2 个神魂、负方 +1；「双方战胜场次最多的前 20% 玩家」再额外 +1——
// 单服单人战场里前 20% 就是自己，所以只要有胜场就享受这份额外奖励。
func terrEndBonus(pl *terrPlayer, won bool) int {
	if pl == nil {
		return 0
	}
	n := 1
	if won {
		n = 2
	}
	if pl.Wins > 0 {
		n++
	}
	return n
}

func (b *terrBattle) closeAll() {
	b.mu.Lock()
	uids := make([]int32, 0, len(b.players))
	snaps := make(map[int32]*terrPlayer, len(b.players))
	for uid, pl := range b.players {
		uids = append(uids, uid)
		cp := *pl
		if terrAutoExchange(&cp) {
			if sess := b.srv.sessionOf(uid); sess != nil {
				sess.SendExt("ETC_PTO.S_SEND_TOP_MSG", xym.Msg{"msg": "仙气折算：50 仙气 → 30 胜利点"})
			}
		}
		snaps[uid] = &cp
	}
	b.players = map[int32]*terrPlayer{}
	scoreA, scoreB := b.scores[0], b.scores[1] // 攻方 / 守方
	b.mu.Unlock()
	terrMgr.mu.Lock()
	for _, uid := range uids {
		if terrMgr.byUID[uid] == b {
			delete(terrMgr.byUID, uid)
		}
	}
	for k, v := range terrMgr.open {
		if v == b {
			delete(terrMgr.open, k)
		}
	}
	terrMgr.mu.Unlock()
	for _, uid := range uids {
		if sess := b.srv.sessionOf(uid); sess != nil {
			pl := snaps[uid]
			won := scoreA > scoreB
			if pl.Side == 1 {
				won = scoreB > scoreA
			}
			terrSettle(sess, pl)
			if n := terrEndBonus(pl, won); n > 0 {
				sess.player.df().GodSoul += int32(n)
				result := "负"
				if won {
					result = "胜"
				}
				sess.SendExt("ETC_PTO.S_SEND_TOP_MSG", xym.Msg{"msg": fmt.Sprintf("领土战结束（%s）：神魂 +%d", result, n)})
				sess.player.saveLog(sess)
			}
			sess.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_MAIN", nil)
		}
	}
}

// ---- 会话侧 ----

func (s *Session) terrBattle() *terrBattle {
	terrMgr.mu.Lock()
	b := terrMgr.byUID[s.role.UID]
	terrMgr.mu.Unlock()
	return b
}

func (s *Session) terrOpen(p *Player) {
	slog.Info("territory", "uid", s.role.UID, "op", "open")
	s.absorbSoulItems(p)
	if b := s.terrBattle(); b != nil {
		s.terrSendOpen(b)
		return
	}
	// 打开界面时如果还没报名，直接报名进场（自动挑一个有人的对手洞府），
	// 否则客户端会立刻收到 CLOSE_MAIN 看不到地图。
	s.terrSign(p, nil)
}

// terrFoeCandidates 是除自己以外的洞府（按 id 升序，保证可复现）。
// 先在持 dongfu 锁的区块里把列表拷出来，锁外再判「是否已在战斗中」，避免 dongfu.mu 与 terrMgr.mu 嵌套。
func (s *Session) terrFoeCandidates(self int32) []*Dongfu {
	var out []*Dongfu
	s.srv.dongfu.do(func() bool {
		ids := make([]int32, 0, len(s.srv.dongfu.List))
		for id := range s.srv.dongfu.List {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			if id != self && s.srv.dongfu.List[id] != nil {
				out = append(out, s.srv.dongfu.List[id])
			}
		}
		return false
	})
	return out
}

// terrFirstFreeFoe 挑第一个还没在打仗的对手洞府（一个洞府同时只能打一场）。
func (s *Session) terrFirstFreeFoe(self int32) *Dongfu {
	for _, df := range s.terrFoeCandidates(self) {
		if s.terrBattleWithDongfu(df.ID) == nil {
			return df
		}
	}
	return nil
}

// terrBattleWithDongfu 找一个「已经存在、且包含我洞府」的战场。
// 报名必须先认这条：否则两个洞府各自开一场，就会出现「A 看到 B、B 看到 C」这种各打各的。
func (s *Session) terrBattleWithDongfu(dfID int32) *terrBattle {
	terrMgr.mu.Lock()
	defer terrMgr.mu.Unlock()
	keys := make([]string, 0, len(terrMgr.open))
	for k := range terrMgr.open {
		keys = append(keys, k)
	}
	slices.Sort(keys) // 固定顺序，避免每次挑到不同的场
	for _, k := range keys {
		if b := terrMgr.open[k]; b != nil && (b.dfA == dfID || b.dfB == dfID) {
			return b
		}
	}
	return nil
}

// terrSign 报名：以洞府为单位，和对手洞府开一场（同一对洞府共用一场）。
func (s *Session) terrSign(p *Player, m xym.Msg) {
	mine, _ := s.terrDongfu()
	if mine == nil {
		s.retMsg("领土战以洞府为单位报名，请先加入洞府。")
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_MAIN", nil)
		return
	}
	if b := s.terrBattle(); b != nil {
		s.terrSendOpen(b)
		return
	}
	// 我洞府已经在打仗了（别人打过来 / 队友先开了）→ 直接进那一场，不另开。
	if b := s.terrBattleWithDongfu(mine.ID); b != nil {
		slog.Info("territory", "uid", s.role.UID, "op", "join_existing", "battle", b.id, "mine", mine.ID)
		s.terrJoin(b, mine.ID, p)
		s.terrSendOpen(b)
		s.terrBroadcastUsers(b)
		return
	}
	// 对手：优先用客户端传来的 vs_dongfu_id；没传就自动挑一个空闲的。
	var foe *Dongfu
	if m != nil {
		if id, ok := m["vs_dongfu_id"].(int64); ok && id > 0 && int32(id) != mine.ID {
			foe = s.terrDongfuByID(int32(id))
		}
	}
	if foe != nil && s.terrBattleWithDongfu(foe.ID) != nil {
		s.terrTell(fmt.Sprintf("%s 正在另一场领土战中，换一个对手。", foe.Name))
		foe = nil
	}
	if foe == nil {
		foe = s.terrFirstFreeFoe(mine.ID)
	}
	if foe == nil {
		s.terrTell("当前没有空闲的可交战洞府。")
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_MAIN", nil)
		return
	}

	key := terrPairKey(terrStation, mine.ID, foe.ID)
	terrMgr.mu.Lock()
	b := terrMgr.open[key]
	terrMgr.mu.Unlock()
	if b == nil {
		// newTerrBattle 自己会取 terrMgr.mu（分配 id），所以不能在上面的锁里调用。
		nb := newTerrBattle(terrStation, s.srv, mine, foe)
		terrMgr.mu.Lock()
		if terrMgr.open[key] == nil {
			terrMgr.open[key] = nb
			b = nb
		} else {
			b = terrMgr.open[key]
		}
		terrMgr.mu.Unlock()
		// 乐观并发：万一这期间别人先给我洞府开了一场，就用那一场，把自己新建的作废。
		if other := s.terrBattleWithDongfu(mine.ID); other != nil && other != b {
			nb.mu.Lock()
			nb.over = true
			nb.mu.Unlock()
			terrMgr.mu.Lock()
			delete(terrMgr.open, key)
			terrMgr.mu.Unlock()
			slog.Info("territory", "uid", s.role.UID, "op", "drop_dup_battle", "battle", nb.id, "use", other.id)
			b = other
		}
	}

	slog.Info("territory", "uid", s.role.UID, "op", "sign", "mine", mine.ID, "foe", foe.ID, "battle", b.id)
	s.terrJoin(b, mine.ID, p)
	s.terrSendOpen(b)
	s.terrBroadcastUsers(b)
}

// terrJoin 把玩家放进战场，按他所在洞府决定攻守方（dfA=攻方 side 0、dfB=守方 side 1）。
func (s *Session) terrJoin(b *terrBattle, dfID int32, p *Player) {
	b.mu.Lock()
	side := 0
	if dfID == b.dfB {
		side = 1
	}
	if _, ok := b.players[s.role.UID]; !ok {
		b.players[s.role.UID] = &terrPlayer{UID: s.role.UID, Name: s.role.Name, Side: side, Gate: b.spawnKey(side), Lineup: 1}
	}
	b.mu.Unlock()
	terrMgr.mu.Lock()
	terrMgr.byUID[s.role.UID] = b
	terrMgr.mu.Unlock()
	s.terrSnapTeam(b, p)
	slog.Info("territory", "uid", s.role.UID, "op", "join", "battle", b.id, "side", side, "dongfu", dfID)
}

// terrSnapTeam 把玩家当前阵容存成快照（含祭坛加成），供别人驱逐/挑战时当守方。
func (s *Session) terrSnapTeam(b *terrBattle, p *Player) {
	if b == nil || p == nil {
		return
	}
	b.mu.Lock()
	pl := b.players[s.role.UID]
	if pl == nil {
		b.mu.Unlock()
		return
	}
	lineup, atkLv, hpLv := pl.Lineup, pl.AtkLv, pl.HpLv
	b.mu.Unlock()

	team := b.arenaTeam(p, lineup, atkLv, hpLv)
	if len(team) == 0 {
		return
	}
	prestige := s.prestige()
	b.mu.Lock()
	if pl := b.players[s.role.UID]; pl != nil {
		pl.Team = battle.CloneTeam(team)
		pl.Prestige = prestige
	}
	b.mu.Unlock()
}

func (s *Session) terrSendOpen(b *terrBattle) {
	b.mu.Lock()
	pl := b.players[s.role.UID]
	if pl == nil {
		b.mu.Unlock()
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_MAIN", nil)
		return
	}
	myName, foeName := b.nameA, b.nameB
	if pl.Side == 1 {
		myName, foeName = b.nameB, b.nameA
	}
	msg := b.openMsg(pl, myName, foeName)
	b.mu.Unlock()
	if v, ok := msg["has_exchange_score"]; ok {
		slog.Info("territory", "uid", s.role.UID, "op", "open_main", "gate", msg["gate_key"], "my_score", msg["my_score"], "exchange", v)
	}
	s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_OPEN_MAIN", msg)
}

// gateListFor 按收件人的阵营统计「我方人数 / 敌方人数」。
// 两个洞府都要看同一张地图，counts 必须以收件人为准，不能固定按 side 0 算。
func (b *terrBattle) gateListFor(side int) []xym.Msg {
	out := make([]xym.Msg, 0, len(b.order))
	for _, k := range b.order {
		users, foes := 0, 0
		for _, pl := range b.players {
			if pl.Gate == k {
				if pl.Side == side {
					users++
				} else {
					foes++
				}
			}
		}
		out = append(out, xym.Msg{"key": k, "user_num": users, "vs_user_num": foes, "dongfu_icon": 0})
	}
	return out
}

func (b *terrBattle) userList() []xym.Msg {
	out := make([]xym.Msg, 0, len(b.players))
	for _, pl := range b.players {
		out = append(out, xym.Msg{"uid": pl.UID, "gate_key": pl.Gate, "old_gate_key": "", "occupy_time": 0,
			"name": pl.Name, "icon": 0, "vip_icon_ts": 0, "grade": 0, "fight_point": 0, "side": pl.Side})
	}
	return out
}

func (b *terrBattle) openMsg(pl *terrPlayer, myName, foeName string) xym.Msg {
	now := time.Now().Unix()
	passed := int(now - b.start.Unix())
	sideUsers, foeUsers := 0, 0
	for _, x := range b.players {
		if x.Side == pl.Side {
			sideUsers++
		} else {
			foeUsers++
		}
	}
	// 客户端在奇异仙阵（Z）据点、has_exchange_score=1 时才显示「兑换胜利点」按钮。
	// 官方：50 仙气 → 30 胜利点，每个玩家只能兑换一次 → 仙气够且本场没兑换过才给 1。
	exchangeFlag := 0
	if pl.Xianqi >= terrXianqiExchange && !pl.Exchanged {
		exchangeFlag = 1
	}
	// 客户端拿 next_event_left_time 显示「距下次关卡事件」的倒计时（不是整场剩余时间）。
	eventLeft := int(time.Until(b.eventAt).Seconds())
	if eventLeft < 0 {
		eventLeft = 0
	}
	if eventLeft > terrEventInterval {
		eventLeft = terrEventInterval
	}
	return xym.Msg{
		"side": pl.Side, "side_size": 5, "station_id": b.station, "passed_time": passed,
		"next_event_left_time": eventLeft, "result": 0,
		"dongfu_name": myName, "dongfu_icon": 0, "server_name": "",
		"score": b.scores[pl.Side], "side_user_num": sideUsers,
		"vs_dongfu_name": foeName, "vs_dongfu_icon": 0, "vs_server_name": "",
		"vs_score": b.scores[1-pl.Side], "vs_side_user_num": foeUsers,
		"gate_list": b.gateListFor(pl.Side), "all_user_list": b.userList(), "now_time": now,
		"occupy_time": 0, "gate_key": pl.Gate, "old_gate_key": "",
		"xianqi": pl.Xianqi, "my_score": pl.Score, "attack_add": pl.AtkLv * 6, "hp_add": pl.HpLv * 6,
		"gold_relive_left_times": 0, "win_times": pl.Wins, "win_times_rank": 0, "show_rule": 0, // 1 = 客户端自动弹规则页（会挡住地图），只在玩家点规则按钮时再看
		"lineup_no": pl.Lineup, "die_lineup_list": []xym.Msg{},
		// 客户端固定读 lineup_info_list[1..3].die_count（三套阵容），少一条就会 nil 索引报错。
		"lineup_info_list": []xym.Msg{{"lineup_no": 1, "die_count": 0}, {"lineup_no": 2, "die_count": 0}, {"lineup_no": 3, "die_count": 0}},
		"all_alive":        1, "has_exchange_score": exchangeFlag,
	}
}

func (s *Session) terrBroadcastUsers(b *terrBattle) {
	b.mu.Lock()
	now := time.Now().Unix()
	users := b.userList()
	out := make(map[int32]xym.Msg, len(b.players))
	for uid, pl := range b.players {
		out[uid] = xym.Msg{"now_time": now, "all_user_list": users, "gate_list": b.gateListFor(pl.Side)}
	}
	b.mu.Unlock()
	for uid, msg := range out {
		if sess := b.srv.sessionOf(uid); sess != nil {
			sess.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_ALL_USER_LIST", msg)
		}
	}
}

func (s *Session) terrMove(p *Player, m xym.Msg) {
	b := s.terrBattle()
	if b == nil {
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_MAIN", nil)
		return
	}
	key, _ := m["gate_key"].(string)
	if key == "" {
		key, _ = m["target_gate_key"].(string)
	}
	b.mu.Lock()
	pl := b.players[s.role.UID]
	ok := false
	if pl != nil && key != "" && b.gates[key] != nil {
		for _, n := range b.adj[pl.Gate] {
			if n == key {
				ok = true
				break
			}
		}
		if ok && key != pl.Gate {
			// 离开擂台会让出擂主身份（规则：擂主是站在该关卡上的玩家）。
			if old := pl.Gate; b.kind[old] == "arena" {
				if o := b.owners[old]; o != nil && o.UID == pl.UID {
					delete(b.owners, old)
				}
			}
			// 擂台（A-F）有擂主镇守，走过去不算占领——必须打赢擂主（见 terrArenaDuel）。
			if g := b.gates[key]; g.Owner != pl.Side && b.kind[key] != "arena" { // 占点
				g.Owner = pl.Side
			}
			pl.Gate = key
		}
	}
	b.mu.Unlock()
	if !ok {
		s.retMsg("无法移动到该据点。")
		return
	}
	// 踏入擂台：没擂主就当擂主，擂主是敌方玩家就自动打一场。
	// 这条路径自己会推 OPEN_MAIN，避免再补一条让客户端/测试读到上一条状态。
	sent := false
	if b.kind[key] == "arena" {
		sent = s.terrArenaEnter(p, key)
	}
	s.terrBroadcastUsers(b)
	if !sent {
		s.terrSendOpen(b)
	}
}

// absorbSoulItems 把背包里的「神魂」道具一次性转成洞府神魂值（老版本发的是道具，背包里看不到）。
func (s *Session) absorbSoulItems(p *Player) {
	cnt := int32(p.itemCount(terrSoulItem))
	if cnt <= 0 {
		return
	}
	if _, rm := p.takeItems(terrSoulItem, cnt); len(rm) == 0 {
		return
	}
	p.df().GodSoul += cnt
	s.SendExt("ETC_PTO.S_SEND_TOP_MSG", xym.Msg{"msg": fmt.Sprintf("背包神魂 %d 个已转入洞府神魂", cnt)})
	p.saveLog(s)
}

// terrTeam 按指定阵容号建队（除妖/妖王战斗用）。playerTeam 默认读 p.FightNo，
// 临时把 FightNo 指到 no 再建队，建完还原——否则战场里切了阵容，战斗还是用主界面选的 1 号阵容。
func (p *Player) terrTeam(no int) ([]*battle.Fighter, []*Pet) {
	if p.lineup(no) == nil {
		return nil, nil
	}
	m := xym.Msg{}
	for i := 1; i <= battle.FightSlots; i++ {
		m[fmt.Sprintf("pos%d", i)] = int64(i)
	}
	old := p.FightNo
	p.FightNo = no
	defer func() { p.FightNo = old }()
	return p.playerTeam(m)
}

// terrMonsterInfo 回场上妖物剩余数量与刷新倒计时（客户端除妖提示/按钮靠它刷新）。
func (s *Session) terrMonsterInfo() {
	msg := xym.Msg{"left_time": 0, "num": 0, "max_num": 0}
	if b := s.terrBattle(); b != nil {
		b.mu.Lock()
		if pl := b.players[s.role.UID]; pl != nil {
			if mob := b.mobs[pl.Gate]; mob != nil {
				left := int(time.Until(mob.at).Seconds())
				if left < 0 {
					left = 0
				}
				msg = xym.Msg{"left_time": left, "num": mob.left, "max_num": mob.max}
			}
		}
		b.mu.Unlock()
	}
	s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_MONSTER_INFO", msg)
}

// terrKill 先打一场妖物/妖王战斗（战报由客户端回放），赢了才结算积分与占点。
func (s *Session) terrKill(p *Player, kind string) {
	b := s.terrBattle()
	if b == nil {
		return
	}
	b.mu.Lock()
	pl := b.players[s.role.UID]
	ok := pl != nil && b.kind[pl.Gate] == kind
	gate, lineup, atkLv, hpLv := "", 1, 0, 0
	if pl != nil {
		gate, lineup, atkLv, hpLv = pl.Gate, pl.Lineup, pl.AtkLv, pl.HpLv
	}
	msg := ""
	if ok {
		if mob := b.mobs[gate]; mob == nil {
			ok = false
		} else if mob.left <= 0 {
			if kind == "boss" {
				ok, msg = false, "妖王已被击杀，等待刷新。"
			} else {
				ok, msg = false, "该据点的妖物已被清空，等待刷新。"
			}
		}
	}
	b.mu.Unlock()
	switch {
	case msg != "":
		s.retMsg(msg)
		return
	case !ok:
		s.retMsg("这里没有可击杀的目标。")
		return
	}

	team1, _ := p.terrTeam(lineup)
	if len(team1) == 0 {
		s.retMsg("该阵容没有可出战的护法。")
		return
	}
	// 祭坛修炼：攻击祭坛每级 +6% 攻击、防守祭坛每级 +6% 生命。
	if atkLv > 0 || hpLv > 0 {
		for _, f := range team1 {
			if atkLv > 0 {
				f.Att = int32(int64(f.Att) * int64(100+6*atkLv) / 100)
			}
			if hpLv > 0 {
				f.HP = int32(int64(f.HP) * int64(100+6*hpLv) / 100)
				f.MaxHP = int32(int64(f.MaxHP) * int64(100+6*hpLv) / 100)
			}
		}
	}
	typ, name, stage := dyMonster, "低级妖物", 1
	switch {
	case kind == "boss":
		typ, name, stage = dyBoss, "妖王", 3
	case gate == "I" || gate == "J":
		typ, name, stage = dyLeader, "高级妖物", 2
	}
	rng := s.dyRng()
	team2 := dyEnemy(stage, 1, typ, rng)
	if len(team2) == 0 {
		s.retMsg("妖物配置缺失。")
		return
	}
	// 妖王血量继承：按上次剩下的血量（hpRate）和已被削掉的上限（capRate）缩放守方。
	bossBase := 0
	if kind == "boss" {
		hp, cap := 100, 100
		b.mu.Lock()
		if mob := b.mobs[gate]; mob != nil {
			if mob.hpRate > 0 {
				hp = mob.hpRate
			}
			if mob.capRate > 0 {
				cap = mob.capRate
			}
			mob.hpRate, mob.capRate = hp, cap
		}
		b.mu.Unlock()
		for _, f := range team2 {
			base := int(f.MaxHP)
			bossBase += base
			f.MaxHP = int32(int64(base) * int64(cap) / 100)
			f.HP = int32(int64(base) * int64(hp) / 100)
			if f.HP > f.MaxHP {
				f.HP = f.MaxHP
			}
		}
	}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), 0}, battle.Formation{}, rng)
	rid := fmt.Sprintf("territory-%d-%d", s.role.UID, time.Now().UnixNano())
	s.SendExt("FIGHT_PTO.S_FIGHT_RECORD", xym.Msg{
		"canop": 14, "rid": rid,
		"w1": s.prestige(), "level1": s.role.Grade, "name1": s.role.Name, "team1": teamMsgs(r1),
		"level2": 1, "name2": name, "team2": teamMsgs(r2),
		"otherparam": recordParams(r1, r2), "record": res.Record,
	})
	if !res.Win {
		if kind == "boss" && bossBase > 0 {
			// 剩下的血量被继承下来（模拟里 team2 的 HP 已被扣掉）。
			remain := 0
			for _, f := range team2 {
				if f.HP > 0 {
					remain += int(f.HP)
				}
			}
			rate := remain * 100 / bossBase
			// 战斗中可能有回复/复活把当前血量抬到初始上限之上，比例夹在 0..100。
			if rate > 100 {
				rate = 100
			}
			if rate < 0 {
				rate = 0
			}
			b.mu.Lock()
			if mob := b.mobs[gate]; mob != nil {
				mob.hpRate = rate
			}
			b.mu.Unlock()
			s.terrTell(fmt.Sprintf("除妖失败，妖王剩余血量 %d%%（血量会被继承）", rate))
			return
		}
		s.retMsg("除妖失败，整顿后再来。")
		return
	}
	b.mu.Lock()
	if x := b.players[s.role.UID]; x != nil {
		if mob := b.mobs[gate]; mob != nil && mob.left > 0 {
			mob.left--
			if kind == "boss" {
				mob.hpRate = 0 // 打死了，等下次场景事件按官方规则复活
			}
			if mob.left == 0 {
				mob.at = time.Now().Add(5 * time.Minute)
			}
		}
		if g := b.gates[gate]; g != nil {
			g.Owner = x.Side
		}
		// 仙气：除妖 10（高级 20）/妖王 50，本方其他玩家各 +2/+5/+20。
		actorGain, sideGain := 10, 2
		switch {
		case kind == "boss":
			actorGain, sideGain = 50, 20
		case gate == "I" || gate == "J":
			actorGain, sideGain = 20, 5
		}
		x.Xianqi += actorGain
		for _, other := range b.players {
			if other.Side == x.Side && other.UID != x.UID {
				other.Xianqi += sideGain
			}
		}
	}
	b.mu.Unlock()
	s.terrMonsterInfo()
	s.terrBroadcastUsers(b)
	s.terrSendOpen(b)
}

// terrExchange 是奇异仙阵（Z）的官方兑换：50 仙气 → 30 胜利点，每场每玩家一次。
func (s *Session) terrExchange(p *Player) {
	b := s.terrBattle()
	if b == nil {
		s.terrTell("你不在领土战场中。")
		return
	}
	b.mu.Lock()
	pl := b.players[s.role.UID]
	if pl == nil {
		b.mu.Unlock()
		s.terrTell("你不在领土战场中。")
		return
	}
	if pl.Exchanged {
		b.mu.Unlock()
		s.terrTell("【兑换】本场已经兑换过了（官方规则：每个玩家只能兑换一次）")
		return
	}
	if pl.Xianqi < terrXianqiExchange {
		left, xq := pl.Score, pl.Xianqi
		b.mu.Unlock()
		s.terrTell(fmt.Sprintf("【兑换】仙气不足：50 仙气兑换 30 胜利点（当前仙气 %d、胜利点 %d）", xq, left))
		return
	}
	pl.Xianqi -= terrXianqiExchange
	pl.Score += 30
	pl.Exchanged = true
	score, xq := pl.Score, pl.Xianqi
	b.mu.Unlock()
	// 战场界面下 S_RET_MSG / 顶部飘字都不显示，提示必须走战场频道（terrTell）。
	s.terrTell(fmt.Sprintf("【兑换】仙气 -50、胜利点 +30（当前仙气 %d、胜利点 %d）", xq, score))
	slog.Info("territory", "uid", s.role.UID, "op", "exchange_point", "score", score, "xianqi", xq)
	s.terrSendOpen(b)
}

// terrSoulExchange 是按胜利点换神魂（50 胜利点 = 1 个 11286），战场频道「兑换神魂」用。
func (s *Session) terrSoulExchange(p *Player) {
	b := s.terrBattle()
	if b == nil {
		s.terrTell("你不在领土战场中。")
		return
	}
	b.mu.Lock()
	pl := b.players[s.role.UID]
	n := 0
	if pl != nil {
		n = pl.Score / terrExchangeNeed
	}
	if n < 1 {
		cur := 0
		if pl != nil {
			cur = pl.Score
		}
		b.mu.Unlock()
		s.terrTell(fmt.Sprintf("【兑换】胜利点不足：50 胜利点兑换 1 个神魂（当前 %d）", cur))
		return
	}
	pl.Score -= n * terrExchangeNeed
	b.mu.Unlock()
	p.df().GodSoul += int32(n)
	s.terrTell(fmt.Sprintf("【兑换】神魂 +%d（已加到洞府神魂）", n))
	s.SendExt("ETC_PTO.S_SEND_TOP_MSG", xym.Msg{"msg": fmt.Sprintf("神魂 +%d（已加到洞府神魂）", n)})
	slog.Info("territory", "uid", s.role.UID, "op", "exchange_soul", "n", n, "god_soul", p.df().GodSoul)
	p.saveLog(s)
	s.terrSendOpen(b)
}

// terrLineup 返回（或切换）战场阵容。no=0 只查询（客户端换阵界面就是靠这条打开）；
// no=1..3 切换时只回主界面，**不要再回 S_TERRITORY_LINEUP_INFO**——客户端收到它会重新打开换阵界面，
// 表现就是"点了确定窗口又弹回来、看起来没生效"。
func (s *Session) terrLineup(p *Player, no int) {
	b := s.terrBattle()
	cur := 1
	if b != nil {
		b.mu.Lock()
		if pl := b.players[s.role.UID]; pl != nil {
			cur = pl.Lineup
		}
		b.mu.Unlock()
	}
	if no != 0 {
		switch {
		case no < 1 || no > 3:
			s.retMsg("阵容编号不正确。")
			return
		case p.lineup(no) == nil:
			s.retMsg("该阵容尚未解锁，不能切换。")
			return
		}
		cur = no
		if b != nil {
			b.mu.Lock()
			if pl := b.players[s.role.UID]; pl != nil {
				pl.Lineup = no
			}
			b.mu.Unlock()
			s.terrSnapTeam(b, p)
			s.terrSendOpen(b)
		}
		return
	}
	list := []xym.Msg{}
	if lu := p.lineup(cur); lu != nil {
		for i, sl := range lu.Slots {
			if sl == nil || sl.Pet <= 0 {
				continue
			}
			pet := p.petAtBag(sl.Pet)
			if pet == nil {
				continue
			}
			list = append(list, xym.Msg{"pos": i + 1, "type": pet.Type, "is_die": 0})
		}
	}
	s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_LINEUP_INFO", xym.Msg{"current_lineup_no": cur, "list": list})
}

// terrAddProp 是祭坛的「修炼」：攻击祭坛（M）每级 +6% 攻击、防守祭坛（N）每级 +6% 生命，
// 最高 5 级，消耗 15×等级 / 12×等级 仙气；修炼后传送回本方出生点（规则页）。
func (s *Session) terrAddProp(p *Player) {
	b := s.terrBattle()
	if b == nil {
		return
	}
	b.mu.Lock()
	pl := b.players[s.role.UID]
	if pl == nil {
		b.mu.Unlock()
		return
	}
	gate := pl.Gate
	var lv *int
	cost := 0
	switch gate {
	case "M":
		lv, cost = &pl.AtkLv, 15*(pl.AtkLv+1)
	case "N":
		lv, cost = &pl.HpLv, 12*(pl.HpLv+1)
	default:
		b.mu.Unlock()
		s.retMsg("这里不能修炼。")
		return
	}
	if *lv >= 5 {
		b.mu.Unlock()
		s.retMsg("修炼已满级。")
		return
	}
	if pl.Xianqi < cost {
		b.mu.Unlock()
		s.retMsg("仙气不足，修炼需要消耗仙气。")
		return
	}
	pl.Xianqi -= cost
	*lv++
	pl.Gate = b.spawnKey(pl.Side)
	b.mu.Unlock()
	s.terrSnapTeam(b, p) // 修炼加成要进阵容快照，别人来驱逐时按加成后的数值打
	s.terrSendOpen(b)
}
func (s *Session) terrSay(p *Player, m xym.Msg) {
	msg, _ := m["msg"].(string)
	if msg == "" {
		return
	}
	// 频道命令一律留日志：客户端偶尔不显示提示，靠日志才能确认命令到底有没有到服务端。
	slog.Info("territory", "uid", s.role.UID, "op", "say", "msg", msg)
	b := s.terrBattle()
	if b == nil {
		s.terrTell("你不在领土战场中。")
		return
	}
	if msg == "兑换神魂" {
		s.terrSoulExchange(p)
		return
	}
	if msg == "兑换胜利点" || msg == "兑换" {
		s.terrExchange(p)
		return
	}
	// 兜底：客户端动作栏的「挑战」按钮能不能渲染不受服务端控制（兑换胜利点就踩过这个坑），
	// 所以战场频道也提供一条命令，站在擂台点上直接发就能打。
	if msg == "挑战擂台" || msg == "挑战" {
		s.terrArenaChallenge(p, xym.Msg{})
		return
	}
	kind := 0
	if v, ok := m["kind"].(int64); ok {
		kind = int(v)
	}
	b.mu.Lock()
	uids := make([]int32, 0, len(b.players))
	for uid := range b.players {
		uids = append(uids, uid)
	}
	b.mu.Unlock()
	for _, uid := range uids {
		if sess := b.srv.sessionOf(uid); sess != nil {
			sess.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_BROADCAST_MSG",
				xym.Msg{"usernum": s.role.UID, "name": s.role.Name, "msg": msg, "from_side": 0, "kind": kind})
		}
	}
}

func (s *Session) terrLeave() {
	b := s.terrBattle()
	if b == nil {
		// 服务器重启/战场已结束时客户端还停在旧战场画面：也要回 CLOSE_MAIN，别让「撤退」没反应。
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_MAIN", nil)
		return
	}
	b.mu.Lock()
	leaving := b.players[s.role.UID]
	delete(b.players, s.role.UID)
	b.dropOwnerOf(s.role.UID) // 离场就不再是擂主
	empty := len(b.players) == 0
	b.mu.Unlock()
	if terrAutoExchange(leaving) {
		s.SendExt("ETC_PTO.S_SEND_TOP_MSG", xym.Msg{"msg": "仙气折算：50 仙气 → 30 胜利点"})
	}
	slog.Info("territory", "uid", s.role.UID, "op", "leave", "score", leaving.Score, "xianqi", leaving.Xianqi)
	terrSettle(s, leaving)
	// 官方：「胜利点大于等于 20 胜利点的离场玩家均将获得战场奖励」。
	if leaving != nil && leaving.Score >= terrLeaveBonusMin {
		s.player.df().GodSoul++
		s.terrTell(fmt.Sprintf("离场奖励：胜利点 %d（≥%d），神魂 +1", leaving.Score, terrLeaveBonusMin))
		s.player.saveLog(s)
	}
	terrMgr.mu.Lock()
	if terrMgr.byUID[s.role.UID] == b {
		delete(terrMgr.byUID, s.role.UID)
	}
	if empty {
		for k, v := range terrMgr.open {
			if v == b {
				delete(terrMgr.open, k)
			}
		}
	}
	terrMgr.mu.Unlock()
	s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_MAIN", nil)
}
