package xymsrv

import "turnrpg/internal/xym"

// spend 扣银两 / 元宝并回 S_ATTR_UPDATE；不够时提示并返回 false。
func (s *Session) spend(cash, gold int32) bool {
	if s.role.Cash < cash {
		s.sendMsg("银两不足。")
		return false
	}
	if s.role.Gold < gold {
		s.sendMsg("元宝不足。")
		return false
	}
	if cash == 0 && gold == 0 {
		return true
	}
	role, err := s.srv.roles.Update(s.role.UID, func(ro *Role) bool {
		ro.Cash -= cash
		ro.Gold -= gold
		return true
	})
	if err != nil {
		s.logErr("save role", err)
		s.sendMsg("保存失败，请重试。")
		return false
	}
	s.role = role
	var attr []xym.Msg
	if cash != 0 {
		attr = append(attr, xym.Msg{"name": "cash", "val": role.Cash})
	}
	if gold != 0 {
		attr = append(attr, xym.Msg{"name": "gold", "val": role.Gold})
	}
	s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": attr})
	return true
}
