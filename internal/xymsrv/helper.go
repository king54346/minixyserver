package xymsrv

import (
	"cmp"
	"fmt"
	"slices"
	"sync"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 援助（HELPER_PTO）：闯关布阵时第 9 格「援助」借用好友 / 路人的队长护法上阵。
//
// 客户端流程（view/HelperView.lua）：点援助格 → C_NEED_HELPER{gid, type} → S_NEW_HELPER{lskill 我的队长技,
// friends[], helpers[]}（每项 uid / name / grade / icon / vip / lskill / leadershape 队长护法编号 / skillfull）；
// 列表里 friends 标 helpType 1、可获友情点 10，helpers（路人）标 2、可获 5；点头像 C_LOOK_PET{uid, type} →
// PET_PTO.S_SHOW_PET；选好后 C_START_FIGHT{uid, htype, posN}，援助护法的 fight_pos 是 9（可拖到其他格）。
// 打完对路人发 S_OPEN_ADD_FRIEND{uid, name, grade, leadershape, friend_point}，客户端弹「加好友」提示，确认或提示
// 播完后自己发 C_APPLY_FRIEND（view/MapView.lua）。
//
// 本服规则（用户要求）：
//   - 同一名玩家的援助护法 30 分钟内只能借用一次（借过的不出现在列表里）；
//   - 借用时借用方得友情点（好友 10 / 路人 5，与客户端列表上的数字一致），被借用方同样得一份；
//   - 援助护法是对方队长：对方的队长技与自己的队长技同时生效；
//   - 列表先好友后路人，两类都有（有人可选时）且有上限：路人最多 helperStrangers 个，总数最多 helperListMax 个。

const (
	helperFightPos  = 9 // 援助护法在 C_START_FIGHT 里的 fight_pos
	helperCooldown  = 30 * 60
	helperListMax   = 10
	helperStrangers = 5
	helperPtFriend  = 10
	helperPtOther   = 5
	helpTypeFriend  = 1
	helpTypeOther   = 2
)

func init() {
	Handle("HELPER_PTO.C_NEED_HELPER", onNeedHelper)
	Handle("HELPER_PTO.C_LOOK_PET", onLookHelperPet)
}

// helperCard 是一名玩家可借出的队长护法（Fighter 的站位由借用方决定）。
type helperCard struct {
	UID       int32
	Name      string
	Grade     int
	Icon      int
	VIP       int
	LSkill    int32
	Shape     int32
	SkillFull bool
	Fighter   *battle.Fighter
	Talents   []int32
}

var helperSnaps = struct {
	sync.Mutex
	m map[int32]*helperCard
}{m: map[int32]*helperCard{}}

// helperOf 取玩家 uid 当前出战阵容的队长护法；对方正被别的请求占用时用上次看到的快照。
func (srv *Server) helperOf(uid int32) *helperCard {
	ro := srv.roles.Get(uid)
	if ro == nil {
		return nil
	}
	q, err := srv.players.Get(uid)
	if err != nil || !q.mu.TryLock() {
		helperSnaps.Lock()
		defer helperSnaps.Unlock()
		return helperSnaps.m[uid]
	}
	defer q.mu.Unlock()
	if q.role == nil {
		q.role = ro
	}
	lu := q.lineup(q.FightNo)
	if lu == nil {
		return nil
	}
	pet := q.petAtFightPos(lu.Leader)
	if pet == nil || gamedata.Pets[pet.Type] == nil {
		return nil
	}
	pi := gamedata.Pets[pet.Type]
	tals, _ := q.activeTalents(pet, pi)
	c := &helperCard{UID: uid, Name: ro.Name, Grade: ro.Grade, Icon: ro.Icon, VIP: ro.VIP, LSkill: pi.LeaderSkill,
		Shape: pet.Type, SkillFull: pet.talentsFull(pi), Fighter: q.fighterOf(pet, 0), Talents: slices.Clone(tals)}
	helperSnaps.Lock()
	helperSnaps.m[uid] = c
	helperSnaps.Unlock()
	return c
}

func (c *helperCard) listMsg() xym.Msg {
	return xym.Msg{"uid": c.UID, "name": c.Name, "grade": c.Grade, "icon": c.Icon, "vip": c.VIP,
		"lskill": c.LSkill, "leadershape": c.Shape, "skillfull": b2i(c.SkillFull)}
}

// helperReady：30 分钟内没借过 uid。
func (p *Player) helperReady(uid int32, now int64) bool {
	return now-p.HelperUsed[uid] >= helperCooldown
}

// onNeedHelper：援助列表。好友在前（在线的、等级高的优先），后面是路人（全服其他真实玩家，随机）。
func onNeedHelper(s *Session, p *Player, _ xym.Msg) {
	me, now := s.role.UID, time.Now().Unix()
	var friends []int32
	s.srv.social.do(func() bool {
		friends = slices.Clone(s.srv.social.Friends[me])
		return false
	})
	var others []int32
	for _, uid := range s.srv.roles.UIDs() {
		if uid != me && !slices.Contains(friends, uid) && p.helperReady(uid, now) {
			others = append(others, uid)
		}
	}
	rng := s.actRng()
	rng.Shuffle(len(others), func(i, j int) { others[i], others[j] = others[j], others[i] })
	var hs []xym.Msg
	for _, uid := range others {
		if len(hs) >= helperStrangers {
			break
		}
		if c := s.srv.helperOf(uid); c != nil {
			hs = append(hs, c.listMsg())
		}
	}
	var fcs []*helperCard
	for _, uid := range friends {
		if p.helperReady(uid, now) {
			if c := s.srv.helperOf(uid); c != nil {
				fcs = append(fcs, c)
			}
		}
	}
	online := func(uid int32) int { return b2i(s.srv.sessionOf(uid) != nil) }
	slices.SortStableFunc(fcs, func(a, b *helperCard) int {
		if c := cmp.Compare(online(b.UID), online(a.UID)); c != 0 {
			return c
		}
		return cmp.Compare(b.Grade, a.Grade)
	})
	fs := []xym.Msg{}
	for _, c := range fcs[:min(len(fcs), helperListMax-len(hs))] {
		fs = append(fs, c.listMsg())
	}
	if hs == nil {
		hs = []xym.Msg{}
	}
	s.SendExt("HELPER_PTO.S_NEW_HELPER", xym.Msg{"lskill": p.leaderSkill(), "friends": fs, "helpers": hs})
}

// onLookHelperPet：援助列表 / 援助格点头像看护法详情（抓包 session-02：C_LOOK_PET{uid, type 1} → S_SHOW_PET）。
func onLookHelperPet(s *Session, _ *Player, m xym.Msg) {
	uid, _ := m["uid"].(int64)
	c := s.srv.helperOf(int32(uid))
	if c == nil {
		s.retMsg("找不到这名护法。")
		return
	}
	f := c.Fighter
	magic := []xym.Msg{}
	if f.Magic != 0 {
		magic = append(magic, xym.Msg{"mid": f.Magic, "lev": f.MagicLv})
	}
	for _, b := range f.Books {
		magic = append(magic, xym.Msg{"mid": b[0], "lev": b[1]})
	}
	tal := []xym.Msg{}
	for _, t := range c.Talents {
		tal = append(tal, xym.Msg{"tid": t})
	}
	s.SendExt("PET_PTO.S_SHOW_PET", xym.Msg{"type": c.Shape, "grade": f.Grade, "att": f.Att, "hp": f.HP,
		"speed": f.Speed, "magic": magic, "talent": tal})
}

// fightHelper 是本次闯关借用的援助：C_START_FIGHT 的 uid 非 0 且 30 分钟内没借过时有效。
func (s *Session) fightHelper(p *Player, m xym.Msg) *helperCard {
	uid, _ := m["uid"].(int64)
	if uid == 0 || int32(uid) == s.role.UID || !p.helperReady(int32(uid), time.Now().Unix()) {
		return nil
	}
	return s.srv.helperOf(int32(uid))
}

// helperFighter 是援助护法站在战斗站位 pos 上的单位（与对方阵容里的同一张卡互不影响）。
func (c *helperCard) fighterAt(pos int) *battle.Fighter {
	f := battle.CloneTeam([]*battle.Fighter{c.Fighter})[0]
	f.Pos = pos
	return f
}

// settleHelper：借用结算（战斗打完，不论胜负）——记 30 分钟冷却，双方各得友情点；对方不是好友时发加好友提示。
func (s *Session) settleHelper(p *Player, c *helperCard) {
	me := s.role.UID
	friend := false
	s.srv.social.do(func() bool {
		friend = s.srv.social.isFriend(me, c.UID)
		return false
	})
	pt := int32(helperPtOther)
	if friend {
		pt = helperPtFriend
	}
	now := time.Now().Unix()
	if p.HelperUsed == nil {
		p.HelperUsed = map[int32]int64{}
	}
	for uid, t := range p.HelperUsed {
		if now-t >= helperCooldown {
			delete(p.HelperUsed, uid)
		}
	}
	p.HelperUsed[c.UID] = now
	p.saveLog(s)
	s.roleUpdate(func(ro *Role) bool { ro.FriendPoint += pt; return true }, "friend_point")
	if ro, err := s.srv.roles.Update(c.UID, func(ro *Role) bool { ro.FriendPoint += pt; return true }); err == nil {
		if t := s.srv.sessionOf(c.UID); t != nil {
			t.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "friend_point", "val": ro.FriendPoint}}})
			t.sendMsg(fmt.Sprintf("%s 借用了你的援助护法，你获得 %d 友情点。", s.role.Name, pt))
		}
	}
	if !friend {
		s.SendExt("HELPER_PTO.S_OPEN_ADD_FRIEND", xym.Msg{"uid": c.UID, "name": c.Name, "grade": c.Grade,
			"leadershape": c.Shape, "friend_point": pt})
	} else {
		s.sendMsg(fmt.Sprintf("借用好友 %s 的援助护法，获得 %d 友情点。", c.Name, pt))
	}
}
