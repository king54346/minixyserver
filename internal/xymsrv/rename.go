package xymsrv

import (
	"errors"
	"strings"

	"turnrpg/internal/xym"
)

const renameToken = 10020 // ItemListView: 改名令 opens ChangeNameMainView.

// Rename shares the name reservation lock with character creation. Persistence
// and publication use the same per-role lock as normal attribute updates.
func (r *Roles) Rename(uid int32, name string) (*Role, error) {
	name = strings.TrimSpace(name)
	if !validRoleName(name) || strings.ContainsAny(name, "#<>") {
		return nil, ErrBadName
	}
	r.createMu.Lock()
	defer r.createMu.Unlock()
	r.mu.RLock()
	for id, role := range r.ByUID {
		if id != uid && strings.EqualFold(role.Name, name) {
			r.mu.RUnlock()
			return nil, ErrNameUsed
		}
	}
	r.mu.RUnlock()
	return r.Update(uid, func(role *Role) bool { role.Name = name; return true })
}

func init() { Handle("ITEM_PTO.C_CHANGE_NAME", onChangeName) }

func onChangeName(s *Session, p *Player, m xym.Msg) {
	name := strings.TrimSpace(m["name"].(string))
	if p.itemCount(renameToken) == 0 {
		s.sendMsg("缺少改名令。")
		return
	}
	if name == s.role.Name {
		s.sendMsg("新名字不能与当前名字相同。")
		return
	}
	role, err := s.srv.roles.Rename(p.UID, name)
	if err != nil {
		switch {
		case errors.Is(err, ErrBadName):
			s.sendMsg("名字需为 1-12 个字符，不能包含空白、控制字符或特殊标记。")
		case errors.Is(err, ErrNameUsed):
			s.sendMsg("这个名字已被使用。")
		default:
			s.logErr("rename role", err)
			s.sendMsg("保存失败，请稍后重试。")
		}
		return
	}
	s.role, p.role = role, role
	changed, removed := p.takeItems(renameToken, 1)
	p.saveLog(s)
	s.sendDelta(p, &bagDelta{items: changed, removed: removed})
	s.SendExt("ITEM_PTO.S_UPDATE_NAME", xym.Msg{"name": role.Name})
}
