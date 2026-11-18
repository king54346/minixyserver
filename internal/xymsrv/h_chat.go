package xymsrv

import (
	"strconv"
	"sync"
	"unicode/utf8"

	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 聊天（CHAT_PTO）：世界频道广播给所有在线玩家，保留最近的消息供打开聊天时拉取。
//
// 依据：view/ChatView.lua（频道 1 世界、2 洞府、3 系统、4 世界广播…；发送 C_CHANNEL_MSG{channel, msg}，客户端的
// utils.encrypt 在本地区是原样；S_CHANNEL_CD 是下次可发言的时间戳，「达到 10 级或 X 后可以发言」；拉历史
// C_FETCH_MSG{channel, is_latest, latest_sn, num_of_msg}）。抓包：C_FETCH_MSG{1, 1, 0, 20} 回
// S_CHANNEL_MSG_IN_RANGE 20 条；广播是 S_CHANNEL_MSG{sn, uid, name, fight_point, channel, type 0, msg, timestamp}。
//
// 估计：10 级以下每 60 秒能发一条；每个频道保留 50 条；洞府频道只发给本洞府的在线成员，历史按洞府分开。
//
// 历史**写全服存档**（globals 的 "chat"），重启后还能拉到之前的聊天；
// 早先只在内存里，服务器一重启聊天记录就空了（2026-11-18 用户报）。

const (
	chWorld      = 1
	chParty      = 2
	chatKeep     = 50
	chatLowGrade = 10
	chatLowCD    = 60
	chatMaxRunes = 200
)

// chatMsg 是一条聊天记录。用具体类型（而不是 xym.Msg 的 map[string]any）存盘：
// JSON 里的数字读回来是 float64，直接当 int32 用会让编码器报 not a number。
type chatMsg struct {
	SN      int32  `json:"sn"`
	UID     int32  `json:"uid"`
	Name    string `json:"name"`
	FP      int    `json:"fp"`
	Channel int    `json:"channel"`
	Type    int    `json:"type"`
	Msg     string `json:"msg"`
	TS      int64  `json:"ts"`
}

// msg 转成下发用的 S_CHANNEL_MSG（字段类型要和抓包一致）。
func (c chatMsg) msg() xym.Msg {
	return xym.Msg{"sn": c.SN, "uid": c.UID, "name": c.Name, "fight_point": c.FP,
		"channel": c.Channel, "type": c.Type, "msg": c.Msg, "timestamp": c.TS}
}

// chatState 是一个服务器的聊天状态（Server.chat）。
type chatState struct {
	st store.Store

	sync.Mutex
	SN   int32                `json:"sn"`
	Hist map[string][]chatMsg `json:"hist"` // 频道键 → 最近 chatKeep 条（世界 "1"，洞府 "-洞府号"）
	cd   map[int32]int64      // uid -> 下次可发言（10 级以下），只在内存，不落盘
}

// histKey 是历史记录的键：世界频道 "1"；洞府频道 "-洞府号"（不在洞府时 id=0）。
func histKey(ch int, dfID int32) string {
	if ch == chParty {
		return strconv.Itoa(-int(dfID))
	}
	return strconv.Itoa(ch)
}

func newChat(st store.Store) *chatState {
	c := &chatState{st: st, Hist: map[string][]chatMsg{}, cd: map[int32]int64{}}
	loadGlobal(st, globalChat, c)
	if c.Hist == nil {
		c.Hist = map[string][]chatMsg{}
	}
	if c.cd == nil {
		c.cd = map[int32]int64{}
	}
	return c
}

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
	var dfID int32
	if ch == chParty {
		mates, dfID = s.srv.dongfuMates(s.role.UID)
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
	power := s.srv.fightPointOf(s.role.UID, p)
	key := histKey(ch, dfID)
	chat.Lock()
	chat.SN++
	rec := chatMsg{SN: chat.SN, UID: s.role.UID, Name: s.role.Name, FP: power,
		Channel: ch, Type: 0, Msg: text, TS: now}
	h := append(chat.Hist[key], rec)
	chat.Hist[key] = h[max(0, len(h)-chatKeep):]
	saveGlobal(chat.st, globalChat, chat) // 存档要在锁内写（saveGlobal 约定调用方持锁）
	chat.Unlock()
	msg := rec.msg()
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
	ch, latest, before, n := int(m["channel"].(int64)), m["is_latest"].(int64) != 0, m["latest_sn"].(int64), int(m["num_of_msg"].(int64))
	var dfID int32
	if ch == chParty {
		_, dfID = s.srv.dongfuMates(s.role.UID) // 不在洞府时是 0：没有历史
	}
	key := histKey(ch, dfID)
	chat := s.srv.chat
	chat.Lock()
	list := make([]xym.Msg, 0, len(chat.Hist[key]))
	for _, x := range chat.Hist[key] {
		if latest || int64(x.SN) < before {
			list = append(list, x.msg())
		}
	}
	chat.Unlock()
	if n > 0 && len(list) > n {
		list = list[len(list)-n:]
	}
	s.SendExt("CHAT_PTO.S_CHANNEL_MSG_IN_RANGE", xym.Msg{"channel": ch, "msgs": list})
}
