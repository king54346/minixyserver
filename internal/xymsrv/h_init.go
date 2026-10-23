package xymsrv

import (
	"sort"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 进主界面时客户端拉取的初始化数据（P2 第一批）。回包顺序、配对关系照官方抓包
// （analysis/captures/session-01.jsonl），由 replay_test.go 对照检查。

func init() {
	Handle("PET_PTO.C_APPLY_PET_LIST", onApplyPetList)
	Handle("LINEUP_PTO.C_GET_LINEUPS", func(s *Session, p *Player, _ xym.Msg) {
		s.SendExt("LINEUP_PTO.S_SEND_LINEUP", p.lineupsMsg())
	})
	Handle("TASK_PTO.C_OPEN_MAP_2", onOpenMap2)
	Handle("ETC_PTO.C_ON_QUEST_SWITCH", func(s *Session, _ *Player, m xym.Msg) {
		key, _ := m["key"].(string)
		state := gamedata.Static.QuestSwitch[key]
		if v, ok := switchOn[key]; ok {
			state = v
		}
		s.SendExt("ETC_PTO.S_SEND_REPLY_QUEST_SWITCH", xym.Msg{"key": key, "state": state})
	})
	Handle("ETC_PTO.C_GET_ENTRANCE_DATA", func(s *Session, _ *Player, _ xym.Msg) {
		s.SendExt("ETC_PTO.S_UPDATE_ENTRANCE", xym.Msg{"list": gamedata.Static.Entrance})
	})
	Handle("ETC_PTO.C_APPLY_BAG_COUNT", func(s *Session, p *Player, m xym.Msg) {
		t := int(m["type"].(int64))
		n := 0
		if t > 0 && t < len(p.BagLimit) {
			n = p.BagLimit[t]
		}
		s.SendExt("ETC_PTO.S_SEND_BAG_COUNT", xym.Msg{"type": t, "count": n})
	})
	Handle("CARD_FRAME_PTO.C_OPEN_FRAME_BAG", func(s *Session, _ *Player, _ xym.Msg) {
		s.SendExt("CARD_FRAME_PTO.S_OPEN_FRAME_BAG", nil)
	})
	// 日常 / 主线任务（C_APPLY_DAILY、C_OPEN_MAIN_TASK）见 tasks.go。
	Handle("TREE_PTO.C_EVENT_LIST", func(s *Session, _ *Player, _ xym.Msg) {
		s.SendExt("TREE_PTO.S_EVENT_LIST", nil)
	})

	// 背包的道具 / 装备 / 法术书及穿戴见 h_bag.go；碎片（法术碎片、装备碎片、玄铁）还没有，回空表。

	// 官方服对这几条不回包（抓包确认），只是上报/查询无结果。
	for _, name := range []string{
		"ETC_PTO.C_APPLY_FACT",
		"GM_TICKET_PTO.C_QUERY_GM_STATE",
		"ETC_PTO.C_GET_REWARD_MENU_STATE",
	} {
		Handle(name, ignore)
	}
}

// pushAfterLogin：官方服在 S_LOGIN_SUC 之后主动推的数据（好友、图鉴与天将）。
// S_SEND_HAS_DONGFU 只有已加入洞府才推：yes=0 会让客户端直接弹「开启洞府」（party_mgr.S_SEND_HAS_DONGFU，真机验证）。
func (s *Session) pushAfterLogin() {
	s.sendFriends() // 好友、申请（h_friend.go）
	s.sendApplies()
	s.pushCardSets()
	s.pushDongfuLogin() // 洞府职位与修为（dongfu.go）
}

// onApplyPetList：全量卡牌。官方回 S_UPDATE_PET（详情）+ S_PET_LIST（列表页用的简表）。
func onApplyPetList(s *Session, p *Player, _ xym.Msg) {
	pets := p.sortedPets()
	full := make([]xym.Msg, 0, len(pets))
	brief := make([]xym.Msg, 0, len(pets))
	for _, pet := range pets {
		full = append(full, p.petFull(pet))
		brief = append(brief, p.petBrief(pet))
	}
	s.sendAll("PET_PTO.S_UPDATE_PET", "pets", full)
	s.sendAll("PET_PTO.S_PET_LIST", "pets", brief)
}

// onOpenMap2：闯关地图。mapid 0 = 当前该打的地图；type 0 普通 / 1 精英。
func onOpenMap2(s *Session, p *Player, m xym.Msg) {
	mapID, typ := int(m["mapid"].(int64)), int(m["type"].(int64))
	if typ != 1 {
		typ = 0
	}
	pr := p.Prog[typ]
	if mapID == 0 {
		mapID = max(nextTask(p.Prog[0].Last)/100, p.Prog[0].Last/100, 1)
	}
	var tasks []int
	for t, st := range pr.Stars {
		if t/100 == mapID && st > 0 {
			tasks = append(tasks, t)
		}
	}
	sort.Ints(tasks)
	info := make([]xym.Msg, 0, len(tasks))
	for _, t := range tasks {
		info = append(info, xym.Msg{"taskid": t, "stars": pr.Stars[t]})
	}
	sum := gamedata.MapTasks[mapID]
	s.SendExt("TASK_PTO.S_SEND_MAP_2", xym.Msg{"curr_task": pr.Last, "mapid": mapID, "type": typ, "info": info,
		"map_fin": b2i(sum > 0 && len(tasks) >= sum), "map_task_sum": sum, "map_task_fin": len(tasks)})
}
