package xymsrv

import (
	"sync"
	"unicode/utf8"

	"turnrpg/internal/xym"
)

// 聊天（CHAT_PTO）：世界频道广播给所有在线玩家，保留最近的消息供打开聊天时拉取。
//
// 依据：view/ChatView.lua（频道 1 世界、2 洞府、3 系统、4 世界广播…；发送 C_CHANNEL_MSG{channel, msg}，客户端的
// utils.encrypt 在本地区是原样；S_CHANNEL_CD 是下次可发言的时间戳，「达到 10 级或 X 后可以发言」；拉历史
// C_FETCH_MSG{channel, is_latest, latest_sn, num_of_msg}）。抓包：C_FETCH_MSG{1, 1, 0, 20} 回
// S_CHANNEL_MSG_IN_RANGE 20 条；广播是 S_CHANNEL_MSG{sn, uid, name, fight_point, channel, type 0, msg, timestamp}。
//
// 估计：10 级以下每 60 秒能发一条；每个频道保留 50 条；洞府频道只发给本洞府的在线成员，历史按洞府分开（dongfu.go）；
// 历史只在内存里，重启清空。

const (
	chWorld      = 1
	chParty      = 2
	chatKeep     = 50
	chatLowGrade = 10
	chatLowCD    = 60
	chatMaxRunes = 200
)

// chatState 是一个服务器的聊天状态（Server.chat）。
type chatState struct {
	sync.Mutex
	sn   int32
	hist map[int][]xym.Msg
	cd   map[int32]int64 // uid -> 下次可发言（10 级以下）
}

func newChat() *chatState { return &chatState{hist: map[int][]xym.Msg{}, cd: map[int32]int64{}} }

func init() {
	Handle("CHAT_PTO.C_CHANNEL_MSG", onChannelMsg)
	Handle("CHAT_PTO.C_FETCH_MSG", onFetchMsg)
	Handle("CHAT_PTO.C_GET_SPEAKER_COUNT", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("CHAT_PTO.S_SEND_SPEAKER_COUNT", xym.Msg{"count": p.itemCount(speakerItem)})
	})
	Handle("CHAT_PTO.C_BAN_USER_MSG", ignore) // 屏蔽在客户端本地做
}

// speakerItem 是喇叭道具（世界广播）。
const speakerItem = 10021

func onChannelMsg(s *Session, p *Player, m xym.Msg) {
	ch, text := int(m["channel"].(int64)), m["msg"].(string)
	var mates []int32
	key := ch // 历史的键：世界频道 1；洞府频道按洞府分开（-洞府号）
	if ch == chParty {
		var id int32
		mates, id = s.srv.dongfuMates(s.role.UID)
		key = -int(id)
	}
	switch {
	case ch == chParty && mates == nil:
		s.retMsg("您还没有加入洞府。")
		return
	case ch != chWorld && ch != chParty:
		s.retMsg("这个频道不能发言。")
		return
	case text == "" || utf8.RuneCountInString(text) > chatMaxRunes:
		return
	}
	now := nowUnix()
	chat := s.srv.chat
	if s.role.Grade < chatLowGrade {
		chat.Lock()
		next := chat.cd[s.role.UID]
		if now < next {
			chat.Unlock()
			s.SendExt("CHAT_PTO.S_CHANNEL_CD", xym.Msg{"channel": ch, "timestamp": next})
			return
		}
		chat.cd[s.role.UID] = now + chatLowCD
		chat.Unlock()
		s.SendExt("CHAT_PTO.S_CHANNEL_CD", xym.Msg{"channel": ch, "timestamp": now + chatLowCD})
	}
	chat.Lock()
	chat.sn++
	msg := xym.Msg{"sn": chat.sn, "uid": s.role.UID, "name": s.role.Name, "fight_point": s.srv.fightPoint(s.role.UID),
		"channel": ch, "type": 0, "msg": text, "timestamp": now}
	h := append(chat.hist[key], msg)
	chat.hist[key] = h[max(0, len(h)-chatKeep):]
	chat.Unlock()
	if ch == chParty {
		for _, uid := range mates {
			s.srv.notify(uid, func(t *Session) { t.SendExt("CHAT_PTO.S_CHANNEL_MSG", msg) })
		}
		return
	}
	for _, t := range s.srv.onlineSessions() {
		t.SendExt("CHAT_PTO.S_CHANNEL_MSG", msg)
	}
}

// onFetchMsg：拉频道历史（is_latest 1 = 最新的 num 条；否则 latest_sn 之前的 num 条）。
func onFetchMsg(s *Session, p *Player, m xym.Msg) {
	ch, latest, before, n := int(m["channel"].(int64)), m["is_latest"].(int64) != 0, int32(m["latest_sn"].(int64)), int(m["num_of_msg"].(int64))
	key := ch
	if ch == chParty {
		_, id := s.srv.dongfuMates(s.role.UID)
		key = -int(id) // 不在洞府时是 0：没有历史
	}
	chat := s.srv.chat
	chat.Lock()
	var list []xym.Msg
	for _, x := range chat.hist[key] {
		if latest || x["sn"].(int32) < before {
			list = append(list, x)
		}
	}
	chat.Unlock()
	if n > 0 && len(list) > n {
		list = list[len(list)-n:]
	}
	if list == nil {
		list = []xym.Msg{}
	}
	s.SendExt("CHAT_PTO.S_CHANNEL_MSG_IN_RANGE", xym.Msg{"channel": ch, "msgs": list})
}
