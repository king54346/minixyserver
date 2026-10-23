package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"sync"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 宝卷（夺宝，DUOBAO_PTO）：和其他玩家的阵容战斗收集宝卷残页，凑齐当前星级要的残页数得到一张该星级的宝卷，
// 宝卷兑换成相应星级的装备 / 法术（3、4 星还能换宝石）。
//
// 依据：ui/DuoBao_Help 规则文字——每场消耗 2 点活力；胜利得一片残页，失败不损失；无论输赢有一定概率获得额外惊喜；
// 凑齐残页自动得到完整宝卷；每次得到宝卷后重置当前宝卷级别，重置到 1 星时额外得到一张古老宝卷（随机兑换 1–5 星）；
// 胜利 +2、失败 +1 五星宝卷保底进度，满 1500 必得五星宝卷。view/DuobaoMainView.lua（1–5 星要 3 / 4 / 5 / 6 / 8 片残页）、
// DuobaoTargetView.lua（C_FIGHT_DUOBAO.idx = 对手序号 − 1、「更换对手」重发 C_OPEN_DUOBAO）、
// DuobaoTicketView.lua（C_USE_TICKET.type 0 装备 / 1 法术 / 2 宝石，宝石只有 3、4 星宝卷可选，level 0 = 古老宝卷）。
// 抓包 session-01：3 个对手；失败 S_REWARD_MSG{type 3}、胜利 {type 2, cash 100}（有时多一本法术书）；回包顺序
// S_FIGHT_RECORD、S_ATTR_UPDATE、背包、S_OPEN_DUOBAO、S_OPEN_TICKET、S_SEND_BAODI_COUNT、S_REWARD_MSG；
// 1 星凑满 3 片后得 1 星宝卷、重置成 3 星；3 星凑满 5 片后得 3 星宝卷、重置成 1 星并多一张古老宝卷；
// 兑换回 S_UPDATE_SUIT、S_OPEN_TICKET、S_SEND_BAODI_COUNT、S_REWARD_MSG{type 1}（1 星宝卷换 1 星装备…）。
//
// 估计：对手先取打开过宝卷的其他玩家（登记时的出战阵容），不够用 NPC 补，NPC 由易到难：卡牌等级为我方平均的
// 80% / 90% / 100%，星级逐个加高；重置的星级按 40 / 30 / 18 / 9 / 3% 抽；额外惊喜 15% 一本 1–3 星法术书；
// 古老宝卷按 40 / 30 / 18 / 9 / 3% 抽星级；3 星宝卷换一级宝石袋（10309）、4 星换二级宝石袋（10403）。

const (
	dbCost      = 2 // 活力
	dbFoes      = 3
	dbBaodiMax  = 1500
	dbWinCash   = 100
	dbSurprise  = 15
	dbTicketMax = 5
)

var (
	dbNeed       = [dbTicketMax + 1]int{0, 3, 4, 5, 6, 8}
	dbStarW      = [5]int{40, 30, 18, 9, 3}
	dbGem        = map[int]int{3: 10309, 4: 10403}
	dbNPCGrade   = [dbFoes]int{80, 90, 100}
	dbNPCStarW   = [dbFoes][5]int{{40, 35, 20, 5, 0}, {20, 30, 30, 15, 5}, {5, 15, 30, 30, 20}}
	dbSurnames   = []rune("赵钱孙李周吴郑王冯陈褚卫蒋沈韩杨朱秦许何吕施张孔曹严华金魏陶姜")
	dbGivennames = []string{"逍遥", "无忌", "长风", "清扬", "若尘", "子墨", "青云", "流云", "惊鸿", "问天", "醉月", "听雨"}
)

// DuobaoState 是宝卷存档（Player.Duobao）。
type DuobaoState struct {
	Class   int                  `json:"class"` // 当前宝卷星级
	Count   int                  `json:"count"` // 当前残页
	Tickets [dbTicketMax + 1]int `json:"tickets"`
	Baodi   int                  `json:"baodi"`
	Fights  int                  `json:"fights"`
	Foes    []*dbFoe             `json:"foes,omitempty"`
}

type dbFoe struct {
	UID   int32             `json:"uid"`
	Name  string            `json:"name"`
	Level int               `json:"level"`
	Face  int               `json:"face"`
	Team  []*battle.Fighter `json:"team"`
	Zf    battle.Formation  `json:"zf,omitempty"` // 对手的阵法（zhenfa.go）
}

func (p *Player) duobao() *DuobaoState {
	if p.Duobao == nil {
		p.Duobao = &DuobaoState{Class: 1}
	}
	return p.Duobao
}

// ---- 对手：打开过宝卷的玩家登记出战阵容 ----

var dbBoard = struct {
	sync.Mutex
	foes map[int32]*dbFoe
}{foes: map[int32]*dbFoe{}}

func (s *Session) dbRegister(team []*battle.Fighter) {
	if len(team) == 0 {
		return
	}
	f := &dbFoe{UID: s.role.UID, Name: s.role.Name, Level: s.role.Grade, Face: s.role.Icon, Team: battle.CloneTeam(team)}
	if s.player != nil {
		f.Zf = s.player.formation()
	}
	for _, x := range f.Team {
		x.Pos += 100
	}
	dbBoard.Lock()
	dbBoard.foes[s.role.UID] = f
	dbBoard.Unlock()
}

// npcFoe 是第 i 个（0 起）NPC 对手：卡牌等级为 grade 的 dbNPCGrade[i]%。
func npcFoe(i, grade, n, level int, rng *rand.Rand) *dbFoe {
	f := &dbFoe{Name: string(dbSurnames[rng.IntN(len(dbSurnames))]) + dbGivennames[rng.IntN(len(dbGivennames))],
		Level: level, Face: 1 + rng.IntN(2)}
	g := max(1, grade*dbNPCGrade[i]/100)
	for slot := 1; slot <= n; slot++ {
		if t := slPick(randStar(dbNPCStarW[i], rng), rng); t != 0 {
			if x := petFighter(t, g, slot, 0); x != nil {
				f.Team = append(f.Team, x)
			}
		}
	}
	return f
}

// rollFoes 换一批对手。
func (s *Session) dbRollFoes(p *Player) {
	d := p.duobao()
	team, _ := p.dyTeam(nil)
	s.dbRegister(team)
	rng := s.actRng()
	d.Foes = nil
	dbBoard.Lock()
	var others []*dbFoe
	for uid, f := range dbBoard.foes {
		if uid != s.role.UID {
			others = append(others, f)
		}
	}
	dbBoard.Unlock()
	rng.Shuffle(len(others), func(i, j int) { others[i], others[j] = others[j], others[i] })
	for _, f := range others[:min(len(others), dbFoes-1)] { // 至少留一个 NPC，保证有打得过的
		d.Foes = append(d.Foes, &dbFoe{UID: f.UID, Name: f.Name, Level: f.Level, Face: f.Face, Team: battle.CloneTeam(f.Team), Zf: f.Zf})
	}
	grade := 1
	if len(team) > 0 {
		sum := 0
		for _, f := range team {
			sum += f.Grade
		}
		grade = sum / len(team)
	}
	for i := 0; len(d.Foes) < dbFoes; i++ {
		d.Foes = append(d.Foes, npcFoe(min(i, dbFoes-1), grade, max(5, len(team)), s.role.Grade, rng))
	}
	p.saveLog(s)
}

func (s *Session) dbOpenMsg(p *Player) {
	d := p.duobao()
	list := []xym.Msg{}
	for _, f := range d.Foes {
		var icon [2]int32
		for i, x := range f.Team[:min(2, len(f.Team))] {
			icon[i] = x.Icon
		}
		list = append(list, xym.Msg{"face": f.Face, "uid": f.UID, "vip_icon_ts": 0, "name": f.Name, "level": f.Level,
			"xgrade": 0, "icon1": icon[0], "icon2": icon[1]})
	}
	s.SendExt("DUOBAO_PTO.S_OPEN_DUOBAO", xym.Msg{"class": d.Class, "count": d.Count, "fight_list": list})
}

func (s *Session) dbTicketMsg(p *Player) {
	d := p.duobao()
	m := xym.Msg{}
	for i, n := range d.Tickets {
		m[fmt.Sprintf("count%d", i)] = n
	}
	s.SendExt("DUOBAO_PTO.S_OPEN_TICKET", m)
}

func (s *Session) dbBaodiMsg(p *Player) {
	s.SendExt("DUOBAO_PTO.S_SEND_BAODI_COUNT", xym.Msg{"baodi_count": p.duobao().Baodi})
}

func init() {
	Handle("DUOBAO_PTO.C_OPEN_DUOBAO", func(s *Session, p *Player, m xym.Msg) {
		s.dbRollFoes(p)
		s.dbOpenMsg(p)
	})
	Handle("DUOBAO_PTO.C_GET_BAODI_COUNT", func(s *Session, p *Player, m xym.Msg) { s.dbBaodiMsg(p) })
	Handle("DUOBAO_PTO.C_OPEN_TICKET", func(s *Session, p *Player, m xym.Msg) { s.dbTicketMsg(p) })
	Handle("DUOBAO_PTO.C_FIGHT_DUOBAO", onDuobaoFight)
	Handle("DUOBAO_PTO.C_USE_TICKET", onUseTicket)
}

// win 记一场胜利：残页 +1，凑满得宝卷并重置星级（重置到 1 星多一张古老宝卷）；保底 +2，满了得五星宝卷。
func (d *DuobaoState) win(rng *rand.Rand) {
	d.Count++
	if d.Count >= dbNeed[d.Class] {
		d.Tickets[d.Class]++
		d.Count = 0
		d.Class = randStar(dbStarW, rng)
		if d.Class == 1 {
			d.Tickets[0]++
		}
	}
	d.baodi(2)
}

func (d *DuobaoState) baodi(n int) {
	if d.Baodi += n; d.Baodi >= dbBaodiMax {
		d.Baodi -= dbBaodiMax
		d.Tickets[dbTicketMax]++
	}
}

func onDuobaoFight(s *Session, p *Player, m xym.Msg) {
	d := p.duobao()
	idx := int(m["idx"].(int64))
	if len(d.Foes) == 0 {
		s.dbRollFoes(p)
	}
	if idx < 0 || idx >= len(d.Foes) {
		s.retMsg("对手已变化，请重新选择。")
		s.dbOpenMsg(p)
		return
	}
	team1, _ := p.dyTeam(nil)
	if len(team1) == 0 {
		s.retMsg("阵容为空，请先上阵。")
		return
	}
	if !s.roleUpdate(func(ro *Role) bool {
		if ro.HPoint < dbCost {
			return false
		}
		ro.HPoint -= dbCost
		return true
	}) {
		s.retMsg("活力不足，夺宝每场消耗两点活力。")
		return
	}
	foe := d.Foes[idx]
	team2 := battle.CloneTeam(foe.Team)
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	rng := s.actRng()
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), 0}, foe.Zf, rng)
	d.Fights++
	p.track(evDuobao, 1)
	if res.Win {
		p.track(evDuobaoWin, 1)
	}
	rid := fmt.Sprintf("duobao:%d", d.Fights)
	s.fightRecord(rid, foe.Name, foe.Level, r1, r2, res.Record)
	delta := &bagDelta{}
	cash := int32(0)
	if res.Win {
		d.win(rng)
		cash = dbWinCash
	} else {
		d.baodi(1)
	}
	if rng.IntN(100) < dbSurprise {
		if l := gamedata.BooksByStar[1+rng.IntN(3)]; len(l) > 0 {
			p.giveBook(l[rng.IntN(len(l))], delta)
		}
	}
	p.saveLog(s)
	s.roleUpdate(func(ro *Role) bool { ro.Cash += cash; return true }, "hpoint", "cash", "gold", "soulexp", "grade", "exp", "next_exp")
	list := s.sendDelta(p, delta)
	s.dbOpenMsg(p)
	s.dbTicketMsg(p)
	s.dbBaodiMsg(p)
	typ := 3
	if res.Win {
		typ = 2
	}
	r := rewardMsg(typ, list)
	r["cash"], r["record"], r["no_fight_again"] = cash, rid, 0
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
}

// ticketStar 是一张 level 星宝卷兑换的星级（古老宝卷随机 1–5 星）。
func ticketStar(level int, rng *rand.Rand) int {
	if level == 0 {
		return randStar(dbStarW, rng)
	}
	return level
}

// onUseTicket：兑换 count 张 level 星宝卷为装备（type 0）/ 法术（1）/ 宝石（2，只有 3、4 星）。
func onUseTicket(s *Session, p *Player, m xym.Msg) {
	d := p.duobao()
	typ, level, count := int(m["type"].(int64)), int(m["level"].(int64)), int(m["count"].(int64))
	switch {
	case level < 0 || level > dbTicketMax || count <= 0 || count > d.Tickets[level]:
		s.retMsg("宝卷数量不足。")
		s.dbTicketMsg(p)
		return
	case typ == 2 && dbGem[level] == 0:
		s.retMsg("只有三星、四星宝卷可以兑换宝石。")
		s.dbTicketMsg(p)
		return
	}
	rng := s.actRng()
	delta := &bagDelta{}
	for range count {
		star := ticketStar(level, rng)
		switch typ {
		case 0:
			var l []int
			for _, id := range gamedata.SuitsByStar[star] {
				if gamedata.Suits[id].Kind != "ITK_SHOE" { // 鞋只出自解忧
					l = append(l, id)
				}
			}
			if len(l) > 0 {
				p.giveSuit(l[rng.IntN(len(l))], delta)
			}
		case 1:
			if l := gamedata.BooksByStar[star]; len(l) > 0 {
				p.giveBook(l[rng.IntN(len(l))], delta)
			}
		case 2:
			delta.items = append(delta.items, p.addItem(dbGem[level], 1)...)
			delta.gains = append(delta.gains, gain{rtItem, dbGem[level], 1})
		}
	}
	d.Tickets[level] -= count
	p.saveLog(s)
	list := s.sendDelta(p, delta)
	s.dbTicketMsg(p)
	s.dbBaodiMsg(p)
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, list))
}
