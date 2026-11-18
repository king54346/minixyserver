package xymsrv

import "turnrpg/internal/xym"

// 突发事件（主界面「事件」按钮，客户端 view/EmergencyEventListView.lua）：C_EVENT_LIST → TREE_PTO.S_EVENT_LIST。
// 这条消息把几十种活动的开关 / 列表放在一起（答题 answer_question_open、通用列表 event_list…），没填的字段编码为
// 0 / 空，所以每次都要发完整的一份——只发自己那部分会把别的活动的条目冲掉。
//
// 各模块用 regEvent 登记自己往消息里填什么（在自己的 init 里），这里只负责拼装和发送：
//   - exam.go    科举答题（answer_question_open）
//   - qujing.go  西天取经进行中（event_list type 112）
// 模块状态变了要刷新时调 s.pushEventList()（持 p.mu 时用 s.sendEventList(p)）。

// eventSource 往 S_EVENT_LIST 里填一个活动的内容（调用方持 p.mu）。event_list 已是 []xym.Msg，用 addEvent 追加。
type eventSource func(p *Player, m xym.Msg)

var eventSources []eventSource

// regEvent 登记一个突发事件来源（只在 init 里调用）。
func regEvent(fn eventSource) { eventSources = append(eventSources, fn) }

// addEvent 往 event_list 里加一条（type 见 EmergencyEventListView.lua 顶上的 EMERGENCY_TYPE_*）。
func addEvent(m xym.Msg, typ int, extra xym.Msg) {
	e := xym.Msg{"type": typ, "state": 0, "count": 0, "left_time": 0, "desc": "", "title": "", "icon": 0}
	for k, v := range extra {
		e[k] = v
	}
	m["event_list"] = append(m["event_list"].([]xym.Msg), e)
}

// eventListMsg 是当前完整的突发事件消息（调用方持 p.mu）。
func eventListMsg(p *Player) xym.Msg {
	m := xym.Msg{"event_list": []xym.Msg{}}
	for _, fn := range eventSources {
		fn(p, m)
	}
	return m
}

func init() {
	Handle("TREE_PTO.C_EVENT_LIST", func(s *Session, p *Player, _ xym.Msg) { s.sendEventList(p) })
}

// sendEventList 发一份完整的突发事件（调用方持 p.mu）。
func (s *Session) sendEventList(p *Player) {
	m := eventListMsg(p)
	// 客户端用这个倒计时显示「魔潮入侵」和直达按钮，事件列表里只有这一个字段能表达魔潮。
	// 用户要求「排期后事件里也要有提示」，所以两个阶段都填：
	// 进行中 = 距结束；等待开启（已排期）= 距开始。
	// 注意：这个客户端的「战斗」按钮没有禁用态，等待态下它也是亮可点的——
	// 点进去是洞府魔潮页，那里会显示「将于 X 后到来」，不会提前开打。
	dongfu := s.srv.dongfu
	dongfu.do(func() bool {
		if df, _ := dongfu.of(p.UID); df != nil {
			now := nowUnix()
			if iv := df.Invade; iv != nil {
				switch iv.status(now) {
				case 0:
					m["monster_invade_left_time"] = iv.Start + invadeDuration - now
				case 4:
					m["monster_invade_left_time"] = iv.Start - now
				}
			}
			// 破阵之战：客户端事件列表只用 dongfu_battle 数组表达（EmergencyEventListView
			// 的 refreshDongFuPoZhen）——left_time = 距本阶段结束，left_fight_times = 我的剩余挑战次数。
			// 不填它，事件里就永远没有破阵入口，玩家只能看到「洞府 → 破阵」那张静态规则页。
			// 点这一行时客户端发 C_GET_DONGFU_BATTLE_MAIN_INFO 打开破阵主界面。
			nowT := dfBattleNow()
			if b, stage, _ := s.srv.dfBattleGet(df, p, nowT); b != nil {
				switch stage {
				case dfBattleDeploy, dfBattleFight, dfBattlePending:
					m["dongfu_battle"] = []xym.Msg{{
						"left_time":        dfBattleEventLeft(dfBattleScheduleOf(nowT), stage, nowT),
						"left_fight_times": max(0, dfBattleUserTimes-b.user(p.UID).Times),
					}}
				}
			}
		}
		return false
	})
	s.SendExt("TREE_PTO.S_EVENT_LIST", m)
}

// pushEventList 同 sendEventList，自己加锁（登录后推送等不持锁的地方用）。
func (s *Session) pushEventList() {
	p := s.player
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s.sendEventList(p)
}
