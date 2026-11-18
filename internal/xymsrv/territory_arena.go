package xymsrv

import (
	"fmt"
	"strings"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

// 洞府领土争夺：擂台（人界/地界/天界）与每 5 分钟的关卡场景事件。
//
// 规则依据是客户端规则页原文（client2/apk-src/assets/script/logic/dongfu_mgr.lua 的 GATE_EVENT_TEXT）：
//   - 「进入本关卡时，若擂主为敌方护法，将与其进行一场战斗。本次战斗不会对擂主产生影响，
//     若战胜擂主将获得 X 仙气」；
//   - 「如果当前没有擂主，则将从本关卡内所有玩家中随机抽取 1 名玩家成为擂主。擂主获得 X 仙气值」；
//   - 「前往人、地、天擂台是获取胜利点的有效途径」「挑战妖物和妖王是获得仙气的有效途径」；
//   - 「战场内每隔 5 分钟，所有的关卡均会触发各自的关卡事件」。
//
// 擂主就是**真实玩家**：谁站上这个擂台据点、并打赢原擂主，谁就是擂主，两个洞府的玩家在这里互抢。
// 守擂阵容取「成为擂主那一刻」的阵容快照，之后挑战都用快照——
// 既符合「本次战斗不会对擂主产生影响」，也避免跨会话去读对方的 Player 造成数据竞争。

// terrArenaReward[tier] = {挑战胜利点, 挑战仙气}，tier 1/2/3 = 人界/地界/天界。
var terrArenaReward = [4][2]int{{0, 0}, {15, 10}, {25, 15}, {40, 25}}

// terrOwnerReward[tier] 是「成为擂主」时按规则拿到的仙气（规则页：擂主获得 X 仙气值）。
var terrOwnerReward = [4]int{0, 5, 8, 12}

// terrOwner 是一个擂台的擂主——真实玩家。
type terrOwner struct {
	UID      int32
	Name     string
	Side     int
	Grade    int
	Prestige int // 成为擂主那一刻的威望快照（战报里的 w2）
	Team     []*battle.Fighter // 成为擂主那一刻的守擂阵容快照
}

// terrArenaStage 从关卡名判断擂台档次：天界擂=3、地界擂=2、其余（人界擂）=1。
func terrArenaStage(name string) int {
	switch {
	case strings.HasPrefix(name, "天界"):
		return 3
	case strings.HasPrefix(name, "地界"):
		return 2
	}
	return 1
}

// terrArenaTier 取某个据点作为擂台时的档次；不是擂台返回 0。
func (b *terrBattle) terrArenaTier(key string) int {
	if b.kind[key] != "arena" {
		return 0
	}
	return terrArenaStage(b.name[key])
}

// arenaTeam 按玩家在战场里的阵容建队，并叠加祭坛修炼加成（攻击 +6%/级、生命 +6%/级）。
func (b *terrBattle) arenaTeam(p *Player, lineup, atkLv, hpLv int) []*battle.Fighter {
	team, _ := p.terrTeam(lineup)
	if len(team) == 0 {
		return nil
	}
	if atkLv > 0 || hpLv > 0 {
		for _, f := range team {
			if atkLv > 0 {
				f.Att = int32(int64(f.Att) * int64(100+6*atkLv) / 100)
			}
			if hpLv > 0 {
				f.HP = int32(int64(f.HP) * int64(100+6*hpLv) / 100)
				f.MaxHP = int32(int64(f.MaxHP) * int64(100+6*hpLv) / 100)
			}
		}
	}
	return team
}

// becomeOwnerLocked 把某个擂台的擂主换成 pl（调用方必须已持有 b.mu）。
// team 是擂主的守擂阵容快照；同时把据点归属记成本方。
func (b *terrBattle) becomeOwnerLocked(gate string, pl *terrPlayer, grade, prestige int, team []*battle.Fighter) {
	if b.owners == nil {
		b.owners = map[string]*terrOwner{}
	}
	// 快照是「别人来打你时的敌方阵容」，站位要落到 100+（客户端战报按站位分左右两排，
	// 直接用我方 1-9 会把两边画在同一侧，看起来就是卡片错位）。
	snap := battle.CloneTeam(team)
	for _, f := range snap {
		if f.Pos < 100 {
			f.Pos += 100
		}
	}
	b.owners[gate] = &terrOwner{UID: pl.UID, Name: pl.Name, Side: pl.Side, Grade: grade, Prestige: prestige, Team: snap}
	if g := b.gates[gate]; g != nil {
		g.Owner = pl.Side
	}
}

// dropOwnerOf 清掉某玩家持有的所有擂台（离场或被人驱逐时调用）。
func (b *terrBattle) dropOwnerOf(uid int32) {
	for k, o := range b.owners {
		if o != nil && o.UID == uid {
			delete(b.owners, k)
		}
	}
}

// broadcast 给战场内所有玩家发一条频道消息（系统公告用 usernum=0）。
func (b *terrBattle) broadcast(msg xym.Msg) {
	if msg == nil {
		return
	}
	b.mu.Lock()
	uids := make([]int32, 0, len(b.players))
	for uid := range b.players {
		uids = append(uids, uid)
	}
	b.mu.Unlock()
	for _, uid := range uids {
		if sess := b.srv.sessionOf(uid); sess != nil {
			sess.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_BROADCAST_MSG", msg)
		}
	}
}

// sysBroadcast 是一条系统频道消息。
func sysBroadcast(text string) xym.Msg {
	return xym.Msg{"usernum": 0, "name": "战场", "msg": text, "from_side": 0, "kind": 0}
}

// terrTell 在战场频道里回一条系统提示，只发给这名玩家。
// 实测（用户反馈）：战场界面打开时 `S_RET_MSG` 和顶部飘字都看不到，
// 而战场频道 `S_TERRITORY_BROADCAST_MSG`（客户端 DongFuChatChannelView）是确定可见的，
// 所以玩家操作的结果提示统一走这里；retMsg 仍然保留（逻辑/测试用）。
func (s *Session) terrTell(text string) {
	// 先发频道再发 retMsg：客户端测试里 until(S_RET_MSG) 读到就停，频道那条必须排在前面才能被一起读到。
	s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_BROADCAST_MSG", sysBroadcast(text))
	s.retMsg(text)
}

// fireEventLocked 触发一轮关卡场景事件，调用方必须已持有 b.mu。
// 官方规则：「每隔 5 分钟触发场景事件时：本关卡的妖物数量变更为 N」，
// 妖王还有单独一条：「妖王的当前血量恢复上限的 20%，降低初始值的 8%」。
func (b *terrBattle) fireEventLocked() xym.Msg {
	now := time.Now()
	revived := 0
	for k, mob := range b.mobs {
		if b.kind[k] == "boss" {
			if mob.left <= 0 { // 被打死的妖王：上限 -8%、血量回到上限的 20%
				mob.capRate -= 8
				if mob.capRate < 20 {
					mob.capRate = 20
				}
				mob.hpRate = 20
				mob.left = mob.max
				revived++
			}
		} else {
			mob.left = mob.max
		}
		mob.at = now.Add(terrEventInterval * time.Second)
	}
	text := "【场景事件】各关卡妖物、妖王已重新聚集"
	if revived > 0 {
		text = fmt.Sprintf("【场景事件】妖物已重新聚集；%d 只妖王复活（血量回 20%%）", revived)
	}
	return sysBroadcast(text)
}

// terrChallengeList 回该据点上的敌方玩家（is_owner 标记当前擂主）。
// 只有擂台据点才有对手；自己就是擂主、或没有敌人时列表为空。
func (s *Session) terrChallengeList(typ int) {
	msg := xym.Msg{"challenge_type": typ, "cd_left_time": 0, "list": []xym.Msg{}}
	if b := s.terrBattle(); b != nil {
		type foeRow struct {
			uid     int32
			name    string
			isOwner int
		}
		var rows []foeRow
		b.mu.Lock()
		if pl := b.players[s.role.UID]; pl != nil && b.kind[pl.Gate] == "arena" {
			for _, other := range b.players {
				if other.Side == pl.Side || other.Gate != pl.Gate {
					continue
				}
				isOwner := 0
				if o := b.owners[pl.Gate]; o != nil && o.UID == other.UID {
					isOwner = 1
				}
				rows = append(rows, foeRow{uid: other.UID, name: other.Name, isOwner: isOwner})
			}
		}
		b.mu.Unlock()
		// 头像/等级/战力要查角色资料，放到锁外算（阵容摘要可能持有玩家锁）。
		if len(rows) > 0 {
			list := make([]xym.Msg, 0, len(rows))
			for _, r := range rows {
				icon, grade, xgrade := 0, 0, 0
				if ro := b.srv.roles.Get(r.uid); ro != nil {
					icon, grade, xgrade = ro.Icon, int(ro.Grade), ro.XGrade
				}
				list = append(list, xym.Msg{
					"is_owner": r.isOwner, "vs_usernum": r.uid, "name": r.name, "icon": icon,
					"grade": grade, "xgrade": xgrade, "cd_left_time": 0, "vs_server_name": "",
					"fight_point": b.srv.fightPointOf(r.uid, s.player),
				})
			}
			msg["list"] = list
		}
	}
	s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CHALLENGE_USER_LIST", msg)
}

// terrArenaEnter 在玩家踏入擂台据点后调用（此时 b.mu 已释放）：
//   - 没有擂主 → 自己成为擂主并按规则得仙气；
//   - 擂主是敌方玩家 → 自动与其打一场（规则：进入本关卡时…将与其进行一场战斗）；
//   - 擂主是自己人 → 什么也不做。
// 返回 true 表示它已经把 OPEN_MAIN 推出去了（调用方不要再补一次，否则客户端会收到两条、
// 测试里也会读到上一条的状态）。
func (s *Session) terrArenaEnter(p *Player, gate string) bool {
	b := s.terrBattle()
	if b == nil {
		return false
	}
	b.mu.Lock()
	pl := b.players[s.role.UID]
	if pl == nil || pl.Gate != gate {
		b.mu.Unlock()
		return false
	}
	tier := b.terrArenaTier(gate)
	if tier == 0 {
		b.mu.Unlock()
		return false
	}
	cur := b.owners[gate]
	var enemy *terrOwner
	if cur != nil && cur.Side != pl.Side {
		cp := *cur
		enemy = &cp
	}
	free := cur == nil
	lineup, atkLv, hpLv := pl.Lineup, pl.AtkLv, pl.HpLv
	b.mu.Unlock()

	if free {
		team := b.arenaTeam(p, lineup, atkLv, hpLv)
		if len(team) == 0 {
			return false
		}
		b.mu.Lock()
		x := b.players[s.role.UID]
		if x != nil && b.owners[gate] == nil {
			b.becomeOwnerLocked(gate, x, int(s.role.Grade), s.prestige(), team)
			x.Xianqi += terrOwnerReward[tier]
			b.mu.Unlock()
			s.terrTell(fmt.Sprintf("你成为%s的擂主：仙气 +%d。", b.name[gate], terrOwnerReward[tier]))
			b.broadcast(sysBroadcast(fmt.Sprintf("%s 登上%s，成为擂主。", s.role.Name, b.name[gate])))
			s.terrSendOpen(b)
			return true
		}
		b.mu.Unlock()
		return false
	}
	if enemy != nil {
		return s.terrArenaDuel(p, gate, enemy, tier, true)
	}
	return false
}

// terrArenaChallenge 是显式挑战（动作栏「挑战」或频道命令「挑战擂台」）。
func (s *Session) terrArenaChallenge(p *Player, m xym.Msg) {
	b := s.terrBattle()
	if b == nil {
		s.terrTell("你不在领土战场中。")
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_DUEL", nil)
		return
	}
	b.mu.Lock()
	pl := b.players[s.role.UID]
	if pl == nil {
		b.mu.Unlock()
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_DUEL", nil)
		return
	}
	gate := pl.Gate
	tier := b.terrArenaTier(gate)
	cur := b.owners[gate]
	var enemy *terrOwner
	if cur != nil && cur.Side != pl.Side {
		cp := *cur
		enemy = &cp
	}
	b.mu.Unlock()

	if tier == 0 {
		s.terrTell("这里不是擂台。")
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_DUEL", nil)
		return
	}
	if enemy == nil {
	s.terrTell("这个擂台没有敌方擂主。")
		s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_DUEL", nil)
		return
	}
	s.terrArenaDuel(p, gate, enemy, tier, false)
}

// terrArenaDuel 与敌方擂主打一场：赢了才占点、易主并给奖励；输了不结算。
// auto=true 表示由「走入擂台」自动触发，失败时不额外弹提示，免得每次移动都刷屏。
// 返回 true 表示打赢并已经把 OPEN_MAIN 推出去了。
func (s *Session) terrArenaDuel(p *Player, gate string, owner *terrOwner, tier int, auto bool) bool {
	b := s.terrBattle()
	if b == nil {
		return false
	}
	b.mu.Lock()
	pl := b.players[s.role.UID]
	if pl == nil || pl.Gate != gate {
		b.mu.Unlock()
		return false
	}
	lineup, atkLv, hpLv := pl.Lineup, pl.AtkLv, pl.HpLv
	b.mu.Unlock()

	team1 := b.arenaTeam(p, lineup, atkLv, hpLv)
	if len(team1) == 0 {
		if !auto {
			s.retMsg("该阵容没有可出战的护法。")
			s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_DUEL", nil)
		}
		return false
	}
	team2 := battle.CloneTeam(owner.Team)
	if len(team2) == 0 {
		if !auto {
			s.retMsg("对方守擂阵容已失效。")
			s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_DUEL", nil)
		}
		return false
	}

	rng := s.dyRng()
	// 战报必须用「打之前」的双方快照：simulate 会把 team1/team2 的 HP 打到 0，
	// 直接拿它们发 S_FIGHT_RECORD 会渲染成空白/错位的卡片（与荡妖那边一致，先 Clone）。
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), 0}, battle.Formation{}, rng)
	rid := fmt.Sprintf("territory-arena-%d-%d", s.role.UID, time.Now().UnixNano())
	s.SendExt("FIGHT_PTO.S_FIGHT_RECORD", xym.Msg{
		"canop": 14, "rid": rid,
		"w1": s.prestige(), "level1": s.role.Grade, "name1": s.role.Name, "team1": teamMsgs(r1),
		"level2": owner.Grade, "name2": owner.Name, "team2": teamMsgs(r2), "w2": owner.Prestige,
		"otherparam": recordParams(r1, r2), "record": res.Record,
	})
	if !res.Win {
		if !auto {
			s.terrTell(fmt.Sprintf("挑战失败，%s 守住了擂台。", owner.Name))
			s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_DUEL", nil)
		}
		return false
	}

	point, xianqi := terrArenaReward[tier][0], terrArenaReward[tier][1]
	bounty := terrOwnerReward[tier]
	b.mu.Lock()
	if x := b.players[s.role.UID]; x != nil {
		x.Wins++
		x.Score += point
		x.Xianqi += xianqi + bounty
		// 易主：用这次出战的那套阵容（已吃过祭坛加成）作为守擂快照。
		b.becomeOwnerLocked(gate, x, int(s.role.Grade), s.prestige(), r1)
	}
	b.mu.Unlock()

	s.terrTell(fmt.Sprintf("战胜擂主 %s：胜利点 +%d、仙气 +%d（含擂主 +%d）。", owner.Name, point, xianqi+bounty, bounty))
	s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_CLOSE_DUEL", nil)
	b.broadcast(sysBroadcast(fmt.Sprintf("%s 击败 %s，成为%s的擂主。", s.role.Name, owner.Name, b.name[gate])))
	s.terrBroadcastUsers(b)
	s.terrSendOpen(b)
	return true
}

// terrEjectList 是「驱逐」列表：同一据点上可被驱逐的敌方玩家。
func (s *Session) terrEjectList() {
	msg := xym.Msg{"cd_left_time": 0, "list": []xym.Msg{}}
	if b := s.terrBattle(); b != nil {
		var rows []int32
		b.mu.Lock()
		if pl := b.players[s.role.UID]; pl != nil {
			for _, other := range b.players {
				if other.Side != pl.Side && other.Gate == pl.Gate {
					rows = append(rows, other.UID)
				}
			}
		}
		b.mu.Unlock()
		if len(rows) > 0 {
			list := make([]xym.Msg, 0, len(rows))
			for _, uid := range rows {
				icon, grade, xgrade, name := 0, 0, 0, ""
				if ro := b.srv.roles.Get(uid); ro != nil {
					icon, grade, xgrade, name = ro.Icon, int(ro.Grade), ro.XGrade, ro.Name
				}
				list = append(list, xym.Msg{"vs_usernum": uid, "name": name, "icon": icon,
					"grade": grade, "xgrade": xgrade, "cd_left_time": 0, "vs_server_name": "",
					"fight_point": b.srv.fightPointOf(uid, s.player)})
			}
			msg["list"] = list
		}
	}
	s.SendExt("DONGFU_TERRITORY_PTO.S_TERRITORY_EJECT_USER_LIST", msg)
}

// terrEject 驱逐同一据点上的一名敌方玩家：先和对方打一场，**赢了才**把他赶回出生点。
// 规则原文：「…时若有敌方可战斗护法将先与之战斗，胜利后方可」——驱逐不能跳过战斗。
func (s *Session) terrEject(p *Player, m xym.Msg) {
	b := s.terrBattle()
	if b == nil {
		s.terrTell("你不在领土战场中。")
		return
	}
	target, _ := m["vs_usernum"].(int64)
	if target == 0 {
		s.terrTell("没有可驱逐的对手。")
		return
	}
	gate, ok := "", false
	var team2 []*battle.Fighter
	lineup, atkLv, hpLv, otherName := 0, 0, 0, ""
	var otherGrade, otherPrestige int
	b.mu.Lock()
	if pl := b.players[s.role.UID]; pl != nil {
		if other := b.players[int32(target)]; other != nil && other.Side != pl.Side && other.Gate == pl.Gate {
			gate = pl.Gate
			otherName = other.Name
			lineup, atkLv, hpLv = pl.Lineup, pl.AtkLv, pl.HpLv
			otherPrestige = other.Prestige
			if len(other.Team) > 0 {
				team2 = battle.CloneTeam(other.Team)
			}
			ok = true
		}
	}
	b.mu.Unlock()
	if !ok {
		s.terrTell("没有可驱逐的对手。")
		return
	}
	gateName := b.name[gate]
	if ro := b.srv.roles.Get(int32(target)); ro != nil {
		otherGrade = int(ro.Grade)
	}

	team1 := b.arenaTeam(p, lineup, atkLv, hpLv)
	if len(team1) == 0 {
		s.terrTell("该阵容没有可出战的护法。")
		return
	}
	if len(team2) == 0 {
		s.terrTell("对方阵容不可用（可能刚进场），稍后再试。")
		return
	}
	// 战报里守方站位要 100+（我方快照是 1-9）。
	for _, f := range team2 {
		if f.Pos < 100 {
			f.Pos += 100
		}
	}

	rng := s.dyRng()
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{p.leaderSkill(), 0}, battle.Formation{}, rng)
	rid := fmt.Sprintf("territory-eject-%d-%d", s.role.UID, time.Now().UnixNano())
	s.SendExt("FIGHT_PTO.S_FIGHT_RECORD", xym.Msg{
		"canop": 14, "rid": rid,
		"w1": s.prestige(), "level1": s.role.Grade, "name1": s.role.Name, "team1": teamMsgs(r1),
		"level2": otherGrade, "name2": otherName, "team2": teamMsgs(r2), "w2": otherPrestige,
		"otherparam": recordParams(r1, r2), "record": res.Record,
	})
	if !res.Win {
		s.terrTell(fmt.Sprintf("驱逐失败，%s 守住了%s。", otherName, gateName))
		return
	}

	b.mu.Lock()
	if other := b.players[int32(target)]; other != nil && other.Gate == gate {
		b.dropOwnerOf(other.UID)
		other.Gate = b.spawnKey(other.Side)
	}
	b.mu.Unlock()
	if sess := b.srv.sessionOf(int32(target)); sess != nil {
		sess.terrTell(fmt.Sprintf("你被 %s 从%s驱逐，已退回出生点。", s.role.Name, gateName))
		sess.terrSendOpen(b)
	}
	s.terrTell(fmt.Sprintf("已把 %s 驱逐回出生点。", otherName))
	b.broadcast(sysBroadcast(fmt.Sprintf("%s 在%s击败并驱逐了 %s。", s.role.Name, gateName, otherName)))
	s.terrBroadcastUsers(b)
	s.terrSendOpen(b)
}
