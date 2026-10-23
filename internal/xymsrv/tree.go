package xymsrv

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 摇钱树（TREE_PTO）：每天种一棵，成熟后收获元宝；生长期可以用甘露浇灌升级（等级越高元宝越多），长了妖虱要先除妖；
// 摇钱树林按财富值排行，可以驻足观赏别人的树，树主摇落元宝时观赏者各得一份。
//
// 依据：view/GoldTreeMainView.lua（state 1 未种 / 2 生长 / 3 成熟；剩余时间 > 4 小时、没有妖虱和果实时可「培养」；
// 有妖虱时按钮是「除妖」→ C_FIGHT_BUG，胜利 S_FIGHT_WIN；gold > 0 时「收获」→ C_GET_GOLD；摇钱树林里自己的树
// 「摇落元宝」→ C_THROW_GOLD，别人的「驻足观赏」→ C_LIKE_TREE{rankpos}）；logic/tree_mgr.lua（升到 2–10 级要
// 6 / 30 / 40 / 50 / 100 / 140 / 180 / 240 / 300 个甘露 10238）。
// 抓包：一天只能种一棵（S_RET_MSG「今天已经种过一棵摇钱树了，明天再来吧。」）；2 级成熟树收获 44 元宝：
// S_ATTR_UPDATE、S_REWARD_MSG{type 1, gold 44}、S_OPEN_PLANT{state 1, level 1}（收获后回到 1 级未种）；
// S_TREE_RANK{rich 财富值, list[uid, icon, grade, name, times, count 树下人数, isfans, pos]}。
//
// 估计：生长 8 小时；元宝见 treeGoldByLevel（10 级 1500，用户要求）；种下时 50% 长 1–2 只妖虱，收获时每只没除的
// 妖虱扣 20% 元宝；妖虱是按人物等级取主线关卡的普通阵容；财富值 = 累计收获的元宝；
// 每天可以观赏一棵树，树主每天可以摇落一次，观赏者各得 2 元宝；护树好友（好友系统未做）为空。

const (
	treeNone   = 1
	treeGrow   = 2
	treeRipe   = 3
	treeGrowth = 8 * time.Hour
	treeWater  = 4 * time.Hour // 剩余时间超过它才能培养
	treeMaxLv  = 10
	treeDew    = 10238 // 甘露
	treeShake  = 2     // 摇落元宝时每位观赏者得的元宝
)

var treeWaterNeed = [treeMaxLv + 1]int32{0, 0, 6, 30, 40, 50, 100, 140, 180, 240, 300}

// treeGoldByLevel：各级成熟后收获的元宝。1、2 级照抓包（20 / 44），10 级 1500（用户要求），中间逐级加速增长，
// 与培养所需甘露（treeWaterNeed）越往后越多相称。
var treeGoldByLevel = [treeMaxLv + 1]int32{0, 20, 44, 100, 180, 280, 420, 600, 820, 1100, 1500}

func treeGold(level int) int32 { return treeGoldByLevel[min(max(level, 1), treeMaxLv)] }

// TreeState 是摇钱树存档（Player.Tree）。
type TreeState struct {
	State   int    `json:"state"`
	Level   int    `json:"level"`
	Planted int64  `json:"planted"` // 种下的 unix 秒
	PlantOn string `json:"plant_on"`
	Bug     int    `json:"bug"`
	Nums    int    `json:"nums"` // 累计种植
	Rich    int    `json:"rich"` // 财富值
	Push    int    `json:"push"`
	LikeOn  string `json:"like_on,omitempty"`  // 今天已观赏
	ShakeOn string `json:"shake_on,omitempty"` // 今天已摇落
}

func (p *Player) tree() *TreeState {
	if p.Tree == nil {
		p.Tree = &TreeState{State: treeNone, Level: 1}
	}
	t := p.Tree
	if t.State == treeGrow && time.Now().Unix() >= t.Planted+int64(treeGrowth/time.Second) {
		t.State = treeRipe
	}
	return t
}

func (t *TreeState) left() int64 {
	if t.State != treeGrow {
		return 0
	}
	return max(0, t.Planted+int64(treeGrowth/time.Second)-time.Now().Unix())
}

// harvest 是成熟时可收的元宝（扣掉没除的妖虱）。
func (t *TreeState) harvest() int32 {
	if t.State != treeRipe {
		return 0
	}
	return treeGold(t.Level) * int32(max(0, 5-t.Bug)) / 5
}

func (s *Session) treeMsg(p *Player) {
	t := p.tree()
	s.SendExt("TREE_PTO.S_OPEN_PLANT", xym.Msg{"state": t.State, "times": 0, "lefttime": t.left(), "gold": t.harvest(),
		"bug": t.Bug, "level": t.Level, "extra_fruit": -1, "planted_nums": t.Nums})
}

// ---- 摇钱树林（种树 / 收获 / 打开时登记；存成全服数据 treeUID，重启不丢）----
// 之前只在内存里：服务器一重启林子就空了，要等树主自己再打开摇钱树才会出现，别人没法驻足观赏。

type treeRow struct {
	UID     int32
	Name    string
	Icon    int
	Grade   int
	Rich    int
	Planted bool // 今天种着树（还没收获的也进林子）
	Fans    map[int32]bool
}

var treeWood = struct {
	sync.Mutex
	st   store.Store // 已载入的存档（测试里每个服务器各一个）
	init bool
	rows map[int32]*treeRow
}{rows: map[int32]*treeRow{}}

// treeWoodLoad 在持锁时调用：第一次用到（或换了存档）时从全服数据读入林子。
func (s *Session) treeWoodLoad() {
	st := s.srv.players.st
	if treeWood.init && treeWood.st == st {
		return
	}
	treeWood.init, treeWood.st = true, st
	rows := map[int32]*treeRow{}
	loadGlobal(st, treeUID, &rows, "tree wood")
	for _, r := range rows {
		if r.Fans == nil {
			r.Fans = map[int32]bool{}
		}
	}
	treeWood.rows = rows
}

// treeWoodSave 在持锁时调用。
func treeWoodSave() { saveGlobal(treeWood.st, treeUID, treeWood.rows, "tree wood") }

func (s *Session) treeRegister(p *Player) {
	t := p.tree()
	treeWood.Lock()
	defer treeWood.Unlock()
	s.treeWoodLoad()
	r := treeWood.rows[s.role.UID]
	if r == nil {
		r = &treeRow{UID: s.role.UID, Fans: map[int32]bool{}}
		treeWood.rows[s.role.UID] = r
	}
	r.Name, r.Icon, r.Grade, r.Rich, r.Planted = s.role.Name, s.role.Icon, s.role.Grade, t.Rich, t.State != treeNone
	treeWoodSave()
}

// treeRank 是有财富值的树，从高到低（前 20）。
func treeRank() []treeRow {
	treeWood.Lock()
	defer treeWood.Unlock()
	var out []treeRow
	for _, r := range treeWood.rows {
		if r.Rich > 0 || r.Planted {
			c := *r
			c.Fans = maps.Clone(r.Fans)
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b treeRow) int { return cmp.Or(cmp.Compare(b.Rich, a.Rich), cmp.Compare(a.UID, b.UID)) })
	return out[:min(len(out), 20)]
}

func (s *Session) treeRankMsg(p *Player) {
	s.treeRegister(p)
	list := []xym.Msg{}
	for i, r := range treeRank() {
		isfans := b2i(r.Fans[s.role.UID])
		if r.UID == s.role.UID {
			isfans = -1
		}
		list = append(list, xym.Msg{"uid": r.UID, "icon": r.Icon, "vip_icon_ts": 0, "grade": r.Grade, "xgrade": 0,
			"name": r.Name, "times": r.Rich, "count": len(r.Fans), "isfans": isfans, "pos": i + 1, "cool_holiday": 0})
	}
	s.SendExt("TREE_PTO.S_TREE_RANK", xym.Msg{"rich": p.tree().Rich, "list": list})
}

func init() {
	Handle("TREE_PTO.C_LOOK_TREE", func(s *Session, p *Player, m xym.Msg) { s.treeMsg(p) })
	Handle("TREE_PTO.C_APPLY_HELPER", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("TREE_PTO.S_HELPER_LIST", xym.Msg{"list": []xym.Msg{}})
	})
	Handle("TREE_PTO.C_PLANT_TREE", onPlantTree)
	Handle("TREE_PTO.C_WATERING_TREE", onWaterTree)
	Handle("TREE_PTO.C_FIGHT_BUG", onTreeBug)
	Handle("TREE_PTO.C_GET_GOLD", onTreeGold)
	Handle("TREE_PTO.C_TREE_RANK", func(s *Session, p *Player, m xym.Msg) { s.treeRankMsg(p) })
	Handle("TREE_PTO.C_LIKE_TREE", onLikeTree)
	Handle("TREE_PTO.C_THROW_GOLD", onShakeTree)
	Handle("TREE_PTO.C_THX_FRIEND", func(s *Session, p *Player, m xym.Msg) { s.retMsg("还没有护树的好友哦~") })
	Handle("TREE_PTO.C_OPEN_PUSH", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("TREE_PTO.S_SEND_PUSH_LEVEL", xym.Msg{"level": p.tree().Push})
	})
	Handle("TREE_PTO.C_SET_PUSH", func(s *Session, p *Player, m xym.Msg) {
		p.tree().Push = int(m["level"].(int64))
		p.saveLog(s)
		s.SendExt("TREE_PTO.S_SEND_PUSH_LEVEL", xym.Msg{"level": p.tree().Push})
	})
}

// onPlantTree：每天一棵。
func onPlantTree(s *Session, p *Player, m xym.Msg) {
	t := p.tree()
	switch { // 拒绝时官方只回 S_RET_MSG（抓包）
	case t.PlantOn == today():
		s.retMsg("今天已经种过一棵摇钱树了，明天再来吧。")
		return
	case t.State != treeNone:
		s.retMsg("摇钱树还没有收获。")
		return
	}
	rng := s.actRng()
	t.State, t.Level, t.Planted, t.PlantOn = treeGrow, 1, time.Now().Unix(), today()
	t.Bug = 0
	if rng.IntN(2) == 0 {
		t.Bug = 1 + rng.IntN(2)
	}
	t.Nums++
	p.track(evTree, 1)
	p.saveLog(s)
	s.treeMsg(p)
}

// onWaterTree：消耗甘露把摇钱树升一级（生长期剩余超过 4 小时、没有妖虱）。
func onWaterTree(s *Session, p *Player, m xym.Msg) {
	t := p.tree()
	switch {
	case t.State != treeGrow || t.left() <= int64(treeWater/time.Second) || t.Bug > 0 || t.Level >= treeMaxLv:
		s.retMsg("现在不能培养摇钱树。")
	default:
		need := treeWaterNeed[t.Level+1]
		if p.itemCount(treeDew) < need {
			s.retMsg(fmt.Sprintf("甘露不足：需要 %d 个。", need))
			break
		}
		changed, removed := p.takeItems(treeDew, need)
		t.Level++
		p.saveLog(s)
		s.sendDelta(p, &bagDelta{items: changed, removed: removed})
	}
	s.treeMsg(p)
}

// treeBugFor 是妖虱的阵容（测试换成极弱的）。
var treeBugFor = func(grade int) []*battle.Fighter {
	return mijingEnemy(min(mijingDiffs, max(1, grade/10)))
}

// onTreeBug：和妖虱战斗，赢了少一只（S_FIGHT_WIN 让界面刷新）。
func onTreeBug(s *Session, p *Player, m xym.Msg) {
	t := p.tree()
	if t.Bug <= 0 || int32(m["usernum"].(int64)) != s.role.UID {
		s.retMsg("树上没有妖虱。")
		s.treeMsg(p)
		return
	}
	team1, _ := p.dyTeam(nil)
	team2 := treeBugFor(s.role.Grade)
	if len(team1) == 0 || len(team2) == 0 {
		s.retMsg("阵容为空，请先上阵。")
		return
	}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), 0}, battle.Formation{}, s.actRng())
	s.fightRecord(strconv.FormatInt(recordSeq.Add(1), 10), "妖虱", s.role.Grade, r1, r2, res.Record)
	if res.Win {
		t.Bug--
		p.saveLog(s)
		s.SendExt("TREE_PTO.S_FIGHT_WIN", nil)
	}
	s.treeMsg(p)
}

// onTreeGold：收获元宝，树回到 1 级未种。
func onTreeGold(s *Session, p *Player, m xym.Msg) {
	t := p.tree()
	gold := t.harvest()
	if t.State != treeRipe {
		s.retMsg("摇钱树还没有成熟。")
		s.treeMsg(p)
		return
	}
	t.State, t.Level, t.Bug = treeNone, 1, 0
	t.Rich += int(gold)
	p.saveLog(s)
	s.roleUpdate(func(ro *Role) bool { ro.Gold += gold; return true }, "cash", "gold", "soulexp", "grade", "exp", "next_exp")
	r := rewardMsg(1, nil)
	r["gold"] = gold
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
	s.treeMsg(p)
	s.treeRegister(p)
}

// onLikeTree：驻足观赏第 rankpos 名的树（每天一棵）。
func onLikeTree(s *Session, p *Player, m xym.Msg) {
	t := p.tree()
	pos := int(m["rankpos"].(int64))
	s.treeRegister(p) // 确保林子已载入
	rank := treeRank()
	switch {
	case t.LikeOn == today():
		s.retMsg("今天已经驻足观赏过一棵摇钱树了。")
	case pos < 1 || pos > len(rank) || rank[pos-1].UID == s.role.UID:
		s.retMsg("这棵摇钱树已经不在林中了。")
	default:
		treeWood.Lock()
		if r := treeWood.rows[rank[pos-1].UID]; r != nil {
			r.Fans[s.role.UID] = true
			treeWoodSave()
		}
		treeWood.Unlock()
		t.LikeOn = today()
		p.saveLog(s)
	}
	s.treeRankMsg(p)
}

// onShakeTree：树主摇落元宝，树下观赏的人各得 2 元宝（每天一次），之后观赏者清空。
func onShakeTree(s *Session, p *Player, m xym.Msg) {
	t := p.tree()
	if t.ShakeOn == today() {
		s.retMsg("今天已经摇落过元宝了。")
		return
	}
	s.treeRegister(p) // 确保林子已载入
	treeWood.Lock()
	var fans []int32
	if r := treeWood.rows[s.role.UID]; r != nil {
		for uid := range r.Fans {
			fans = append(fans, uid)
		}
		r.Fans = map[int32]bool{}
		treeWoodSave()
	}
	treeWood.Unlock()
	if len(fans) == 0 {
		s.retMsg("树下还没有观赏的人。")
		return
	}
	for _, uid := range fans {
		if _, err := s.srv.roles.Update(uid, func(ro *Role) bool { ro.Gold += treeShake; return true }); err != nil {
			s.logErr("shake tree", err)
		}
	}
	t.ShakeOn = today()
	p.saveLog(s)
	s.retMsg(fmt.Sprintf("摇落元宝成功，树下的 %d 位观赏者各获得 %d 元宝。", len(fans), treeShake))
	s.treeRankMsg(p)
}
