package xymsrv

import "turnrpg/internal/xym"

func init() {
	Handle("ETC_PTO.C_STOP_USER_UPGRADE", func(s *Session, _ *Player, _ xym.Msg) {
		allowed := s.role.StopUpgrade == 1 || s.role.Grade == 30 || s.role.Grade == 40
		if s.roleUpdate(func(ro *Role) bool {
			if ro.StopUpgrade == 0 && ro.Grade != 30 && ro.Grade != 40 {
				return false
			}
			if ro.StopUpgrade == 1 {
				ro.StopUpgrade = 0
			} else {
				ro.StopUpgrade = 1
			}
			return true
		}, "stop_upgrade") {
			return
		}
		if !allowed {
			s.retMsg("仅人物30级或40级可开启停级。")
		} else {
			s.retMsg("停级状态未保存，请稍后重试。")
		}
		s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "stop_upgrade", "val": int64(s.role.StopUpgrade)}}})
	})
}
