package xymsrv

import "turnrpg/internal/xym"

const (
	dfPushTask = iota
	dfPushUpgrade
	dfPushInvade
	dfPushGankAttack
	dfPushGankDefense
	dfPushBattle
	dfPushCave
)

var dfPushFields = [...]string{
	dfPushTask:        "push_msg_xiuwei_task",
	dfPushUpgrade:     "push_msg_dongfu_upgrade",
	dfPushInvade:      "push_msg_monster_invade",
	dfPushGankAttack:  "push_msg_gank_attack",
	dfPushGankDefense: "push_msg_gank_defense",
	dfPushBattle:      "push_msg_battle",
	dfPushCave:        "push_msg_cave",
}

func init() {
	Handle("DONGFU_PTO.C_SELECT_DONGFU_PUSH_MSG", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			// 零值表示全开，兼容没有该字段的旧成员存档。
			var muted uint8
			for i, field := range dfPushFields {
				if m[field] == int64(0) {
					muted |= 1 << i
				}
			}
			changed := me.PushMuted != muted
			me.PushMuted = muted
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO", s.dfJobInfo(p, df, me))
			return changed
		})
	})
}

// 调用方持洞府锁。只给开启该项的在线成员发提示。
func (srv *Server) pushDongfuEvent(df *Dongfu, kind int, text string) {
	for _, member := range df.Members {
		if member.PushMuted&(1<<kind) != 0 {
			continue
		}
		srv.notify(member.UID, func(s *Session) {
			if s.inGame.Load() {
				s.SendExt("ETC_PTO.S_SEND_NO_CACHE_MSG", xym.Msg{"msg": text})
			}
		})
	}
}
