package xymsrv

import (
	"slices"
	"strconv"

	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

// 洞府守卫（组织页三个守卫位：人界 human / 地界 earth / 天界 heaven，view/PartyView.lua GuardTexs、
// view/PartyAdminiView.lua 内务「守卫」页）：
//   - S_SEND_DONGFU_INFO.guard 是三个守卫 {usernum, icon, vip_icon_ts, name}，icon 0 显示「空」（抓包三位都有人）。
//   - 府主 / 洞主在内务里 C_GET_CHANGE_GUARD_MEMBER_LIST{guard_type}（带锁等 S_SEND_CHANGE_GUARD_MEMBER_LIST）选人，
//     C_CHANGE_GUARD{other, guard_type} 后等 S_SEND_DONGFU_INFO。
//   - 点守卫 C_CHALLENGE_DONGFU_GUARD{type}：和守卫的出战阵容切磋。
//   - 争夺（dongfu_gank.go）里敌方洞府的守卫就是这三位。
//
// 估计：一人只能守一个位置；挑战守卫是切磋，胜负都不换人（奖励弹窗按胜负 2 / 3）。

var dfGuardTypes = []string{"human", "earth", "heaven"}

func (srv *Server) dfGuardList(df *Dongfu) []xym.Msg {
	var out []xym.Msg
	for _, uid := range df.Guards {
		m := xym.Msg{"usernum": uid, "icon": 0, "vip_icon_ts": 0, "name": ""}
		if uid != 0 && df.member(uid) != nil {
			name, icon, _ := srv.roleBrief(uid)
			m["icon"], m["name"] = icon, name
		} else {
			m["usernum"] = 0
		}
		out = append(out, m)
	}
	return out
}

func onChangeGuardList(s *Session, p *Player, m xym.Msg) {
	gt := m["guard_type"].(string)
	var list []xym.Msg
	admin := false
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		if admin = isAdmin(me.Position); !admin {
			return false
		}
		for _, x := range df.Members {
			name, icon, grade := s.srv.roleBrief(x.UID)
			online := 0
			if s.srv.sessionOf(x.UID) != nil {
				online = 1
			}
			list = append(list, xym.Msg{"usernum": x.UID, "name": name, "icon": icon, "vip_icon_ts": 0, "grade": grade,
				"week_contribute": x.week(), "dongfu_contribute": x.Contri, "position": x.Position, "is_online": online})
		}
		return false
	})
	if !admin {
		s.sendMsg("只有府主和洞主可以任命守卫。")
	}
	for _, x := range list { // 出了洞府锁再取战力（realFoe 会查洞府）
		x["fight_point"] = s.srv.fightPointOf(x["usernum"].(int32), p)
	}
	if list == nil {
		list = []xym.Msg{}
	}
	s.SendExt("DONGFU_PTO.S_SEND_CHANGE_GUARD_MEMBER_LIST", xym.Msg{"list": list, "guard_type": gt})
}

func onChangeGuard(s *Session, p *Player, m xym.Msg) {
	other, gt := int32(m["other"].(int64)), m["guard_type"].(string)
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		i := slices.Index(dfGuardTypes, gt)
		ok := false
		switch {
		case !isAdmin(me.Position):
			s.sendMsg("只有府主和洞主可以任命守卫。")
		case i < 0 || df.member(other) == nil:
			s.sendMsg("该成员不在洞府里。")
		default:
			for k, uid := range df.Guards { // 一人只守一个位置
				if uid == other {
					df.Guards[k] = 0
				}
			}
			df.Guards[i] = other
			name, _, _ := s.srv.roleBrief(other)
			df.log(name + " 被任命为守卫")
			ok = true
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
		return ok
	})
}

func onChallengeGuard(s *Session, p *Player, m xym.Msg) {
	i := slices.Index(dfGuardTypes, m["type"].(string))
	var uid int32
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		if i >= 0 {
			uid = df.Guards[i]
		}
		return false
	})
	switch {
	case i < 0 || uid == 0:
		s.retMsg("该守卫位空缺。")
		return
	case uid == s.role.UID:
		s.retMsg("这是你自己。")
		return
	}
	foe := s.srv.realFoe(uid, p)
	team1, _ := p.dyTeam(nil)
	if foe == nil || len(foe.Team) == 0 || len(team1) == 0 {
		s.retMsg("对方正忙，请稍后再试。")
		return
	}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(foe.Team)
	res := s.simulate(p, team1, battle.CloneTeam(foe.Team), [2]int32{p.leaderSkill(), foe.Leader}, foe.Zf, s.actRng())
	s.fightRecordW(strconv.FormatInt(recordSeq.Add(1), 10), foe.Name, foe.Grade, foe.W, r1, r2, res.Record)
	typ := 3
	if res.Win {
		typ = 2
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(typ, nil))
}

// fightPointOf 是 uid 的出战战力（论道快照 / 当前阵容）。
func (srv *Server) fightPointOf(uid int32, me *Player) int {
	if f := srv.realFoe(uid, me); f != nil {
		return f.FP
	}
	return 0
}

func init() {
	Handle("DONGFU_PTO.C_GET_CHANGE_GUARD_MEMBER_LIST", onChangeGuardList)
	Handle("DONGFU_PTO.C_CHANGE_GUARD", onChangeGuard)
	Handle("DONGFU_PTO.C_CHALLENGE_DONGFU_GUARD", onChallengeGuard)
}
