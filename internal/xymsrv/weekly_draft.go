package xymsrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

const (
	draftOpen = iota
	draftStart
	draftPick
	draftUpload
	draftPair
	draftSkip
	draftFight
	draftRedraft
)
const draftRounds = 8
const draftExcludedName = "助人海星"

var draftFightLimit, draftSkipLimit = 20, 5
var draftRedraftGold int32 = 5000

var draftZone = time.FixedZone("Asia/Shanghai", 8*3600)
var draftRaces = [...]int{1, 3, 2, 2, 4, 4}

type draftLineup struct {
	Cards  [8]int32 `json:"cards"`
	Leader int      `json:"leader"`
}

type draftMatch struct {
	UID    int32       `json:"uid"`
	Name   string      `json:"name"`
	Grade  int         `json:"grade"`
	Score  int         `json:"score"`
	Lineup draftLineup `json:"lineup"`
}

type draftPlayer struct {
	Generation int32                 `json:"generation"`
	Packs      [draftRounds][6]int32 `json:"packs"`
	Picked     []int32               `json:"picked"`
	Defense    draftLineup           `json:"defense"`
	Match      *draftMatch           `json:"match,omitempty"`
	Day        string                `json:"day"`
	Fights     int                   `json:"fights"`
	Skips      int                   `json:"skips"`
	Score      int                   `json:"score"`
	Played     int                   `json:"played"`
}

type draftRank struct {
	UID   int32  `json:"uid"`
	Name  string `json:"name"`
	Score int    `json:"score"`
}

type draftSeason struct {
	Week  string                 `json:"week"`
	Users map[int32]*draftPlayer `json:"users"`
	Last  []draftRank            `json:"last,omitempty"`
}

type weeklyDraft struct {
	mu sync.Mutex
	st store.Store
	draftSeason
}

func openWeeklyDraft(st store.Store) *weeklyDraft {
	d := &weeklyDraft{st: st}
	loadGlobal(st, globalDraft, &d.draftSeason)
	if d.Users == nil {
		d.Users = map[int32]*draftPlayer{}
	}
	return d
}

func (d *weeklyDraft) save(next draftSeason) error {
	if d.st != nil {
		data, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if err := d.st.SaveGlobal(string(globalDraft), data); err != nil {
			return err
		}
	}
	d.draftSeason = next
	return nil
}

func (srv *Server) draftRanks(users map[int32]*draftPlayer) []draftRank {
	var rows []draftRank
	for uid, u := range users {
		if u.Played > 0 {
			if ro := srv.roles.Get(uid); ro != nil {
				rows = append(rows, draftRank{uid, ro.Name, u.Score})
			}
		}
	}
	slices.SortFunc(rows, func(a, b draftRank) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return int(a.UID - b.UID)
	})
	return rows[:min(10, len(rows))]
}

// 调用方持轮抓锁。发奖按赛季和角色去重，任何一次存档失败都可以安全重试。
func (srv *Server) draftRollover(now time.Time) error {
	d := srv.draft
	week := ttWeek(now.In(draftZone))
	if d.Week == week {
		return nil
	}
	last := srv.draftRanks(d.Users)
	for i, row := range last {
		spec := srv.cfg.DraftRewards[i]
		if len(spec.rewards()) == 0 {
			continue
		}
		if spec.Title == "" {
			spec.Title = "轮抓周榜奖励"
		}
		if spec.Text == "" {
			spec.Text = fmt.Sprintf("轮抓赛季 %s 第%d名。", d.Week, i+1)
		}
		key := fmt.Sprintf("weekly-draft/%s/%d", d.Week, row.UID)
		if _, _, err := srv.sendOpsMail(key, row.UID, RewardRule{ID: "weekly-draft", Reward: spec}, d.Week); err != nil {
			return err
		}
	}
	return d.save(draftSeason{Week: week, Users: map[int32]*draftPlayer{}, Last: last})
}

func (srv *Server) draftLoop() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-srv.opsStop:
			return
		case now := <-ticker.C:
			d := srv.draft
			d.mu.Lock()
			err := srv.draftRollover(now)
			d.mu.Unlock()
			if err != nil {
				slog.Error("draft rollover", "err", err)
			}
		}
	}
}

func draftPacks(rng *rand.Rand) ([draftRounds][6]int32, error) {
	var packs [draftRounds][6]int32
	var pools [5][]int32
	var rare []int32
	seen := map[string]bool{}
	for _, id := range gamedata.PetsByStar[5] {
		pi := gamedata.Pets[id]
		if pi == nil || pi.Name == draftExcludedName || pi.Star != 5 || pi.Race < 1 || pi.Race > 4 || seen[pi.Name] {
			continue
		}
		seen[pi.Name] = true
		if pi.Special && !xlExcluded(id) {
			rare = append(rare, id)
		} else {
			pools[pi.Race] = append(pools[pi.Race], id)
		}
	}
	// 即使八轮都不命中仙灵，也必须有足够的普通卡。
	for _, race := range draftRaces {
		need := 0
		for _, r := range draftRaces {
			if r == race {
				need += draftRounds
			}
		}
		if len(pools[race]) < need {
			return packs, fmt.Errorf("轮抓种族 %d 候选不足", race)
		}
	}
	if len(rare) < draftRounds {
		return packs, fmt.Errorf("轮抓仙灵候选不足")
	}
	shuffle := func(pool []int32) { rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] }) }
	shuffle(rare)
	for i := range pools {
		shuffle(pools[i])
	}
	for i := range packs {
		rareSlot := -1
		if rng.IntN(10) == 0 {
			id := rare[0]
			rare = rare[1:]
			race := gamedata.Pets[id].Race
			rareSlot = slices.Index(draftRaces[:], race)
			if rareSlot+1 < len(draftRaces) && draftRaces[rareSlot+1] == race {
				rareSlot += rng.IntN(2)
			}
			packs[i][rareSlot] = id
		}
		for j, race := range draftRaces {
			if j == rareSlot {
				continue
			}
			packs[i][j], pools[race] = pools[race][0], pools[race][1:]
		}
	}
	return packs, nil
}

// Replace an unpicked helper in an existing draft without charging or
// changing any confirmed picks. Preserve race, rarity and unique names.
func replaceUnpickedDraftHelper(p *draftPlayer) bool {
	used := make(map[string]bool)
	for _, pack := range p.Packs {
		for _, id := range pack {
			if pi := gamedata.Pets[id]; pi != nil {
				used[pi.Name] = true
			}
		}
	}
	changed := false
	for round := len(p.Picked); round < draftRounds; round++ {
		for slot, id := range p.Packs[round] {
			pi := gamedata.Pets[id]
			if pi == nil || pi.Name != draftExcludedName {
				continue
			}
			for _, replacement := range gamedata.PetsByStar[5] {
				card := gamedata.Pets[replacement]
				if card.Race != pi.Race || used[card.Name] || (card.Special && !xlExcluded(replacement)) {
					continue
				}
				p.Packs[round][slot] = replacement
				used[card.Name] = true
				changed = true
				break
			}
		}
	}
	return changed
}

// Use the super-starwar loadout and levels, with every innate talent unlocked.
// Draft deliberately keeps its original no-relation-stat-multiplier rule.
func draftFighter(id int32, pos int) *battle.Fighter {
	pi := gamedata.Pets[id]
	talents := []int32{}
	for _, slot := range pi.Talents {
		if len(slot) > 0 {
			talents = append(talents, slot[len(slot)-1])
		}
	}
	f := superFighter(pi, id, pos, talents, nil, true)
	f.ID = id
	f.Att = pi.Att(swSuperGrade) + 30
	f.HP = pi.HP(swSuperGrade) + 120
	for _, suit := range f.Suits {
		if suit[0] == 1 {
			f.Att += 706
		}
		if suit[0] == 2 {
			f.HP += 2824
		}
	}
	f.MaxHP = f.HP
	return f
}

func draftTeam(lineup draftLineup, side int) ([]*battle.Fighter, int32) {
	team := make([]*battle.Fighter, 0, len(lineup.Cards))
	var skill int32
	for i, id := range lineup.Cards {
		team = append(team, draftFighter(id, 100*side+i+1))
		if i+1 == lineup.Leader {
			skill = gamedata.Pets[id].LeaderSkill
		}
	}
	return team, skill
}

func draftCardDetail(id int32) xym.Msg {
	f := draftFighter(id, 1)
	suits := make([]xym.Msg, 3)
	for i := range suits {
		suits[i] = xym.Msg{"type": 0, "grade": 0}
	}
	for _, suit := range f.Suits {
		suits[suit[0]-1] = xym.Msg{"type": suit[1], "grade": suit[2]}
	}
	books := []xym.Msg{}
	for i, book := range f.Books {
		books = append(books, xym.Msg{"type": book[0], "grade": book[1], "jxgrade": f.BookJX[i]})
	}
	return xym.Msg{"att": f.Att, "hp": f.MaxHP, "speed": f.Speed, "talents": f.Talents, "magicLevel": f.MagicLv, "suits": suits, "magics": books}
}

func draftReadLineup(m xym.Msg, picked []int32) (draftLineup, bool) {
	var lineup draftLineup
	lineup.Leader = int(m["leader"].(int64))
	rows := m["lineup"].([]xym.Msg)
	if len(rows) != 8 || len(picked) != 8 || lineup.Leader < 1 || lineup.Leader > 8 {
		return lineup, false
	}
	for i, row := range rows {
		id := int32(row["card"].(int64))
		if !slices.Contains(picked, id) || slices.Contains(lineup.Cards[:i], id) {
			return lineup, false
		}
		lineup.Cards[i] = id
	}
	return lineup, true
}

func (srv *Server) draftPair(uid int32, score int, exclude int32, rng *rand.Rand) *draftMatch {
	var candidates []draftMatch
	for foeUID, u := range srv.draft.Users {
		if foeUID == uid || foeUID == exclude || u.Defense.Leader == 0 {
			continue
		}
		if ro := srv.roles.Get(foeUID); ro != nil {
			candidates = append(candidates, draftMatch{foeUID, ro.Name, ro.Grade, u.Score, u.Defense})
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	dist := func(n int) int { return max(n-score, score-n) }
	slices.SortFunc(candidates, func(a, b draftMatch) int {
		if d := dist(a.Score) - dist(b.Score); d != 0 {
			return d
		}
		return int(a.UID - b.UID)
	})
	return &candidates[rng.IntN(min(5, len(candidates)))]
}

func init() { Handle("DRAFT_PTO.C_DRAFT_ACTION", onDraftAction) }

func (s *Session) sendDraftState(u *draftPlayer, message string, now time.Time) {
	d := s.srv.draft
	names := map[int32]string{}
	details := map[int32]xym.Msg{}
	state := xym.Msg{"week": d.Week, "uid": s.role.UID, "ranks": s.srv.draftRanks(d.Users), "last": d.Last,
		"message": message, "score": ttInitScore, "fights": draftFightLimit, "skips": draftSkipLimit, "names": names, "redraftCost": draftRedraftGold, "details": details}
	if u != nil {
		state["generation"] = u.Generation
		addNames := func(ids []int32) {
			for _, id := range ids {
				if pi := gamedata.Pets[id]; pi != nil {
					names[id] = pi.Name
					details[id] = draftCardDetail(id)
				}
			}
		}
		addNames(u.Picked)
		if u.Match != nil {
			addNames(u.Match.Lineup.Cards[:])
		}
		state["picked"], state["defense"], state["match"], state["score"] = u.Picked, u.Defense, u.Match, u.Score
		if u.Day == now.In(draftZone).Format("2006-01-02") {
			state["fights"], state["skips"] = draftFightLimit-u.Fights, draftSkipLimit-u.Skips
		}
		if len(u.Picked) < draftRounds {
			pack := u.Packs[len(u.Picked)]
			state["round"], state["pack"] = len(u.Picked)+1, pack
			addNames(pack[:])
			for _, id := range pack {
				if gamedata.Pets[id].Special && !xlExcluded(id) {
					state["rare"] = id
				}
			}
		}
	}
	data, _ := json.Marshal(state)
	s.SendExt("DRAFT_PTO.S_DRAFT_STATE", xym.Msg{"state": string(data)})
}

func onDraftAction(s *Session, _ *Player, m xym.Msg) {
	now := time.Now().In(draftZone)
	day := now.Format("2006-01-02")
	d := s.srv.draft
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := s.srv.draftRollover(now); err != nil {
		s.sendMsg("轮抓结算暂不可用，请稍后再试。")
		return
	}
	uid := s.role.UID
	old := d.Users[uid]
	if old != nil && len(old.Picked) < draftRounds {
		updated := *old
		if replaceUnpickedDraftHelper(&updated) {
			next := d.draftSeason
			next.Users = maps.Clone(d.Users)
			next.Users[uid] = &updated
			if err := d.save(next); err != nil {
				s.sendMsg("轮抓牌面更新失败，请稍后再试。")
				return
			}
			old = &updated
		}
	}
	fail := func(msg string) { s.sendDraftState(d.Users[uid], msg, now) }
	action := int(m["action"].(int64))
	if action == draftOpen {
		fail("")
		return
	}
	if old == nil && action != draftStart {
		fail("请先开始本周轮抓。")
		return
	}
	var u draftPlayer
	if old != nil {
		u = *old
		u.Picked = slices.Clone(u.Picked)
	}
	if u.Day != day {
		u.Day, u.Fights, u.Skips = day, 0, 0
	}
	paid := false
	stateMessage := ""
	var result *battle.Result
	var team1, team2 []*battle.Fighter
	var foe *draftMatch
	var delta int
	switch action {
	case draftStart:
		if old != nil {
			fail("")
			return
		}
		packs, err := draftPacks(s.actRng())
		if err != nil {
			fail(err.Error())
			return
		}
		u.Packs, u.Score = packs, ttInitScore
	case draftRedraft:
		// The generation identifies exactly the draft being abandoned. A retry of
		// an already committed request returns state without charging again.
		if int32(m["card"].(int64)) != u.Generation {
			fail("牌面已更新，请确认当前阵容后再操作。")
			return
		}
		if ro := s.srv.roles.Get(uid); ro == nil || ro.Gold < draftRedraftGold {
			fail(fmt.Sprintf("元宝不足，重新轮抓需要%d元宝。", draftRedraftGold))
			return
		}
		packs, err := draftPacks(s.actRng())
		if err != nil {
			fail(err.Error())
			return
		}
		u.Packs, u.Picked, u.Defense, u.Match = packs, nil, draftLineup{}, nil
		u.Generation++
		paid = true
	case draftPick:
		round, card := int(m["round"].(int64)), int32(m["card"].(int64))
		if round != len(u.Picked)+1 || round > draftRounds || !slices.Contains(u.Packs[round-1][:], card) {
			fail("该牌面已更新，请重新选择。")
			return
		}
		u.Picked = append(u.Picked, card)
	case draftUpload:
		lineup, ok := draftReadLineup(m, u.Picked)
		if !ok {
			fail("阵容必须使用本周选定的全部八张牌。")
			return
		}
		u.Defense = lineup
	case draftPair, draftSkip:
		uploadWithMatch := action == draftPair && len(m["lineup"].([]xym.Msg)) > 0
		if uploadWithMatch {
			lineup, ok := draftReadLineup(m, u.Picked)
			if !ok {
				fail("阵容必须使用本周选定的全部八张牌。")
				return
			}
			u.Defense = lineup
		}
		if u.Defense.Leader == 0 {
			fail("请先上传本周防守阵容。")
			return
		}
		if u.Fights >= draftFightLimit {
			fail("今日排位次数已用完。")
			return
		}
		var exclude int32
		if action == draftSkip {
			if u.Match == nil || u.Skips >= draftSkipLimit {
				fail("当前无法跳过对手。")
				return
			}
			exclude = u.Match.UID
		} else if u.Match != nil {
			break // Upload the current defense while keeping the locked opponent.
		}
		match := s.srv.draftPair(uid, u.Score, exclude, s.actRng())
		if match == nil {
			if uploadWithMatch {
				stateMessage = "阵容已上传，暂无其他已上传阵容的对手。"
				break
			}
			fail("暂无其他已上传阵容的对手。")
			return
		}
		u.Match = match
		if action == draftSkip {
			u.Skips++
		}
	case draftFight:
		if u.Match == nil || u.Fights >= draftFightLimit {
			fail("请先匹配对手并确认剩余次数。")
			return
		}
		lineup, ok := draftReadLineup(m, u.Picked)
		if !ok {
			fail("请确认本场八名护法的阵容。")
			return
		}
		foe = u.Match
		var a, b int32
		team1, a = draftTeam(lineup, 0)
		team2, b = draftTeam(foe.Lineup, 1)
		week := d.Week
		d.mu.Unlock()
		res := battle.SimulateWith(battle.CloneTeam(team1), battle.CloneTeam(team2), battle.Options{Leaders: [2]int32{a, b}, DrawOnTimeout: true}, s.actRng())
		d.mu.Lock()
		now = time.Now().In(draftZone)
		if week != ttWeek(now) || d.Week != week || day != now.Format("2006-01-02") || d.Users[uid] != old {
			fail("轮抓状态已变化，请重新匹配。")
			return
		}
		result = &res
		delta = ttScoreDelta(u.Score, foe.Score, res.Win)
		if res.Draw {
			delta = ttDrawDelta(u.Score, foe.Score)
		}
		next := max(0, u.Score+delta)
		delta, u.Score = next-u.Score, next
		u.Match = nil
		u.Fights++
		u.Played++
	default:
		fail("未知的轮抓操作。")
		return
	}
	next := d.draftSeason
	next.Users = maps.Clone(next.Users)
	next.Users[uid] = &u
	var saveErr error
	if paid {
		saveErr = s.saveDraftPurchase(next)
	} else {
		saveErr = d.save(next)
	}
	if err := saveErr; err != nil {
		slog.Error("save weekly draft", "uid", uid, "err", err)
		fail("轮抓存档失败，请重试。")
		return
	}
	if result != nil {
		s.zfParam = ""
		rid := fmt.Sprintf("draft:%d", recordSeq.Add(1))
		s.fightRecord(rid, foe.Name, foe.Grade, team1, team2, result.Record)
		status := "失败"
		if result.Draw {
			status = "平局"
		} else if result.Win {
			status = "胜利"
		}
		s.sendMsg(fmt.Sprintf("%s，轮抓积分 %+d，当前 %d 分。", status, delta, u.Score))
		return
	}
	fail(stateMessage)
}

// Caller holds the draft mutex. The per-role lock serializes the purchase with
// all other balance updates; both rows commit before either cache is published.
func (s *Session) saveDraftPurchase(next draftSeason) error {
	atomic, ok := s.srv.draft.st.(interface {
		SaveRoleAndGlobal(store.Role, string, []byte) error
	})
	if !ok {
		return errors.New("轮抓付费存档需要支持事务的数据库")
	}
	roles := s.srv.roles
	lock := roles.updateLock(s.role.UID)
	lock.Lock()
	defer lock.Unlock()
	ro := roles.Get(s.role.UID)
	if ro == nil || ro.Gold < draftRedraftGold {
		return errors.New("元宝不足")
	}
	ro.Gold -= draftRedraftGold
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err = atomic.SaveRoleAndGlobal(store.Role(*ro), string(globalDraft), data); err != nil {
		return err
	}
	roles.mu.Lock()
	roles.ByUID[ro.UID] = ro
	roles.mu.Unlock()
	s.srv.draft.draftSeason = next
	s.role = ro
	s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "gold", "val": ro.Gold}}})
	return nil
}
