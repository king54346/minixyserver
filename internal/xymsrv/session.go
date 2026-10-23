package xymsrv

import (
	"bufio"
	"log/slog"
	"net"
	"slices"
	"sync"
	"time"

	"turnrpg/internal/xym"
)

const (
	sendQueueSize = 512
	writeTimeout  = 10 * time.Second
	maxLoginFails = 5
)

// Session 对应一条客户端连接：serve 协程读帧并分发，writeLoop 协程写。
// Send* 可以从任意协程调用；发送队列满说明客户端不读了，直接断开。
type Session struct {
	id     uint64
	srv    *Server
	conn   net.Conn
	sendCh chan []byte

	closeOnce sync.Once
	closing   chan struct{}

	// 以下只在 serve 协程中访问
	ver        [3]int   // 客户端版本（帧 2/6/20 头上的 3×u16）
	acc        *Account // 账号已验证
	role       *Role    // 已进游戏
	player     *Player  // 与 role 同时设置
	creating   bool     // 已发 S_CREATE，等 C_CREATE
	loginFails int
	bound      int32                 // 登记在 srv.online 的 uid
	lastRecord []byte                // 最近一场 S_FIGHT_RECORD（编码后），「重播」C_PLAY_RECORD{type 1} 原样重发
	zfParam    string                // 最近一场战斗的阵法显示参数（zhenfa.go simulate），发战报时补进 otherparam
	asks       map[int]func(opt int) // 等回答的 S_ASK_BOX（askbox.go）：session -> 回调
	askSeq     int
}

func newSession(srv *Server, conn net.Conn, id uint64) *Session {
	return &Session{id: id, srv: srv, conn: conn, sendCh: make(chan []byte, sendQueueSize), closing: make(chan struct{})}
}

// Close 发起关闭：writeLoop 尽量把队列里剩下的（比如踢下线提示）发完再关连接。
func (s *Session) Close() { s.closeOnce.Do(func() { close(s.closing) }) }

func (s *Session) send(main byte, body []byte) {
	b := xym.AppendS2C(nil, main, body)
	select {
	case <-s.closing:
		return
	default:
	}
	select {
	case s.sendCh <- b:
	default:
		slog.Warn("send queue full, closing session", "sess", s.id)
		s.Close()
	}
}

// SendExt 发一条扩展协议，name 为 "MODULE.S_XXX"。
func (s *Session) SendExt(name string, m xym.Msg) {
	spec := xym.ByName(name)
	if spec == nil {
		panic("xymsrv: unknown message " + name)
	}
	slog.Debug("s2c", "sess", s.id, "msg", name)
	if name == "FIGHT_PTO.S_FIGHT_RECORD" && s.zfParam != "" {
		ps, _ := m["otherparam"].([]xym.Msg)
		m["otherparam"] = append(slices.Clone(ps), xym.Msg{"idx": 11, "value": s.zfParam})
		s.zfParam = ""
	}
	b := spec.MustEncode(m)
	if name == "FIGHT_PTO.S_FIGHT_RECORD" {
		s.lastRecord = b
		if s.srv != nil && s.role != nil {
			s.srv.saveRecord(s.role.UID, b)
		}
	}
	s.send(xym.MainExt, b)
}

// maxFrameBody 是一帧服务端消息体的上限（帧头长度是 u16，留点余量）。
const maxFrameBody = 60000

// sendAll 发一张全量表（is_all 1，列表在 key 下）。编码后超过一帧时对半拆成几包：第一包 is_all 1
// （客户端清空重建），其余 is_all 0（按条追加 / 更新）。护法、装备很多的号登录时会用到。
func (s *Session) sendAll(name, key string, list []xym.Msg) {
	spec := xym.ByName(name)
	parts := [][]xym.Msg{list}
	for i := 0; i < len(parts); {
		if len(parts[i]) > 1 && len(spec.MustEncode(xym.Msg{"is_all": 1, key: parts[i]})) > maxFrameBody {
			h := len(parts[i]) / 2
			parts = slices.Insert(parts, i+1, parts[i][h:])
			parts[i] = parts[i][:h]
			continue
		}
		i++
	}
	for i, l := range parts {
		isAll := 0
		if i == 0 {
			isAll = 1
		}
		s.SendExt(name, xym.Msg{"is_all": isAll, key: l})
	}
}

// sendMsg 弹一个文本框（S_MSG）。
func (s *Session) sendMsg(text string) { s.send(xym.SMsg, []byte(text)) }

// sendFail 发 S_LOGIN_FAIL；msg 非空时先用 S_MSG 说明原因（offline_probe 对 101/102/103/106+ 不再弹框）。
func (s *Session) sendFail(code uint32, msg string) {
	if msg != "" {
		s.sendMsg(msg)
	}
	s.send(xym.SLoginFail, xym.U32LE(code))
}

func (s *Session) logErr(what string, err error) {
	slog.Error(what, "sess", s.id, "uid", s.bound, "err", err)
}

// UID 返回已登录角色的 uid（未登录为 0）。
func (s *Session) UID() int32 {
	if s.role == nil {
		return 0
	}
	return s.role.UID
}

func (s *Session) serve() {
	go s.writeLoop()
	defer s.cleanup()
	s.send(xym.SSeed, xym.U32LE(0)) // 种子 0 = 不加密，与官方服一致
	r := bufio.NewReaderSize(s.conn, 8192)
	for {
		s.conn.SetReadDeadline(s.deadline())
		main, body, err := xym.ReadC2S(r)
		if err != nil {
			return
		}
		select {
		case <-s.closing:
			return
		default:
		}
		s.dispatch(main, body)
	}
}

func (s *Session) deadline() time.Time {
	cfg := &s.srv.cfg
	var d time.Duration
	switch {
	case s.role != nil:
		d = cfg.IdleTimeout
	case s.creating:
		d = cfg.CreateTimeout
	default:
		d = cfg.HandshakeTimeout
	}
	if d <= 0 {
		return time.Time{}
	}
	return time.Now().Add(d)
}

func (s *Session) dispatch(main byte, body []byte) {
	switch main {
	case xym.MainExt, xym.MainExtLarge:
		if s.role == nil {
			slog.Debug("ext before login, dropped", "sess", s.id)
			return
		}
		s.handleExt(body)
	case xym.CLogin, xym.CRegister:
		s.onLogin(main, body)
	case xym.CSidLogin:
		s.onSidLogin(body)
	case xym.CCreate:
		s.onCreate(body)
	case xym.CRecover:
		s.onRecover(body)
	case xym.CDelUser:
		slog.Info("C_DEL_USER ignored", "sess", s.id) // 原版 LoginView.NeedDelAccount 才发，本地不删号
	case xym.CNewLogin, xym.CNtLogin, xym.CNtLenovo, 8, 9, 10, 11:
		// SDK / 游客登录：本服只有账号密码，引导去装新包。
		s.sendFail(xym.FailRefused, "本服只支持账号密码登录，请使用细水长流专用客户端。")
	default:
		slog.Warn("unknown main cmd", "sess", s.id, "main", main, "len", len(body))
	}
}

func (s *Session) writeLoop() {
	defer s.conn.Close()
	for {
		select {
		case b := <-s.sendCh:
			if err := s.write(b, writeTimeout); err != nil {
				s.Close()
				return
			}
		case <-s.closing:
			for {
				select {
				case b := <-s.sendCh:
					if s.write(b, time.Second) != nil {
						return
					}
				default:
					return
				}
			}
		}
	}
}

func (s *Session) write(b []byte, timeout time.Duration) error {
	s.conn.SetWriteDeadline(time.Now().Add(timeout))
	_, err := s.conn.Write(b)
	return err
}

func (s *Session) cleanup() {
	s.Close()
	if s.role != nil {
		slog.Info("logout", "sess", s.id, "uid", s.role.UID)
	}
	s.srv.removeSession(s, s.bound)
}
