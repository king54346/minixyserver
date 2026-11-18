package xymsrv

import (
	randv2 "math/rand/v2"
	"sort"
	"sync"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

// 洞府破阵（DONGFU_BATTLE_*，客户端 view/PartyPoZhenView.lua + PartyPoZhenAwardGetView.lua）。
//
// 规则取自客户端「破阵之战」说明页原文（逐条对应，不要按印象改）：
//   - 周2、4、6 早上 10:00，系统自动开启破阵玩法，消耗 50×洞府等级 的洞府元宝；
//   - 开启后系统按【历史最高战力】自动选取 3×洞府等级 名成员入阵防守；
//   - 早上 10:00–12:00，被选中的防守成员可以更换入阵的阵容；
//   - 12:00 由系统按等级与积分匹配对阵（两两对抗），双方各自进攻对方的防守阵型；
//   - 12:00–18:30 所有洞府成员可挑战敌方阵中玩家，每人 3 次，每个防守目标可被挑战 5 次；
//   - 19:00 结算：两方各自累加自己的破阵积分，分数较高的洞府获胜；
//   - 破阵积分 =（防守方死亡护法数 − 攻击方死亡护法数），同一防守目标取最高的一次。
//
// 「自己的破阵积分」= 自己打出去的最高积分之和（攻击方视角）：
// 打对方防守目标时，积分记在那个目标上、归进攻方所有。所以
// 我方总分 = sum(Versus[].Score)（我打的），对方总分 = sum(Mine[].Score)（对方打的）。
const (
	dfBattleCostPerLv     = 50 // 开启消耗 50×洞府等级 洞府元宝
	dfBattleDefPerLv      = 3  // 每级 3 名防守成员
	dfBattleUserTimes     = 3  // 每位成员 3 次挑战
	dfBattleDefChallenges = 5  // 每个防守目标可被挑战 5 次
	dfBattleOpenHour      = 10 // 10:00 开启
	dfBattleDeployHour    = 12 // 12:00 布阵截止 + 匹配
	dfBattleSettleHour    = 19 // 19:00 结算
	// 18:30 挑战截止（协议里的 left_time 用它算「X时Y分后结束」）。
	dfBattleFightEnd = 18*60 + 30

	// dfBattleVer 是 DFBattle 的结构版本：改了模型就 +1，旧存档会被重建（同一天不重复扣元宝）。
	dfBattleVer = 2

	// dfBattleRaidWorkers 是后台模拟「对方进攻我方」的并发数。
	// 一个目标是 5 场 8v8，高等级洞府目标多，串行要 1-2 分钟（实测 30 目标 >100 秒）。
	dfBattleRaidWorkers = 4

	dfBattleWinPoint  = 3 // 每场挑战 3 破阵点（官方没写来源，本服自定）
	dfBattleLosePoint = 1 // 失败也给 1 点，别让参与者白打
)

// 破阵阶段。
const (
	dfBattleClosed  = 0 // 不是开启日，或还没到 10:00
	dfBattleDeploy  = 1 // 10:00–12:00 布阵期（防守成员可换阵容）
	dfBattleFight   = 2 // 12:00–18:30 挑战期
	dfBattlePending = 3 // 18:30–19:00 等待结算
	dfBattleSettled = 4 // 19:00 之后，已出结果
)

// dfBattleGate 是一个阵眼（防守位）。
type dfBattleGate struct {
	Type string // human / earth / heaven
	Name string // 人境守卫 / 地境守卫 / 天境守卫
}

var dfBattleGates = []dfBattleGate{
	{"human", "人境守卫"},
	{"earth", "地境守卫"},
	{"heaven", "天境守卫"},
}

// DFBattleDefender 是阵中的一个防守目标。
// Score 是「这个目标身上产生的、归进攻方的最高破阵积分」：
// Mine 里的表示对方打出来的（算对方总分），Versus 里的表示我打出来的（算我方总分）。
type DFBattleDefender struct {
	UID        int32             `json:"uid"`
	Name       string            `json:"name"`
	Icon       int32             `json:"icon"`
	Gate       int               `json:"gate"` // 0/1/2
	FP         int               `json:"fp"`   // 入阵时的战力
	Team       []*battle.Fighter `json:"-"`    // 入阵阵容快照（不落盘：Fighter 太大且可重建）
	Score      int               `json:"score,omitempty"`
	Challenged int               `json:"challenged,omitempty"` // 已被挑战次数
}

// DFBattleUser 是某个成员在本次破阵里的进度。
type DFBattleUser struct {
	Shop   map[int32]int `json:"shop,omitempty"`
	Times  int           `json:"times,omitempty"` // 已挑战次数
	Point  int           `json:"point,omitempty"` // 破阵点
	UseMax bool          `json:"use_max,omitempty"`
}

// DFBattle 是一个洞府本比赛日的破阵。
type DFBattle struct {
	Day       string                  `json:"day"`
	Ver       int                     `json:"ver,omitempty"`
	Vs        int32                   `json:"vs"`
	VsName    string                  `json:"vs_name"`
	VsIcon    int32                   `json:"vs_icon"`
	VsLv      int32                   `json:"vs_lv"`
	Lv        int32                   `json:"lv"`   // 开启时的洞府等级
	Cost      int32                   `json:"cost"` // 扣掉的洞府元宝
	Mine      []*DFBattleDefender     `json:"mine,omitempty"`
	Versus    []*DFBattleDefender     `json:"versus,omitempty"`
	Users     map[int32]*DFBattleUser `json:"users,omitempty"`
	Result    int                     `json:"result,omitempty"` // 0 未结算 / 1 胜 / 2 负 / 3 平
	Settled   bool                    `json:"settled,omitempty"`
	VsRaided  bool                    `json:"vs_raided,omitempty"`  // 对手进攻我方已算过
	VsRaiding bool                    `json:"vs_raiding,omitempty"` // 正在后台算（防重复）
}

// dfBattleShopGood 是破阵点商店的一件商品。kind 决定发货方式：
// lingqi / soul（神魂道具）/ gold（洞府元宝）。
type dfBattleShopGood struct {
	Name  string
	Kind  string
	ID    int32
	Type  int
	Cost  int
	Limit int
	N     int32
}

var dfBattleShopGoods = []dfBattleShopGood{
	{"洞府灵气", "lingqi", 1, 1, 20, 3, 200},
	{"神魂", "soul", 2, 2, 30, 2, 1},
	{"洞府元宝", "gold", 3, 3, 50, 1, 50},
}

// dfBattleNow 是取当前时间的钩子（测试用来固定「现在」，让日程判断可复现）。
var dfBattleNow = time.Now

// dfBattleRaidAsync 决定「对方进攻我方」是否真跑后台模拟。
// 测试里关掉它，分数才能完全可控（否则后台算完会覆盖手造的成绩）。
var dfBattleRaidAsync = true

// dfBattleDayOf 是本破阵的比赛日（跟测试时钟一致，不用真实 today()）。
func dfBattleDayOf(t time.Time) string { return t.Format("2006-01-02") }

// dfBattleWeekday 判断是否开启日：周2、4、6。
func dfBattleWeekday(d time.Weekday) bool {
	return d == time.Tuesday || d == time.Thursday || d == time.Saturday
}

// dfBattleSchedule 是某个自然日的破阵日程。
type dfBattleSchedule struct {
	Open   bool // 当天是不是开启日
	Start  time.Time
	Deploy time.Time
	End    time.Time
	Settle time.Time
}

func dfBattleScheduleOf(now time.Time) dfBattleSchedule {
	y, m, d := now.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	return dfBattleSchedule{
		Open:   dfBattleWeekday(now.Weekday()),
		Start:  day.Add(dfBattleOpenHour * time.Hour),
		Deploy: day.Add(dfBattleDeployHour * time.Hour),
		End:    day.Add(time.Duration(dfBattleFightEnd) * time.Minute),
		Settle: day.Add(dfBattleSettleHour * time.Hour),
	}
}

// dfBattleStageAt 算当前阶段。
func dfBattleStageAt(sc dfBattleSchedule, now time.Time) int {
	if !sc.Open {
		return dfBattleClosed
	}
	switch {
	case now.Before(sc.Start):
		return dfBattleClosed
	case now.Before(sc.Deploy):
		return dfBattleDeploy
	case now.Before(sc.End):
		return dfBattleFight
	case now.Before(sc.Settle):
		return dfBattlePending
	}
	return dfBattleSettled
}

// dfBattleNextStart 是下一次开启时刻（找最近的周2/4/6 10:00）。
func dfBattleNextStart(now time.Time) time.Time {
	for i := 0; i <= 7; i++ {
		d := now.AddDate(0, 0, i)
		if !dfBattleWeekday(d.Weekday()) {
			continue
		}
		y, m, dd := d.Date()
		at := time.Date(y, m, dd, dfBattleOpenHour, 0, 0, 0, d.Location())
		if at.After(now) {
			return at
		}
	}
	return now.AddDate(0, 0, 7)
}

// dfBattleLeft 是当前阶段距下一个节点的秒数（客户端显示「X时Y分后结束」）。
func dfBattleLeft(sc dfBattleSchedule, stage int, now time.Time) int {
	switch stage {
	case dfBattleDeploy:
		return int(sc.Deploy.Sub(now).Seconds())
	case dfBattleFight:
		return int(sc.End.Sub(now).Seconds())
	case dfBattlePending:
		return int(sc.Settle.Sub(now).Seconds())
	}
	return int(dfBattleNextStart(now).Sub(now).Seconds())
}

// 阵眼名（洞天）。
func dfBattleStationName(station int32) string {
	if station == 0 {
		return "桃花源"
	}
	return "洞天" + itoa(int(station))
}

// dfBattleDead 数一支队伍里阵亡（HP<=0）的护法数。
func dfBattleDead(hp []int64) int {
	n := 0
	for _, v := range hp {
		if v <= 0 {
			n++
		}
	}
	return n
}

// dfBattleTeamOf 取某人的阵容快照。**所有调用方都已持洞府锁**（dfBattleDefenders /
// dfBattleFoeDefenders / 布阵换阵容都在 Dongfus.do 里跑）：
// - 自己（uid==me）不能用 realFoe：handler 已持 p.mu，realFoe 会再取一次 → 死锁；
// - 别人也必须用 realFoeLocked：realFoe 尾部的 dongfuOf 会再取一次洞府锁 → 自锁死
//   （2026-11-17 线上就是这里把洞府锁永久占住，全服登录排队卡死）。
var dfBattleTeamOf = func(srv *Server, uid int32, me *Player) *ldFoe {
	if me != nil && me.UID == uid {
		if team, _ := me.dyTeam(nil); len(team) > 0 {
			name, icon := "", int32(0)
			if me.role != nil {
				name, icon = me.role.Name, int32(me.role.Icon)
			}
			return &ldFoe{UID: uid, Name: name, Icon: icon, Team: team}
		}
	}
	if f := srv.realFoeLocked(uid, me); f != nil && len(f.Team) > 0 {
		return f
	}
	return fakeFoeCached(uid)
}

func (b *DFBattle) user(uid int32) *DFBattleUser {
	if b.Users == nil {
		b.Users = map[int32]*DFBattleUser{}
	}
	u := b.Users[uid]
	if u == nil {
		u = &DFBattleUser{}
		b.Users[uid] = u
	}
	return u
}

// MyScore 是我方总分：我打对方防守目标取得的最高积分之和。
func (b *DFBattle) MyScore() int {
	n := 0
	for _, d := range b.Versus {
		n += d.Score
	}
	return n
}

// VsScore 是对方总分：对方打我方防守目标取得的最高积分之和。
func (b *DFBattle) VsScore() int {
	n := 0
	for _, d := range b.Mine {
		n += d.Score
	}
	return n
}

// dfBattleMyTimes 是本洞府已用掉的挑战次数。
func (b *DFBattle) dfBattleMyTimes() int {
	n := 0
	for _, u := range b.Users {
		n += u.Times
	}
	return n
}

// dfBattleGet 取本洞府当前的破阵。**调用方必须已持洞府锁**。
// 返回 (battle, stage, msg)：battle 为 nil 表示今天开不起来（msg 是给玩家的原因）。
//
// 开启是惰性的：到点后第一次访问就自动扣元宝、选防守成员、生成对手阵型。
func (srv *Server) dfBattleGet(df *Dongfu, me *Player, now time.Time) (*DFBattle, int, string) {
	sc := dfBattleScheduleOf(now)
	stage := dfBattleStageAt(sc, now)
	if stage == dfBattleClosed {
		return nil, stage, ""
	}
	day := dfBattleDayOf(now)
	sameDay := df.Battle != nil && df.Battle.Day == day
	if df.Battle == nil || !sameDay || df.Battle.Ver < dfBattleVer {
		// 只在布阵期 / 挑战期惰性开局。18:30 之后（等待结算、已结算）才第一次访问的，
		// 不补开——那时已经打不了，开一场只会白扣洞府元宝，还会让玩家看到一场「假战败」。
		if stage != dfBattleDeploy && stage != dfBattleFight {
			return nil, stage, ""
		}
		lv := df.Lv
		if lv < 1 {
			lv = 1
		}
		cost := dfBattleCostPerLv * lv
		// 同一天重建（结构升级）不再扣一次钱。
		if !sameDay {
			if df.Gold < cost {
				return nil, stage, "洞府元宝不足，无法开启破阵（需 " + itoa(int(cost)) + "）。"
			}
			df.Gold -= cost
		}
		df.Battle = srv.dfBattleCreate(df, lv, cost, me, now)
	}
	b := df.Battle
	// 12:00 之后对方开始进攻：惰性模拟一次（对方用自己被选中的防守阵容当攻击方）。
	// 必须是后台任务——每个目标 5 场，高等级洞府几十个目标，同步跑会把请求卡住几分钟。
	if stage >= dfBattleFight && !b.VsRaided && !b.VsRaiding {
		if dfBattleRaidAsync {
			srv.dfBattleStartRaid(df, b, now)
		} else {
			b.VsRaided = true
		}
	}
	// 后台还在算对方得分时先不结算，等下一次访问（否则会把对方当 0 分定胜负）。
	if stage >= dfBattleSettled && !b.Settled && !b.VsRaiding {
		b.settleLocked()
	}
	return b, stage, ""
}

// dfBattleEventLeft 是事件列表里「破阵」那一行的剩余秒数：
// 布阵期=距 12:00 开始挑战，挑战期=距 18:30 结束，等待结算=距 19:00 出结果。
func dfBattleEventLeft(sc dfBattleSchedule, stage int, now time.Time) int {
	switch stage {
	case dfBattleDeploy:
		return int(sc.Deploy.Sub(now).Seconds())
	case dfBattleFight:
		return int(sc.End.Sub(now).Seconds())
	case dfBattlePending:
		return int(sc.Settle.Sub(now).Seconds())
	}
	return 0
}

// dfBattleCreate 开一场：选防守成员、定对手、生成对手阵型。**调用方持洞府锁**。
func (srv *Server) dfBattleCreate(df *Dongfu, lv, cost int32, me *Player, now time.Time) *DFBattle {
	b := &DFBattle{Day: dfBattleDayOf(now), Ver: dfBattleVer, Lv: lv, Cost: cost, Users: map[int32]*DFBattleUser{}}
	b.Mine = srv.dfBattleDefenders(df, lv, me)
	b.Vs, b.VsName, b.VsIcon, b.VsLv = srv.dfBattleFoe(df.ID)
	b.Versus = srv.dfBattleFoeDefenders(b.Vs, b.VsLv, me)
	// 挑战期已经过了才第一次开局（结构升级重建 / 结算后才登录）：不补算一场假攻防，
	// 直接记为已结束、无结果，否则玩家会看到一个「自己 0 分、对方有分」的假战败。
	if sc := dfBattleScheduleOf(now); !now.Before(sc.End) {
		b.VsRaided, b.Settled = true, true
	}
	return b
}

// dfBattleDefenders 按战力选 3×等级 名成员入阵。**调用方持洞府锁**。
func (srv *Server) dfBattleDefenders(df *Dongfu, lv int32, me *Player) []*DFBattleDefender {
	type cand struct {
		uid int32
		fp  int
	}
	list := make([]cand, 0, len(df.Members))
	for _, m := range df.Members {
		if m == nil {
			continue
		}
		// 关掉自动报名的成员不参与自动选取（界面上那个「关闭自动报名」按钮）。
		if m.BattleOff {
			continue
		}
		list = append(list, cand{m.UID, srv.fightPointOf(m.UID, me)})
	}
	// 战力降序，同分按 uid 升序保证可复现。
	sort.Slice(list, func(i, j int) bool {
		if list[i].fp != list[j].fp {
			return list[i].fp > list[j].fp
		}
		return list[i].uid < list[j].uid
	})
	n := int(lv) * dfBattleDefPerLv
	if n > len(list) {
		n = len(list)
	}
	if n < 1 && len(list) > 0 {
		n = 1
	}
	out := make([]*DFBattleDefender, 0, n)
	for i := 0; i < n; i++ {
		f := dfBattleTeamOf(srv, list[i].uid, me)
		if f == nil || len(f.Team) == 0 {
			continue
		}
		out = append(out, &DFBattleDefender{
			UID: list[i].uid, Name: f.Name, Icon: f.Icon, Gate: i % len(dfBattleGates),
			FP: list[i].fp, Team: battle.CloneTeam(f.Team),
		})
	}
	return out
}

// dfBattleFoe 挑对手：优先另一个真实洞府，没有就用野外洞府（和争夺同一套假对手）。
func (srv *Server) dfBattleFoe(self int32) (id int32, name string, icon, lv int32) {
	var pick int32
	for k, o := range srv.dongfu.List {
		if o == nil || k == self {
			continue
		}
		if pick == 0 || k < pick {
			pick = k
		}
	}
	if pick != 0 {
		o := srv.dongfu.List[pick]
		return pick, o.Name, o.Icon, max(1, o.Lv)
	}
	npc := int32(gankNPCBase + 2) // 青丘狐穴（野外洞府）
	if t := srv.dongfu.gankTargetOf(npc); t != nil {
		return npc, t.name, t.icon, 3
	}
	return npc, "守阵洞府", 0, 3
}

// dfBattleFoeDefenders 生成对方阵中的防守目标。**调用方持洞府锁**。
func (srv *Server) dfBattleFoeDefenders(vs, vsLv int32, me *Player) []*DFBattleDefender {
	lv := max(1, int(vsLv))
	n := lv * dfBattleDefPerLv
	var uids []int32
	if o := srv.dongfu.List[vs]; o != nil {
		for _, m := range o.Members {
			if m != nil {
				uids = append(uids, m.UID)
			}
		}
	}
	out := make([]*DFBattleDefender, 0, n)
	if len(uids) == 0 {
		// 野外洞府：用假对手凑满 n 个（dfBattleTeamOf 内部对假 uid 会落到 fakeFoeCached）。
		for i := 0; i < n; i++ {
			uid := int32(ldFakeUIDBase + 40 + (int(vs%100)*3+i)%60)
			f := dfBattleTeamOf(srv, uid, me)
			if f == nil || len(f.Team) == 0 {
				continue
			}
			out = append(out, &DFBattleDefender{UID: f.UID, Name: f.Name, Icon: f.Icon,
				Gate: i % len(dfBattleGates), FP: f.FP, Team: battle.CloneTeam(f.Team)})
		}
		return out
	}
	if n > len(uids) {
		n = len(uids)
	}
	for i := 0; i < n; i++ {
		uid := uids[i]
		f := dfBattleTeamOf(srv, uid, me)
		if f == nil || len(f.Team) == 0 {
			continue
		}
		out = append(out, &DFBattleDefender{UID: f.UID, Name: f.Name, Icon: f.Icon,
			Gate: i % len(dfBattleGates), FP: f.FP, Team: battle.CloneTeam(f.Team)})
	}
	return out
}

// dfBattleStartRaid 在后台模拟「对方进攻我方防守目标」：每个目标最多 5 次，取最高积分。
//
// 为什么要异步：一个目标 5 场、高等级洞府有 3×等级 个目标（10 级 = 30 个目标 = 150 场），
// 同步跑会把 C_GET_DONGFU_BATTLE_OPEN_INFO 卡住几分钟（线上实测 elapsed>5m，客户端一直收不到回包）。
//
// 调用方持洞府锁；函数只做拷贝和起 goroutine，模拟在锁外跑，算完再回锁写分。
func (srv *Server) dfBattleStartRaid(df *Dongfu, b *DFBattle, now time.Time) {
	type raidJob struct {
		idx      int
		atk, def []*battle.Fighter
	}
	jobs := make([]raidJob, 0, len(b.Mine))
	for i, d := range b.Mine {
		if i >= len(b.Versus) || len(d.Team) == 0 || len(b.Versus[i].Team) == 0 {
			continue
		}
		jobs = append(jobs, raidJob{i, battle.CloneTeam(b.Versus[i].Team), battle.CloneTeam(d.Team)})
	}
	if len(jobs) == 0 {
		b.VsRaided = true
		return
	}
	b.VsRaiding = true
	dfID, day, seed := df.ID, b.Day, now.UnixNano()
	go func() {
		scores := make([]int, len(jobs))
		var wg sync.WaitGroup
		sem := make(chan struct{}, dfBattleRaidWorkers)
		for k := range jobs {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				j := jobs[k]
				best, set := 0, false
				rng := randv2.New(randv2.NewPCG(uint64(seed)+uint64(k)*7919, uint64(dfID)*31+7))
				for n := 0; n < dfBattleDefChallenges; n++ {
					t1 := battle.CloneTeam(j.atk)
					t2 := battle.CloneTeam(j.def)
					res := battle.SimulateWith(t1, t2, battle.Options{}, rng)
					gain := dfBattleDead(res.HP[1]) - dfBattleDead(res.HP[0])
					if !set || gain > best {
						best, set = gain, true
					}
				}
				scores[k] = best
			}(k)
		}
		wg.Wait()
		srv.dongfu.do(func() bool {
			o := srv.dongfu.List[dfID]
			if o == nil || o.Battle == nil || o.Battle.Day != day {
				return false
			}
			// 期间可能已经被重建（跨天/改版），换了一场就丢弃这次结果。
			if o.Battle != b {
				return false
			}
			for k, j := range jobs {
				if j.idx >= len(o.Battle.Mine) {
					continue
				}
				o.Battle.Mine[j.idx].Score = scores[k]
				o.Battle.Mine[j.idx].Challenged = dfBattleDefChallenges
			}
			o.Battle.VsRaided, o.Battle.VsRaiding = true, false
			return true
		})
	}()
}

// settleLocked 19:00 结算：比总分，定结果。**调用方持洞府锁**。
func (b *DFBattle) settleLocked() {
	if b.Settled {
		return
	}
	b.Settled = true
	mine, vs := b.MyScore(), b.VsScore()
	// 双方都没打（比如中途改版重建、或者没人参加）不算平局，保持 0 分不出结果。
	if mine == 0 && vs == 0 {
		b.Result = 0
		return
	}
	switch {
	case mine > vs:
		b.Result = 1
	case mine < vs:
		b.Result = 2
	default:
		b.Result = 3
	}
}

// dfBattleSendInfo 给客户端推主界面或开启前信息。
//
// 两个界面订阅的消息不一样，所以 b 存在时两条都发（各取所需）：
//   - 洞府 → 争夺/灵穴/破阵 那一栏（PartyFightDetailView）只订阅 S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO；
//   - 事件 → 破阵 打开的破阵主界面（PartyPoZhenView，经 party_mgr / EmergencyEventListView）
//     只订阅 S_SEND_DONGFU_BATTLE_MAIN_INFO。
//
// 早先布阵期只发 PRE_OPEN，导致从事件进入破阵主界面时一片空白。
func (s *Session) dfBattleSendInfo(p *Player) {
	fp := s.srv.fightPointOf(s.role.UID, p)
	var (
		main      xym.Msg
		stage     int
		tell      string
		hasBattle bool
	)
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		b, st, errMsg := s.srv.dfBattleGet(df, p, dfBattleNow())
		stage, tell = st, errMsg
		if b == nil {
			return false
		}
		hasBattle = true
		main = s.dfBattleMainInfo(p, df, b, st, fp)
		return false
	})
	if tell != "" {
		s.retMsg(tell)
	}
	// 主界面数据先发（打开破阵主界面时立刻有内容），规则栏的 PRE_OPEN 随后补一条。
	if hasBattle {
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_MAIN_INFO", main)
	}
	s.SendExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO", s.dfBattlePreOpenMsg(stage))
}

// dfBattlePreOpenMsg 是 S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO。
// 注意：status 在协议里是 int8，但编码器只认 int，传 int8 会 panic「not a number」。
func (s *Session) dfBattlePreOpenMsg(stage int) xym.Msg {
	sc := dfBattleScheduleOf(dfBattleNow())
	st := 0
	if stage == dfBattleDeploy || stage == dfBattleFight || stage == dfBattlePending || stage == dfBattleSettled {
		st = 1
	}
	return xym.Msg{"status": st, "left_time": dfBattleLeft(sc, stage, dfBattleNow())}
}

// dfBattleMainInfo 是 S_SEND_DONGFU_BATTLE_MAIN_INFO。调用方持洞府锁。
//
// 协议里 team（敌方）/ my_team（我方）都是「按阵眼分组」的数组：每项 {type, list:[成员]}。
func (s *Session) dfBattleMainInfo(p *Player, df *Dongfu, b *DFBattle, stage, fp int) xym.Msg {
	u := b.user(s.role.UID)
	sc := dfBattleScheduleOf(dfBattleNow())
	left := dfBattleLeft(sc, stage, dfBattleNow())
	leftTimes := max(0, dfBattleUserTimes-u.Times)
	if stage != dfBattleFight {
		leftTimes = 0
	}

	// 敌方阵中成员（我打它们）。
	team := make([]xym.Msg, 0, len(dfBattleGates))
	for gi, g := range dfBattleGates {
		members := []xym.Msg{}
		for _, d := range b.Versus {
			if d.Gate != gi {
				continue
			}
			members = append(members, xym.Msg{
				"usernum": d.UID, "vip_icon_ts": 0, "name": d.Name, "icon": d.Icon,
				"gate": gi, "left_challenge_times": leftTimes, "beat_score": d.Score,
				"my_fight_point": fp, "fight_point": d.FP, "record_list": []xym.Msg{},
			})
		}
		team = append(team, xym.Msg{"type": g.Type, "list": members})
	}

	// 我方阵中成员（对方打它们）。
	myTeam := make([]xym.Msg, 0, len(dfBattleGates))
	for gi, g := range dfBattleGates {
		members := []xym.Msg{}
		for _, d := range b.Mine {
			if d.Gate != gi {
				continue
			}
			members = append(members, xym.Msg{
				"usernum": d.UID, "vip_icon_ts": 0, "name": d.Name, "icon": d.Icon,
				"gate": gi, "left_challenge_times": leftTimes, "beat_score": d.Score,
				"my_fight_point": d.FP, "fight_point": d.FP,
			})
		}
		myTeam = append(myTeam, xym.Msg{"type": g.Type, "list": members})
	}

	// 进度条用的上限：我方防守目标数 × 每个 5 次 × 单场最大 6 个护法差；次数上限 = 成员数 × 3。
	maxScore := len(b.Mine) * dfBattleDefChallenges * 6
	maxTimes := len(df.Members) * dfBattleUserTimes

	vsName := b.VsName
	if b.Settled {
		// 结算后把结果附在对手名后，客户端没有独立的结果字段。
		switch b.Result {
		case 1:
			vsName += "（我方获胜）"
		case 2:
			vsName += "（我方落败）"
		case 3:
			vsName += "（平局）"
		}
	}

	return xym.Msg{
		"battle_point":              u.Point,
		"max_total_beat_score":      maxScore,
		"max_total_challenge_times": maxTimes,
		"dongfu_name":               df.Name,
		"dongfu_icon":               df.Icon,
		"server_name":               "",
		"total_beat_score":          b.MyScore(),
		"total_challenge_times":     b.dfBattleMyTimes(),
		"vs_dongfu_name":            vsName,
		"vs_dongfu_icon":            b.VsIcon,
		"vs_server_name":            "再续前缘",
		"vs_total_beat_score":       b.VsScore(),
		"vs_total_challenge_times":  b.MineChallenged(),
		"left_time":                 left,
		"left_fight_times":          leftTimes,
		"team":                      team,
		"my_team":                   myTeam,
	}
}

// MineChallenged 是我方防守目标已被挑战的总次数（协议里的 vs_total_challenge_times）。
func (b *DFBattle) MineChallenged() int {
	n := 0
	for _, d := range b.Mine {
		n += d.Challenged
	}
	return n
}

// dfBattleFightable 判断当前能否挑战：阶段、次数、目标次数。
func dfBattleFightable(b *DFBattle, stage int, u *DFBattleUser, d *DFBattleDefender) string {
	if stage != dfBattleFight {
		return "现在不是破阵挑战时间（12:00–18:30）。"
	}
	if u.Times >= dfBattleUserTimes {
		return "你本次破阵的挑战次数已用完。"
	}
	if d.Challenged >= dfBattleDefChallenges {
		return "该防守目标已被挑战满 " + itoa(dfBattleDefChallenges) + " 次。"
	}
	return ""
}

// onDfBattleFight 打一场破阵：进攻敌方阵中某个防守目标，按护法死亡差算破阵积分。
func onDfBattleFight(s *Session, p *Player, m xym.Msg) {
	// C_START_DONGFU_BATTLE_FIGHT = {varstr type, int32 vs_usernum}
	target, _ := m["vs_usernum"].(int64)
	if target == 0 {
		target, _ = m["usernum"].(int64)
	}
	gateIdx := -2
	if g, ok := m["gate"].(int64); ok {
		gateIdx = int(g)
	}
	if t, ok := m["type"].(string); ok {
		for k, g := range dfBattleGates {
			if g.Type == t {
				gateIdx = k
				break
			}
		}
	}

	sent := false
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		b, stage, errMsg := s.srv.dfBattleGet(df, p, dfBattleNow())
		if b == nil {
			if errMsg != "" {
				s.retMsg(errMsg)
			} else {
				s.retMsg("破阵还没开启。")
			}
			return false
		}
		u := b.user(s.role.UID)
		// 选目标：优先 usernum 命中，其次按 gate 取该阵眼下第一个还能打的目标。
		var d *DFBattleDefender
		for _, x := range b.Versus {
			if int32(target) == x.UID {
				d = x
				break
			}
		}
		if d == nil && gateIdx >= 0 {
			for _, x := range b.Versus {
				if x.Gate == gateIdx && x.Challenged < dfBattleDefChallenges {
					d = x
					break
				}
			}
		}
		if d == nil {
			s.retMsg("对方阵中找不到这个防守目标。")
			return false
		}
		if msg := dfBattleFightable(b, stage, u, d); msg != "" {
			s.retMsg(msg)
			return false
		}

		team1, _ := p.dyTeam(nil)
		if len(team1) == 0 || len(d.Team) == 0 {
			s.retMsg("阵容不可用，稍后再试。")
			return false
		}
		t1 := battle.CloneTeam(team1)
		t2 := battle.CloneTeam(d.Team)
		for _, f := range t2 {
			if f.Pos < 100 {
				f.Pos += 100 // 守方站位 100+，客户端按站位分左右两排
			}
		}
		r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(d.Team)
		res := s.simulate(p, t1, t2, [2]int32{p.leaderSkill(), 0}, battle.Formation{}, s.actRng())
		s.fightRecordW(dfBattleRid(s.role.UID), d.Name, 0, 0, r1, r2, res.Record)

		// 破阵积分 =（防守方死亡 − 攻击方死亡），同一目标取最高。
		gain := dfBattleDead(res.HP[1]) - dfBattleDead(res.HP[0])
		d.Challenged++
		if d.Challenged == 1 || gain > d.Score {
			d.Score = gain
		}
		u.Times++
		if gain > 0 {
			u.Point += dfBattleWinPoint
		} else {
			u.Point += dfBattleLosePoint
		}
		s.retMsg("破阵积分 +" + itoa(gain) + "（防守方阵亡 " + itoa(dfBattleDead(res.HP[1])) +
			" − 我方阵亡 " + itoa(dfBattleDead(res.HP[0])) + "）")
		sent = true
		return true
	})
	if !sent {
		return
	}
	s.dfBattleSendInfo(p)
}

func dfBattleRid(uid int32) string {
	return "dfbattle-" + itoa(int(uid)) + "-" + itoa(int(time.Now().UnixNano()/1000))
}

// dfBattleTeamDataMsg 是 S_SEND_DONGFU_BATTLE_TEAM_FIGHT_DATA（阵容预览）。调用方持洞府锁。
func (s *Session) dfBattleTeamDataMsg(p *Player, df *Dongfu, useMax bool) xym.Msg {
	list := []xym.Msg{}
	if lu := p.lineup(1); lu != nil {
		for i, sl := range lu.Slots {
			if sl == nil || sl.Pet <= 0 {
				continue
			}
			if pet := p.petAtBag(sl.Pet); pet != nil {
				list = append(list, xym.Msg{"pos": i + 1, "type": pet.Type})
			}
		}
	}
	// 「历史最高战力阵容」的快照没做，useMax 只作为开关状态回传（见 onDfBattleChangeTeam）。
	_ = useMax
	return xym.Msg{
		"fight_point": 0,
		"gate_name":   dfBattleStationName(df.Station),
		"station_id":  df.Station,
		"list":        list,
	}
}

// onDfBattleChangeTeam 布阵期更换入阵阵容（只有被选中的防守成员能换）。
func onDfBattleChangeTeam(s *Session, p *Player, m xym.Msg) {
	changed := ""
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		b, stage, errMsg := s.srv.dfBattleGet(df, p, dfBattleNow())
		if b == nil {
			changed = errMsg
			if changed == "" {
				changed = "破阵还没开启。"
			}
			return false
		}
		u := b.user(s.role.UID)
		u.UseMax = !u.UseMax
		switch stage {
		case dfBattleDeploy:
			hit := false
			for _, d := range b.Mine {
				if d.UID != s.role.UID {
					continue
				}
				f := dfBattleTeamOf(s.srv, d.UID, p)
				if f == nil || len(f.Team) == 0 {
					break
				}
				d.Team = battle.CloneTeam(f.Team)
				d.FP = f.FP
				hit = true
			}
			if hit {
				changed = "入阵阵容已更新。"
			} else {
				changed = "你不在本次入阵名单里，无法更换阵容。"
			}
		default:
			changed = "只有 10:00–12:00 的布阵期才能更换阵容。"
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_TEAM_FIGHT_DATA",
			s.dfBattleTeamDataMsg(p, df, u.UseMax))
		return true
	})
	if changed != "" {
		s.retMsg(changed)
	}
	s.dfBattleSendInfo(p)
}

// onDfBattleTeamData 取阵容预览。
func onDfBattleTeamData(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		useMax := false
		if b, _, _ := s.srv.dfBattleGet(df, p, dfBattleNow()); b != nil {
			useMax = b.user(s.role.UID).UseMax
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_TEAM_FIGHT_DATA",
			s.dfBattleTeamDataMsg(p, df, useMax))
		return false
	})
}

// dfBattleStatusText 是给玩家看的一句话状态（规则栏没有状态控件，只能走 retMsg 浮字）。
func dfBattleStatusText(b *DFBattle, stage int, leftTimes int, now time.Time) string {
	if b == nil {
		switch stage {
		case dfBattleClosed:
			return "破阵未开启，下次开启：" + dfBattleNextStart(now).Format("01-02 15:04") + "。"
		}
		return "破阵暂未开启。"
	}
	switch stage {
	case dfBattleDeploy:
		return "破阵已开启（布阵中），12:00 开始挑战。"
	case dfBattleFight:
		return "破阵挑战中：你还有 " + itoa(leftTimes) + " 次，18:30 结束。"
	case dfBattlePending:
		return "破阵挑战已结束，19:00 出结果。"
	default:
		switch b.Result {
		case 1:
			return "破阵结束：我方获胜（" + itoa(b.MyScore()) + " : " + itoa(b.VsScore()) + "）。"
		case 2:
			return "破阵结束：我方落败（" + itoa(b.MyScore()) + " : " + itoa(b.VsScore()) + "）。"
		case 3:
			return "破阵结束：平局（" + itoa(b.MyScore()) + " : " + itoa(b.VsScore()) + "）。"
		}
		return "破阵今日已结束（" + itoa(b.MyScore()) + " : " + itoa(b.VsScore()) + "）。"
	}
}

// onDfBattleOpenInfo 处理「洞府详情 → 破阵」这一栏（C_GET_DONGFU_BATTLE_OPEN_INFO）。
//
// 客户端的这个入口只订阅 S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO（见 view/PartyFightDetailView.lua
// 的 reg 列表：争夺=S_GANK_LIST、反击=S_REVENGE_LIST、破阵=S_DONGFU_BATTLE_PRE_OPEN_INFO、
// 领土=S_TERRITORY_AVAIL_VS_DONGFU_LIST）。之前这里回的是 S_DONGFU_BATTLE_OPEN_INFO，
// 客户端无人接收，所以界面既没有状态也没有倒计时。
//
// 现在始终发 PRE_OPEN_INFO（带 status / left_time），另外在已开启时补一条胜平负汇总。
func onDfBattleOpenInfo(s *Session, p *Player, m xym.Msg) {
	var (
		pre    xym.Msg
		main   xym.Msg
		tell   string
		opened bool
	)
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		now := dfBattleNow()
		b, stage, _ := s.srv.dfBattleGet(df, p, now)
		pre = s.dfBattlePreOpenMsg(stage)
		leftTimes := 0
		if b != nil {
			leftTimes = max(0, dfBattleUserTimes-b.user(s.role.UID).Times)
		}
		tell = dfBattleStatusText(b, stage, leftTimes, now)
		if b == nil {
			return false
		}
		opened = true
		win, draw, lose := 0, 0, 0
		switch b.Result {
		case 1:
			win = 1
		case 2:
			lose = 1
		case 3:
			draw = 1
		}
		team := make([]xym.Msg, 0, len(dfBattleGates))
		for _, g := range dfBattleGates {
			team = append(team, xym.Msg{"type": g.Type,
				"list": []xym.Msg{{"name": b.VsName, "icon": b.VsIcon}}})
		}
		main = xym.Msg{"team": team, "score": b.MyScore(),
			"total_win_times": win, "total_draw_times": draw, "total_lose_times": lose}
		return false
	})
	s.SendExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_PRE_OPEN_INFO", pre)
	if opened {
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_OPEN_INFO", main)
	}
	// 规则栏本身没有状态控件（只有规则文字 + 关闭自动报名），一句话浮字让玩家知道开没开。
	if tell != "" {
		s.retMsg(tell)
	}
}

// onDfBattleRank 是本方与对手的破阵排行。
func onDfBattleRank(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		b, _, _ := s.srv.dfBattleGet(df, p, dfBattleNow())
		mine, vs, vsName, vsIcon := 0, 0, "暂无对手", int32(0)
		if b != nil {
			mine, vs, vsName, vsIcon = b.MyScore(), b.VsScore(), b.VsName, b.VsIcon
		}
		list := []xym.Msg{
			{"pos": 1, "icon": df.Icon, "name": df.Name, "server_name": "", "score": mine},
			{"pos": 2, "icon": vsIcon, "name": vsName, "server_name": "再续前缘", "score": vs},
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i]["score"].(int) > list[j]["score"].(int)
		})
		for i := range list {
			list[i]["pos"] = i + 1
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_RANK", xym.Msg{"list": list})
		return false
	})
}

// onDfBattleShop / onDfBattleBuy 是破阵点商店。
func onDfBattleShop(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		b, _, _ := s.srv.dfBattleGet(df, p, dfBattleNow())
		point := 0
		var shop map[int32]int
		if b != nil {
			u := b.user(s.role.UID)
			point, shop = u.Point, u.Shop
		}
		list := []xym.Msg{}
		for _, g := range dfBattleShopGoods {
			left := g.Limit
			if shop != nil {
				left = max(0, g.Limit-shop[g.ID])
			}
			list = append(list, xym.Msg{"goods_id": g.ID, "name": g.Name, "type": g.Type,
				"point": g.Cost, "left_num": left})
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_BATTLE_POINT_SHOP_LIST",
			xym.Msg{"point": point, "list": list})
		return false
	})
}

func onDfBattleBuy(s *Session, p *Player, m xym.Msg) {
	goodsID := int32(0)
	if v, ok := m["goods_id"].(int64); ok {
		goodsID = int32(v)
	}
	bought := false
	var kind string
	var n int32
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		b, _, errMsg := s.srv.dfBattleGet(df, p, dfBattleNow())
		if b == nil {
			if errMsg != "" {
				s.retMsg(errMsg)
			} else {
				s.retMsg("破阵还没开启。")
			}
			return false
		}
		u := b.user(s.role.UID)
		var g *dfBattleShopGood
		for k := range dfBattleShopGoods {
			if dfBattleShopGoods[k].ID == goodsID {
				g = &dfBattleShopGoods[k]
				break
			}
		}
		switch {
		case g == nil:
			s.retMsg("商品不存在。")
			return false
		case u.Point < g.Cost:
			s.retMsg("破阵点不足。")
			return false
		case u.Shop != nil && u.Shop[goodsID] >= g.Limit:
			s.retMsg("该商品已达限购次数。")
			return false
		}
		u.Point -= g.Cost
		if u.Shop == nil {
			u.Shop = map[int32]int{}
		}
		u.Shop[goodsID]++
		kind, n = g.Kind, g.N
		switch kind {
		case "lingqi":
			df.accrueLingqi()
			df.Lingqi += n
		case "gold":
			df.Gold += n
		}
		bought = true
		return true
	})
	if !bought {
		return
	}
	if kind == "soul" {
		p.addItem(terrSoulItem, n)
	}
	onDfBattleShop(s, p, m)
	s.dfBattleSendInfo(p)
}

// onDfBattleSwitchAuto 切换自动报名（决定下次开启时是否被系统自动选入防守）。
// 状态记在成员上（DFMember.BattleOff），否则破阵每天重建会丢掉这个开关。
func onDfBattleSwitchAuto(s *Session, p *Player, m xym.Msg) {
	tell := ""
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		if df.Battle == nil || df.Battle.Day != dfBattleDayOf(dfBattleNow()) {
			// 没开也允许设，下一次开启时生效。
		}
		me.BattleOff = !me.BattleOff
		if me.BattleOff {
			tell = "已关闭自动报名，下次开启时不会被系统选入防守。"
		} else {
			tell = "已开启自动报名，下次开启时会被系统选入防守。"
		}
		return true
	})
	if tell != "" {
		s.retMsg(tell)
	}
	s.dfBattleSendInfo(p)
}

func init() {
	Handle("DONGFU_PTO.C_OPEN_DONGFU_BATTLE", func(s *Session, p *Player, m xym.Msg) { s.dfBattleSendInfo(p) })
	Handle("DONGFU_PTO.C_GET_DONGFU_BATTLE_MAIN_INFO", func(s *Session, p *Player, m xym.Msg) { s.dfBattleSendInfo(p) })
	Handle("DONGFU_PTO.C_GET_DONGFU_BATTLE_OPEN_INFO", onDfBattleOpenInfo)
	Handle("DONGFU_PTO.C_GET_DONGFU_BATTLE_RANK", onDfBattleRank)
	Handle("DONGFU_PTO.C_START_DONGFU_BATTLE_FIGHT", onDfBattleFight)
	Handle("DONGFU_PTO.C_BATTLE_CHANGE_TEAM_FIGHT_DATA", onDfBattleChangeTeam)
	Handle("DONGFU_PTO.C_GET_BATTLE_TEAM_FIGHT_DATA", onDfBattleTeamData)
	Handle("DONGFU_PTO.C_OPEN_BATTLE_POINT_SHOP", onDfBattleShop)
	Handle("DONGFU_PTO.C_BUY_BATTLE_POINT_SHOP_GOODS", onDfBattleBuy)
	Handle("DONGFU_PTO.C_BATTLE_SWITCH_AUTO_OPEN", onDfBattleSwitchAuto)
	Handle("DONGFU_PTO.C_PLAY_DONGFU_BATTLE_FIGHT_RECORD", func(s *Session, p *Player, m xym.Msg) { s.retMsg("战报已失效。") })
}
