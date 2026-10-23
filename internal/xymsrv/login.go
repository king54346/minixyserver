package xymsrv

import (
	"errors"
	"log/slog"
	"time"

	"turnrpg/internal/xym"
)

// 登录流程（与 47.96.252.180:28417 抓包对照，analysis/captures/login-probe.jsonl）：
//
//	S_SEED(0) → C_LOGIN / C_REGISTER / C_SID_LOGIN
//	  失败：[S_MSG] + S_LOGIN_FAIL(code)
//	  成功：S_SEND_SID → 有角色：[S_SEND_NEW_HOTFIX_CONTENT] + S_LOGIN_SUC
//	                  → 无角色：S_CREATE → C_CREATE → [hotfix] + S_NEW_USER + S_LOGIN_SUC
//	C_RECOVER：S_LOGIN_FAIL(104 安全码对 / 105 密码已改)，失败同上。

func readVersion(r *xym.Reader) (v [3]int) {
	for i := range v {
		v[i] = r.U16()
	}
	return v
}

// authFail 把账号库的错误换成 S_LOGIN_FAIL。101/102/1/100 客户端自己有提示，不带 S_MSG。
func (s *Session) authFail(err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		s.sendFail(xym.FailInvalid, "")
	case errors.Is(err, ErrNoAccount):
		s.sendFail(xym.FailNoAccount, "")
	case errors.Is(err, ErrBadPassword):
		s.sendFail(xym.FailBadPassword, "")
	case errors.Is(err, ErrNameTaken):
		s.sendFail(xym.FailNameTaken, "")
	case errors.Is(err, ErrBadCode):
		s.sendFail(xym.FailRefused, "安全码要 4-16 位字母或数字，而且不能和密码一样。")
	default:
		slog.Error("account store", "sess", s.id, "err", err)
		s.sendFail(xym.FailRefused, "服务器存档出错，请稍后再试。")
	}
	if errors.Is(err, ErrBadPassword) || errors.Is(err, ErrBadCode) {
		if s.loginFails++; s.loginFails >= maxLoginFails {
			s.Close()
		}
	}
}

// onLogin 处理帧 2（C_LOGIN）与帧 20（C_REGISTER）：版本 + 账号 + 密码 [+ 设备号 [+ 安全码]]。
func (s *Session) onLogin(main byte, body []byte) {
	if s.acc != nil {
		slog.Warn("login frame after auth, ignored", "sess", s.id, "main", main)
		return
	}
	r := &xym.Reader{B: body}
	s.ver = readVersion(r)
	urs, pwd := r.Str(), r.Str()
	hasDevice := r.More()
	var device, code string
	if hasDevice {
		device = r.Str()
	}
	if main == xym.CRegister && r.More() {
		code = r.Str()
	}
	if r.Err != nil {
		s.sendFail(xym.FailInvalid, "")
		return
	}

	var acc *Account
	var err error
	if main == xym.CRegister {
		switch {
		case !hasDevice:
			s.sendFail(xym.FailRefused, "客户端版本太旧，请安装最新的细水长流客户端再注册。")
			return
		case code == "":
			s.sendFail(xym.FailRefused, "注册需要设置安全码（4-16 位字母或数字）。")
			return
		}
		acc, err = s.srv.accounts.Register(urs, pwd, code)
		if err == nil {
			slog.Info("register", "sess", s.id, "uid", acc.UID, "urs", acc.URS, "device", device)
		}
	} else {
		acc, err = s.srv.accounts.Login(urs, pwd, device)
	}
	if err != nil {
		s.authFail(err)
		return
	}
	sid, err := s.srv.accounts.NewSID(acc)
	if err != nil {
		s.authFail(err)
		return
	}
	s.afterAuth(acc, sid)
}

// onSidLogin 处理帧 6（断线重连）：版本 + uid u32 + sid。
func (s *Session) onSidLogin(body []byte) {
	if s.acc != nil {
		return
	}
	r := &xym.Reader{B: body}
	s.ver = readVersion(r)
	uid, sid := r.U32(), r.Str()
	if r.Err != nil {
		s.sendFail(xym.FailInvalid, "")
		return
	}
	acc := s.srv.accounts.BySID(uid, sid)
	if acc == nil {
		// sid 过期（别处用密码登录过）：按原版「账号密码有误」处理，客户端清掉密码回登录界面。
		s.authFail(ErrBadPassword)
		return
	}
	s.afterAuth(acc, sid)
}

func (s *Session) afterAuth(acc *Account, sid string) {
	s.acc = acc
	s.bound = acc.UID
	s.srv.bindOnline(acc.UID, s)
	s.SendExt("LOGIN_PTO.S_SEND_SID", xym.Msg{"uid": acc.UID, "sid": sid})
	if role := s.srv.roles.Get(acc.UID); role != nil {
		s.enterGame(role, false)
		return
	}
	s.creating = true
	s.send(xym.SCreate, nil)
	slog.Info("auth ok, awaiting C_CREATE", "sess", s.id, "uid", acc.UID)
}

// onCreate 处理帧 3（C_CREATE）：头像 u16 + 名字。名字不行就弹框，起名界面留着让玩家再填。
func (s *Session) onCreate(body []byte) {
	if s.acc == nil || !s.creating {
		slog.Warn("C_CREATE out of order", "sess", s.id)
		return
	}
	r := &xym.Reader{B: body}
	icon, name := r.U16(), r.Str()
	if r.Err != nil {
		s.sendMsg("起名数据有误，请重试。")
		return
	}
	role, err := s.srv.roles.Create(s.acc.UID, icon, name)
	switch {
	case err == nil:
		slog.Info("role created", "sess", s.id, "uid", role.UID, "name", role.Name)
		s.enterGame(role, true)
	case errors.Is(err, ErrRoleExists):
		s.enterGame(s.srv.roles.Get(s.acc.UID), false)
	case errors.Is(err, ErrBadName):
		s.sendMsg("名字要 1-12 个字，不能有空格。")
	case errors.Is(err, ErrNameUsed):
		s.sendMsg("这个名字已经有人用了，换一个吧。")
	default:
		slog.Error("create role", "sess", s.id, "err", err)
		s.sendMsg("建角失败，请稍后再试。")
	}
}

func (s *Session) enterGame(role *Role, isNew bool) {
	player, err := s.srv.players.Get(role.UID)
	if err != nil {
		s.logErr("load player", err)
		s.sendFail(xym.FailRefused, "读取角色存档出错，请联系管理员。")
		s.Close()
		return
	}
	s.role, s.player, s.creating = role, player, false
	player.role = role
	player.mu.Lock()
	leveled := false
	for _, pet := range player.Pets { // 补升：卡牌升级上线前攒下的经验、或人物升级后放开的上限
		leveled = pet.addExp(0, role.PetCap()) > 0 || leveled
	}
	if leveled {
		player.saveLog(s)
	}
	player.mu.Unlock()
	if h := s.srv.cfg.Hotfix; h != nil {
		s.send(xym.MainExt, h)
	}
	if isNew {
		s.SendExt("LOGIN_PTO.S_NEW_USER", nil) // 开场剧情等它关起名框，要在 S_LOGIN_SUC 之前
	}
	s.SendExt("LOGIN_PTO.S_LOGIN_SUC", role.LoginSuc(s.acc.URS, time.Now()))
	s.pushAfterLogin()
	slog.Info("enter game", "sess", s.id, "uid", role.UID, "name", role.Name,
		"ver", s.ver, "addr", s.conn.RemoteAddr().String())
}

// onRecover 处理帧 21（找回密码）：版本 + 账号 + 安全码 + 新密码（空 = 只核对）+ 设备号。
func (s *Session) onRecover(body []byte) {
	r := &xym.Reader{B: body}
	s.ver = readVersion(r)
	urs, code, newPwd := r.Str(), r.Str(), r.Str()
	if r.More() {
		_ = r.Str() // 设备号，本地不用
	}
	if r.Err != nil {
		s.sendFail(xym.FailInvalid, "")
		return
	}
	res, err := s.srv.accounts.Recover(urs, code, newPwd)
	switch {
	case err == nil:
		if res == xym.RecoverDone {
			slog.Info("password reset", "sess", s.id, "urs", urs)
		}
		s.sendFail(res, "")
	case errors.Is(err, ErrNoAccount):
		s.sendFail(xym.FailNoAccount, "没有「"+urs+"」这个账号。")
	case errors.Is(err, ErrBadCode):
		s.sendFail(FailRecoverBadCode, "安全码不对。")
		if s.loginFails++; s.loginFails >= maxLoginFails {
			s.Close()
		}
	case errors.Is(err, ErrInvalid):
		s.sendFail(xym.FailInvalid, "新密码不合法：1-64 个字符，不能和安全码一样。")
	default:
		slog.Error("recover", "sess", s.id, "err", err)
		s.sendFail(xym.FailRefused, "服务器存档出错，请稍后再试。")
	}
}

// FailRecoverBadCode 找回密码安全码错（offline_probe.recover 对 106-109 只收尾，话由 S_MSG 说）。
const FailRecoverBadCode = 106
