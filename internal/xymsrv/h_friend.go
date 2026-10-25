package xymsrv

import (
	"fmt"
	"slices"
	"strconv"

	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

// 好友（FRIEND_PTO）：加好友（按玩家 id 申请，对方同意）、删除 / 拒绝、送礼收礼（友情点）、私信、切磋。
//
// 依据：logic/friend.lua（S_FRIEND_INFO 合并进好友列表、S_UPDATE_FRIEND_STAT 的 stat 位 1 在线 / 4 有私信，
// gift 位 1 可收 / 2 可送，收到「有私信」后客户端发 C_GET_FRIEND_MSG 取 S_FRIEND_MSG）；view/FriendView.lua、
// FriendDetailView.lua（C_FRIEND_OP op 0 收礼、1 送礼、5 删除 / 拒绝申请、6 一键送礼、7 一键收礼）；
// view/FriendAddView.lua（按 id 申请，force 1 / need_confirm 1）；抓包 session-02：拒绝申请（op 5）回 S_DEL_FRIEND{fid}，
// S_FRIEND_INFO / S_APPLY_FRIEND_EXT 的字段。
//
// 估计：好友上限 50；每天每个好友送 1 次，收礼每次 10 友情点、每天最多领 30 次；对方已向我申请时我再申请直接成为好友；
// 切磋不发奖励。

const (
	maxFriends    = 50
	giftFriendPt  = 10
	giftDailyMax  = 30
	statOnline    = 1
	statHasMsg    = 4
	giftCanRecv   = 1
	giftCanSend   = 2
	friendOpRecv  = 0
	friendOpSend  = 1
	friendOpDel   = 5
	friendOpAllS  = 6
	friendOpAllR  = 7
	maxFriendMsgs = 50
)

func init() {
	Handle("FRIEND_PTO.C_QUERY_ALL_FRIENDS", func(s *Session, p *Player, m xym.Msg) { s.sendFriends() })
	Handle("FRIEND_PTO.C_SHOW_APPLY_LIST", func(s *Session, p *Player, m xym.Msg) { s.sendApplies() })
	Handle("FRIEND_PTO.C_APPLY_FRIEND", onApplyFriend)
	Handle("FRIEND_PTO.C_ACCEPT_FRIEND", onAcceptFriend)
	Handle("FRIEND_PTO.C_FRIEND_OP", onFriendOp)
	Handle("FRIEND_PTO.C_FRIEND_MSG", onFriendMsg)
	Handle("FRIEND_PTO.C_GET_FRIEND_MSG", onGetFriendMsg)
	Handle("FRIEND_PTO.C_APPLY_GIFT_COUNT", func(s *Session, p *Player, m xym.Msg) { s.sendGiftCount() })
	Handle("FRIEND_PTO.C_FIGHT_FRIEND", onFightFriend)
}

// friendInfo 是 S_FRIEND_INFO.friends 的一项。
func (srv *Server) friendInfo(uid int32, msgs int, gift int) xym.Msg {
	ro := srv.roles.Get(uid)
	if ro == nil {
		return nil
	}
	online := srv.sessionOf(uid) != nil
	return xym.Msg{"uid": uid, "name": ro.Name, "grade": ro.Grade, "xgrade": 0, "icon": ro.Icon, "vip_icon_ts": 0,
		"vip": ro.VIP, "leader_skl": 0, "fight_point": srv.fightPoint(uid), "lastlogin": 0, "msglen": msgs,
		"gift_flag": b2i(gift&giftCanRecv != 0), "is_online": b2i(online), "dongfu_name": "", "dongfu_icon": 0,
		"lastlogout": 0, "name_ext": "", "id_ext": "", "app_channel": ""}
}

// fightPoint 取玩家登记过的出战阵容战力（宝卷 / 试炼登记的快照），没有就是 0。
func (srv *Server) fightPoint(uid int32) int {
	dbBoard.Lock()
	defer dbBoard.Unlock()
	if f := dbBoard.foes[uid]; f != nil {
		n := 0
		for _, x := range f.Team {
			n += int(x.Att)*4 + int(x.HP)
		}
		return n
	}
	return 0
}

// giftBits：me 对好友 f 的礼物状态（位 1 可收、位 2 可送）。调用方持有 so.mu。
func (so *Social) giftBits(me, f int32) int {
	g := 0
	if slices.Contains(so.Pending[me], f) {
		g |= giftCanRecv
	}
	if !slices.Contains(so.Sent[me], f) {
		g |= giftCanSend
	}
	return g
}

func (s *Session) sendFriends() {
	me := s.role.UID
	var list, stats []xym.Msg
	s.srv.social.do(func() bool {
		so := s.srv.social
		for _, f := range so.Friends[me] {
			g := so.giftBits(me, f)
			if info := s.srv.friendInfo(f, len(so.Msgs[me][f]), g); info != nil {
				list = append(list, info)
				stats = append(stats, s.srv.friendStat(f, len(so.Msgs[me][f]) > 0, g))
			}
		}
		return false
	})
	if list == nil {
		list = []xym.Msg{}
	}
	s.SendExt("FRIEND_PTO.S_FRIEND_INFO", xym.Msg{"friends": list})
	if len(stats) > 0 {
		s.SendExt("FRIEND_PTO.S_UPDATE_FRIEND_STAT", xym.Msg{"friends": stats})
	}
}

func (srv *Server) friendStat(uid int32, hasMsg bool, gift int) xym.Msg {
	st := 0
	if srv.sessionOf(uid) != nil {
		st |= statOnline
	}
	if hasMsg {
		st |= statHasMsg
	}
	return xym.Msg{"uid": uid, "stat": st, "help_cd": 0, "gift": gift}
}

func (s *Session) sendApplies() {
	var list []xym.Msg
	s.srv.social.do(func() bool {
		for _, uid := range s.srv.social.Applies[s.role.UID] {
			if ro := s.srv.roles.Get(uid); ro != nil {
				list = append(list, xym.Msg{"uid": uid, "name": ro.Name, "grade": ro.Grade, "icon": ro.Icon,
					"vip_icon_ts": 0, "fightpower": s.srv.fightPoint(uid)})
			}
		}
		return false
	})
	if list == nil {
		list = []xym.Msg{}
	}
	s.SendExt("FRIEND_PTO.S_APPLY_FRIEND_EXT", xym.Msg{"friends": list})
}

func (s *Session) sendGiftCount() {
	n := 0
	s.srv.social.do(func() bool { n = len(s.srv.social.Pending[s.role.UID]); return false })
	s.SendExt("FRIEND_PTO.S_COUNT_GIFT", xym.Msg{"count": n})
}

// makeFriends 让 a、b 互为好友（调用方持有 so.mu）。
func (so *Social) makeFriends(a, b int32) {
	so.Friends[a] = addUniq(so.Friends[a], b)
	so.Friends[b] = addUniq(so.Friends[b], a)
	so.Applies[a] = remove(so.Applies[a], b)
	so.Applies[b] = remove(so.Applies[b], a)
}

// notifyNewFriend 把 b 加进在线的 a 的好友列表。
func (srv *Server) notifyNewFriend(a, b int32) {
	if t := srv.sessionOf(a); t != nil {
		if info := srv.friendInfo(b, 0, giftCanSend); info != nil {
			t.SendExt("FRIEND_PTO.S_FRIEND_INFO", xym.Msg{"friends": []xym.Msg{info}})
		}
	}
}

// onApplyFriend：向 fid 申请好友（对方已向我申请过就直接成为好友）。
func onApplyFriend(s *Session, p *Player, m xym.Msg) {
	me, fid := s.role.UID, int32(m["fid"].(int64))
	if fid == me || s.srv.roles.Get(fid) == nil {
		s.retMsg("找不到这个玩家。")
		return
	}
	msg, direct := "", false
	s.srv.social.do(func() bool {
		so := s.srv.social
		switch {
		case so.isFriend(me, fid):
			msg = "他已经是您的好友了！"
		case len(so.Friends[me]) >= maxFriends || len(so.Friends[fid]) >= maxFriends:
			msg = fmt.Sprintf("好友已满（最多 %d 个）。", maxFriends)
		case slices.Contains(so.Applies[me], fid):
			so.makeFriends(me, fid)
			direct = true
			return true
		default:
			so.Applies[fid] = addUniq(so.Applies[fid], me)
			msg = "好友申请已发送，等待对方同意。"
			return true
		}
		return false
	})
	if direct {
		s.srv.notifyNewFriend(me, fid)
		s.srv.notifyNewFriend(fid, me)
		s.retMsg("你们已经成为好友。")
		return
	}
	s.retMsg(msg)
	if t := s.srv.sessionOf(fid); t != nil {
		t.sendApplies()
	}
}

func onAcceptFriend(s *Session, p *Player, m xym.Msg) {
	me, fid := s.role.UID, int32(m["fid"].(int64))
	ok, full := false, false
	s.srv.social.do(func() bool {
		so := s.srv.social
		if !slices.Contains(so.Applies[me], fid) {
			return false
		}
		if len(so.Friends[me]) >= maxFriends || len(so.Friends[fid]) >= maxFriends {
			full = true
			return false
		}
		so.makeFriends(me, fid)
		ok = true
		return true
	})
	switch {
	case full:
		s.retMsg(fmt.Sprintf("好友已满（最多 %d 个）。", maxFriends))
	case ok:
		s.srv.notifyNewFriend(me, fid)
		s.srv.notifyNewFriend(fid, me)
	}
	s.sendApplies()
}

// onFriendOp：0 收礼、1 送礼、5 删除好友 / 拒绝申请、6 一键送礼、7 一键收礼。
func onFriendOp(s *Session, p *Player, m xym.Msg) {
	me, fid, op := s.role.UID, int32(m["fid"].(int64)), int(m["op"].(int64))
	switch op {
	case friendOpDel:
		s.delFriend(me, fid)
	case friendOpSend, friendOpAllS:
		s.sendGifts(me, fid, op == friendOpAllS)
	case friendOpRecv, friendOpAllR:
		s.recvGifts(me, fid, op == friendOpAllR)
	}
}

func (s *Session) delFriend(me, fid int32) {
	wasFriend := false
	s.srv.social.do(func() bool {
		so := s.srv.social
		wasFriend = so.isFriend(me, fid)
		so.Applies[me] = remove(so.Applies[me], fid) // 拒绝申请
		so.Friends[me] = remove(so.Friends[me], fid)
		so.Friends[fid] = remove(so.Friends[fid], me)
		delete(so.Msgs[me], fid)
		return true
	})
	s.SendExt("FRIEND_PTO.S_DEL_FRIEND", xym.Msg{"fid": fid}) // 抓包：拒绝申请也回这条
	if wasFriend {
		if t := s.srv.sessionOf(fid); t != nil {
			t.SendExt("FRIEND_PTO.S_DEL_FRIEND", xym.Msg{"fid": me})
		}
	}
}

func (s *Session) sendGifts(me, fid int32, all bool) {
	var to []int32
	s.srv.social.do(func() bool {
		so := s.srv.social
		targets := []int32{fid}
		if all {
			targets = so.Friends[me]
		}
		for _, f := range targets {
			if so.isFriend(me, f) && !slices.Contains(so.Sent[me], f) {
				so.Sent[me] = append(so.Sent[me], f)
				so.Pending[f] = addUniq(so.Pending[f], me)
				to = append(to, f)
			}
		}
		return len(to) > 0
	})
	if len(to) == 0 {
		s.retMsg("今天已经送过礼物了。")
		return
	}
	stats := []xym.Msg{}
	for _, f := range to {
		stats = append(stats, s.srv.friendStatFor(me, f))
		if t := s.srv.sessionOf(f); t != nil {
			t.SendExt("FRIEND_PTO.S_UPDATE_FRIEND_STAT", xym.Msg{"friends": []xym.Msg{s.srv.friendStatFor(f, me)}})
			t.sendGiftCount()
		}
	}
	s.SendExt("FRIEND_PTO.S_UPDATE_FRIEND_STAT", xym.Msg{"friends": stats})
}

// friendStatFor 是 viewer 眼里好友 f 的状态。
func (srv *Server) friendStatFor(viewer, f int32) xym.Msg {
	var g int
	var hasMsg bool
	srv.social.do(func() bool {
		g = srv.social.giftBits(viewer, f)
		hasMsg = len(srv.social.Msgs[viewer][f]) > 0
		return false
	})
	return srv.friendStat(f, hasMsg, g)
}

func (s *Session) recvGifts(me, fid int32, all bool) {
	var from []int32
	full := false
	s.srv.social.do(func() bool {
		so := s.srv.social
		for _, f := range slices.Clone(so.Pending[me]) {
			if !all && f != fid {
				continue
			}
			if so.Got[me] >= giftDailyMax {
				full = true
				break
			}
			so.Pending[me] = remove(so.Pending[me], f)
			so.Got[me]++
			from = append(from, f)
		}
		return len(from) > 0
	})
	if len(from) == 0 {
		if full {
			s.retMsg(fmt.Sprintf("今天领取的礼物已达上限（%d 次）。", giftDailyMax))
		} else {
			s.retMsg("没有可以领取的礼物。")
		}
		return
	}
	pt := int32(giftFriendPt * len(from))
	s.roleUpdate(func(ro *Role) bool { ro.FriendPoint += pt; return true }, "friend_point")
	stats := []xym.Msg{}
	for _, f := range from {
		stats = append(stats, s.srv.friendStatFor(me, f))
	}
	s.SendExt("FRIEND_PTO.S_UPDATE_FRIEND_STAT", xym.Msg{"friends": stats})
	s.sendGiftCount()
	s.retMsg(fmt.Sprintf("领取礼物，获得 %d 友情点。", pt))
}

// onFriendMsg：给好友发私信，对方在线就提示「有新私信」（客户端随后来取）。
func onFriendMsg(s *Session, p *Player, m xym.Msg) {
	me, fid, text := s.role.UID, int32(m["fid"].(int64)), m["msg"].(string)
	ok := false
	s.srv.social.do(func() bool {
		so := s.srv.social
		if !so.isFriend(me, fid) {
			return false
		}
		if so.Msgs[fid] == nil {
			so.Msgs[fid] = map[int32][]string{}
		}
		l := append(so.Msgs[fid][me], text)
		so.Msgs[fid][me] = l[max(0, len(l)-maxFriendMsgs):]
		ok = true
		return true
	})
	if !ok {
		s.retMsg("对方不是您的好友。")
		return
	}
	if t := s.srv.sessionOf(fid); t != nil {
		t.SendExt("FRIEND_PTO.S_UPDATE_FRIEND_STAT", xym.Msg{"friends": []xym.Msg{s.srv.friendStatFor(fid, me)}})
	}
}

func onGetFriendMsg(s *Session, p *Player, m xym.Msg) {
	me, fid := s.role.UID, int32(m["fid"].(int64))
	var texts []string
	s.srv.social.do(func() bool {
		texts = s.srv.social.Msgs[me][fid]
		if len(texts) == 0 {
			return false
		}
		delete(s.srv.social.Msgs[me], fid)
		return true
	})
	if len(texts) == 0 {
		return
	}
	l := make([]xym.Msg, 0, len(texts))
	for _, t := range texts {
		l = append(l, xym.Msg{"text": t})
	}
	s.SendExt("FRIEND_PTO.S_FRIEND_MSG", xym.Msg{"fid": fid, "msg": l})
}

// onFightFriend：和好友（或聊天里点到的玩家）的出战阵容切磋，不发奖励。对方正在操作时稍后再试。
func onFightFriend(s *Session, p *Player, m xym.Msg) {
	fid := int32(m["fid"].(int64))
	ro := s.srv.roles.Get(fid)
	if ro == nil || fid == s.role.UID {
		s.retMsg("找不到这个玩家。")
		return
	}
	other, err := s.srv.players.Get(fid)
	if err != nil || !other.mu.TryLock() {
		s.retMsg("对方正忙，请稍后再试。")
		return
	}
	other.role = ro
	team2, _ := other.dyTeam(nil)
	w2 := prestigeOf(ro.Grade, other)
	other.mu.Unlock()
	team1, _ := p.dyTeam(nil)
	if len(team1) == 0 || len(team2) == 0 {
		s.retMsg("阵容为空，无法切磋。")
		return
	}
	for _, f := range team2 {
		f.Pos += 100
	}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), other.leaderSkill()}, other.formation(), s.actRng())
	s.fightRecordW(strconv.FormatInt(recordSeq.Add(1), 10), ro.Name, ro.Grade, w2, r1, r2, res.Record)
	p.track(evFriendFight, 1)
	r := rewardMsg(3, nil)
	if res.Win {
		r = rewardMsg(2, nil)
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
}
