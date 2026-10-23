package xymsrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 关卡成就挑战（2 星 / 3 星）与关卡录像。
//
// 成就挑战（关卡详情「成就挑战」→ view/LevelDetailListView.lua CMapGateInfoView:onClickBtnFightNow）：
//   - 通关是 1 星；1 星时挑战成就一（gate.lua achieve1，精英 jy_achieve1），达成得 2 星；2 星时挑战成就二得 3 星；
//     3 星时按钮发 C_CLEAR_GATE_STAR{gid, type}（重置星级再挑战）。
//   - 选阵（view/TempView.lua，客户端先按成就的种族 / 星级 / 上阵数 / 后阵要求校验阵容）后发
//     TASK_PTO.C_START_FIGHT_WITH_TMP_TEAM{gid, type, ach, leader_num, tmp_team}，等 S_FIGHT_RECORD。
//   - 成就条件 auto/tachieve.lua：胜利且回合数 < roundless、我方阵亡数 < dieless（0 不限）、上阵数 < countless（0 不限）；
//     奖励 reward（26001 / 28001 / 30001 / 32001）。
//
// 关卡录像（关卡详情「?」→ TASK_PTO.C_APPLY_RECORD{type, gid} → S_RECORD_LIST{type, gid, list[rdtype, fight_point, id, name]}，
// view/MapRecordView.lua「看看高手们是如何以最低战力通过此关的」：rdtype 1 过关录像、2 / 3 两档成就录像；
// 「播放」发 FIGHT_PTO.C_PLAY_RECORD{type 2 = REP_TYPE_TASK, gid, ttype, rdtype}）：
//   - 每关每档存全服战力最低的一场胜利（通关、成就达成时比较），播放时原样重发 S_FIGHT_RECORD。
//   - 每条录像单独存一行（store 的玩家存档接口，uid = gateRecBase + gid×8 + type×4 + rdtype），不进全服大块存档。
//
// 估计：成就挑战与普通挑战一样消耗关卡体力（赢输都扣）；失败不提示具体没达成哪条。

const gateRecBase = 1_000_000_000

// gateStar 是关卡 gid 的星数：没通关 0，通关 1，成就挑战成功后 2 / 3。
func (pr *Progress) gateStar(gid int) int {
	if pr.Gates[gid] == 0 {
		return 0
	}
	return max(1, pr.GateStars[gid])
}

// gateRecord 是一条关卡录像（重建 S_FIGHT_RECORD 用）。
type gateRecord struct {
	UID    int32             `json:"uid"`
	Name   string            `json:"name"`
	Grade  int               `json:"grade"`
	W1     int               `json:"w1"`
	FP     int               `json:"fp"`
	Name2  string            `json:"name2"`
	T1     []*battle.Fighter `json:"t1"`
	T2     []*battle.Fighter `json:"t2"`
	Record []byte            `json:"record"`
}

var gateRecMu sync.Mutex

func gateRecKey(gid, typ, rd int) int32 { return int32(gateRecBase + gid*8 + typ*4 + rd) }

func loadGateRecord(st store.Store, gid, typ, rd int) *gateRecord {
	if st == nil {
		return nil
	}
	data, err := st.LoadPlayer(gateRecKey(gid, typ, rd))
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("load gate record", "gid", gid, "err", err)
		}
		return nil
	}
	r := &gateRecord{}
	if json.Unmarshal(data, r) != nil {
		return nil
	}
	return r
}

// offerGateRecord：战力比已存的低（或还没有）就存成该档录像。
func (s *Session) offerGateRecord(gid, typ, rd int, r *gateRecord) {
	st := s.srv.players.st
	if st == nil {
		return
	}
	gateRecMu.Lock()
	defer gateRecMu.Unlock()
	if old := loadGateRecord(st, gid, typ, rd); old != nil && old.FP <= r.FP {
		return
	}
	data, err := json.Marshal(r)
	if err == nil {
		err = st.SavePlayer(gateRecKey(gid, typ, rd), data)
	}
	if err != nil {
		slog.Error("save gate record", "gid", gid, "err", err)
	}
}

// newGateRecord 记下这一场（team1 / team2 是开打前的副本）。
func (s *Session) newGateRecord(p *Player, pets []*Pet, name2 string, t1, t2 []*battle.Fighter, rec []byte) *gateRecord {
	return &gateRecord{UID: s.role.UID, Name: s.role.Name, Grade: s.role.Grade, W1: s.prestige(), FP: p.teamCap(pets),
		Name2: name2, T1: t1, T2: t2, Record: rec}
}

func init() {
	Handle("TASK_PTO.C_START_FIGHT_WITH_TMP_TEAM", onStarFight)
	Handle("TASK_PTO.C_CLEAR_GATE_STAR", onClearGateStar)
	Handle("TASK_PTO.C_APPLY_RECORD", onApplyGateRecord)
}

func onApplyGateRecord(s *Session, p *Player, m xym.Msg) {
	typ, gid := int(m["type"].(int64)), int(m["gid"].(int64))
	list := []xym.Msg{}
	for rd := 1; rd <= 3; rd++ {
		if r := loadGateRecord(s.srv.players.st, gid, typ, rd); r != nil {
			list = append(list, xym.Msg{"rdtype": rd, "fight_point": r.FP, "id": r.UID, "name": r.Name})
		}
	}
	s.SendExt("TASK_PTO.S_RECORD_LIST", xym.Msg{"type": typ, "gid": gid, "list": list})
}

// playGateRecord 播放关卡录像（C_PLAY_RECORD{type 2}）。
func (s *Session) playGateRecord(m xym.Msg) {
	gid, typ, rd := int(m["gid"].(int64)), int(m["ttype"].(int64)), int(m["rdtype"].(int64))
	r := loadGateRecord(s.srv.players.st, gid, typ, rd)
	if r == nil {
		s.sendMsg("暂无录像。")
		return
	}
	s.SendExt("FIGHT_PTO.S_FIGHT_RECORD", xym.Msg{
		"canop": 14, "rid": "gate:" + strconv.Itoa(gid) + ":" + strconv.Itoa(rd),
		"w1": r.W1, "level1": r.Grade, "name1": r.Name, "team1": teamMsgs(r.T1),
		"level2": 1, "name2": r.Name2, "team2": teamMsgs(r.T2),
		"otherparam": recordParams(r.T1, r.T2), "record": r.Record,
	})
}

func (s *Session) gateInfoMsg(p *Player, gid, typ int) xym.Msg {
	pr := p.Prog[typ]
	return xym.Msg{"gid": gid, "type": typ, "star": pr.gateStar(gid), "times": pr.today(gid), "no_reward": gateNoLimit, "tickets": p.SweepTickets}
}

// syncTaskStar：任务的星数 = 其下各关星数的最小值（全部通关后才 ≥ 1；clearGate 用它判断任务是否完成）。
func (pr *Progress) syncTaskStar(task int) {
	if pr.Stars[task] == 0 {
		return
	}
	lo := 3
	for _, id := range gamedata.TaskGates[task] {
		lo = min(lo, pr.gateStar(id))
	}
	pr.Stars[task] = max(lo, 1)
}

// onStarFight：成就挑战。
func onStarFight(s *Session, p *Player, m xym.Msg) {
	gid, typ := int(m["gid"].(int64)), modeOf(m)
	g := gamedata.Gates[gid]
	if g == nil {
		s.sendMsg("关卡不存在。")
		return
	}
	pr := p.Prog[typ]
	star := pr.gateStar(gid)
	mode := g.Mode[typ]
	var ach *gamedata.AchieveInfo
	if star >= 1 && star <= 2 {
		ach = gamedata.Achieves[mode.Achieve[star-1]]
	}
	switch {
	case star == 0:
		s.sendMsg("成就挑战需要先通过此关！")
		return
	case star >= 3 || ach == nil:
		s.sendMsg("这一关的成就都已达成。")
		return
	case s.role.TPoint < mode.Cost:
		s.retMsg("体力不足。")
		s.SendExt("ETC_PTO.S_OPEN_POINT_BUY", p.pointBuyMsg(pointTypeT))
		return
	}
	team1, leader, snap := p.tmpTeam(m)
	team2 := enemyTeamFor(mode)
	if len(team1) == 0 || len(team2) == 0 {
		s.sendMsg("你的阵容为空，请添加护法！")
		return
	}
	if ach.Countless > 0 && len(team1) >= ach.Countless {
		s.sendMsg(fmt.Sprintf("上阵护法需少于%d个。", ach.Countless))
		return
	}
	var pets []*Pet
	for _, x := range snap {
		if pet := p.petByNpc(x["npcnum"].(int32)); pet != nil { // tmpTeam 的快照里是 int32
			pets = append(pets, pet)
		}
	}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{leader, 0}, battle.Formation{}, nil)
	rid := strconv.FormatInt(recordSeq.Add(1), 10)
	s.fightRecord(rid, g.Name, 1, battle.CloneTeam(r1), battle.CloneTeam(r2), res.Record)

	ok := res.Win && (ach.Roundless == 0 || res.Rounds < ach.Roundless)
	if ok && ach.Dieless > 0 {
		alive, err := battle.Survivors(battle.CloneTeam(r1), battle.CloneTeam(r2), res.Record)
		dead := 0
		for _, f := range r1 {
			if err == nil && !alive[f.Pos] {
				dead++
			}
		}
		ok = err == nil && dead < ach.Dieless
	}
	s.roleUpdate(func(ro *Role) bool { ro.TPoint = max(ro.TPoint-mode.Cost, 0); return true }, "tpoint")
	if !ok {
		p.saveLog(s)
		s.SendExt("TASK_PTO.S_GATE_INFO", s.gateInfoMsg(p, gid, typ))
		r := rewardMsg(boxFightLose, nil)
		r["record"], r["star"] = rid, star
		s.SendExt("ETC_PTO.S_REWARD_MSG", r)
		return
	}
	if pr.GateStars == nil {
		pr.GateStars = map[int]int{}
	}
	pr.GateStars[gid] = star + 1
	pr.syncTaskStar(g.Task)
	rw := gamedata.Rewards[ach.Reward]
	if rw == nil {
		rw = &gamedata.RewardInfo{}
	}
	s.roleUpdate(func(ro *Role) bool { ro.Cash += int32(rw.Cash()); ro.AddExp(int32(rw.MExp())); return true },
		"cash", "grade", "exp", "next_exp")
	drops := &bagDelta{}
	p.rollDrops(rw, rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(p.UID))), drops)
	p.saveLog(s)
	s.offerGateRecord(gid, typ, star+1, s.newGateRecord(p, pets, g.Name, r1, r2, res.Record))
	s.SendExt("TASK_PTO.S_GATE_INFO", s.gateInfoMsg(p, gid, typ))
	list := s.sendDelta(p, drops)
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"cash": rw.Cash(), "mexp": rw.MExp(), "type": 2, "record": rid,
		"star": star + 1, "list": list})
}

// onClearGateStar：3 星后重置星级（回到 1 星）重新挑战成就。
func onClearGateStar(s *Session, p *Player, m xym.Msg) {
	gid, typ := int(m["gid"].(int64)), modeOf(m)
	if g := gamedata.Gates[gid]; g != nil && p.Prog[typ].gateStar(gid) >= 3 {
		p.Prog[typ].GateStars[gid] = 1
		p.Prog[typ].syncTaskStar(g.Task)
		p.saveLog(s)
	}
	s.SendExt("TASK_PTO.S_GATE_INFO", s.gateInfoMsg(p, gid, typ))
}
