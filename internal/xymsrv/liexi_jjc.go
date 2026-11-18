package xymsrv

import (
	"cmp"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 时空竞技场（LIEXI_PTO.C_JJC_*）、积分榜、时空宝藏（C_SHOP_*）。
//
// 流程（view/TemporalRiftMainView.lua、TemporalRiftPvpTeamSelectView.lua）：主界面 jjc_state 0 时点「挑战」发
// C_JJC_BUY_TICKET（扣当天次数）→ state 1；布阵界面「匹配」发 C_JJC_MATCH → S_JJC_MATCH_END → 客户端重拉
// S_JJC_SEND_LINEUP（带对手）→ state 2；「开战」发 C_JJC_FIGHT → 战报 + S_JJC_FIGHT_END{state 0}。
//
// 估计（T9）：初始积分 1500，胜 +30、负 −20（不低于 0）；每胜 1 徽记；本周首次 5 / 10 / 15 连胜各得 5 / 10 / 20 徽记；
// 对手优先找积分相近的其他玩家（用他们最近一次竞技场的阵容快照），没有就用论道的假对手；
// 周二刷新时按上周积分给前 100 名发邮件（第 1 名 200 徽记 + 500 元宝，2–10 名 100 + 200，11–100 名 50 + 100）。

const (
	lxInitScore = 1500
	lxWinScore  = 30
	lxLoseScore = 20
	lxRankSize  = 100
)

var lxStreaks = []struct{ n, crest int }{{5, 5}, {10, 10}, {15, 20}}

type lxUser struct {
	Name    string            `json:"name"`
	Grade   int               `json:"grade"`
	Icon    int32             `json:"icon"`
	FP      int               `json:"fp"`
	Score   int               `json:"score"`
	Win     int               `json:"win"`
	Loss    int               `json:"loss"`
	Team    []*battle.Fighter `json:"team,omitempty"`
	Details []lxDetail        `json:"details,omitempty"`
}

// Liexi 是全服的竞技场积分榜（每周清空，上周榜单用来发奖）。
type Liexi struct {
	mu    sync.Mutex
	st    store.Store
	Week  int               `json:"week"`
	Users map[int32]*lxUser `json:"users"`
}

func openLiexi(st store.Store) *Liexi {
	l := &Liexi{st: st}
	loadGlobal(st, globalLiexi, l)
	return l
}

// do 持锁执行 fn（先滚动周：上周榜单发奖），fn 返回 true 时存档。
func (srv *Server) lxDo(fn func(l *Liexi) bool) {
	l := srv.liexi
	l.mu.Lock()
	dirty := false
	var pay []lxRankRow
	if wk := lxWeekOf(lxNow()); l.Week != wk {
		if l.Week != 0 {
			pay = l.rank()
		}
		l.Week, l.Users = wk, map[int32]*lxUser{}
		dirty = true
	}
	if l.Users == nil {
		l.Users = map[int32]*lxUser{}
	}
	if fn(l) || dirty {
		saveGlobal(l.st, globalLiexi, l)
	}
	l.mu.Unlock()
	for i, r := range pay { // 发奖在锁外（邮件有自己的锁）
		crest, gold := lxRankReward(i + 1)
		spec := MailSpec{Title: "时空竞技场排行奖励", Gold: gold,
			Text: fmt.Sprintf("上周时空竞技场积分 %d，本服排名第 %d，奖励时空徽记 ×%d（已直接发放）。", r.Score, i+1, crest)}
		if _, err := srv.SendMail(r.UID, spec); err != nil {
			slog.Error("liexi rank mail", "uid", r.UID, "err", err)
		}
		srv.lxAddCrest(r.UID, crest)
	}
}

func lxRankReward(rank int) (crest int, gold int32) {
	switch {
	case rank == 1:
		return 200, 500
	case rank <= 10:
		return 100, 200
	default:
		return 50, 100
	}
}

// lxAddCrest 给不一定在线的玩家加徽记（存进玩家存档）。
func (srv *Server) lxAddCrest(uid int32, n int) {
	q, err := srv.players.Get(uid)
	if err != nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	x := q.liexi()
	x.Crest += n
	x.Exp += n
	if err := q.save(); err != nil {
		slog.Error("liexi crest save", "uid", uid, "err", err)
	}
}

type lxRankRow struct {
	UID   int32
	Score int
}

func (l *Liexi) rank() []lxRankRow {
	var rows []lxRankRow
	for uid, u := range l.Users {
		if u.Win+u.Loss > 0 {
			rows = append(rows, lxRankRow{uid, u.Score})
		}
	}
	slices.SortFunc(rows, func(a, b lxRankRow) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.UID, b.UID)
	})
	return rows[:min(len(rows), lxRankSize)]
}

func (l *Liexi) user(uid int32, ro *Role) *lxUser {
	u := l.Users[uid]
	if u == nil {
		u = &lxUser{Score: lxInitScore}
		l.Users[uid] = u
	}
	if ro != nil {
		u.Name, u.Grade, u.Icon = ro.Name, ro.Grade, int32(ro.Icon)
	}
	return u
}

func init() {
	Handle("LIEXI_PTO.C_JJC_BUY_TICKET", onLxJjcBuy)
	Handle("LIEXI_PTO.C_JJC_GET_LINEUP", func(s *Session, p *Player, m xym.Msg) { s.lxJjcLineup(p) })
	Handle("LIEXI_PTO.C_JJC_UPDATE_TEAM", func(s *Session, p *Player, m xym.Msg) { s.lxUpdateTeam(p, 0, m) })
	Handle("LIEXI_PTO.C_JJC_QUERY_PET", func(s *Session, p *Player, m xym.Msg) { s.lxQueryPet(p, 0, m) })
	Handle("LIEXI_PTO.C_JJC_GET_TAL_LIST", func(s *Session, p *Player, m xym.Msg) { s.lxTalList(p, 0, m) })
	Handle("LIEXI_PTO.C_JJC_GET_TAL_BAG", func(s *Session, p *Player, m xym.Msg) { s.lxTalBag(p, 0) })
	Handle("LIEXI_PTO.C_JJC_ARM_TAL", func(s *Session, p *Player, m xym.Msg) { s.lxArm(p, 0, m, true) })
	Handle("LIEXI_PTO.C_JJC_UNARM_TAL", func(s *Session, p *Player, m xym.Msg) { s.lxArm(p, 0, m, false) })
	Handle("LIEXI_PTO.C_JJC_MATCH", onLxJjcMatch)
	Handle("LIEXI_PTO.C_JJC_FIGHT", onLxJjcFight)
	Handle("LIEXI_PTO.C_JJC_GET_RANK_LIST", func(s *Session, p *Player, m xym.Msg) { s.lxRankList(p) })
	Handle("LIEXI_PTO.C_SHOP_GET_GOODS_LIST", func(s *Session, p *Player, m xym.Msg) {
		shop, _ := m["shop"].(int64)
		s.lxShop(p, int(shop))
	})
	Handle("LIEXI_PTO.C_SHOP_BUY_GOODS", onLxBuy)
}

func onLxJjcBuy(s *Session, p *Player, m xym.Msg) {
	x := p.liexi()
	switch {
	case s.role.Grade < lxGrade:
		s.retMsg(fmt.Sprintf("时空裂隙需要人物等级达到 %d 级。", lxGrade))
		return
	case x.JjcUsed >= lxJjcDaily:
		s.retMsg("今天的竞技场挑战次数已用完。")
		return
	}
	if x.Jjc.State == lxStateClosed {
		x.JjcUsed++
		x.Jjc.State, x.Jjc.Foe = 1, 0
		if x.JTeam == nil {
			x.JTeam = p.lxDefaultTeam()
		}
		p.saveLog(s)
	}
	s.SendExt("LIEXI_PTO.S_JJC_BUY_TICKET_SUC", nil)
}

// lxSnapshot 是自己当前竞技场阵容的快照（给别人当对手、上排行榜）。
func (p *Player) lxSnapshot() ([]*battle.Fighter, []lxDetail, int) {
	x := p.liexi()
	c := x.ctx(0)
	cands := p.lxCandidates()
	var team []*battle.Fighter
	var details []lxDetail
	fp := 0
	for pos := 1; pos <= lineupSlots; pos++ {
		pet := cands[c.team[pos]]
		if pet == nil {
			continue
		}
		lp := p.lxPetOf(pet, x.TJPlus)
		if lp == nil {
			continue
		}
		team = append(team, lp.fighter(pos, c.armed[pet.Type]))
		details = append(details, lp.detailRec(pos, c.armed[pet.Type]))
		fp += int(lp.att)*4 + int(lp.hp)
	}
	return team, details, fp
}

func (s *Session) lxJjcLineup(p *Player) {
	x := p.liexi()
	if x.JTeam == nil {
		x.JTeam = p.lxDefaultTeam()
	}
	msg := xym.Msg{"state": x.Jjc.State, "factor_list": idList(lxFactors(x.Week, 0)), "my_team": teamMsg(x.JTeam),
		"candidate_pets": p.candidateMsg(), "vs_team": []xym.Msg{}, "vs_user_name": "", "vs_host_name": "", "vs_fight_point": 0, "vs_score": 0}
	me := s.role.UID
	s.srv.lxDo(func(l *Liexi) bool {
		u := l.user(me, s.role)
		msg["score"], msg["win_cnt"], msg["loss_cnt"] = u.Score, u.Win, u.Loss
		msg["rank_pos"] = 0
		for i, r := range l.rank() {
			if r.UID == me {
				msg["rank_pos"] = i + 1
			}
		}
		if x.Jjc.State == 2 {
			if f := l.foe(x.Jjc.Foe); f != nil {
				vs := []xym.Msg{}
				for _, d := range f.Details {
					vs = append(vs, d.msg())
				}
				msg["vs_team"], msg["vs_user_name"], msg["vs_host_name"] = vs, f.Name, ttServerName
				msg["vs_fight_point"], msg["vs_score"] = f.FP, f.Score
			}
		}
		return false
	})
	s.SendExt("LIEXI_PTO.S_JJC_SEND_LINEUP", msg)
}

// foe 是对手（真实玩家的快照，或论道假对手临时生成的）。
func (l *Liexi) foe(uid int32) *lxUser {
	if u := l.Users[uid]; u != nil && len(u.Team) > 0 {
		return u
	}
	if isFakeUID(uid) {
		f := fakeFoeCached(uid)
		u := &lxUser{Name: f.Name, Grade: f.Grade, Icon: f.Icon, FP: f.FP, Score: lxInitScore}
		for _, x := range f.Team {
			c := *x
			c.Pos = x.Pos % 100
			u.Team = append(u.Team, &c)
			d := lxDetail{PetType: x.Icon, Pos: c.Pos, Grade: x.Grade, At: x.Att, HP: x.HP, Sp: x.Speed, TfmID: x.Magic, TfmLv: x.MagicLv}
			d.Orgin = append(d.Orgin, x.Talents[:min(3, len(x.Talents))]...)
			u.Details = append(u.Details, d)
		}
		return u
	}
	return nil
}

func onLxJjcMatch(s *Session, p *Player, m xym.Msg) {
	x := p.liexi()
	if x.Jjc.State == lxStateClosed {
		s.SendExt("LIEXI_PTO.S_JJC_MATCH_END", xym.Msg{"state": x.Jjc.State})
		return
	}
	team, details, fp := p.lxSnapshot()
	me := s.role.UID
	rng := s.actRng()
	s.srv.lxDo(func(l *Liexi) bool {
		u := l.user(me, s.role)
		u.Team, u.Details, u.FP = team, details, fp
		var cands []int32
		for uid, o := range l.Users {
			if uid != me && len(o.Team) > 0 {
				cands = append(cands, uid)
			}
		}
		slices.SortFunc(cands, func(a, b int32) int {
			return cmp.Compare(abs(l.Users[a].Score-u.Score), abs(l.Users[b].Score-u.Score))
		})
		if len(cands) > 0 {
			x.Jjc.Foe = cands[rng.IntN(min(5, len(cands)))]
		} else {
			x.Jjc.Foe = ldFakeUIDBase + int32(rng.IntN(ldFakeCount))
		}
		return true
	})
	x.Jjc.State = 2
	p.saveLog(s)
	s.SendExt("LIEXI_PTO.S_JJC_MATCH_END", xym.Msg{"state": x.Jjc.State})
}

func onLxJjcFight(s *Session, p *Player, m xym.Msg) {
	x := p.liexi()
	if x.Jjc.State != 2 {
		s.retMsg("请先匹配对手。")
		return
	}
	team1, details, fp := p.lxSnapshot()
	if len(team1) == 0 {
		s.retMsg("阵容为空，请先上阵护法。")
		return
	}
	var foe *lxUser
	s.srv.lxDo(func(l *Liexi) bool {
		foe = l.foe(x.Jjc.Foe)
		return false
	})
	if foe == nil {
		x.Jjc.State = 1
		s.retMsg("对手已经离开，请重新匹配。")
		s.SendExt("LIEXI_PTO.S_JJC_FIGHT_END", xym.Msg{"state": x.Jjc.State})
		return
	}
	factors := lxFactors(x.Week, 0)
	team2 := battle.CloneTeam(foe.Team)
	for _, f := range team2 {
		f.Pos = f.Pos%100 + 100
	}
	lxApplyStatFactors(team1, factors)
	lxApplyStatFactors(team2, factors)
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulateWith(team1, team2, battle.Options{Factors: factors}, s.actRng())
	rid := "liexi:" + strconv.FormatInt(recordSeq.Add(1), 10)
	s.fightRecord(rid, foe.Name, foe.Grade, r1, r2, res.Record)
	me := s.role.UID
	crest := 0
	s.srv.lxDo(func(l *Liexi) bool {
		u := l.user(me, s.role)
		u.Team, u.Details, u.FP = battle.CloneTeam(r1), details, fp
		for _, f := range u.Team {
			f.Pos %= 100
		}
		if res.Win {
			u.Score += lxWinScore
			u.Win++
		} else {
			u.Score = max(u.Score-lxLoseScore, 0)
			u.Loss++
		}
		return true
	})
	if res.Win {
		crest = 1
		x.Jjc.Streak++
		for _, st := range lxStreaks {
			if x.Jjc.Streak == st.n && !slices.Contains(x.Jjc.Got, st.n) {
				x.Jjc.Got = append(x.Jjc.Got, st.n)
				crest += st.crest
				s.sendMsg(fmt.Sprintf("本周首次 %d 连胜，额外获得时空徽记 ×%d！", st.n, st.crest))
			}
		}
	} else {
		x.Jjc.Streak = 0
	}
	x.Crest += crest
	x.Exp += crest
	x.Jjc.State, x.Jjc.Foe = lxStateClosed, 0
	p.saveLog(s)
	typ := boxFightLose
	if res.Win {
		typ = 2
	}
	rw := rewardMsg(typ, nil)
	rw["record"] = rid
	s.SendExt("ETC_PTO.S_REWARD_MSG", rw)
	if crest > 0 {
		s.sendMsg(fmt.Sprintf("获得时空徽记 ×%d。", crest))
	}
	s.SendExt("LIEXI_PTO.S_JJC_FIGHT_END", xym.Msg{"state": x.Jjc.State})
}

func (s *Session) lxRankList(p *Player) {
	me := s.role.UID
	msg := xym.Msg{"champion": []xym.Msg{}, "local_rank": []xym.Msg{}, "my_score": lxInitScore, "my_local_rank_pos": 0}
	s.srv.lxDo(func(l *Liexi) bool {
		if u := l.Users[me]; u != nil {
			msg["my_score"] = u.Score
		}
		var local []xym.Msg
		for i, r := range l.rank() {
			u := l.Users[r.UID]
			team := []xym.Msg{}
			for _, d := range u.Details {
				team = append(team, d.msg())
			}
			row := xym.Msg{"uid": r.UID, "score": u.Score, "icon": u.Icon, "grade": u.Grade, "xgrade": 0, "name": u.Name,
				"fpoint": u.FP, "dongfu_name": "", "dongfu_icon": 0, "vip_icon_ts": 0, "team": team}
			if r.UID == me && i < 127 {
				msg["my_local_rank_pos"] = i + 1
			}
			if i == 0 {
				c := xym.Msg{"hostname": ttServerName}
				for k, v := range row {
					c[k] = v
				}
				msg["champion"] = []xym.Msg{c}
			}
			local = append(local, row)
		}
		if local != nil {
			msg["local_rank"] = local
		}
		return false
	})
	s.SendExt("LIEXI_PTO.S_JJC_SEND_RANK_LIST", msg)
}

// ---- 时空宝藏 ----

// lxGood 是一件兑换商品。goods_type 是奖励类型（config.lua REWARD_TYPE_*：3 道具、100 神通、101 天匠激发）。
type lxGood struct {
	typ    int
	id     int32
	count  int32
	name   string
	gold   int32
	pieces int
	crest  int
	limit  int
	effect string
}

const (
	lxShopGain     = 1 // 增益兑换（本周有效）
	lxShopTreasure = 2 // 宝物兑换
	rtTal          = 100
	rtTJ           = 101
)

// lxGoods 是本周两个商店的商品：增益 = 天匠激发 +1 + 4 个随机 5 级自选神通；宝物固定。
func lxGoods(week, shop int) []lxGood {
	if shop == lxShopTreasure {
		return []lxGood{
			{typ: rtItem, id: 10317, count: 1, name: "超级请神符", crest: 60, limit: 2, effect: "可在登峰造极中招募一次"},
			{typ: rtItem, id: 11461, count: 1, name: "灵仙请神符", crest: 40, limit: 3, effect: "可在每日推荐中招募一次"},
			{typ: rtItem, id: 10310, count: 1, name: "培养吸纳石", crest: 20, limit: 2, effect: "取出护法培养用掉的战力丹与气血丹"},
			{typ: rtItem, id: 10311, count: 1, name: "精修吸纳石", crest: 20, limit: 2, effect: "取出法术书精修用掉的材料"},
			{typ: rtItem, id: 10126, count: 10, name: "战力丹×10", pieces: 100, limit: 10, effect: "护法培养攻击用"},
			{typ: rtItem, id: 10127, count: 10, name: "气血丹×10", pieces: 100, limit: 10, effect: "护法培养生命用"},
		}
	}
	out := []lxGood{{typ: rtTJ, id: 1, count: 1, name: "天匠激发+1", gold: 150, limit: 5, effect: "激发等级本周裂隙中额外+1"}}
	rng := lxWeekRng(week, 7)
	groups := slices.Clone(gamedata.LiexiTals)
	rng.Shuffle(len(groups), func(i, j int) { groups[i], groups[j] = groups[j], groups[i] })
	for _, g := range groups[:min(4, len(groups))] {
		t := g.Members[min(5, len(g.Members))-1]
		name := fmt.Sprintf("%s%d", g.Name, min(5, len(g.Members)))
		out = append(out, lxGood{typ: rtTal, id: t, count: 1, name: name, pieces: 100, limit: 1, effect: "本周裂隙关卡与竞技场中可装备"})
	}
	return out
}

// lxTitles 是称谓与所需经验（累计得到的碎片 + 徽记）。
var lxTitles = []struct {
	name string
	exp  int
}{{"初入时空", 0}, {"时空行者", 2000}, {"裂隙旅人", 6000}, {"时空守望", 15000}, {"时空主宰", 40000}}

func lxTitle(exp int) (name string, cur, next int) {
	i := 0
	for j, t := range lxTitles {
		if exp >= t.exp {
			i = j
		}
	}
	if i+1 < len(lxTitles) {
		return lxTitles[i].name, exp, lxTitles[i+1].exp
	}
	return lxTitles[i].name, exp, 0
}

func (s *Session) lxShop(p *Player, shop int) {
	if shop != lxShopTreasure {
		shop = lxShopGain
	}
	x := p.liexi()
	list := []xym.Msg{}
	for i, g := range lxGoods(x.Week, shop) {
		left := g.limit - x.Bought[fmt.Sprintf("%d-%d", shop, i+1)]
		list = append(list, xym.Msg{"idx": i + 1, "goods_type": g.typ, "goods_id": g.id, "goods_count": g.count,
			"goods_name": g.name, "gold_need": g.gold, "pieces_need": g.pieces, "crest_need": g.crest, "effect": g.effect,
			"limit_times": max(left, 0)})
	}
	title, exp, maxExp := lxTitle(x.Exp)
	s.SendExt("LIEXI_PTO.S_SHOP_SEND_GOODS_LIST", xym.Msg{"crest_count": x.Crest, "pieces_count": x.Pieces, "exp": exp,
		"max_exp": maxExp, "stitle": title, "shop": shop, "goods_list": list})
}

func onLxBuy(s *Session, p *Player, m xym.Msg) {
	x := p.liexi()
	shop64, _ := m["shop"].(int64)
	idx64, _ := m["idx"].(int64)
	n64, _ := m["buy_num"].(int64)
	shop, idx, n := int(shop64), int(idx64), int(max(n64, 1))
	goods := lxGoods(x.Week, shop)
	if idx < 1 || idx > len(goods) {
		return
	}
	g := goods[idx-1]
	key := fmt.Sprintf("%d-%d", shop, idx)
	switch {
	case x.Bought[key]+n > g.limit:
		s.sendMsg("超过本周的兑换次数。")
	case x.Pieces < g.pieces*n:
		s.sendMsg("时空碎片不足。")
	case x.Crest < g.crest*n:
		s.sendMsg("时空徽记不足。")
	case g.gold > 0 && !s.spend(0, g.gold*int32(n)):
	default:
		x.Pieces -= g.pieces * n
		x.Crest -= g.crest * n
		x.Bought[key] += n
		switch g.typ {
		case rtTJ:
			x.TJPlus = min(x.TJPlus+n, 5)
		case rtTal:
			for range n {
				x.Shop = append(x.Shop, g.id)
			}
		case rtItem:
			d := &bagDelta{}
			d.items = p.addItem(int(g.id), g.count*int32(n))
			s.sendDelta(p, d)
		}
		p.saveLog(s)
		s.sendMsg(fmt.Sprintf("兑换成功：%s ×%d。", g.name, n))
	}
	s.lxShop(p, shop)
}
