package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 关卡扫荡（关卡详情「扫荡」「扫荡N次」→ TASK_PTO.C_EXPLORE{gateid, type 0 一次 / 1 多次}）：视作直接战胜本关，
// 扣体力、发与胜利相同的奖励，不打战斗。
//
// 依据：view/LevelDetailListView.lua（扫荡券数 S_GATE_LIST.tickets；券为 0 时按钮显示元宝图标；多次的次数是
// challange_times，为 0 时 5 次）；logic/activity.lua ACT_TANSUO 12 级开放；guide「[扫荡5次]视作直接战胜了5次
// 本关卡」。用户给定：每花 100 元宝得 1 张扫荡券（handlers.go 每条请求前后比元宝，见 trackGoldSpent）。
//
// 估计：每次扫荡用 1 张券，没券时每次 2 元宝；体力不够时能扫几次扫几次；奖励合成一个 S_REWARD_MSG{type 1}；
// 扫完回 S_GATE_LIST 刷新券数。

const (
	sweepGrade    = 12
	sweepTimes    = 5
	sweepGold     = 2
	goldPerTicket = 100
)

// trackGoldSpent 记花掉的元宝，每满 100 得 1 张扫荡券。
func (p *Player) trackGoldSpent(n int32) {
	if n <= 0 {
		return
	}
	p.GoldSpent += int64(n)
	p.SweepTickets += int(p.GoldSpent / goldPerTicket)
	p.GoldSpent %= goldPerTicket
}

func init() {
	Handle("TASK_PTO.C_EXPLORE", onExplore)
	Handle("TASK_PTO.C_APPLY_EXPLORE_TIME", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("TASK_PTO.S_SEND_EXPLORE_TIME", xym.Msg{"lefttime": 0})
	})
}

func onExplore(s *Session, p *Player, m xym.Msg) {
	gid, typ := int(m["gateid"].(int64)), 0
	n := 1
	if t, _ := m["type"].(int64); t == 1 {
		n = sweepTimes
	}
	g := gamedata.Gates[gid]
	for _, t := range []int{0, 1} {
		if p.Prog[t] != nil && p.Prog[t].Gates[gid] > 0 {
			typ = t
		}
	}
	switch {
	case s.role.Grade < sweepGrade:
		s.retMsg(fmt.Sprintf("%d 级开放扫荡。", sweepGrade))
		return
	case g == nil || p.Prog[typ] == nil || p.Prog[typ].Gates[gid] == 0:
		s.retMsg("通关后才能扫荡。")
		return
	}
	mode := g.Mode[typ]
	if mode.Limit > 0 {
		n = min(n, mode.Limit-p.Prog[typ].today(gid))
		if n < 1 {
			// 与挑战同样弹重置确认框（h_task.go onStartFight），确认后直接扫荡，不用再去点上面的挑战购买（用户要求）
			s.retMsg("")
			text := fmt.Sprintf("今天「%s」的挑战次数已用完（%d/%d）。#r花费#G%d元宝#n重置挑战次数并继续扫荡？", g.Name, mode.Limit, mode.Limit, gateResetGold)
			s.askBox(text, []string{"重置并扫荡", "关闭"}, func(opt int) {
				if opt != 0 || p.Prog[typ].today(gid) < mode.Limit || !s.spend(0, gateResetGold) {
					return
				}
				p.Prog[typ].Daily[gid] = 0
				p.saveLog(s)
				onExplore(s, p, m)
			})
			return
		}
	}
	if mode.Cost > 0 {
		n = min(n, s.role.TPoint/mode.Cost)
	}
	if n < 1 {
		s.retMsg("体力不足。")
		s.SendExt("ETC_PTO.S_OPEN_POINT_BUY", p.pointBuyMsg(pointTypeT))
		return
	}
	useTickets := min(n, p.SweepTickets)
	gold := int32(n-useTickets) * sweepGold
	if gold > 0 && !s.spend(0, gold) {
		return
	}
	p.SweepTickets -= useTickets

	rw := gamedata.Rewards[mode.Reward]
	if rw == nil {
		rw = &gamedata.RewardInfo{}
	}
	cash, mexp, pexp := int32(rw.Cash())*int32(n), int32(rw.MExp())*int32(n), int32(rw.PExp())*int32(n)
	role, err := s.srv.roles.Update(s.role.UID, func(ro *Role) bool {
		ro.TPoint -= mode.Cost * n
		ro.Cash += cash
		ro.AddExp(mexp)
		return true
	})
	if err != nil {
		s.logErr("save role", err)
		return
	}
	s.role = role
	var full []xym.Msg
	for fp := 1; fp <= lineupSlots; fp++ {
		if pet := p.petAtFightPos(fp); pet != nil {
			pet.addExp(pexp, role.PetCap())
			full = append(full, p.petFull(pet))
		}
	}
	drops := &bagDelta{}
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(p.UID)))
	p.track(evGate, n)
	for range n {
		p.clearGate(typ, gid)
		p.rollDrops(rw, rng, drops)
	}
	p.saveLog(s)

	s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{
		{"name": "tpoint", "val": role.TPoint}, {"name": "grade", "val": role.Grade},
		{"name": "exp", "val": role.Exp}, {"name": "next_exp", "val": nextExp(role.Grade)},
		{"name": "cash", "val": role.Cash},
	}})
	if len(full) > 0 {
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": full})
	}
	s.SendExt("TASK_PTO.S_GATE_INFO", s.gateInfoMsg(p, gid, typ))
	list := s.sendDelta(p, drops)
	s.sendGateList(p, gid/100, typ)
	list = append(list, xym.Msg{"rtype": rtItem, "type": itemCash, "count": cash})
	r := rewardMsg(boxNormal, list)
	r["mexp"], r["pexp"] = mexp, pexp
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
}
