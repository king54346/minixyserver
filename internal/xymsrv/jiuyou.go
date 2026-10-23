package xymsrv

import (
	"cmp"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 九幽试炼（JIUYOU_PTO，玩法栏「九幽试炼」→ view/ActivityJiuyouMainView.lua）：每天一个打不死的首领，
// 比谁对它造成的伤害高；每日排行 24:00 结算发奖；九幽币在九幽商店换东西。
//
// 依据：
//   - ActivityJiuyouMainView 规则：27 级开启；每天轮换首领，首领有自己的天赋（法术）/ 神通；最高伤害记录并排行，
//     每日 24:00 按排名发奖；每日前 jyFree 次挑战免费且可获得九幽币，之后每次挑战需购买次数。
//     S_MAINVIEW_INFO{max_damage_value_today, ranking_today, level_name, remain_time 到刷新的秒数,
//     remain_challenges_today, reward_id（挑战奖励）, get_reward_nums「可领 N 次」, boss_id, magic_ids, talent_ids, env_ids}。
//   - 挑战：C_START_FIGHT → QUEST_PTO.S_OPEN_TMP_TEAM{key "jiuyoushilian", fight_ca 首领阵型, tmp_team} 打开选阵
//     （view/Mijing_FightTeamView.lua；九幽不显示战力和载入按钮）→ QUEST_PTO.C_PREPARE_FINISH{key, id, leader_num, tmp_team}
//     → S_FIGHT_RECORD（WAR_START 的 wartype = 1，war/netwar.lua WAR_TYPE_JIUYOU：战斗里显示「今日最高 / 本次挑战」伤害面板）
//     → S_WIN_BATTLE_INFO{max_hurt_today, cur_hurt, hurt_increment, rank, rank_reward_id, challenge_reward_id, surpass_*}
//     （view/ActivityJiuyouRewardView.lua 结算界面）。
//   - 首领：auto/fight_npc.lua 里 npctype 1、生命 999999999 的单位与 auto/fight_ca.lua 的阵型（28667 九幽魔君、
//     28668 九幽猴将 + 四个小怪、28678 转生使者、28682 混乱魔主、28710 风伯、28720 凰鸟、28721 魔化窦建德 + 王世充）；
//     首领带 130234 魔甲 / 130235 藤甲（battle：免疫生命流失、按生命百分比的神通伤害封顶）。
//   - 奖励：auto/reward.lua 53113「九幽试炼挑战奖励」九幽币 ×50；53097–53103、53169–53172 各名次段排名奖励；
//     53104–53112 九幽商店兑换（5 星护法魂蛋、宝石袋、炼神石…）。九幽币是道具 11485（ActivityJiuyouShopView CurrencyID）。
//   - 排行：C_OPEN_RANK → S_RANK_VIEW_INFO{my_reward_id, ranking_list[has_real_player, player_name, rank, grade,
//     damage_value, reward_id, is_me, xgrade]}；C_OPEN_RANK_LINEUP{rank} → S_OPENING_BATTLE_TEAM_VIEW{tmp_team}。
//
// 估计（没有抓包）：首领按日期轮换；排行只有真人；每天最多买 5 次；商店价格与每日限购；
// 前一天的排名奖励在第二天第一次有人打开九幽 / 每 20 秒的定时推进时用邮件发。全服存档见 globals.go。

const (
	jyCoin       = 11485 // 九幽币
	jyFree       = 5     // 每日免费（有九幽币）次数
	jyBuyGold    = 300   // 买一次挑战次数（用户定）
	jyBuyMax     = 20
	jyGrade      = 27
	jyKey        = "jiuyoushilian"
	jyChallenge  = 53113 // 挑战奖励：九幽币 ×50
	jyChallengeN = 50
	jyBossHP     = 999999999
)

// jyBoss 是一种首领：阵型号（客户端 fight_ca，选阵界面显示敌人用）与阵上的单位（fight_npc）。
type jyBoss struct {
	ca    int32
	name  string
	units []jyUnit // 首个是主首领（S_MAINVIEW_INFO.boss_id / 法术 / 神通取它）
}

type jyUnit struct {
	id, shape   int32
	name        string
	race, speed int
	grade, slot int
	magic       int32
	magicLv     int
	talents     []int32
	att         int32
}

var jyBosses = []jyBoss{
	{28667, "九幽魔君", []jyUnit{{43481, 55031, "九幽魔君", 2, 2, 100, 3, 17578, 10, []int32{20050, 130233, 130234, 130236}, 500}}},
	{28668, "九幽猴将", []jyUnit{
		{43482, 55004, "九幽猴将", 2, 2, 100, 3, 17579, 10, []int32{20070, 130233, 130235, 130236}, 500},
		{43483, 21001, "犀牛精", 2, 2, 100, 1, 17579, 10, []int32{20070, 130233, 130235, 130236}, 500},
		{43484, 21005, "小钻风", 2, 2, 100, 2, 17579, 10, []int32{20070, 130233, 130235, 130236}, 500},
		{43485, 21003, "马面", 4, 2, 100, 4, 17579, 10, []int32{20070, 130233, 130235, 130236}, 500},
		{43486, 21004, "赤身鬼", 2, 2, 100, 5, 17579, 10, []int32{20070, 130233, 130235, 130236}, 500},
	}},
	{28678, "转生使者", []jyUnit{{43489, 55028, "转生使者（一）", 3, 2, 400, 3, 17589, 10, []int32{130246, 130233, 130234, 130236}, 500}}},
	{28682, "混乱魔主", []jyUnit{{43577, 55019, "混乱魔主", 3, 2, 400, 3, 17592, 1, []int32{130257, 130233, 130234, 130236}, 500}}},
	{28710, "风伯", []jyUnit{{43751, 55022, "风伯", 3, 2, 400, 3, 17593, 1, []int32{130233, 130234, 130236}, 500}}},
	{28720, "凰鸟", []jyUnit{{43821, 55013, "凰鸟", 3, 2, 400, 3, 17595, 1, []int32{130233, 130234, 130236}, 500}}},
	{28721, "魔化窦建德", []jyUnit{
		{43822, 55025, "魔化窦建德", 2, 3, 400, 1, 17596, 1, []int32{130233, 130234, 130236}, 500},
		{43823, 55010, "魔化王世充", 2, 2, 400, 5, 17596, 1, []int32{130233, 130234, 130236}, 500},
	}},
}

// jyNow 是当前时间（测试可改）。
var jyNow = time.Now

func jyDay(t time.Time) string { return t.Format("2006-01-02") }

// jyBossOf 是 day 那天的首领（按日序轮换）。
func jyBossOf(t time.Time) *jyBoss {
	d := (t.Unix() + 8*3600) / 86400
	return &jyBosses[d%int64(len(jyBosses))]
}

func (b *jyBoss) team() []*battle.Fighter {
	var out []*battle.Fighter
	for _, u := range b.units {
		out = append(out, &battle.Fighter{ID: u.id, Pos: 100 + u.slot, Name: u.name, Att: u.att, HP: jyBossHP, MaxHP: jyBossHP,
			Speed: u.speed, Icon: u.shape, Race: u.race, Grade: u.grade, Talents: u.talents, Magic: u.magic, MagicLv: u.magicLv})
	}
	return out
}

// jyRankTiers：名次上限 → 排名奖励号与内容（auto/reward.lua 53097–53103、53169–53172：九幽币、WP10002、WP10003）。
var jyRankTiers = []struct {
	upTo   int
	reward int32
	coin   int32
	extra  int32
}{
	{1, 53097, 1500, 7}, {2, 53098, 1400, 6}, {3, 53099, 1300, 6}, {5, 53100, 1100, 6}, {10, 53101, 1000, 5},
	{20, 53102, 900, 5}, {30, 53103, 800, 4}, {50, 53169, 700, 4}, {75, 53170, 650, 3}, {100, 53171, 600, 3},
	{1 << 30, 53172, 550, 3},
}

func jyTier(rank int) int {
	for i, t := range jyRankTiers {
		if rank <= t.upTo {
			return i
		}
	}
	return len(jyRankTiers) - 1
}

// jyGoods：九幽商店（奖励号 53104–53112 的内容；价格、每日限购为估计）。
type jyGood struct {
	reward int32
	item   int
	price  int32
	limit  int
}

var jyGoods = []jyGood{
	{53104, 10117, 3000, 1}, {53105, 10308, 800, 2}, {53106, 10403, 300, 5}, {53107, 10309, 100, 10},
	{53108, 10140, 200, 5}, {53109, 10201, 20, 20}, {53110, 10015, 50, 10}, {53111, 10124, 100, 5}, {53112, 10125, 100, 5},
	// 阵魂符篆（阵法培养，zhenfa.go）：客户端奖励表 49119「阵魂符篆」= 1 个，用来显示；1 九幽币一个，每天限购 250（本服定）
	{49119, 10440, 1, 250},
}

// JiuyouState 是玩家自己的九幽数据（Player.Jiuyou）。
type JiuyouState struct {
	Day    string      `json:"day"`
	Fights int         `json:"fights"` // 今日已挑战次数
	Bought int         `json:"bought"` // 今日买的次数
	Shop   map[int]int `json:"shop,omitempty"`
}

func (p *Player) jiuyou() *JiuyouState {
	if p.Jiuyou == nil {
		p.Jiuyou = &JiuyouState{}
	}
	j := p.Jiuyou
	if d := jyDay(jyNow()); j.Day != d {
		j.Day, j.Fights, j.Bought, j.Shop = d, 0, 0, map[int]int{}
	}
	if j.Shop == nil {
		j.Shop = map[int]int{}
	}
	return j
}

func (j *JiuyouState) left() int { return max(0, jyFree+j.Bought-j.Fights) }

// jyEntry 是排行上的一位真人（当天最高伤害与那一场的阵容）。
type jyEntry struct {
	Name   string    `json:"name"`
	Grade  int       `json:"grade"`
	Damage int64     `json:"damage"`
	Team   []xym.Msg `json:"team,omitempty"` // S_OPENING_BATTLE_TEAM_VIEW.tmp_team
}

// Jiuyou 是全服九幽排行（存档位 jiuyouUID）。
type Jiuyou struct {
	mu sync.Mutex
	st store.Store

	Day     string             `json:"day"`
	Entries map[int32]*jyEntry `json:"entries,omitempty"`
}

func openJiuyou(st store.Store) *Jiuyou {
	j := &Jiuyou{st: st}
	loadGlobal(st, jiuyouUID, j, "jiuyou")
	return j
}

// do 持锁执行 fn（先跨天结算），返回要发的排名奖励邮件。
func (j *Jiuyou) do(fn func() bool) []swMail {
	j.mu.Lock()
	defer j.mu.Unlock()
	var mails []swMail
	dirty := false
	if d := jyDay(jyNow()); j.Day != d {
		if j.Day != "" {
			mails = j.settle()
		}
		j.Day, j.Entries = d, map[int32]*jyEntry{}
		dirty = true
	}
	if j.Entries == nil {
		j.Entries = map[int32]*jyEntry{}
	}
	if fn != nil && fn() {
		dirty = true
	}
	if dirty {
		saveGlobal(j.st, jiuyouUID, j, "jiuyou")
	}
	return mails
}

// ranked 是当天排行（伤害从高到低）。调用方持锁。
func (j *Jiuyou) ranked() []int32 {
	uids := make([]int32, 0, len(j.Entries))
	for uid, e := range j.Entries {
		if e.Damage > 0 {
			uids = append(uids, uid)
		}
	}
	slices.SortFunc(uids, func(a, b int32) int {
		if c := cmp.Compare(j.Entries[b].Damage, j.Entries[a].Damage); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	return uids
}

func (j *Jiuyou) rankOf(uid int32) int {
	if i := slices.Index(j.ranked(), uid); i >= 0 {
		return i + 1
	}
	return 0
}

// settle 是 j.Day 那天的排名奖励邮件。调用方持锁。
func (j *Jiuyou) settle() []swMail {
	var out []swMail
	for i, uid := range j.ranked() {
		t := jyRankTiers[jyTier(i+1)]
		out = append(out, swMail{to: uid, spec: MailSpec{Title: "九幽试炼排名奖励",
			Text:  fmt.Sprintf("你在 %s 的九幽试炼中排名第 %d，获得排名奖励。", j.Day, i+1),
			Items: map[string]int32{"WP" + strconv.Itoa(jyCoin): t.coin, "WP10002": t.extra, "WP10003": t.extra}}})
	}
	return out
}

func (srv *Server) jyDo(fn func(j *Jiuyou) bool) {
	mails := srv.jiuyou.do(func() bool {
		if fn == nil {
			return false
		}
		return fn(srv.jiuyou)
	})
	srv.sendSWMails(mails)
}

func init() {
	Handle("JIUYOU_PTO.C_OPEN_MAIN", func(s *Session, p *Player, m xym.Msg) { s.jyMain(p) })
	Handle("JIUYOU_PTO.C_START_FIGHT", onJYStart)
	Handle("JIUYOU_PTO.C_OPEN_RANK", onJYRank)
	Handle("JIUYOU_PTO.C_OPEN_RANK_LINEUP", onJYRankLineup)
	Handle("JIUYOU_PTO.C_OPEN_SHOP", func(s *Session, p *Player, m xym.Msg) { s.jyShop(p) })
	Handle("JIUYOU_PTO.C_BUY_ITEM", onJYBuy)
	Handle("QUEST_PTO.C_PREPARE_FINISH", onPrepareFinish)
}

func (s *Session) jyMain(p *Player) {
	now := jyNow()
	boss := jyBossOf(now)
	st := p.jiuyou()
	var dmg int64
	var rank int
	s.srv.jyDo(func(j *Jiuyou) bool {
		if e := j.Entries[s.role.UID]; e != nil {
			dmg = e.Damage
		}
		rank = j.rankOf(s.role.UID)
		return false
	})
	head := boss.units[0]
	tals := []xym.Msg{}
	for _, t := range head.talents[:min(3, len(head.talents))] {
		tals = append(tals, xym.Msg{"talent_id": t})
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	s.SendExt("JIUYOU_PTO.S_MAINVIEW_INFO", xym.Msg{"max_damage_value_today": dmg, "ranking_today": rank,
		"level_name": boss.name, "remain_time": int(midnight.Sub(now).Seconds()), "remain_challenges_today": st.left(),
		"reward_id": jyChallenge, "get_reward_nums": max(0, jyFree-st.Fights), "special_acts": []xym.Msg{},
		"boss_id": head.id, "magic_ids": []xym.Msg{{"id": head.magic}}, "talent_ids": tals, "env_ids": []xym.Msg{}})
}

// onJYStart：还有次数就打开选阵；免费次数用完时弹确认框，花 jyBuyGold 元宝买一次（每天最多 jyBuyMax 次）再打开。
func onJYStart(s *Session, p *Player, m xym.Msg) {
	st := p.jiuyou()
	switch {
	case s.role.Grade < jyGrade:
		s.retMsg(fmt.Sprintf("九幽试炼于玩家%d级开启。", jyGrade))
		return
	case st.left() == 0 && st.Bought >= jyBuyMax:
		s.retMsg("今日挑战次数已用完，明天再来吧。")
		return
	case st.left() == 0:
		text := fmt.Sprintf("今日的九幽试炼挑战次数已经用完了（今日已购买%d/%d次）。#r花费#G%d元宝#n购买1次挑战次数并开始挑战？",
			st.Bought, jyBuyMax, jyBuyGold)
		s.askBox(text, []string{"购买", "关闭"}, func(opt int) {
			st := p.jiuyou()
			if opt != 0 || st.left() > 0 || st.Bought >= jyBuyMax || !s.spend(0, jyBuyGold) {
				return
			}
			st.Bought++
			p.saveLog(s)
			onJYStart(s, p, m)
		})
		return
	}
	boss := jyBossOf(jyNow())
	team := []xym.Msg{}
	leader := int32(0)
	if lu := p.lineup(p.FightNo); lu != nil {
		for fp, sl := range lu.Slots {
			if pet := p.petAtFightPos(fp + 1); sl != nil && pet != nil {
				team = append(team, xym.Msg{"fight_pos": fp + 1, "npcnum": pet.NpcNum, "wear_pos": fp + 1})
				if lu.Leader == fp+1 {
					leader = pet.NpcNum
				}
			}
		}
	}
	s.SendExt("QUEST_PTO.S_OPEN_TMP_TEAM", xym.Msg{"key": jyKey, "id": boss.units[0].id, "type": 0, "tips": "",
		"leader_num": leader, "fight_ca": boss.ca, "tmp_team": team, "pet_list": []xym.Msg{}})
}

// onPrepareFinish：选阵确认（QUEST_PTO.C_PREPARE_FINISH）。目前只有九幽试炼用。
func onPrepareFinish(s *Session, p *Player, m xym.Msg) {
	if m["key"] != jyKey {
		s.sendMsg("这个玩法暂未开放。")
		return
	}
	st := p.jiuyou()
	if st.left() == 0 {
		s.sendMsg("今日挑战次数已用完。")
		return
	}
	team1, leader, snap := p.tmpTeam(m)
	if len(team1) == 0 {
		s.sendMsg("你的阵容为空，请添加护法！")
		return
	}
	boss := jyBossOf(jyNow())
	team2 := boss.team()
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := battle.SimulateWith(team1, team2, battle.Options{WarType: 1, Leaders: [2]int32{leader, 0},
		Formations: [2]battle.Formation{p.formation()}}, s.actRng())
	s.zfParam = res.ZfParam
	dmg := jyDamage(res.Record)
	rid := "jiuyou:" + strconv.FormatInt(recordSeq.Add(1), 10)
	s.fightRecord(rid, boss.name, boss.units[0].grade, r1, r2, res.Record)

	free := st.Fights < jyFree
	st.Fights++
	p.track(evJiuyou, 1)
	delta := &bagDelta{}
	if free {
		delta.items = append(delta.items, p.addItem(jyCoin, jyChallengeN)...)
	}
	p.saveLog(s)
	s.sendDelta(p, delta)

	var best, prev int64
	var rank int
	s.srv.jyDo(func(j *Jiuyou) bool {
		e := j.Entries[s.role.UID]
		if e == nil {
			e = &jyEntry{}
			j.Entries[s.role.UID] = e
		}
		prev = e.Damage
		e.Name, e.Grade = s.role.Name, s.role.Grade
		if dmg > e.Damage {
			e.Damage, e.Team = dmg, snap
		}
		best, rank = e.Damage, j.rankOf(s.role.UID)
		return true
	})
	win := xym.Msg{"max_hurt_today": best, "cur_hurt": dmg, "hurt_increment": max(0, dmg-prev), "rank": rank,
		"surpass_str": "", "rank_reward_id": 0, "challenge_reward_id": 0, "surpass_reward_id": 0}
	if rank > 0 {
		win["rank_reward_id"] = jyRankTiers[jyTier(rank)].reward
	}
	if free {
		win["challenge_reward_id"] = jyChallenge
	}
	s.SendExt("JIUYOU_PTO.S_WIN_BATTLE_INFO", win)
	s.jyMain(p)
}

// tmpTeam 按临时阵容 tmp_team[fight_pos, npcnum, wear_pos] + leader_num 建我方队伍（选阵界面 Mijing_FightTeamView /
// TempView 发的格式），返回队伍、队长技、阵容快照（带 pet_type，S_OPENING_BATTLE_TEAM_VIEW 用）。
func (p *Player) tmpTeam(m xym.Msg) ([]*battle.Fighter, int32, []xym.Msg) {
	tmp, _ := m["tmp_team"].([]xym.Msg)
	leaderNum, _ := m["leader_num"].(int64)
	var team []*battle.Fighter
	var leader int32
	used := map[int32]bool{}
	snap := []xym.Msg{}
	for _, t := range tmp {
		pos, npc := int(t["fight_pos"].(int64)), int32(t["npcnum"].(int64))
		pet := p.petByNpc(npc)
		if pet == nil || used[npc] || pos < 1 || pos > battle.FightSlots || gamedata.Pets[pet.Type] == nil {
			continue
		}
		used[npc] = true
		team = append(team, p.fighterOf(pet, pos))
		snap = append(snap, xym.Msg{"fight_pos": pos, "npcnum": npc, "wear_pos": t["wear_pos"], "pet_type": pet.Type})
		if int64(npc) == leaderNum {
			leader = gamedata.Pets[pet.Type].LeaderSkill
		}
	}
	p.applySpecialRelates(team)
	return team, leader, snap
}

// jyDamage 是战报里对敌方（站位 101 起）造成的总伤害。
func jyDamage(rec []byte) int64 {
	cmds, err := battle.DecodeRecord(rec)
	if err != nil {
		slog.Error("jiuyou record", "err", err)
		return 0
	}
	var n int64
	for _, c := range cmds {
		if c.ID != battle.CmdSubHP {
			continue
		}
		for _, t := range c.Targets {
			if t[0] > 100 {
				n += t[1]
			}
		}
	}
	return n
}

func onJYRank(s *Session, p *Player, m xym.Msg) {
	list := []xym.Msg{}
	var mine int32
	s.srv.jyDo(func(j *Jiuyou) bool {
		for i, uid := range j.ranked() {
			if i >= 100 {
				break
			}
			e := j.Entries[uid]
			r := jyRankTiers[jyTier(i+1)].reward
			if uid == s.role.UID {
				mine = r
			}
			list = append(list, xym.Msg{"has_real_player": 1, "player_name": e.Name, "rank": i + 1, "grade": e.Grade,
				"damage_value": e.Damage, "reward_id": r, "is_me": b2i(uid == s.role.UID), "xgrade": 0})
		}
		return false
	})
	s.SendExt("JIUYOU_PTO.S_RANK_VIEW_INFO", xym.Msg{"my_reward_id": mine, "ranking_list": list})
}

func onJYRankLineup(s *Session, p *Player, m xym.Msg) {
	rank := int(m["rank"].(int64))
	team := []xym.Msg{}
	s.srv.jyDo(func(j *Jiuyou) bool {
		if r := j.ranked(); rank >= 1 && rank <= len(r) {
			team = j.Entries[r[rank-1]].Team
		}
		return false
	})
	s.SendExt("JIUYOU_PTO.S_OPENING_BATTLE_TEAM_VIEW", xym.Msg{"tmp_team": team})
}

func (s *Session) jyShop(p *Player) {
	st := p.jiuyou()
	list := []xym.Msg{}
	for _, g := range jyGoods {
		left := max(0, g.limit-st.Shop[g.item])
		list = append(list, xym.Msg{"limited_type": 1, "sold_out": b2i(left == 0), "special_type": 0, "price": g.price,
			"item_id": g.item, "reward_id": g.reward, "limited_purchase_nums": left, "upper_limited_purchase": g.limit})
	}
	s.SendExt("JIUYOU_PTO.S_SHOP_VIEW_INFO", xym.Msg{"goods_list": list})
}

func onJYBuy(s *Session, p *Player, m xym.Msg) {
	id, n := int(m["id"].(int64)), int(m["cnt"].(int64))
	st := p.jiuyou()
	i := slices.IndexFunc(jyGoods, func(g jyGood) bool { return g.item == id })
	switch {
	case i < 0 || n < 1:
		s.retMsg("没有这件商品。")
	case st.Shop[id]+n > jyGoods[i].limit:
		s.retMsg("今日限购次数已用完，明日刷新。")
	case p.itemCount(jyCoin) < jyGoods[i].price*int32(n):
		s.retMsg("九幽币不足。")
	default:
		g := jyGoods[i]
		ch, rm := p.takeItems(jyCoin, g.price*int32(n))
		d := &bagDelta{items: ch, removed: rm}
		d.items = append(d.items, p.addItem(g.item, int32(n))...)
		d.gains = append(d.gains, gain{rtItem, g.item, int32(n)})
		st.Shop[id] += n
		p.saveLog(s)
		list := s.sendDelta(p, d)
		s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, list))
	}
	s.jyShop(p)
}
