package xymsrv

import (
	"time"
	"turnrpg/internal/xym"
)

// Adapted from xym_gm_broadcast_20261031; audit is handled by the route.
func (srv *Server) gmBroadcast(text string, popup, toChat bool) int {
	var msg xym.Msg
	if toChat {
		chat := srv.chat
		chat.Lock()
		chat.SN++
		rec := chatMsg{SN: chat.SN, UID: 0, Name: "系统", FP: 0, Channel: chWorld, Type: 0,
			Msg: text, TS: time.Now().Unix()}
		key := histKey(chWorld, 0)
		h := append(chat.Hist[key], rec)
		chat.Hist[key] = h[max(0, len(h)-chatKeep):]
		saveGlobal(chat.st, globalChat, chat) // 系统公告也存进聊天记录（重启后还能拉到）
		chat.Unlock()
		msg = rec.msg()
	}
	sent := 0
	for _, s := range srv.onlineSessions() {
		if !s.inGame.Load() {
			continue
		}
		if popup {
			s.SendExt("ETC_PTO.S_SEND_NO_CACHE_MSG", xym.Msg{"msg": text})
		}
		if toChat {
			s.SendExt("CHAT_PTO.S_CHANNEL_MSG", msg)
		}
		sent++
	}
	return sent
}
