package xymsrv

import (
	"encoding/json"
	"log/slog"
	"slices"
	"sync"
	"time"

	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 关卡排行（关卡界面「排行」→ view/LevelRankMainView.lua，三个页签）：
//   - 星级排行 C_APPLY_STAR_RANK → S_SEND_STAR_RANK{self_pos, self_star, list[pos, face, uid, grade, fight_point, ca, star,
//     name…], 自己的 face / uid / …}：star = 普通 + 精英所有关卡的星数之和（通关 1 星，成就 2 / 3 星）。
//   - 进度排行 C_APPLY_TASK_RANK → S_SEND_TASK_RANK{…taskid, etaskid}：普通 / 精英打到的最远一关（关卡号，客户端
//     gateInfo[taskid]、地图 = taskid / 10000）。
//   - 录像排行 C_APPLY_VIDEO_RANK → S_SEND_VIDEO_RANK{…cnt}：保持着几条关卡录像（每关每档全服战力最低的一场，gate_star.go）；
//     点玩家 C_APPLY_VIDEO_LIST{uid, page} → S_SEND_VIDEO_LIST{list[pos, taskid 关卡号, rdtype, tasktype, fight_point]}，
//     「播放」走 C_PLAY_RECORD{type 2}。
//   - 都只列前 100；ca 是该名次的奖励号（界面上显示两件奖励）。
//
// 没有官方抓包。估计：奖励用奖励表「排行榜奖励第 1 … 第 51-100」（39321–39333）；「每周日晚 22 点发排行奖励」还没做；
// 排名每分钟重算一次（全服玩家 + records 表的关卡录像），同分按战力。

const (
	lrTop      = 100
	lrCacheSec = 60
)

// lrEntry 是一名玩家的排行数据。
type lrEntry struct {
	Name            string
	DFName          string
	videoList       []xym.Msg
	Face, Grade, FP int
	Star            int
	Task, ETask     int // 普通 / 精英最远的关卡号
	Videos          int
	UID             int32
	DFIcon          int32
}

// lrCache 是排行数据的缓存（Server.lrCache）。
type lrCache struct {
	sync.Mutex
	at   int64
	list []*lrEntry
}

// lrCa 是第 pos 名的奖励号。
func lrCa(pos int) int {
	switch {
	case pos <= 10:
		return 39320 + pos
	case pos <= 20:
		return 39331
	case pos <= 50:
		return 39332
	default:
		return 39333
	}
}

// progressOf 是 p 的星数与最远关卡（调用方持 p.mu）。
func progressOf(p *Player) (star, task, etask int) {
	for typ, pr := range p.Prog {
		if pr == nil {
			continue
		}
		for gid, n := range pr.Gates {
			if n <= 0 {
				continue
			}
			star += pr.gateStar(gid)
			if typ == 0 {
				task = max(task, gid)
			} else {
				etask = max(etask, gid)
			}
		}
	}
	return
}

// lrEntries 是全服的排行数据（缓存一分钟）。me 是请求者自己的存档（已持锁，用最新数据）。
func (srv *Server) lrEntries(me *Player) []*lrEntry {
	c := &srv.lrCache
	c.Lock()
	defer c.Unlock()
	now := time.Now().Unix()
	if c.list == nil || now-c.at >= lrCacheSec {
		c.list, c.at = srv.lrBuild(me), now
	} else if me != nil { // 自己的数据随时更新
		for _, e := range c.list {
			if e.UID == me.UID {
				e.Star, e.Task, e.ETask = progressOf(me)
			}
		}
	}
	return c.list
}

func (srv *Server) lrBuild(me *Player) []*lrEntry {
	videos := map[int32][]xym.Msg{} // 录像主人 → 他保持的关卡录像（records 表按 kind 一次取出，不用逐关查）
	if st := srv.players.st; st != nil {
		recs, err := st.ListRecords(store.RecordGate)
		if err != nil {
			slog.Error("list gate records", "err", err)
		}
		for _, rc := range recs {
			var r gateRecord
			if json.Unmarshal(rc.Data, &r) != nil {
				continue
			}
			gid, typ, rd := int(rc.Key/8), int(rc.Key%8/4), int(rc.Key%4)
			videos[rc.Owner] = append(videos[rc.Owner], xym.Msg{"taskid": gid, "rdtype": rd, "tasktype": typ, "fight_point": r.FP})
		}
	}
	var out []*lrEntry
	for _, uid := range srv.roles.UIDs() {
		ro := srv.roles.Get(uid)
		if ro == nil {
			continue
		}
		e := &lrEntry{UID: uid, Name: ro.Name, Face: ro.Icon, Grade: ro.Grade, videoList: videos[uid], Videos: len(videos[uid])}
		p := me
		if p == nil || p.UID != uid {
			q, err := srv.players.Get(uid)
			if err != nil || !q.mu.TryLock() {
				continue
			}
			p = q
			e.Star, e.Task, e.ETask = progressOf(p)
			e.FP = prestigeOf(ro, p)
			q.mu.Unlock()
		} else {
			e.Star, e.Task, e.ETask = progressOf(p)
			e.FP = prestigeOf(ro, p)
		}
		if df, _ := srv.dongfuOf(uid); df != nil {
			e.DFName, e.DFIcon = df.Name, df.Icon
		}
		out = append(out, e)
	}
	return out
}

// lrSend 排好序发一个页签：less 排序，score 决定是否上榜，item / self 填列表项和自己那份的页签字段。
func (s *Session) lrSend(p *Player, name string, less func(a, b *lrEntry) int, score func(e *lrEntry) bool,
	item func(e *lrEntry, m xym.Msg), self func(e *lrEntry, m xym.Msg)) {
	all := s.srv.lrEntries(p)
	var ranked []*lrEntry
	for _, e := range all {
		if score(e) {
			ranked = append(ranked, e)
		}
	}
	slices.SortStableFunc(ranked, func(a, b *lrEntry) int {
		if c := less(a, b); c != 0 {
			return c
		}
		return b.FP - a.FP
	})
	ranked = ranked[:min(len(ranked), lrTop)]
	list := []xym.Msg{}
	selfPos := 0
	for i, e := range ranked {
		m := xym.Msg{"pos": i + 1, "face": e.Face, "uid": e.UID, "vip_icon_ts": 0, "grade": e.Grade, "fight_point": e.FP,
			"ca": lrCa(i + 1), "name": e.Name, "dongfu_name": e.DFName, "dongfu_icon": e.DFIcon, "xgrade": 0}
		item(e, m)
		list = append(list, m)
		if e.UID == p.UID {
			selfPos = i + 1
		}
	}
	me := &lrEntry{UID: p.UID}
	for _, e := range all {
		if e.UID == p.UID {
			me = e
		}
	}
	msg := xym.Msg{"self_pos": selfPos, "list": list, "face": s.role.Icon, "uid": p.UID, "vip_icon_ts": 0, "grade": s.role.Grade,
		"fight_point": me.FP, "name": s.role.Name, "dongfu_name": me.DFName, "dongfu_icon": me.DFIcon, "servername": "", "xgrade": 0}
	self(me, msg)
	s.SendExt(name, msg)
}

func init() {
	Handle("TASK_PTO.C_APPLY_STAR_RANK", func(s *Session, p *Player, _ xym.Msg) {
		s.lrSend(p, "TASK_PTO.S_SEND_STAR_RANK", func(a, b *lrEntry) int { return b.Star - a.Star },
			func(e *lrEntry) bool { return e.Star > 0 },
			func(e *lrEntry, m xym.Msg) { m["star"] = e.Star },
			func(e *lrEntry, m xym.Msg) { m["self_star"], m["star"] = e.Star, e.Star })
	})
	Handle("TASK_PTO.C_APPLY_TASK_RANK", func(s *Session, p *Player, _ xym.Msg) {
		s.lrSend(p, "TASK_PTO.S_SEND_TASK_RANK", func(a, b *lrEntry) int {
			if a.Task != b.Task {
				return b.Task - a.Task
			}
			return b.ETask - a.ETask
		}, func(e *lrEntry) bool { return e.Task > 0 },
			func(e *lrEntry, m xym.Msg) { m["taskid"], m["etaskid"] = e.Task, e.ETask },
			func(e *lrEntry, m xym.Msg) {
				m["self_taskid"], m["self_etaskid"], m["taskid"], m["etaskid"] = e.Task, e.ETask, e.Task, e.ETask
			})
	})
	Handle("TASK_PTO.C_APPLY_VIDEO_RANK", func(s *Session, p *Player, _ xym.Msg) {
		s.lrSend(p, "TASK_PTO.S_SEND_VIDEO_RANK", func(a, b *lrEntry) int { return b.Videos - a.Videos },
			func(e *lrEntry) bool { return e.Videos > 0 },
			func(e *lrEntry, m xym.Msg) { m["cnt"] = e.Videos },
			func(e *lrEntry, m xym.Msg) { m["self_cnt"] = e.Videos })
	})
	Handle("TASK_PTO.C_APPLY_VIDEO_LIST", func(s *Session, p *Player, m xym.Msg) {
		uid, _ := m["uid"].(int64)
		list := []xym.Msg{}
		for _, e := range s.srv.lrEntries(p) {
			if int64(e.UID) == uid {
				for i, v := range e.videoList {
					x := xym.Msg{"pos": i + 1}
					for k, val := range v {
						x[k] = val
					}
					list = append(list, x)
				}
			}
		}
		s.SendExt("TASK_PTO.S_SEND_VIDEO_LIST", xym.Msg{"list": list})
	})
}
