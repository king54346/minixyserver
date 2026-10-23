package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 任务（主界面「任务」→ 日常任务 / 主线任务 / 支线任务，logic/daily.lua、view/DailyView.lua）。
//
// 日常任务（活跃度）：ETC_PTO.C_APPLY_DAILY → S_SEND_DAILY{active_point, is_new_style 1, reward 已领宝箱数,
// list[type, state, desc, current, max, add], newbie_task_list}。每项玩法今天做了 current 次（上限 max），每次给
// add 点活跃度。抓包（session-02）给出 14 项的 max / add（下表）。活跃度满 25/50/75/100/125 各领一个宝箱：
// C_GET_ACTIVE_REWARD（领下一个），宝箱内容照客户端 DailyView.DailyRewardDatas；C_OPEN_ACTIVE_REWARD{goal}
// 是预览（客户端本地有数据，不必回）。
//
// 主线任务：S_SEND_DAILY.newbie_task_list[type 1, taskid, state 1 进行中 / 2 可领, proc "当前/目标"]，任务表是客户端
// auto/newbietask_new.lua（gamedata.NewbieTasks：名字 + 奖励），完成条件在服务端——这里按任务名判定（下面 newbieRules）。
// 同一类任务排成一条链（关卡 30xxx、精英关卡 31xxx、32xxx 每 10 个号一组、10xxx 各自一条），每条只显示最前面没领的那个；
// 领奖 C_GET_NEWBIE_TASK_REWARD{taskid}（客户端随后发 C_OPEN_MAIN_TASK）。
//
// 支线任务：C_OPEN_MAIN_TASK → S_OPEN_MAIN_TASK{list}；抓包里官方也是空表，照发空表。
//
// 估计 / 未做：首充的进度恒为 0；星战按报名、天梯按每场挑战计；「获得N个关卡的3星评价」按成就挑战的关卡星数（gate_star.go）；
// 关卡活跃按挑战次数（含扫荡）算。

// 活跃度事件（Player.track）。
const (
	evMijing      = "mijing"
	evWish        = "wish"
	evShilian     = "shilian"
	evGate        = "gate"
	evDangyao     = "dangyao"
	evDuobao      = "duobao"
	evDuobaoWin   = "duobao_win"
	evFriendFight = "friend_fight"
	evRecruit     = "recruit"
	evJoke        = "joke"
	evTree        = "tree"
	evFeisheng    = "feisheng"
	evLundao      = "lundao"
	evDongfu      = "dongfu"
	evStarwar     = "starwar"
	evTianti      = "tianti"
	evJiuyou      = "jiuyou"
)

type dailyItem struct {
	typ      int
	desc     string
	max, add int
	event    string
}

// dailyItems 照抓包 S_SEND_DAILY 的顺序与数值；event 为空的玩法本服没有，进度恒为 0。
var dailyItems = []dailyItem{
	{2, "秘境", 3, 5, evMijing},
	{3, "缘魂", 2, 5, evWish},
	{5, "试炼", 3, 5, evShilian},
	{6, "关卡", 10, 2, evGate},
	{7, "荡妖", 10, 3, evDangyao},
	{8, "论道", 10, 2, evLundao},
	{9, "夺宝", 10, 2, evDuobao},
	{14, "招护法", 5, 2, evRecruit},
	{15, "星战", 1, 10, evStarwar},
	{16, "解忧", 1, 5, evJoke},
	{17, "天梯", 10, 1, evTianti},
	{18, "摇钱树", 1, 1, evTree},
	{11, "好友切磋", 3, 2, evFriendFight},
	{19, "洞府事务", 3, 2, evDongfu},
}

// activeChests：活跃度宝箱（客户端 DailyView.DailyRewardDatas）：25/50/75/100/125。
var activeChests = []struct {
	items      [][2]int // {道具, 数量}
	cash, gold int32
}{
	{[][2]int{{10006, 3}}, 10000, 0},
	{[][2]int{{10004, 2}}, 20000, 0},
	{[][2]int{{10002, 1}}, 30000, 0},
	{[][2]int{{10114, 1}}, 50000, 0},
	{[][2]int{{10303, 1}, {11486, 60}}, 80000, 50 + activeFullGold},
}

// activeFullGold：每天活跃度满（125，领最后一个宝箱）额外送的元宝（用户要求；客户端预览由 hotfix-local/04 同步）。
const activeFullGold = 100000

const activeStep = 25

// TaskState 是任务存档（Player.Tasks）。
type TaskState struct {
	Counters map[string]int `json:"counters,omitempty"` // 累计（主线用）
	Day      string         `json:"day"`
	Today    map[string]int `json:"today,omitempty"` // 今天（活跃度用）
	Chests   int            `json:"chests"`          // 今天已领的活跃宝箱
	Done     map[int32]bool `json:"done,omitempty"`  // 已领奖的主线任务
}

func (p *Player) tasks() *TaskState {
	if p.Tasks == nil {
		p.Tasks = &TaskState{}
	}
	t := p.Tasks
	if t.Counters == nil {
		t.Counters = map[string]int{}
	}
	if t.Done == nil {
		t.Done = map[int32]bool{}
	}
	if d := today(); t.Day != d {
		t.Day, t.Today, t.Chests = d, map[string]int{}, 0
	}
	if t.Today == nil {
		t.Today = map[string]int{}
	}
	return t
}

// track 记一次玩法：累计和今日各 +n。
func (p *Player) track(ev string, n int) {
	if n <= 0 {
		return
	}
	t := p.tasks()
	t.Counters[ev] += n
	t.Today[ev] += n
}

// trackMax 把累计计数 key 提到至少 n（「完成过某图」这类只要做到一次的）。
func (p *Player) trackMax(key string, n int) {
	t := p.tasks()
	t.Counters[key] = max(t.Counters[key], n)
}

// dyMapKey：荡妖完成一轮时记「完成第 N 图普通 / 精英妖窟」（特殊妖窟号 > 100 记为精英）。
func dyMapKey(stage int) string {
	if stage > 100 {
		return fmt.Sprintf("dy_elite_%d", stage-100)
	}
	return fmt.Sprintf("dy_normal_%d", stage)
}

func (p *Player) activePoint() int {
	t := p.tasks()
	n := 0
	for _, it := range dailyItems {
		if it.event != "" {
			n += min(t.Today[it.event], it.max) * it.add
		}
	}
	return n
}

func (p *Player) dailyMsg() xym.Msg {
	t := p.tasks()
	list := []xym.Msg{}
	for _, it := range dailyItems {
		cur := 0
		if it.event != "" {
			cur = min(t.Today[it.event], it.max)
		}
		list = append(list, xym.Msg{"type": it.typ, "state": 1, "desc": it.desc, "current": cur, "max": it.max, "add": it.add})
	}
	return xym.Msg{"active_point": p.activePoint(), "is_new_style": 1, "reward": t.Chests, "list": list,
		"newbie_task_list": p.newbieList()}
}

// ---- 主线任务 ----

// newbieRule：按任务名解析出的完成条件；progress 返回（当前, 目标）。
type newbieRule struct {
	re       *regexp.Regexp
	progress func(p *Player, m []int) (int, int)
}

func atoiAll(ss []string) []int {
	out := make([]int, 0, len(ss))
	for _, s := range ss {
		n, _ := strconv.Atoi(s)
		out = append(out, n)
	}
	return out
}

// gateTaskProgress：任务 X-Y（task X*100+Y）已通关的关卡数 / 总关卡数。
func (p *Player) gateTaskProgress(typ, task int) (int, int) {
	gates := gamedata.TaskGates[task]
	n := 0
	for _, gid := range gates {
		if p.Prog[typ] != nil && p.Prog[typ].Gates[gid] > 0 {
			n++
		}
	}
	return n, max(len(gates), 1)
}

func (p *Player) countWorn(star int, suits bool) int {
	n := 0
	for fp := 1; fp <= lineupSlots; fp++ {
		sl := p.fightSlot(fp)
		if sl == nil {
			continue
		}
		if suits {
			for _, pos := range sl.Suit {
				if su := p.suitByPos(pos); su != nil && suitStar(su.Type) >= star {
					n++
				}
			}
		} else {
			for _, pos := range sl.Book {
				if b := p.bookByPos(pos); b != nil && magicStar(b.Type) >= star {
					n++
				}
			}
		}
	}
	return n
}

func (p *Player) counter(key string) int { return p.tasks().Counters[key] }

func countFn(key string) func(p *Player, m []int) (int, int) {
	return func(p *Player, m []int) (int, int) { return p.counter(key), m[0] }
}

var newbieRules = []newbieRule{
	{regexp.MustCompile(`^通关关卡(\d+)-(\d+)$`), func(p *Player, m []int) (int, int) { return p.gateTaskProgress(0, m[0]*100+m[1]) }},
	{regexp.MustCompile(`^通关精英关卡(\d+)-(\d+)$`), func(p *Player, m []int) (int, int) { return p.gateTaskProgress(1, m[0]*100+m[1]) }},
	{regexp.MustCompile(`^完成(\d+)图普通妖窟$`), func(p *Player, m []int) (int, int) { return min(p.counter(fmt.Sprintf("dy_normal_%d", m[0])), 1), 1 }},
	{regexp.MustCompile(`^完成(\d+)图精英妖窟$`), func(p *Player, m []int) (int, int) { return min(p.counter(fmt.Sprintf("dy_elite_%d", m[0])), 1), 1 }},
	{regexp.MustCompile(`^任意(\d+)个护法达到境界(\d+)$`), func(p *Player, m []int) (int, int) {
		n := 0
		for _, pet := range p.Pets {
			if pet.SoulGrade >= m[1] {
				n++
			}
		}
		return n, m[0]
	}},
	{regexp.MustCompile(`^穿戴(?:任意)?(\d+)件(\d+)星或以上装备$`), func(p *Player, m []int) (int, int) { return p.countWorn(m[1], true), m[0] }},
	{regexp.MustCompile(`^穿戴任意(\d+)件(\d+)星或以上法术$`), func(p *Player, m []int) (int, int) { return p.countWorn(m[1], false), m[0] }},
	{regexp.MustCompile(`^(\d+)件装备强化达到(\d+)级$`), func(p *Player, m []int) (int, int) {
		n := 0
		for _, su := range p.Suits {
			if su.Grade >= m[1] {
				n++
			}
		}
		return n, m[0]
	}},
	{regexp.MustCompile(`^任意(\d+)件法术强化等级达到(\d+)级$`), func(p *Player, m []int) (int, int) {
		n := 0
		for _, b := range p.Books {
			if b.Grade >= m[1] {
				n++
			}
		}
		return n, m[0]
	}},
	{regexp.MustCompile(`^(\d+)个护法的天赋法术达到(\d+)级$`), func(p *Player, m []int) (int, int) {
		n := 0
		for _, pet := range p.Pets {
			if pet.magicLv() >= m[1] {
				n++
			}
		}
		return n, m[0]
	}},
	{regexp.MustCompile(`^在宝卷获取战中获得(\d+)次胜利$`), countFn(evDuobaoWin)},
	{regexp.MustCompile(`^完成(\d+)次任意难度秘境$`), countFn(evMijing)},
	{regexp.MustCompile(`^完成(\d+)次解忧$`), countFn(evJoke)},
	{regexp.MustCompile(`^完成(\d+)次种树$`), countFn(evTree)},
	{regexp.MustCompile(`^缘魂达到(\d+)次$`), countFn(evWish)},
	{regexp.MustCompile(`^完成(\d+)次飞升$`), countFn(evFeisheng)},
	{regexp.MustCompile(`^试炼达到第(\d+)关$`), func(p *Player, m []int) (int, int) {
		best := 0
		if p.Shilian != nil {
			best = p.Shilian.Best
		}
		return min(best, m[0]), m[0]
	}},
	{regexp.MustCompile(`^将战位(\d+)护法设队长$`), func(p *Player, m []int) (int, int) {
		if lu := p.lineup(p.FightNo); lu != nil && lu.Leader == m[0] {
			return 1, 1
		}
		return 0, 1
	}},
	{regexp.MustCompile(`^首次开启妖窟！$`), func(p *Player, m []int) (int, int) { return min(p.counter(evDangyao), 1), 1 }},
	{regexp.MustCompile(`^从关卡\d+-\d+【[^】]*】获得(.+)魂魄$`), nil}, // 特判，见 progressOf
	// 本服没有的玩法：进度恒为 0
	{regexp.MustCompile(`^在论道中进行(\d+)`), func(p *Player, m []int) (int, int) { return min(p.counter(evLundao), m[0]), m[0] }},
	{regexp.MustCompile(`^天梯累计挑战(\d+)`), func(p *Player, m []int) (int, int) { return min(p.counter(evTianti), m[0]), m[0] }},
	{regexp.MustCompile(`^报名(\d+)次星战`), func(p *Player, m []int) (int, int) { return min(p.counter(evStarwar), m[0]), m[0] }},
	{regexp.MustCompile(`^获得了(\d+)个关卡的(\d+)星评价$`), func(p *Player, m []int) (int, int) { // 成就挑战（gate_star.go）
		n := 0
		for _, pr := range p.Prog {
			for gid := range pr.Gates {
				if pr.gateStar(gid) >= m[1] {
					n++
				}
			}
		}
		return min(n, m[0]), m[0]
	}},
}

// progressOf 是任务的（当前, 目标）；不认识的任务按 0/1。
func (p *Player) progressOf(task gamedata.NewbieTask) (int, int) {
	for _, r := range newbieRules {
		sm := r.re.FindStringSubmatch(task.Name)
		if sm == nil {
			continue
		}
		if r.progress == nil { // 获得某护法魂魄：有该护法或它的魂魄即完成
			for t, pi := range gamedata.Pets {
				if pi.Name == sm[1] && (p.Souls[t] > 0 || p.petByType(t) != nil) {
					return 1, 1
				}
			}
			return 0, 1
		}
		cur, need := r.progress(p, atoiAll(sm[1:]))
		return min(cur, need), need
	}
	return 0, 1
}

// newbieChain：任务所在的链（同链按 id 顺序做完一个出下一个）。
func newbieChain(id int32) int32 {
	switch {
	case id >= 30000 && id < 32000:
		return id / 1000 // 30 普通关卡、31 精英关卡
	case id >= 32000:
		return id / 10 // 32xx 每 10 个号一组
	}
	return id // 10xxx 各自一条
}

// activeNewbie 是每条链当前的任务（最前面没领奖的）。
func (p *Player) activeNewbie() []gamedata.NewbieTask {
	t := p.tasks()
	seen := map[int32]bool{}
	var out []gamedata.NewbieTask
	for _, task := range gamedata.NewbieTasks {
		ch := newbieChain(task.ID)
		if seen[ch] || t.Done[task.ID] {
			continue
		}
		seen[ch] = true
		out = append(out, task)
	}
	return out
}

func (p *Player) newbieList() []xym.Msg {
	list := []xym.Msg{}
	for _, task := range p.activeNewbie() {
		cur, need := p.progressOf(task)
		state := 1
		if cur >= need {
			state = 2
		}
		list = append(list, xym.Msg{"type": 1, "taskid": task.ID, "state": state, "proc": fmt.Sprintf("%d/%d", cur, need)})
	}
	return list
}

func init() {
	Handle("ETC_PTO.C_APPLY_DAILY", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("ETC_PTO.S_SEND_DAILY", p.dailyMsg())
	})
	Handle("ETC_PTO.C_OPEN_MAIN_TASK", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("ETC_PTO.S_OPEN_MAIN_TASK", xym.Msg{"list": []xym.Msg{}}) // 支线：官方抓包也是空表
	})
	Handle("ETC_PTO.C_GET_ACTIVE_REWARD", onActiveReward)
	Handle("ETC_PTO.C_GET_NEWBIE_TASK_REWARD", onNewbieReward)
}

func (s *Session) grantTaskReward(p *Player, items [][2]int, drops []string, cash, gold, soul int32) {
	d := &bagDelta{}
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(p.UID)))
	for _, it := range items {
		d.items = append(d.items, p.addItem(it[0], int32(it[1]))...)
		d.gains = append(d.gains, gain{rtItem, it[0], int32(it[1])})
	}
	if len(drops) > 0 {
		cash += int32(p.grantReward(&gamedata.RewardInfo{Drops: drops}, rng, d))
	}
	if cash > 0 || gold > 0 || soul > 0 {
		s.roleUpdate(func(ro *Role) bool { ro.Cash += cash; ro.Gold += gold; ro.SoulExp += soul; return true },
			"cash", "gold", "soulexp")
	}
	p.saveLog(s)
	list := s.sendDelta(p, d)
	for _, c := range [][2]int32{{mailGold, gold}, {mailCash, cash}, {mailSoul, soul}} { // 元宝 / 银两 / 魂值的图标
		if c[1] > 0 {
			list = append(list, xym.Msg{"rtype": rtItem, "type": c[0], "count": c[1]})
		}
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, list))
}

func onActiveReward(s *Session, p *Player, m xym.Msg) {
	t := p.tasks()
	if t.Chests >= len(activeChests) || p.activePoint() < (t.Chests+1)*activeStep {
		s.retMsg("活跃度还不够领取下一个宝箱。")
		s.SendExt("ETC_PTO.S_SEND_DAILY", p.dailyMsg())
		return
	}
	c := activeChests[t.Chests]
	t.Chests++
	s.grantTaskReward(p, c.items, nil, c.cash, c.gold, 0)
	s.SendExt("ETC_PTO.S_SEND_DAILY", p.dailyMsg())
}

func onNewbieReward(s *Session, p *Player, m xym.Msg) {
	id := int32(m["taskid"].(int64))
	for _, task := range p.activeNewbie() {
		if task.ID != id {
			continue
		}
		if cur, need := p.progressOf(task); cur < need {
			s.retMsg("任务还没有完成。")
			break
		}
		p.tasks().Done[id] = true
		var drops []string
		for _, d := range task.Drops {
			if strings.TrimSpace(d) != "" {
				drops = append(drops, d)
			}
		}
		s.grantTaskReward(p, nil, drops, task.Cash, task.Gold, task.Soul)
		break
	}
	s.SendExt("ETC_PTO.S_SEND_DAILY", p.dailyMsg())
}
