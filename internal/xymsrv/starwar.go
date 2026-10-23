package xymsrv

import (
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strconv"
	"sync"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 星战（STARWAR_PTO）：每天报名、20:00 开赛的单败淘汰赛。周一、三、五是普通星战（3 / 4 / 5 星场 × 5V5 / 8V8），
// 周二、四、六、日换成超级星战（统一 8V8、统一属性）。
//
// 依据：
//   - 抓包（session-01/02）：C_GET_TODAY_RACE_AND_PETS → S_SEND_TODAY_RACE_AND_PETS（全 0：不限种族、无指定护法）；
//     C_OPEN_STARWAR → S_OPEN_STARWAR{war_type 0, ctype 5, ftype 8（已报名 5 星 8V8）, reward3/4/5 各星场奖池,
//     lefttime 距开赛秒数}；C_APPLY_USER_COUNT{ctype} → S_SEND_USER_COUNT{count5, count8}；
//     C_APPLY_PET_LIST → S_OPEN_PET_LIST{leader_pos, fighter[pos, grade, icon, hp, ap]}（报名时的护法数据）。
//   - logic/starwar_mgr.lua：报名费 3 / 5 / 8 万银两（JoinCost）；5V5 = 前排 3 + 替补 2（站位 1–3、6–7），
//     8V8 = 前排 5 + 替补 3；开赛时间 BEGIN_ENTER_TIME_H = 20；reloadWarList 按 icon + grade 认回自己的卡，
//     对不上就提示「你的星战护法已经升级…不点击更改按钮可以保持原有数据」——所以报名时存的是快照。
//     超级星战 SuperStarWarType = 10：8 人必须上满、有队长位（C_SUPER_SIGN_USER{ctype 10, ftype 8, leader_pos}）；
//     属性照 evalPetAttackAndHP：100 级基础属性 + 有缘武器 706 攻 / 有缘衣服 2824 血（40 级 5 星）+ 30 攻 / 120 血，
//     再乘全部缘分第 1 档的连乘倍率（保留两位小数）；天生法术 6 级，自动带第一本有缘法术书（6 级、精修 3）。
//   - view/StarWarView.lua：S_OPEN_FIGHT{round 第几轮, fail 1 已淘汰, left 下一轮倒计时, leftfighter 剩余人数}；
//     S_GAME_OFF（普通星战结束）/ S_SUPER_GAME_OFF{total_gold, total_user}；S_SEND_RANK.list 里 round 2 是决赛、
//     3 / 4 是两场半决赛（冠军、四强名单）；C_APPLY_RANK_RECORD{usernum, rank} 看该名次那场的录像。
//   - 规则（玩家公告）：超级星战于周二、四、六、日替换星战；8V8、100 级、缘分全激活、队长技能有效、40 级 5 星
//     标准装备（无战靴、无宝石套装）、丹药 30、1 本缘法术、法术 6 级精修 3 级、神通与境界取玩家数据。
//
// 估计（没有比赛过程的抓包）：签位 5V5 16 人、8V8 64 人、超级星战 64 人，报名不满由假选手补齐；
// 20:00 起每 2 分钟打一轮（每轮开打时按当时的阵容算），决赛后再留一轮的时间展示结果；
// 奖池 = 报名费 × 签位数（超级星战每个签位 20 元宝），冠军 40%、亚军 20%、四强各 10%、八强各 5%，比赛结束邮件发放；
// 普通星战不带队长技、只算护法之间的缘分；超级星战不收报名费。

const (
	swStartHour  = 20 // starwar_mgr.BEGIN_ENTER_TIME_H
	swRoundSecs  = 120
	swSuper      = 10 // starwar_mgr.SuperStarWarType
	swSuperGold  = 20 // 超级星战每个签位进奖池的元宝
	swSuperSize  = 64
	swSuperGrade = 100
)

var (
	swFee  = map[int]int32{3: 30000, 4: 50000, 5: 80000} // starwar_mgr.JoinCost
	swSize = map[int]int{5: 16, 8: 64}
	// swShare：名次 → 奖池分成 %（1 冠军、2 亚军、4 四强、8 八强）。
	swShare = map[int]int{1: 40, 2: 20, 4: 10, 8: 5}
	// swPos：5V5 / 8V8 能放卡的站位（StarWarTeamView：前排 getOnFightCount、替补从 6 号位起 getOnBenchCount）。
	swPos = map[int][]int{5: {1, 2, 3, 6, 7}, 8: {1, 2, 3, 4, 5, 6, 7, 8}}
)

// swNow 是当前时间（测试可改）。
var swNow = time.Now

// swSuperDay：周二、四、六、日是超级星战。
func swSuperDay(t time.Time) bool {
	switch t.Weekday() {
	case time.Tuesday, time.Thursday, time.Saturday, time.Sunday:
		return true
	}
	return false
}

// swSlot 是报名阵容里的一张卡（S_OPEN_PET_LIST 用）。
type swSlot struct {
	Pos    int   `json:"pos"`
	NpcNum int32 `json:"npc"`
	Type   int32 `json:"type"`
	Grade  int   `json:"grade"` // 报名时护法的等级（客户端用 icon + grade 认回自己的卡）
}

// swEntry 是一位参赛者（UID 0 = 假选手）。
type swEntry struct {
	UID    int32             `json:"uid"`
	Name   string            `json:"name"`
	Grade  int               `json:"grade"`
	Icon   int32             `json:"icon"`
	Slots  []swSlot          `json:"slots,omitempty"`
	Team   []*battle.Fighter `json:"team"`             // 站位 1–8
	Leader int               `json:"leader,omitempty"` // 队长位（超级星战）
	Skill  int32             `json:"skill,omitempty"`  // 队长技
}

func (e *swEntry) power() int {
	n := 0
	for _, f := range e.Team {
		n += int(f.Att)*4 + int(f.HP)
	}
	return n
}

// swMatch 是一场比赛（A、B 是参赛者下标）。有真人参加的场次存录像。
type swMatch struct {
	A   int               `json:"a"`
	B   int               `json:"b"`
	Win int               `json:"win"`
	Rid string            `json:"rid,omitempty"`
	T1  []*battle.Fighter `json:"t1,omitempty"` // 录像里的 team1（A 方，站位 1 起）
	T2  []*battle.Fighter `json:"t2,omitempty"` // team2（B 方，站位 101 起）
	Rec []byte            `json:"rec,omitempty"`
}

func (m *swMatch) loser() int {
	if m.Win == m.A {
		return m.B
	}
	return m.A
}

// swBracket 是一个赛区（星级 × 人数）的签表。
type swBracket struct {
	CType   int          `json:"ctype"`
	FType   int          `json:"ftype"`
	Entries []*swEntry   `json:"entries"`
	Rounds  [][]*swMatch `json:"rounds,omitempty"`
	Paid    bool         `json:"paid,omitempty"`
}

func (b *swBracket) total() int {
	n := 0
	for len(b.Entries) > 1<<n {
		n++
	}
	return n
}

// alive 是打完 done 轮后还在的参赛者（按签位顺序）。
func (b *swBracket) alive(done int) []int {
	if done == 0 {
		out := make([]int, len(b.Entries))
		for i := range out {
			out[i] = i
		}
		return out
	}
	var out []int
	for _, m := range b.Rounds[done-1] {
		out = append(out, m.Win)
	}
	return out
}

func (b *swBracket) pool() int32 {
	if b.CType == swSuper {
		return int32(swSuperGold * len(b.Entries))
	}
	return swFee[b.CType] * int32(len(b.Entries))
}

// place 是参赛者 i 的名次（1 冠军、2 亚军、4 四强、8 八强…；比赛没打完时按目前的位置）。
func (b *swBracket) place(i int) int {
	for r, ms := range b.Rounds {
		for _, m := range ms {
			if m.loser() == i && m.A != m.B {
				return 1 << (b.total() - r)
			}
		}
	}
	return 1
}

// indexOf 是真人 uid 在签表里的下标（不在为 -1）。
func (b *swBracket) indexOf(uid int32) int {
	for i, e := range b.Entries {
		if e.UID == uid && uid != 0 {
			return i
		}
	}
	return -1
}

// lastMatch 是参赛者 i 最近打的一场（没有返回 nil）。
func (b *swBracket) lastMatch(i int) *swMatch {
	for r := len(b.Rounds) - 1; r >= 0; r-- {
		for _, m := range b.Rounds[r] {
			if (m.A == i || m.B == i) && m.A != m.B {
				return m
			}
		}
	}
	return nil
}

// swSign 是一位玩家今天的报名。
type swSign struct {
	CType int      `json:"ctype"`
	FType int      `json:"ftype"`
	Entry *swEntry `json:"entry"`
}

func swKey(ctype, ftype int) string { return strconv.Itoa(ctype) + ":" + strconv.Itoa(ftype) }

// Starwar 是全服星战（存档位 starwarUID）。
type Starwar struct {
	mu sync.Mutex
	st store.Store

	Day      string                `json:"day"`
	Start    int64                 `json:"start"` // 今天开赛时间（Unix 秒；管理接口可以提前）
	Super    bool                  `json:"super"`
	Signs    map[int32]*swSign     `json:"signs,omitempty"`
	Brackets map[string]*swBracket `json:"brackets,omitempty"`

	played bool // 本次推进打了比赛 / 发了奖（要存档）
}

// swMail 是比赛结束后要发的奖励邮件。
type swMail struct {
	to   int32
	spec MailSpec
}

func openStarwar(st store.Store) *Starwar {
	w := &Starwar{st: st}
	loadGlobal(st, starwarUID, w, "starwar")
	return w
}

// do 持锁执行 fn（先滚动到今天、推进比赛），fn 返回 true 时存档；返回比赛结束要发的邮件。
func (w *Starwar) do(fn func() bool) []swMail {
	w.mu.Lock()
	defer w.mu.Unlock()
	mails, dirty := w.advance(swNow())
	if fn != nil && fn() {
		dirty = true
	}
	if dirty {
		saveGlobal(w.st, starwarUID, w, "starwar")
	}
	return mails
}

func swStartOf(now time.Time) int64 {
	return time.Date(now.Year(), now.Month(), now.Day(), swStartHour, 0, 0, 0, now.Location()).Unix()
}

// end 是今天比赛（含展示决赛结果的一轮）结束的时间。
func (w *Starwar) end() int64 {
	r := 0
	for _, b := range w.Brackets {
		r = max(r, b.total())
	}
	if w.Brackets == nil {
		r = 6 // 还没建签表：默认签位 64 人
	}
	return w.Start + int64(r)*swRoundSecs
}

// phase：0 报名中、1 比赛中、2 已结束。
func (w *Starwar) phase(now int64) int {
	switch {
	case now < w.Start:
		return 0
	case now < w.end():
		return 1
	}
	return 2
}

// advance 滚动到今天（昨天没结算的先结算）、到点建签表、打该打的轮次、结束时发奖。调用方持锁。
func (w *Starwar) advance(now time.Time) ([]swMail, bool) {
	var mails []swMail
	dirty := false
	day := now.Format("2006-01-02")
	if w.Day != day {
		if w.Day != "" {
			mails = append(mails, w.finish(1<<62)...) // 昨天的打完、发奖
		}
		w.Day, w.Start, w.Super, w.Signs, w.Brackets = day, swStartOf(now), swSuperDay(now), map[int32]*swSign{}, nil
		dirty = true
	}
	if w.Signs == nil {
		w.Signs = map[int32]*swSign{}
	}
	if now.Unix() >= w.Start {
		if w.Brackets == nil {
			w.build()
			dirty = true
		}
		m := w.finish(now.Unix())
		if len(m) > 0 || w.played {
			dirty = true
		}
		w.played = false
		mails = append(mails, m...)
	}
	return mails, dirty
}

// build 建今天各赛区的签表：报名的真人 + 假选手补满签位，打乱顺序。
func (w *Starwar) build() {
	w.Brackets = map[string]*swBracket{}
	keys := [][2]int{{swSuper, 8}}
	if !w.Super {
		keys = [][2]int{{3, 5}, {3, 8}, {4, 5}, {4, 8}, {5, 5}, {5, 8}}
	}
	seed := uint64(w.Start)
	for _, k := range keys {
		b := &swBracket{CType: k[0], FType: k[1]}
		uids := make([]int32, 0)
		for uid, sg := range w.Signs {
			if sg.CType == k[0] && sg.FType == k[1] {
				uids = append(uids, uid)
			}
		}
		slices.Sort(uids)
		for _, uid := range uids {
			b.Entries = append(b.Entries, w.Signs[uid].Entry)
		}
		size := swSize[k[1]]
		if k[0] == swSuper {
			size = swSuperSize
		}
		for size < len(b.Entries) {
			size *= 2
		}
		rng := rand.New(rand.NewPCG(seed, uint64(k[0]*10+k[1])))
		for i := len(b.Entries); i < size; i++ {
			b.Entries = append(b.Entries, swFakeEntry(k[0], k[1], i, rng))
		}
		rng.Shuffle(len(b.Entries), func(i, j int) { b.Entries[i], b.Entries[j] = b.Entries[j], b.Entries[i] })
		w.Brackets[swKey(k[0], k[1])] = b
	}
}

// finish 打完开赛时间到 now 为止该打的轮次（第 r 轮在 Start + (r-1)·2 分钟开打），结束了就发奖。
func (w *Starwar) finish(now int64) []swMail {
	if w.Brackets == nil {
		if w.Start == 0 || now < w.Start {
			return nil
		}
		w.build()
	}
	var mails []swMail
	keys := make([]string, 0, len(w.Brackets))
	for k := range w.Brackets {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		b := w.Brackets[k]
		for len(b.Rounds) < b.total() && now >= w.Start+int64(len(b.Rounds))*swRoundSecs {
			w.playRound(b)
		}
		if !b.Paid && len(b.Rounds) == b.total() && now >= w.end() {
			b.Paid = true
			w.played = true
			mails = append(mails, b.rewards()...)
		}
	}
	return mails
}

// playRound 打 b 的下一轮：上一轮的胜者按顺序两两对阵。
func (w *Starwar) playRound(b *swBracket) {
	r := len(b.Rounds)
	alive := b.alive(r)
	rng := rand.New(rand.NewPCG(uint64(w.Start)+uint64(r), uint64(b.CType*10+b.FType)))
	var ms []*swMatch
	for i := 0; i+1 < len(alive); i += 2 {
		ms = append(ms, w.play(b, alive[i], alive[i+1], rng))
	}
	b.Rounds = append(b.Rounds, ms)
	w.played = true
}

// play 打一场。有真人时用战斗模拟并存录像（真人放 team1）；两个假选手按战力比例随机。
func (w *Starwar) play(b *swBracket, a, c int, rng *rand.Rand) *swMatch {
	ea, ec := b.Entries[a], b.Entries[c]
	if ea.UID == 0 && ec.UID == 0 {
		m := &swMatch{A: a, B: c, Win: c}
		pa, pc := float64(ea.power()), float64(ec.power())
		if rng.Float64()*(pa+pc) < pa {
			m.Win = a
		}
		return m
	}
	if ea.UID == 0 { // 真人放 team1
		a, c, ea, ec = c, a, ec, ea
	}
	t1, t2 := battle.CloneTeam(ea.Team), battle.CloneTeam(ec.Team)
	for _, f := range t2 {
		f.Pos += 100
	}
	m := &swMatch{A: a, B: c, Rid: "starwar:" + strconv.FormatInt(recordSeq.Add(1), 10),
		T1: battle.CloneTeam(t1), T2: battle.CloneTeam(t2)}
	var leaders [2]int32
	if b.CType == swSuper { // 普通星战不带队长技
		leaders = [2]int32{ea.Skill, ec.Skill}
	}
	if len(t1) == 0 || len(t2) == 0 {
		m.Win = a
		if len(t1) == 0 {
			m.Win = c
		}
		return m
	}
	res := battle.SimulateLeaders(t1, t2, leaders, rng)
	m.Rec = res.Record
	m.Win = c
	if res.Win {
		m.Win = a
	}
	return m
}

// rewards 是 b 打完后真人的奖励邮件（冠军、亚军、四强、八强按奖池分成）。
func (b *swBracket) rewards() []swMail {
	var out []swMail
	pool := b.pool()
	for i, e := range b.Entries {
		if e.UID == 0 {
			continue
		}
		pl := b.place(i)
		pct := swShare[pl]
		if pct == 0 {
			continue
		}
		n := pool * int32(pct) / 100
		spec := MailSpec{Title: "星战奖励", Text: fmt.Sprintf("恭喜你在今日%s中获得%s，奖励%s。", b.title(), swPlaceName(pl), swAmount(b, n))}
		if b.CType == swSuper {
			spec.Title, spec.Gold = "超级星战奖励", n
		} else {
			spec.Cash = n
		}
		out = append(out, swMail{to: e.UID, spec: spec})
	}
	return out
}

func (b *swBracket) title() string {
	if b.CType == swSuper {
		return "超级星战"
	}
	return fmt.Sprintf("%d星战%dV%d", b.CType, b.FType, b.FType)
}

func swPlaceName(pl int) string {
	switch pl {
	case 1:
		return "冠军"
	case 2:
		return "亚军"
	}
	return fmt.Sprintf("%d强", pl)
}

func swAmount(b *swBracket, n int32) string {
	if b.CType == swSuper {
		return fmt.Sprintf("%d元宝", n)
	}
	return fmt.Sprintf("%d银两", n)
}

var (
	swNamesA = []rune("天地玄黄宇宙洪荒日月盈昃辰宿列张寒来暑往秋收冬藏")
	swNamesB = []string{"星君", "剑仙", "散仙", "真君", "天师", "道人", "法王", "神将", "灵童", "仙姑", "游侠", "大圣"}
)

// swFakeEntry 生成一位假选手：普通星战按星级上限随机卡（40–80 级），超级星战用 4、5 星卡套统一属性。
func swFakeEntry(ctype, ftype, i int, rng *rand.Rand) *swEntry {
	e := &swEntry{Name: string(swNamesA[rng.IntN(len(swNamesA))]) + string(swNamesA[rng.IntN(len(swNamesA))]) + swNamesB[rng.IntN(len(swNamesB))],
		Grade: 40 + rng.IntN(21), Leader: 1}
	w := [5]int{0, 0, 10, 30, 60}
	switch ctype {
	case 3:
		w = [5]int{10, 30, 60, 0, 0}
	case 4:
		w = [5]int{0, 10, 30, 60, 0}
	case swSuper:
		w = [5]int{0, 0, 0, 30, 70}
	}
	used := map[string]bool{}
	for k, pos := range swPos[ftype] {
		var t int32
		for range 20 {
			t = slPick(randStar(w, rng), rng)
			if pi := gamedata.Pets[t]; pi != nil && !used[pi.Name] && len(pi.Talents[0]) > 0 {
				break
			}
		}
		pi := gamedata.Pets[t]
		if pi == nil {
			continue
		}
		used[pi.Name] = true
		tal := rng.IntN(10)
		var f *battle.Fighter
		if ctype == swSuper {
			var tals []int32
			for _, l := range pi.Talents {
				if len(l) > 0 {
					tals = append(tals, l[min(tal, len(l)-1)])
				}
			}
			f = superFighter(pi, t, pos, tals, nil, tal >= maxTalentLen(pi)-1)
		} else {
			f = petFighter(t, 40+rng.IntN(41), pos, tal)
			f.Pos = pos
		}
		f.ID = int32(700000 + i*10 + k)
		e.Team = append(e.Team, f)
	}
	if len(e.Team) > 0 {
		e.Icon = e.Team[0].Icon
		if ctype == swSuper {
			e.Skill = gamedata.Pets[e.Team[0].Icon].LeaderSkill
		}
	}
	return e
}

// superFighter 是超级星战里卡 pi 在站位 pos 的统一属性（starwar_mgr.evalPetAttackAndHP / evalRelateMagicsData /
// evalRelateSuitsData）：100 级 + 有缘武器 706 攻 / 有缘衣服 2824 血 + 丹药 30 攻 / 120 血，乘全部缘分第 1 档；
// 天生法术 6 级，带第一本有缘法术书（6 级、精修 3）；不穿战靴，先攻取卡牌本身。
func superFighter(pi *gamedata.PetInfo, t int32, pos int, tals []int32, tj []int, full bool) *battle.Fighter {
	att, hp := float64(pi.Att(swSuperGrade)), float64(pi.HP(swSuperGrade))
	ra, rh := 1.0, 1.0
	var weapon, shirt, book int32
	for _, id := range pi.Relate {
		r := gamedata.Relates[id]
		if r == nil {
			continue
		}
		if len(r.Att) > 0 {
			ra *= 1 + r.Att[0]/100
		}
		if len(r.HP) > 0 {
			rh *= 1 + r.HP[0]/100
		}
		if len(r.NEquip) > 0 {
			if si := gamedata.Suits[int(r.NEquip[0])]; si != nil {
				switch si.Kind {
				case "ITK_WEAPON":
					weapon = r.NEquip[0]
				case "ITK_SHIRT":
					shirt = r.NEquip[0]
				}
			}
		}
		if book == 0 && len(r.NMagic) > 0 {
			book = r.NMagic[0]
		}
	}
	var suits [][5]int32
	if weapon != 0 {
		att += 706
		suits = append(suits, [5]int32{1, weapon, 40, 0})
	}
	if shirt != 0 {
		hp += 2824
		suits = append(suits, [5]int32{2, shirt, 40, 0})
	}
	a := int32(float64(int64(att+30)) * floor2(ra))
	h := int32(float64(int64(hp+120)) * floor2(rh))
	f := &battle.Fighter{Pos: pos, Name: pi.Name, Att: a, HP: h, MaxHP: h, Speed: pi.Speed, Icon: t, Race: pi.Race,
		Grade: swSuperGrade, Talents: tals, TalentTJ: tj, Magic: pi.Magic, MagicLv: 6, Suits: suits, SkillFull: full}
	if book != 0 {
		f.Books, f.BookJX, f.BookTJ = [][2]int32{{book, 6}}, []int{3}, []int{0}
	}
	return f
}

// swFighter 是普通星战里自己的一张卡：当前等级的基础属性、只算护法之间的缘分（不带装备、法术书、宝石）。
func (p *Player) swFighter(pet *Pet, pos int, names map[string]bool) *battle.Fighter {
	pi := gamedata.Pets[pet.Type]
	ra, rh := 1.0, 1.0
	for _, id := range pi.Relate {
		r := gamedata.Relates[id]
		if r == nil || len(r.NPet) == 0 {
			continue
		}
		if act := relateActive(r, names, nil, nil); len(act) > 0 && act[0] {
			if len(r.Att) > 0 {
				ra *= 1 + r.Att[0]/100
			}
			if len(r.HP) > 0 {
				rh *= 1 + r.HP[0]/100
			}
		}
	}
	att := int32(float64(pi.Att(pet.Grade)) * ra)
	hp := int32(float64(pi.HP(pet.Grade)) * rh)
	tals, tj := p.activeTalents(pet, pi)
	return &battle.Fighter{ID: pet.NpcNum, Pos: pos, Name: pi.Name, Att: att, HP: hp, MaxHP: hp, Speed: pi.Speed,
		Icon: pet.Type, Race: pi.Race, Grade: pet.Grade, Talents: tals, TalentTJ: tj, Magic: pi.Magic,
		MagicLv: pet.magicLv(), SkillFull: pet.talentsFull(pi)}
}

// swBuildEntry 按报名的 fighter[pos, npcnum] 建快照。ctype 是星级上限（超级星战为 10），出错返回提示。
func (s *Session) swBuildEntry(p *Player, ctype, ftype, leader int, fighters []xym.Msg) (*swEntry, string) {
	allowed := map[int]bool{}
	for _, pos := range swPos[ftype] {
		allowed[pos] = true
	}
	if len(allowed) == 0 {
		return nil, "参赛人数不对。"
	}
	if len(fighters) != ftype {
		return nil, fmt.Sprintf("你的星战护法数量不足%d人！", ftype)
	}
	type pick struct {
		pos int
		pet *Pet
	}
	var picks []pick
	names := map[string]bool{}
	seenPos := map[int]bool{}
	for _, f := range fighters {
		pos := int(f["pos"].(int64))
		pet := p.petByNpc(int32(f["npcnum"].(int64)))
		if !allowed[pos] || seenPos[pos] {
			return nil, "站位不对，请重新布阵。"
		}
		if pet == nil || gamedata.Pets[pet.Type] == nil {
			return nil, "找不到这张护法。"
		}
		pi := gamedata.Pets[pet.Type]
		if ctype != swSuper && pi.Star > ctype {
			return nil, fmt.Sprintf("%d星战只能派%d星及以下的护法。", ctype, ctype)
		}
		if names[pi.Name] {
			return nil, "阵容中含有了不同星级的同名卡，不能上阵！"
		}
		seenPos[pos] = true
		names[pi.Name] = true
		picks = append(picks, pick{pos, pet})
	}
	slices.SortFunc(picks, func(a, b pick) int { return a.pos - b.pos })
	e := &swEntry{UID: s.role.UID, Name: s.role.Name, Grade: s.role.Grade, Icon: int32(s.role.Icon), Leader: 1}
	for _, pk := range picks {
		pi := gamedata.Pets[pk.pet.Type]
		var f *battle.Fighter
		if ctype == swSuper {
			tals, tj := p.activeTalents(pk.pet, pi) // 神通点、境界取玩家的护法数据
			f = superFighter(pi, pk.pet.Type, pk.pos, tals, tj, pk.pet.talentsFull(pi))
			f.ID = pk.pet.NpcNum
		} else {
			f = p.swFighter(pk.pet, pk.pos, names)
		}
		e.Team = append(e.Team, f)
		e.Slots = append(e.Slots, swSlot{Pos: pk.pos, NpcNum: pk.pet.NpcNum, Type: pk.pet.Type, Grade: pk.pet.Grade})
	}
	if ctype == swSuper {
		if !seenPos[leader] {
			leader = picks[0].pos
		}
		e.Leader = leader
		for _, sl := range e.Slots {
			if sl.Pos == leader {
				e.Skill = gamedata.Pets[sl.Type].LeaderSkill
			}
		}
	}
	return e, ""
}

func init() {
	Handle("STARWAR_PTO.C_GET_TODAY_RACE_AND_PETS", func(s *Session, p *Player, m xym.Msg) {
		// 抓包：官方也是全 0（不限种族、没有指定护法）
		s.SendExt("STARWAR_PTO.S_SEND_TODAY_RACE_AND_PETS", xym.Msg{"race": 0, "type_star3": 0, "type_star4": 0, "type_star5": 0})
	})
	Handle("STARWAR_PTO.C_OPEN_STARWAR", func(s *Session, p *Player, m xym.Msg) { s.swOpen() })
	Handle("STARWAR_PTO.C_APPLY_USER_COUNT", onSWUserCount)
	Handle("STARWAR_PTO.C_SIGN_USER", onSWSign)
	Handle("STARWAR_PTO.C_CHANGE_FTYPE", onSWChange)
	Handle("STARWAR_PTO.C_CHANGE_FIGHTER", onSWChange)
	Handle("STARWAR_PTO.C_SUPER_SIGN_USER", onSWSuperSign)
	Handle("STARWAR_PTO.C_SUPER_CHANGE_FIGHTER", onSWSuperSign)
	Handle("STARWAR_PTO.C_SUPER_CHANGE_FTYPE", onSWSuperSign)
	Handle("STARWAR_PTO.C_APPLY_PET_LIST", onSWPetList)
	Handle("STARWAR_PTO.C_TARGET_PET_LIST", onSWTarget)
	Handle("STARWAR_PTO.C_APPLY_RANK", onSWRank)
	Handle("STARWAR_PTO.C_APPLY_RECORD", onSWRecord)
	Handle("STARWAR_PTO.C_APPLY_RANK_RECORD", onSWRankRecord)
}

// swDo 在星战锁里执行 fn，锁外发结算邮件。
func (s *Session) swDo(fn func(w *Starwar) bool) {
	w := s.srv.starwar
	mails := w.do(func() bool { return fn(w) })
	s.srv.sendSWMails(mails)
}

func (srv *Server) sendSWMails(mails []swMail) {
	for _, m := range mails {
		if _, err := srv.SendMail(m.to, m.spec); err != nil {
			slog.Error("starwar mail", "to", m.to, "err", err)
		}
	}
}

// starwarLoop 每 20 秒推进一次比赛（到点发奖不必等玩家打开界面）。
func (srv *Server) starwarLoop() {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for range t.C {
		srv.mu.Lock()
		closed := srv.closed
		srv.mu.Unlock()
		if closed {
			return
		}
		srv.sendSWMails(srv.starwar.do(nil))
		srv.jyDo(nil) // 九幽试炼跨天发排名奖励（jiuyou.go）
	}
}

// swOpen 回 C_OPEN_STARWAR：报名中 → S_OPEN_STARWAR；比赛中且报了名 → S_OPEN_FIGHT；
// 其余（没报名或已结束）→ S_GAME_OFF / S_SUPER_GAME_OFF（界面变成「查看」）。
func (s *Session) swOpen() {
	var out []func()
	s.swDo(func(w *Starwar) bool {
		now := swNow().Unix()
		sg := w.Signs[s.role.UID]
		switch ph := w.phase(now); {
		case ph == 0:
			msg := xym.Msg{"war_type": 0, "ctype": 0, "ftype": 0, "reward3": swShowPool(3), "reward4": swShowPool(4),
				"reward5": swShowPool(5), "reward_super": 0, "lefttime": w.Start - now}
			if w.Super {
				msg["war_type"], msg["ftype"], msg["reward_super"] = swSuper, 8, swSuperGold*swSuperSize
				msg["reward3"], msg["reward4"], msg["reward5"] = 0, 0, 0
			}
			if sg != nil {
				msg["ctype"], msg["ftype"] = sg.CType, sg.FType
			}
			out = append(out, func() { s.SendExt("STARWAR_PTO.S_OPEN_STARWAR", msg) })
		case ph == 1 && sg != nil:
			msg := w.fightMsg(s.role.UID, now)
			out = append(out, func() { s.SendExt("STARWAR_PTO.S_OPEN_FIGHT", msg) })
		default:
			if w.Super {
				gold, users := int32(0), 0
				if b := w.Brackets[swKey(swSuper, 8)]; b != nil {
					gold, users = b.pool(), len(b.Entries)
				}
				out = append(out, func() {
					s.SendExt("STARWAR_PTO.S_SUPER_GAME_OFF", xym.Msg{"total_gold": gold, "total_user": users})
				})
			} else {
				out = append(out, func() { s.SendExt("STARWAR_PTO.S_GAME_OFF", nil) })
			}
		}
		return false
	})
	for _, f := range out {
		f()
	}
}

// swShowPool 是报名阶段展示的 c 星场奖池（5V5 + 8V8 两个赛区按默认签位）。
func swShowPool(c int) int32 { return swFee[c] * int32(swSize[5]+swSize[8]) }

// fightMsg 是 uid 在比赛中的 S_OPEN_FIGHT。调用方持锁。
func (w *Starwar) fightMsg(uid int32, now int64) xym.Msg {
	sg := w.Signs[uid]
	b := w.Brackets[swKey(sg.CType, sg.FType)]
	msg := xym.Msg{"ctype": sg.CType, "ftype": sg.FType, "reward": 0, "round": 0, "fail": 0, "left": 0, "leftfighter": 0}
	if b == nil {
		return msg
	}
	done := len(b.Rounds)
	me := b.indexOf(uid)
	next := w.Start + int64(done)*swRoundSecs
	if done >= b.total() {
		next = w.end()
	}
	fail := 0
	if me >= 0 && !slices.Contains(b.alive(done), me) {
		fail = 1
	}
	msg["reward"], msg["round"], msg["fail"] = b.pool(), done, fail
	msg["left"], msg["leftfighter"] = max(0, next-now), len(b.alive(done))
	return msg
}

func onSWUserCount(s *Session, p *Player, m xym.Msg) {
	ctype := int(m["ctype"].(int64))
	var c5, c8 int
	s.swDo(func(w *Starwar) bool {
		if w.Brackets != nil {
			if b := w.Brackets[swKey(ctype, 5)]; b != nil {
				c5 = len(b.Entries)
			}
			if b := w.Brackets[swKey(ctype, 8)]; b != nil {
				c8 = len(b.Entries)
			}
			return false
		}
		// 报名中：签位数（不满由假选手补）与真人报名数取大
		for _, sg := range w.Signs {
			if sg.CType == ctype && sg.FType == 5 {
				c5++
			} else if sg.CType == ctype {
				c8++
			}
		}
		if ctype == swSuper {
			c8 = max(c8, swSuperSize)
		} else {
			c5, c8 = max(c5, swSize[5]), max(c8, swSize[8])
		}
		return false
	})
	s.SendExt("STARWAR_PTO.S_SEND_USER_COUNT", xym.Msg{"count5": c5, "count8": c8})
}

// onSWSign：普通星战报名（扣报名费、存阵容快照），回 S_OPEN_STARWAR。
func onSWSign(s *Session, p *Player, m xym.Msg) {
	ctype, ftype := int(m["ctype"].(int64)), int(m["ftype"].(int64))
	fighters, _ := m["fighter"].([]xym.Msg)
	fee := swFee[ctype]
	bad := s.swSignCheck(false)
	var e *swEntry
	if bad == "" {
		e, bad = s.swBuildEntry(p, ctype, ftype, 0, fighters)
	}
	if bad == "" && fee == 0 {
		bad = "没有这个星战。"
	}
	if bad == "" {
		s.swDo(func(w *Starwar) bool {
			switch {
			case w.Super:
				bad = "今天是超级星战，请报名超级星战。"
			case w.phase(swNow().Unix()) != 0:
				bad = "今天的报名已经截止，请明天再来。"
			case w.Signs[s.role.UID] != nil:
				bad = "你今天已经报名了。"
			case !s.roleUpdate(func(ro *Role) bool {
				if ro.Cash < fee {
					return false
				}
				ro.Cash -= fee
				return true
			}, "cash"):
				bad = "银两不足，无法报名。"
			default:
				w.Signs[s.role.UID] = &swSign{CType: ctype, FType: ftype, Entry: e}
				return true
			}
			return false
		})
	}
	if bad != "" {
		s.sendMsg(bad)
		s.swOpen()
		return
	}
	p.track(evStarwar, 1)
	p.saveLog(s)
	s.retMsg("报名成功，20:00 准时开赛！")
	s.swOpen()
}

// swSignCheck 是报名前的状态检查（是不是对应的星战日、报名截止没有、报过没有）。
func (s *Session) swSignCheck(super bool) string {
	var bad string
	s.swDo(func(w *Starwar) bool {
		signed := w.Signs[s.role.UID] != nil
		switch {
		case w.Super && !super:
			bad = "今天是超级星战，请报名超级星战。"
		case !w.Super && super:
			bad = "超级星战在周二、四、六、日开放。"
		case signed && !super:
			bad = "你今天已经报名了。"
		case !signed && w.phase(swNow().Unix()) != 0:
			bad = "今天的报名已经截止，请明天再来。"
		}
		return false
	})
	return bad
}

// onSWChange：C_CHANGE_FTYPE（换 5V5 / 8V8，同时换阵容）、C_CHANGE_FIGHTER（只换阵容）。比赛中改阵容从下一轮生效。
func onSWChange(s *Session, p *Player, m xym.Msg) {
	fighters, _ := m["fighter"].([]xym.Msg)
	var bad string
	var sg *swSign
	s.swDo(func(w *Starwar) bool {
		if sg = w.Signs[s.role.UID]; sg == nil || w.Super {
			bad = "你还没有报名。"
		}
		return false
	})
	if bad == "" {
		ftype := sg.FType
		if f, ok := m["ftype"].(int64); ok {
			ftype = int(f)
		}
		var e *swEntry
		if e, bad = s.swBuildEntry(p, sg.CType, ftype, 0, fighters); bad == "" {
			s.swDo(func(w *Starwar) bool { bad = w.change(s.role.UID, ftype, e); return bad == "" })
		}
	}
	if bad != "" {
		s.sendMsg(bad)
	} else {
		s.retMsg("阵容更换成功。")
	}
	s.swOpen()
}

// change 换 uid 的报名阵容（报名中可换人数；比赛中只能换阵容，已淘汰不能换）。调用方持锁。
func (w *Starwar) change(uid int32, ftype int, e *swEntry) string {
	sg := w.Signs[uid]
	if sg == nil {
		return "你还没有报名。"
	}
	switch w.phase(swNow().Unix()) {
	case 0:
		sg.FType, sg.Entry = ftype, e
		return ""
	case 1:
		if ftype != sg.FType {
			return "比赛已经开始，不能更换赛制。"
		}
		b := w.Brackets[swKey(sg.CType, sg.FType)]
		i := b.indexOf(uid)
		if i < 0 || !slices.Contains(b.alive(len(b.Rounds)), i) || len(b.Rounds) >= b.total() {
			return "你已经被淘汰，不能再更换阵容。"
		}
		sg.Entry, b.Entries[i] = e, e
		return ""
	}
	return "今天的比赛已经结束。"
}

// onSWSuperSign：超级星战报名 / 换阵容（8 人上满、带队长位；不收报名费），回 S_OPEN_STARWAR 或 S_OPEN_FIGHT。
func onSWSuperSign(s *Session, p *Player, m xym.Msg) {
	fighters, _ := m["fighter"].([]xym.Msg)
	leader, _ := m["leader_pos"].(int64)
	bad := s.swSignCheck(true)
	var e *swEntry
	if bad == "" {
		e, bad = s.swBuildEntry(p, swSuper, 8, int(leader), fighters)
	}
	signed := false
	if bad == "" {
		s.swDo(func(w *Starwar) bool {
			if !w.Super {
				bad = "超级星战在周二、四、六、日开放。"
				return false
			}
			if w.Signs[s.role.UID] != nil {
				bad = w.change(s.role.UID, 8, e)
				return bad == ""
			}
			if w.phase(swNow().Unix()) != 0 {
				bad = "今天的报名已经截止，请明天再来。"
				return false
			}
			w.Signs[s.role.UID] = &swSign{CType: swSuper, FType: 8, Entry: e}
			signed = true
			return true
		})
	}
	switch {
	case bad != "":
		s.sendMsg(bad)
	case signed:
		p.track(evStarwar, 1)
		p.saveLog(s)
		s.retMsg("报名成功，20:00 准时开赛！")
	default:
		s.retMsg("阵容更换成功。")
	}
	s.swOpen()
}

// onSWPetList：自己的报名阵容（没报名回空表，客户端等这条的锁）。
func onSWPetList(s *Session, p *Player, m xym.Msg) {
	msg := xym.Msg{"leader_pos": 1, "fighter": []xym.Msg{}}
	s.swDo(func(w *Starwar) bool {
		if sg := w.Signs[s.role.UID]; sg != nil {
			msg["leader_pos"] = max(sg.Entry.Leader, 1)
			msg["fighter"] = swPetList(sg.Entry)
		}
		return false
	})
	s.SendExt("STARWAR_PTO.S_OPEN_PET_LIST", msg)
}

func swPetList(e *swEntry) []xym.Msg {
	out := []xym.Msg{}
	for i, sl := range e.Slots {
		f := e.Team[i]
		out = append(out, xym.Msg{"pos": sl.Pos, "grade": sl.Grade, "icon": sl.Type, "hp": f.HP, "ap": f.Att})
	}
	return out
}

// onSWTarget：本轮（还没打就是下一轮）对手的阵容。
func onSWTarget(s *Session, p *Player, m xym.Msg) {
	msg := xym.Msg{"name": "", "grade": 0, "leader_pos": 1, "fighter": []xym.Msg{}}
	var bad string
	s.swDo(func(w *Starwar) bool {
		sg := w.Signs[s.role.UID]
		if sg == nil || w.Brackets == nil {
			bad = "比赛还没有开始。"
			return false
		}
		b := w.Brackets[swKey(sg.CType, sg.FType)]
		me := b.indexOf(s.role.UID)
		foe := -1
		alive := b.alive(len(b.Rounds))
		if k := slices.Index(alive, me); k >= 0 && len(b.Rounds) < b.total() {
			foe = alive[k^1]
		} else if lm := b.lastMatch(me); lm != nil {
			foe = lm.A + lm.B - me
		}
		if foe < 0 {
			bad = "暂时没有对手。"
			return false
		}
		e := b.Entries[foe]
		fs := []xym.Msg{}
		for _, f := range e.Team {
			fs = append(fs, xym.Msg{"pos": f.Pos % 100, "icon": f.Icon})
		}
		msg = xym.Msg{"name": e.Name, "grade": e.Grade, "leader_pos": max(e.Leader, 1), "fighter": fs}
		return false
	})
	if bad != "" {
		s.retMsg(bad)
	}
	s.SendExt("STARWAR_PTO.S_TARGET_PET_LIST", msg)
}

// onSWRank：赛区的决赛（round 2）与两场半决赛（round 3、4），topreward 是冠军奖励。
func onSWRank(s *Session, p *Player, m xym.Msg) {
	ctype, ftype := int(m["ctype"].(int64)), int(m["ftype"].(int64))
	if ctype == swSuper {
		ftype = 8
	}
	msg := xym.Msg{"topreward": 0, "list": []xym.Msg{}}
	s.swDo(func(w *Starwar) bool {
		b := w.Brackets[swKey(ctype, ftype)]
		if b == nil {
			return false
		}
		msg["topreward"] = b.pool() * int32(swShare[1]) / 100
		n := b.total()
		list := []xym.Msg{}
		if len(b.Rounds) >= n && n >= 1 {
			list = append(list, b.rankItem(2, b.Rounds[n-1][0]))
		}
		if len(b.Rounds) >= n-1 && n >= 2 {
			for k, mt := range b.Rounds[n-2] {
				list = append(list, b.rankItem(3+k, mt))
			}
		}
		msg["list"] = list
		return false
	})
	s.SendExt("STARWAR_PTO.S_SEND_RANK", msg)
}

func (b *swBracket) rankItem(round int, m *swMatch) xym.Msg {
	w, l := b.Entries[m.Win], b.Entries[m.loser()]
	return xym.Msg{"round": round, "winner_uid": w.UID, "winner_vip_icon_ts": 0, "winner_icon": w.Icon, "winner_name": w.Name,
		"loser_uid": l.UID, "loser_icon": l.Icon, "loser_vip_icon_ts": 0, "loser_name": l.Name}
}

// onSWRecord：自己最近一场比赛的录像。
func onSWRecord(s *Session, p *Player, m xym.Msg) {
	var mt *swMatch
	var b *swBracket
	s.swDo(func(w *Starwar) bool {
		if sg := w.Signs[s.role.UID]; sg != nil && w.Brackets != nil {
			b = w.Brackets[swKey(sg.CType, sg.FType)]
			mt = b.lastMatch(b.indexOf(s.role.UID))
		}
		return false
	})
	if mt == nil || mt.Rec == nil {
		s.sendMsg("暂时还没有你的比赛录像。")
		return
	}
	s.swSendRecord(b, mt)
}

// onSWRankRecord：排行里点某人头像——rank 是名次（1 冠军 → 决赛、2 → 半决赛、4 → 八进四）。
func onSWRankRecord(s *Session, p *Player, m xym.Msg) {
	uid, rank := int32(m["usernum"].(int64)), int(m["rank"].(int64))
	var mt *swMatch
	var b *swBracket
	s.swDo(func(w *Starwar) bool {
		keys := make([]string, 0, len(w.Brackets))
		for k := range w.Brackets {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			bb := w.Brackets[k]
			r := bb.total() - 1 // 决赛所在的轮（0 起）
			for x := rank; x > 1; x /= 2 {
				r--
			}
			if r < 0 || r >= len(bb.Rounds) {
				continue
			}
			for _, x := range bb.Rounds[r] {
				if (bb.Entries[x.A].UID == uid || bb.Entries[x.B].UID == uid) && uid != 0 {
					mt, b = x, bb
				}
			}
		}
		return false
	})
	if mt == nil || mt.Rec == nil {
		s.sendMsg("这场比赛没有录像。")
		return
	}
	s.swSendRecord(b, mt)
}

// swSendRecord 发一场星战的 S_FIGHT_RECORD（A 方在 team1）。
func (s *Session) swSendRecord(b *swBracket, m *swMatch) {
	ea, eb := b.Entries[m.A], b.Entries[m.B]
	wv := func(e *swEntry, t []*battle.Fighter) int {
		for _, f := range t {
			if f.SkillFull {
				return max(e.Grade, 1) * 10
			}
		}
		return 0
	}
	s.SendExt("FIGHT_PTO.S_FIGHT_RECORD", xym.Msg{
		"canop": 14, "rid": m.Rid,
		"w1": wv(ea, m.T1), "level1": ea.Grade, "name1": ea.Name, "team1": teamMsgs(m.T1),
		"w2": wv(eb, m.T2), "level2": eb.Grade, "name2": eb.Name, "team2": teamMsgs(m.T2),
		"otherparam": recordParams(m.T1, m.T2), "record": m.Rec,
	})
}

// StarwarStartNow 让今天的星战立刻开赛（管理接口，测试用）：报名截止、建签表、打第一轮。
func (srv *Server) StarwarStartNow() {
	w := srv.starwar
	srv.sendSWMails(w.do(func() bool {
		if now := swNow().Unix(); now < w.Start {
			w.Start = now
		}
		return true
	}))
	srv.sendSWMails(w.do(nil)) // 下一次推进建签表、打第一轮
}
