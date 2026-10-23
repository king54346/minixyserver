package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

// 试炼协议（ENDLESS_FIGHT_PTO）。规则与依据见 shilian.go；回包顺序照官方抓包 session-02。
// C_START_FIGHT / C_SELECT_ADDON / C_CONTINUOUS_FIGHT 等着 S_OPEN_FIGHT 的锁，S_SEND_ADD / S_SEND_STATE
// 也带这把锁，所以拒绝时回 S_SEND_STATE（没有进行中的一轮）或当前界面解锁。

func init() {
	Handle("ENDLESS_FIGHT_PTO.C_OPEN_FIGHT", func(s *Session, p *Player, m xym.Msg) { s.slView(p) })
	Handle("ENDLESS_FIGHT_PTO.C_START_FIGHT", func(s *Session, p *Player, m xym.Msg) { s.slStart(p, false) })
	Handle("ENDLESS_FIGHT_PTO.C_RESTART_LAST", func(s *Session, p *Player, m xym.Msg) { s.slStart(p, true) })
	Handle("ENDLESS_FIGHT_PTO.C_DO_FIGHT", onSlFight)
	Handle("ENDLESS_FIGHT_PTO.C_CONTINUOUS_FIGHT", onSlContinuous)
	Handle("ENDLESS_FIGHT_PTO.C_SELECT_ADDON", onSlAddon)
	Handle("ENDLESS_FIGHT_PTO.C_OPEN_NPC_TEAM", onSlNpcTeam)
	Handle("ENDLESS_FIGHT_PTO.C_OPEN_RANK", onSlRank)
	Handle("ENDLESS_FIGHT_PTO.C_OPEN_PLAYER_TEAM", onSlPlayerTeam)
}

func (s *Session) slRng() *rand.Rand {
	return rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(s.role.UID)+19))
}

// slView：有进行中的一轮就回当前界面（选加成 / 选对手），否则 S_SEND_STATE 打开试炼首页。
func (s *Session) slView(p *Player) {
	d := p.sl()
	switch {
	case d.Run == nil:
		s.slState(p)
	case d.Run.Offer != nil:
		s.slAddMsg(p)
	default:
		s.slOpen(p)
	}
}

func (s *Session) slState(p *Player) {
	d := p.sl()
	s.SendExt("ENDLESS_FIGHT_PTO.S_SEND_STATE", xym.Msg{"state": slStateMain, "maxstar": d.MaxStar,
		"maxstage": d.MaxStage, "times": d.Times, "last_stage": d.Last})
}

func (d *ShilianState) progressMsg() xym.Msg {
	r := d.Run
	return xym.Msg{"attadd": r.Add[1], "hpadd": r.Add[2], "magicadd": r.Add[3], "healadd": r.Add[4],
		"allstar": r.AllStar, "currstar": r.CurStar, "maxstar": d.MaxStar, "maxstage": d.MaxStage, "stage": r.Stage}
}

// slOpen 是 S_OPEN_FIGHT：第 Stage+1 关的三个对手（魔王关只有第一个）。
func (s *Session) slOpen(p *Player) {
	d := p.sl()
	r := d.Run
	m := d.progressMsg()
	st := r.Stage + 1
	m["name"] = fmt.Sprintf("第%d关", st)
	mine, _ := p.dyTeam(nil)
	for i, f := range r.Foes {
		k := fmt.Sprintf("fight%d", i+1)
		if f == nil {
			m[k+"face"], m[k+"count1"], m[k+"count2"], m[k+"reward"] = 0, 0, 0, 0
			continue
		}
		m[k+"face"], m[k+"count1"], m[k+"count2"], m[k+"reward"] = f.face(), len(mine), len(f.Types), b2i(i == 0)
	}
	m["is_con"] = b2i(d.Best >= slConMinBest && st <= slMaxStage)
	m["baoxiang"] = r.Box
	s.SendExt("ENDLESS_FIGHT_PTO.S_OPEN_FIGHT", m)
}

func (s *Session) slAddMsg(p *Player) {
	d := p.sl()
	m := d.progressMsg()
	for i, o := range d.Run.Offer {
		m[fmt.Sprintf("type%d", i+1)], m[fmt.Sprintf("add%d", i+1)] = o[0], o[1]
	}
	s.SendExt("ENDLESS_FIGHT_PTO.S_SEND_ADD", m)
}

// slStart 开始新一轮（restart：从上一轮结束时的关数、星数与加成接着打），消耗一次今日次数。
func (s *Session) slStart(p *Player, restart bool) {
	d := p.sl()
	if d.Times <= 0 {
		s.retMsg("今日试炼次数已用完。")
		s.slState(p)
		return
	}
	r := &slRun{}
	if restart {
		if d.LastRun == nil {
			s.retMsg("没有可以继续的试炼记录。")
			s.slState(p)
			return
		}
		c := *d.LastRun
		r = &slRun{Stage: c.Stage, AllStar: c.AllStar, CurStar: c.CurStar, Add: c.Add}
	}
	d.Times--
	p.track(evShilian, 1)
	d.Runs++
	r.nextFoes(s.slRng())
	d.Run = r
	p.saveLog(s)
	s.slOpen(p)
}

// slBattle 打第 Stage+1 关第 idx 个对手；record 为 false 时只算胜负（连续死战）。
func (s *Session) slBattle(p *Player, idx int, record bool) (bool, error) {
	d := p.sl()
	r := d.Run
	st := r.Stage + 1
	team1 := p.slTeam(r)
	if len(team1) == 0 {
		return false, fmt.Errorf("阵容为空，请先上阵。")
	}
	team2 := r.Foes[idx-1].team(st)
	if len(team2) == 0 {
		return false, fmt.Errorf("试炼对手配置缺失。")
	}
	rng := s.slRng()
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), 0}, battle.Formation{}, rng)
	if record {
		s.SendExt("FIGHT_PTO.S_FIGHT_RECORD", xym.Msg{
			"canop": 14, "rid": fmt.Sprintf("shilian-%d-%d", d.Runs, st),
			"w1": s.prestige(), "level1": s.role.Grade, "name1": s.role.Name, "team1": teamMsgs(r1),
			"level2": slLevel(st), "name2": fmt.Sprintf("第%d关", st), "team2": teamMsgs(r2),
			"otherparam": recordParams(r1, r2), "record": res.Record,
		})
	}
	return res.Win, nil
}

// win 记一关胜利：星、银两、财宝；魔王关按概率记一个魔王魂魄。返回本关银两。
func (r *slRun) win(idx int, rng *rand.Rand) int64 {
	r.Stage++
	mul := 4 - idx
	r.AllStar += slStarPerStage * mul
	r.CurStar += slStarPerStage * mul
	cash := int64(slCashPerStage * r.Stage * mul)
	r.Cash += cash
	if idx == 1 {
		r.Box++
		if slBoss(r.Stage) && rng.IntN(100) < slBossSoulPct {
			r.Souls = append(r.Souls, r.Foes[0].face())
		}
	}
	return cash
}

func (s *Session) slAddCash(n int64) {
	role, err := s.srv.roles.Update(s.role.UID, func(ro *Role) bool { ro.Cash += int32(n); return true })
	if err != nil {
		s.logErr("save role", err)
		return
	}
	s.role = role
	s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "cash", "val": role.Cash}}})
}

// slSettle 结算财宝和魔王魂魄（S_REWARD_MSG{type 1}）。
func (s *Session) slSettle(p *Player) {
	r := p.sl().Run
	delta := &bagDelta{}
	if r.Box > 0 {
		delta.items = append(delta.items, p.addItem(slBoxItem, int32(r.Box))...)
		delta.gains = append(delta.gains, gain{rtItem, slBoxItem, int32(r.Box)})
	}
	for _, t := range r.Souls {
		p.addSoul(t, 1, delta)
	}
	r.Box, r.Souls = 0, nil
	if len(delta.gains) == 0 {
		return
	}
	list := s.sendDelta(p, delta)
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"cash": 0, "mexp": 0, "pexp": 0, "ling": 0, "gold": 0, "soul": 0,
		"type": 1, "item": 0, "no_fight_again": 1, "star": 0, "list": list})
}

// slAfterWin：打完 501 关结束；每 5 关结算并给加成；否则下一关。
func (s *Session) slAfterWin(p *Player) {
	r := p.sl().Run
	switch {
	case r.Stage >= slMaxStage:
		s.slSettle(p)
		s.slFinish(p)
	case r.Stage%slSettleEvery == 0:
		r.nextFoes(s.slRng())
		r.rollOffer(s.slRng())
		p.saveLog(s)
		s.slSettle(p)
		s.slAddMsg(p)
	default:
		r.nextFoes(s.slRng())
		p.saveLog(s)
		s.slOpen(p)
	}
}

// slFinish 结束一轮：更新今日最佳、记下续打用的进度，汇总银两（S_REWARD_MSG{type 13}）后回首页。
func (s *Session) slFinish(p *Player) {
	d := p.sl()
	r := d.Run
	d.Run = nil
	if r.AllStar > d.MaxStar || r.AllStar == d.MaxStar && r.Stage > d.MaxStage {
		d.MaxStar, d.MaxStage = r.AllStar, r.Stage
	}
	d.Best = max(d.Best, r.Stage)
	d.Last = min(r.Stage, slMaxStage-1)
	d.LastRun = &slRun{Stage: d.Last, AllStar: r.AllStar, CurStar: r.CurStar, Add: r.Add}
	p.saveLog(s)
	group, pos := s.slRegister(p)
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"cash": r.Cash, "mexp": 0, "pexp": 0, "ling": 0, "gold": 0, "soul": 0,
		"type": 13, "item": 0, "no_fight_again": 1, "star": 0, "list": []xym.Msg{}})
	s.SendExt("ENDLESS_FIGHT_PTO.S_SEND_SHARE_INFO", xym.Msg{"maxstar": d.MaxStar, "maxstage": d.MaxStage,
		"onrank": b2i(pos > 0), "rankgroup": group, "rankpos": pos, "gotsharereward": 0, "icon": s.role.Icon,
		"vip_icon_ts": 0})
	s.slState(p)
}

// slRegister 把今日最佳登记到排行，返回所在组和名次（0 = 未上榜）。
func (s *Session) slRegister(p *Player) (group, pos int) {
	d := p.sl()
	power := 0
	team := []xym.Msg{}
	for fp := 1; fp <= lineupSlots; fp++ {
		if pet := p.petAtFightPos(fp); pet != nil {
			power += petPower(pet)
			team = append(team, xym.Msg{"pos": fp, "type": pet.Type, "skillfull": 0})
		}
	}
	slRank.put(slRow{UID: s.role.UID, Name: s.role.Name, Grade: s.role.Grade, Icon: s.role.Icon,
		Star: d.MaxStar, Stage: d.MaxStage, FightPoint: power, Team: team})
	group = slGroup(s.role.Grade)
	for i, r := range slRank.list(group) {
		if r.UID == s.role.UID {
			pos = i + 1
		}
	}
	return group, pos
}

func onSlFight(s *Session, p *Player, m xym.Msg) {
	d := p.sl()
	idx := int(m["idx"].(int64))
	r := d.Run
	if r == nil || r.Offer != nil || idx < 1 || idx > 3 || r.Foes[idx-1] == nil {
		s.retMsg("试炼状态已变化，请重新进入。")
		return
	}
	win, err := s.slBattle(p, idx, true)
	if err != nil {
		s.retMsg(err.Error())
		return
	}
	if !win {
		s.slFinish(p)
		return
	}
	s.slAddCash(r.win(idx, s.slRng()))
	s.slAfterWin(p)
}

// onSlContinuous：连续死战——逐关和死战对手打（不发战报），打到下一个 5 的倍数关；中途输了就结束这一轮。
func onSlContinuous(s *Session, p *Player, m xym.Msg) {
	d := p.sl()
	r := d.Run
	if r == nil || r.Offer != nil || d.Best < slConMinBest {
		s.retMsg("现在不能连续死战。")
		s.slView(p)
		return
	}
	var cash int64
	rng := s.slRng()
	for {
		win, err := s.slBattle(p, 1, false)
		if err != nil {
			s.retMsg(err.Error())
			s.slView(p)
			return
		}
		if !win {
			if cash > 0 {
				s.slAddCash(cash)
			}
			s.slFinish(p)
			return
		}
		cash += r.win(1, rng)
		if r.Stage%slSettleEvery == 0 || r.Stage >= slMaxStage {
			break
		}
		r.nextFoes(rng)
	}
	s.slAddCash(cash)
	s.slAfterWin(p)
}

func onSlAddon(s *Session, p *Player, m xym.Msg) {
	d := p.sl()
	idx := int(m["idx"].(int64))
	r := d.Run
	switch {
	case r == nil:
		s.slState(p)
		return
	case r.Offer == nil:
		s.slOpen(p)
		return
	case idx < 1 || idx > len(r.Offer):
		s.slAddMsg(p)
		return
	case r.CurStar < slNeedStar[idx-1]:
		s.retMsg("剩余星数不足！")
		s.slAddMsg(p)
		return
	}
	o := r.Offer[idx-1]
	r.CurStar -= slNeedStar[idx-1]
	r.Add[o[0]] += o[1]
	r.Offer = nil
	p.saveLog(s)
	s.slOpen(p)
}

// onSlNpcTeam：S_SEND_NPC_TEAM，对手阵容详情（pos 1 起）。
func onSlNpcTeam(s *Session, p *Player, m xym.Msg) {
	list := []xym.Msg{}
	idx := int(m["idx"].(int64))
	if r := p.sl().Run; r != nil && idx >= 1 && idx <= 3 && r.Foes[idx-1] != nil {
		for _, f := range r.Foes[idx-1].team(r.Stage + 1) {
			tals := []xym.Msg{}
			for _, t := range f.Talents {
				tals = append(tals, xym.Msg{"tid": t})
			}
			list = append(list, xym.Msg{"pos": f.Pos - 100, "type": f.Icon, "skillfull": 0, "grade": f.Grade,
				"att": f.Att, "hp": f.HP, "speed": f.Speed,
				"magic": []xym.Msg{{"mid": f.Magic, "lev": f.MagicLv}}, "talent": tals})
		}
	}
	s.SendExt("ENDLESS_FIGHT_PTO.S_SEND_NPC_TEAM", xym.Msg{"list": list})
}

// onSlRank：S_OPEN_RANK（组内前 50；抓包里尾部的 grade/name/face… 全为 0）。
func onSlRank(s *Session, p *Player, m xym.Msg) {
	group := int(m["type"].(int64))
	s.slRegister(p)
	rank := []xym.Msg{}
	userpos := 0
	for i, r := range slRank.list(group) {
		if r.UID == s.role.UID {
			userpos = i + 1
		}
		if i < 50 {
			rank = append(rank, xym.Msg{"pos": i + 1, "grade": r.Grade, "name": r.Name, "star": r.Star, "stage": r.Stage,
				"icon": r.Icon, "uid": r.UID, "vip_icon_ts": 0, "fight_point": r.FightPoint, "xgrade": 0})
		}
	}
	s.SendExt("ENDLESS_FIGHT_PTO.S_OPEN_RANK", xym.Msg{"userpos": userpos, "type": group, "rank": rank})
}

// onSlPlayerTeam：排行里第 pos 名的出战阵容（登记时的快照）。
func onSlPlayerTeam(s *Session, p *Player, m xym.Msg) {
	group, pos := int(m["type"].(int64)), int(m["pos"].(int64))
	list := []xym.Msg{}
	if l := slRank.list(group); pos >= 1 && pos <= len(l) {
		list = l[pos-1].Team
	}
	s.SendExt("ENDLESS_FIGHT_PTO.S_SEND_PLAYER_TEAM", xym.Msg{"list": list})
}
