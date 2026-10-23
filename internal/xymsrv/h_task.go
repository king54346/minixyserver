package xymsrv

import (
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 闯关（TASK_PTO）。客户端流程（抓包）：C_APPLY_GATE_LIST → C_GET_GUARANTEE → C_NEED_HELPER →
// C_GET_FIGHT_POINT → C_START_FIGHT；开打的回包依次为 S_FIGHT_RECORD、S_REWARD_START、
// S_ATTR_UPDATE、S_UPDATE_PET、S_GATE_INFO、S_REWARD_MSG、S_REWARD_END。

var recordSeq atomic.Int64 // S_FIGHT_RECORD.rid（战报号，客户端回放/分享用）

func init() {
	Handle("TASK_PTO.C_APPLY_GATE_LIST", onApplyGateList)
	Handle("ETC_PTO.C_GET_GUARANTEE", func(s *Session, _ *Player, m xym.Msg) {
		s.SendExt("ETC_PTO.S_SEND_GUARANTEE", xym.Msg{"reward_id": m["want_reward_id"]})
	})
	Handle("TASK_PTO.C_GET_FIGHT_POINT", onGetFightPoint)
	Handle("TASK_PTO.C_START_FIGHT", onStartFight)
	// 战斗结算界面勾「开启自动挑战」后，倒计时到了发 C_AUTO_HELP_FIGHT{gid 下一关, type, ach}
	// （view/UtilsRewardWinView.lua doTuoGuan）；抓包回包与 C_START_FIGHT 相同，阵型用出战阵容的默认站位。
	Handle("TASK_PTO.C_AUTO_HELP_FIGHT", func(s *Session, p *Player, m xym.Msg) {
		req := xym.Msg{"gid": m["gid"], "type": m["type"], "uid": int64(0), "htype": int64(0), "ach": m["ach"]}
		for i := 1; i <= battle.FightSlots; i++ {
			fp := int64(i)
			if i > lineupSlots {
				fp = 0
			}
			req[fmt.Sprintf("pos%d", i)] = fp
		}
		onStartFight(s, p, req)
	})
	Handle("FIGHT_PTO.C_PLAY_RECORD", onPlayRecord)
}

// onPlayRecord：结算界面「重播」发 C_PLAY_RECORD{type 1 = REP_TYPE_USER}，官方把上一场的 S_FIGHT_RECORD
// 原样再发一次（同一个 rid，不带奖励；抓包 session-01）。其他类型（关卡录像、排行录像）的录像库还没有，忽略。
func onPlayRecord(s *Session, _ *Player, m xym.Msg) {
	if m["type"] == int64(2) { // REP_TYPE_TASK：关卡录像（gate_star.go）
		s.playGateRecord(m)
		return
	}
	if m["type"] != int64(1) || s.lastRecord == nil {
		return
	}
	s.send(xym.MainExt, s.lastRecord)
}

func modeOf(m xym.Msg) int {
	if t, _ := m["type"].(int64); t == 1 {
		return 1
	}
	return 0
}

// onApplyGateList：任务里已打过的关卡 + 下一个能打的关卡（抓包：最后一项 times=0）。
func onApplyGateList(s *Session, p *Player, m xym.Msg) {
	s.sendGateList(p, int(m["taskid"].(int64)), modeOf(m))
}

// gateNoLimit 是 S_GATE_LIST / S_GATE_INFO 的 no_reward：剩余可掉落次数，255 = 不限量（官方抓包）。
// 不发时客户端当成 0，所有关卡的掉落都变灰并显示「明日刷新 / 下周一刷新」（view/LevelDetailListView.lua）。
// 本服关卡掉落不限量。
const gateNoLimit = 255

// sendGateList 回 S_GATE_LIST（tickets 是扫荡券数，sweep.go）。
func (s *Session) sendGateList(p *Player, task, typ int) {
	var list []xym.Msg
	if p.taskOpen(typ, task) {
		for _, gid := range gamedata.TaskGates[task] {
			times := p.Prog[typ].Gates[gid]
			list = append(list, xym.Msg{"gid": gid, "times": p.Prog[typ].today(gid), "star": p.Prog[typ].gateStar(gid), "no_reward": gateNoLimit})
			if times == 0 {
				break
			}
		}
	}
	s.SendExt("TASK_PTO.S_GATE_LIST", xym.Msg{"type": typ, "list": list, "tickets": p.SweepTickets})
}

// petByNpc 按 npcnum 找卡。
func (p *Player) petByNpc(npc int32) *Pet {
	for _, pet := range p.Pets {
		if pet.NpcNum == npc {
			return pet
		}
	}
	return nil
}

// petAtFightPos 返回出战阵容 fight_pos 位置上的卡。
func (p *Player) petAtFightPos(fp int) *Pet {
	lu := p.lineup(p.FightNo)
	if lu == nil || fp < 1 || fp > len(lu.Slots) || lu.Slots[fp-1] == nil || lu.Slots[fp-1].Pet == 0 {
		return nil
	}
	for _, pet := range p.Pets {
		if pet.BagPos == lu.Slots[fp-1].Pet {
			return pet
		}
	}
	return nil
}

func (p *Player) leaderSkill() int32 {
	if lu := p.lineup(p.FightNo); lu != nil {
		if pet := p.petAtFightPos(lu.Leader); pet != nil && gamedata.Pets[pet.Type] != nil {
			return gamedata.Pets[pet.Type].LeaderSkill
		}
	}
	return 0
}

// petPower 是一张卡的战力：客户端 capacity.getPetCapLikeServer 的 (att*4+hp)×各项加成，
// 加成（法术、天赋、洞府修炼）系统未接入，按 1 算。
func petPower(pet *Pet) int {
	pi := gamedata.Pets[pet.Type]
	if pi == nil {
		return 0
	}
	return int(pi.Att(pet.Grade))*4 + int(pi.HP(pet.Grade))
}

func onGetFightPoint(s *Session, p *Player, m xym.Msg) {
	var pets []*Pet
	team, _ := m["team"].([]xym.Msg)
	for _, t := range team {
		if pet := p.petByNpc(int32(t["npcnum"].(int64))); pet != nil {
			pets = append(pets, pet)
		}
	}
	s.SendExt("TASK_PTO.S_SEND_FIGHT_POINT", xym.Msg{"fight_point": p.teamCap(pets)}) // 与阵容「总战力」同一公式（capacity.go）
}

// playerTeam：C_START_FIGHT 的 posN = 第 N 个战斗站位放出战阵容里 fight_pos 为该值的卡（0 = 空）。
func (p *Player) playerTeam(m xym.Msg) ([]*battle.Fighter, []*Pet) { return p.playerTeamWith(m, nil) }

// playerTeamWith 同 playerTeam，helper 非空时 fight_pos 9 放援助护法（helper.go；不进 pets，不加经验）。
func (p *Player) playerTeamWith(m xym.Msg, helper *helperCard) ([]*battle.Fighter, []*Pet) {
	var team []*battle.Fighter
	var pets []*Pet
	for i := 1; i <= battle.FightSlots; i++ {
		fp, _ := m[fmt.Sprintf("pos%d", i)].(int64)
		if fp == helperFightPos && helper != nil {
			team = append(team, helper.fighterAt(i))
			continue
		}
		pet := p.petAtFightPos(int(fp))
		pi := (*gamedata.PetInfo)(nil)
		if pet != nil {
			pi = gamedata.Pets[pet.Type]
		}
		if pi == nil {
			continue
		}
		team = append(team, p.fighterOf(pet, i))
		pets = append(pets, pet)
	}
	p.applySpecialRelates(team) // 项羽 ← 虞姬 挥扫等（special_relate.go）
	return team, pets
}

// fighterOf 是卡 pet 站在战斗站位 pos 时的战斗单位：属性、神通、装备 / 法术书都按它在出战阵容里的位置算
// （不在阵上就没有装备）。调用方要确认 gamedata.Pets[pet.Type] 存在。
func (p *Player) fighterOf(pet *Pet, pos int) *battle.Fighter {
	pi := gamedata.Pets[pet.Type]
	fp := p.fightPos(pet.BagPos)
	att, hp := p.petStats(pet)
	tals, tj := p.activeTalents(pet, pi) // 只带已开启的神通（境界与人物等级，pet_talent.go）
	setTals, gems := p.gearExtras(fp)    // 套装神通、宝石（inlay.go）
	tals = append(slices.Clone(tals), setTals...)
	f := &battle.Fighter{Gems: gems, SkillFull: pet.talentsFull(pi), ID: pet.NpcNum, Pos: pos, Name: pi.Name, Att: att,
		HP: hp, MaxHP: hp, Speed: p.petSpeed(pet), Icon: pet.Type, Race: pi.Race, Grade: pet.Grade,
		Talents: tals, TalentTJ: tj, Magic: pi.Magic, MagicLv: pet.magicLv(),
		Books: p.wornBooks(fp), BookRate: p.wornBookRates(fp), Suits: p.wornSuits(fp), BookTJ: p.wornBookTJ(fp), BookJX: p.wornBookJX(fp)}
	p.applyXiuwei(f) // 洞府修为（dongfu_build.go）
	return f
}

func onStartFight(s *Session, p *Player, m xym.Msg) {
	gid, typ := int(m["gid"].(int64)), modeOf(m)
	g := gamedata.Gates[gid]
	// 下面几种拒绝客户端发请求前自己也会拦（关卡按钮、体力条）；只有不一致时才会走到，
	// S_FIGHT_RECORD 的等待锁没有别的回包能解，客户端会在 10 秒后提示超时。
	if g == nil || !p.gateOpen(typ, gid) {
		s.sendMsg("这一关还没有开放。")
		return
	}
	mode := g.Mode[typ]
	if mode.Limit > 0 && p.Prog[typ].today(gid) >= mode.Limit {
		// 官方：空 S_RET_MSG 解锁 + S_ASK_BOX「今天「X」的挑战次数已用完（n/n）。花费10元宝重置挑战次数并开始挑战？」
		s.retMsg("")
		text := fmt.Sprintf("今天「%s」的挑战次数已用完（%d/%d）。#r花费#G%d元宝#n重置挑战次数并开始挑战？", g.Name, mode.Limit, mode.Limit, gateResetGold)
		s.askBox(text, []string{"重置挑战", "关闭"}, func(opt int) {
			if opt != 0 || !s.spend(0, gateResetGold) {
				return
			}
			p.Prog[typ].Daily[gid] = 0
			p.saveLog(s)
			onStartFight(s, p, m)
		})
		return
	}
	if s.role.TPoint < mode.Cost {
		s.retMsg("体力不足。") // S_RET_MSG 才能解开客户端等 S_FIGHT_RECORD 的锁（energy.go）
		s.SendExt("ETC_PTO.S_OPEN_POINT_BUY", p.pointBuyMsg(pointTypeT))
		return
	}
	helper := s.fightHelper(p, m)
	team1, pets := p.playerTeamWith(m, helper)
	team2 := enemyTeamFor(mode)
	if len(team1) == 0 || len(team2) == 0 {
		s.sendMsg("阵容为空，请先上阵。")
		return
	}
	msg1, msg2, params := teamMsgs(team1), teamMsgs(team2), recordParams(team1, team2)
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2) // 过关录像用（gate_star.go）
	opts := battle.Options{Leaders: [2]int32{p.leaderSkill(), 0}, Formations: [2]battle.Formation{p.formation(), {}}}
	if helper != nil {
		opts.Assist[0] = helper.LSkill // 援助：好友的队长技与自己的同时生效
	}
	res := s.simulateWith(team1, team2, opts, nil)
	rid := strconv.FormatInt(recordSeq.Add(1), 10)
	p.track(evGate, 1)
	slog.Debug("fight", "sess", s.id, "gid", gid, "type", typ, "win", res.Win, "rounds", res.Rounds, "skills", res.Skills)
	s.SendExt("FIGHT_PTO.S_FIGHT_RECORD", xym.Msg{
		"canop": 14, "rid": rid, // canop 照抓包（允许跳过/加速等操作的位掩码）
		"w1": s.prestige(), "level1": s.role.Grade, "name1": s.role.Name, "team1": msg1,
		"level2": 1, "name2": g.Name, "team2": msg2,
		"otherparam": params, "record": res.Record,
	})
	if !res.Win {
		// 官方失败回包没有 S_REWARD_START / S_REWARD_END：客户端把这一对之间的奖励打包成「胜利」界面
		s.loseGate(p, gid, typ, mode, pets, rid)
	} else {
		s.SendExt("ETC_PTO.S_REWARD_START", nil)
		s.winGate(p, gid, typ, mode, pets, rid)
		s.SendExt("ETC_PTO.S_REWARD_END", nil)
		s.offerGateRecord(gid, typ, 1, s.newGateRecord(p, pets, g.Name, r1, r2, res.Record))
	}
	if helper != nil {
		s.settleHelper(p, helper)
	}
}

func teamMsgs(team []*battle.Fighter) []xym.Msg {
	out := make([]xym.Msg, 0, len(team))
	for _, f := range team {
		out = append(out, f.TeamMsg())
	}
	return out
}

// enemyTeamFor 生成关卡敌方阵容（测试可替换）。
var enemyTeamFor = battle.EnemyTeam

// gateResetGold 是挑战次数用完后重置一次的元宝（官方确认框「花费10元宝重置挑战次数」）。
const gateResetGold = 10

// loseGate 结算失败（官方抓包：照样扣体力、记今日次数，回 S_ATTR_UPDATE、S_UPDATE_PET、S_GATE_INFO、
// S_REWARD_MSG{type 3}；之前什么都不回，客户端结算界面沿用默认的「胜利」）。
func (s *Session) loseGate(p *Player, gid, typ int, mode gamedata.GateMode, pets []*Pet, rid string) {
	pr := p.Prog[typ]
	if d := today(); pr.Day != d || pr.Daily == nil {
		pr.Day, pr.Daily = d, map[int]int{}
	}
	pr.Daily[gid]++
	s.roleUpdate(func(ro *Role) bool { ro.TPoint = max(ro.TPoint-mode.Cost, 0); return true },
		"tpoint", "grade", "exp", "next_exp", "cash")
	p.saveLog(s)
	full := make([]xym.Msg, 0, len(pets))
	for _, pet := range pets {
		full = append(full, p.petFull(pet))
	}
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": full})
	s.SendExt("TASK_PTO.S_GATE_INFO", s.gateInfoMsg(p, gid, typ))
	r := rewardMsg(boxFightLose, nil)
	r["record"], r["no_fight_again"], r["star"] = rid, 0, pr.gateStar(gid)
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
}

// winGate 结算胜利：扣体力（只在赢时扣）、发铜钱和经验、记通关。
func (s *Session) winGate(p *Player, gid, typ int, mode gamedata.GateMode, pets []*Pet, rid string) {
	rw := gamedata.Rewards[mode.Reward]
	if rw == nil {
		rw = &gamedata.RewardInfo{}
	}
	role, err := s.srv.roles.Update(s.role.UID, func(ro *Role) bool {
		ro.TPoint -= mode.Cost
		ro.Cash += int32(rw.Cash())
		ro.AddExp(int32(rw.MExp()))
		return true
	})
	if err != nil {
		s.logErr("save role", err)
		return
	}
	s.role = role
	pexp := int32(rw.PExp())
	full := make([]xym.Msg, 0, len(pets))
	for _, pet := range pets {
		pet.addExp(pexp, role.PetCap())
		full = append(full, p.petFull(pet))
	}
	p.clearGate(typ, gid)
	drops := &bagDelta{} // 关卡掉落：奖励表的 item / ritem 编码（第一关必掉 1 张随机 1 星卡等）
	p.rollDrops(rw, rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(p.UID))), drops)
	p.saveLog(s)

	s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{
		{"name": "tpoint", "val": role.TPoint}, {"name": "grade", "val": role.Grade},
		{"name": "exp", "val": role.Exp}, {"name": "next_exp", "val": nextExp(role.Grade)},
		{"name": "cash", "val": role.Cash},
	}})
	s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": full})
	s.SendExt("TASK_PTO.S_GATE_INFO", s.gateInfoMsg(p, gid, typ))
	list := s.sendDelta(p, drops)
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"cash": rw.Cash(), "mexp": rw.MExp(), "pexp": pexp,
		"type": 2, "record": rid, "star": p.Prog[typ].gateStar(gid), "list": list})
}
