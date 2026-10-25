package xymsrv

import (
	"log/slog"
	"runtime/debug"
	"sync"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// Handler 处理一条已解码的客户端扩展协议：在会话的 serve 协程里、持有 p.mu 时调用。
type Handler func(s *Session, p *Player, m xym.Msg)

var handlers = map[int]Handler{}

// Handle 按 "MODULE.C_XXX" 注册 handler；名字不存在或重复注册直接 panic（在 init 里就能发现）。
func Handle(name string, h Handler) {
	spec := xym.ByName(name)
	if spec == nil || xym.Lookup(xym.C2S, spec.PID) != spec {
		panic("xymsrv: not a C2S message: " + name)
	}
	if handlers[spec.PID] != nil {
		panic("xymsrv: duplicate handler: " + name)
	}
	handlers[spec.PID] = h
}

// Handled 返回已注册 handler 的 PID 集合（对接表 / 测试用）。
func Handled() map[int]bool {
	out := make(map[int]bool, len(handlers))
	for pid := range handlers {
		out[pid] = true
	}
	return out
}

var unhandledSeen sync.Map // pid -> struct{}，每个协议号只记一次 Info

// lockReply：带 LOCK 的请求（协议表 LOCK 字段 + 客户端本地锁）-> 能解开这把锁的回包。客户端发这类请求时挂等待锁，15 秒收不到
// 解锁回包就弹「连接超时」并触发 LOADING_TIMEOUT（UtilsLoadingView.lua）。客户端先按回包的
// LOCK 解锁、再调处理函数（net.on_net_ext），所以没实现的请求回一条全零的解锁包也能免掉卡死。
var lockReply = sync.OnceValue(func() map[int]*xym.Spec {
	unlocks := map[string][]*xym.Spec{}
	for _, sp := range xym.All(xym.S2C) {
		if sp.Lock != "" {
			unlocks[sp.Lock] = append(unlocks[sp.Lock], sp)
		}
	}
	out := map[int]*xym.Spec{}
	for _, c := range xym.All(xym.C2S) {
		if c.Lock == "" {
			continue
		}
		// 优先用与 LOCK 同名、且确实带这把锁的回包；其次任一带这把锁的（协议号最小）；
		// 都没有（原表 2 条，LOCK 写的消息并不解锁）就回 LOCK 名字指的那条，至少给客户端一个回应。
		named := xym.ByName(c.Lock)
		if named == nil {
			named = xym.ByName(c.Module + "." + c.Lock)
		}
		if named != nil && xym.Lookup(xym.S2C, named.PID) != named {
			named = nil
		}
		switch l := unlocks[c.Lock]; {
		case named != nil && named.Lock == c.Lock:
			out[c.PID] = named
		case len(l) > 0:
			out[c.PID] = l[0]
		case named != nil:
			out[c.PID] = named
		}
	}
	// 客户端本地挂的等待锁（signal.regUI(..., {lock = true}) 后 send_prot，不在协议表 LOCK 里），
	// 由 analysis/tools/client_locks.py 从客户端源码扫出
	var local map[string]string
	gamedata.MustLoad("client_locks.json", &local)
	for req, reply := range local {
		c, sp := xym.ByName(req), xym.ByName(reply)
		if c != nil && sp != nil && out[c.PID] == nil && xym.Lookup(xym.S2C, sp.PID) == sp {
			out[c.PID] = sp
		}
	}
	return out
})

func (s *Session) handleExt(body []byte) {
	pid, spec, m, err := xym.DecodeExt(xym.C2S, body)
	if err != nil {
		slog.Warn("bad ext frame", "sess", s.id, "pid", pid, "err", err)
		if spec == nil {
			return
		}
		m = nil // 字段解不全也照样走 LOCK 兜底，免得客户端卡住
	}
	full := spec.Module + "." + spec.Name
	slog.Debug("c2s", "sess", s.id, "msg", full, "data", m)
	h := handlers[pid]
	if h == nil || m == nil {
		if _, seen := unhandledSeen.LoadOrStore(pid, struct{}{}); !seen {
			slog.Info("unhandled", "pid", pid, "msg", full)
		}
		if r := lockReply()[pid]; r != nil {
			slog.Debug("lock stub", "msg", full, "reply", r.Module+"."+r.Name)
			s.send(xym.MainExt, r.MustEncode(nil))
		}
		return
	}
	p := s.player
	p.mu.Lock()
	defer p.mu.Unlock()
	// handler 出错（panic）不能拖垮整个进程（所有人一起掉线）：记下堆栈，回一条解锁包，会话照常
	defer func() {
		if r := recover(); r != nil {
			slog.Error("handler panic", "sess", s.id, "uid", s.role.UID, "msg", full, "err", r, "stack", string(debug.Stack()))
			if lr := lockReply()[pid]; lr != nil {
				s.send(xym.MainExt, lr.MustEncode(nil))
			}
		}
	}()
	// 会话里的角色是登录时的副本，别处（别的玩家、GM、测试）改过存档后会过时；每条请求先取最新的，
	// Player.role 跟着同步（p.grade() 等按它判断等级门槛）。
	if ro := s.srv.roles.Get(s.role.UID); ro != nil {
		s.role = ro
	}
	p.role = s.role
	s.applyRegen(p)
	gold := s.role.Gold
	p.refreshShownSkills()
	h(s, p, m)
	if ch := p.refreshShownSkills(); len(ch) > 0 { // 套装效果显示等级随穿戴 / 宝石变化（inlay.go）
		l := make([]xym.Msg, 0, len(ch))
		for _, su := range ch {
			l = append(l, su.msg())
		}
		s.SendExt("ITEM_PTO.S_UPDATE_SUIT", xym.Msg{"is_all": 0, "suits": l})
	}
	if ro := s.srv.roles.Get(s.role.UID); ro != nil && ro.Gold < gold { // 每花 100 元宝得 1 张扫荡券（sweep.go）
		p.trackGoldSpent(gold - ro.Gold)
		if err := p.save(); err != nil {
			s.logErr("save player", err)
		}
	}
	p.role = s.role
}

func ignore(*Session, *Player, xym.Msg) {}

func init() {
	// 登录后客户端上报的设备/渠道信息，原服只记日志，不回包。
	for _, name := range []string{
		"LOGIN_PTO.C_MACHINE_INFO",
		"LOGIN_PTO.C_AFTER_LOGIN",
		"LOGIN_PTO.C_SET_USER_MARK",
		"LOGIN_PTO.C_PUSH_TOKEN_INFO",
		"LOGIN_PTO.C_NOTE_BEW_USER",
		"LOGIN_PTO.C_GOT_UID",
		"ETC_PTO.C_ACTIVATION_LOG",
	} {
		Handle(name, ignore)
	}
}

// b2i：true -> 1，false -> 0（协议里的开关字段）。
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
