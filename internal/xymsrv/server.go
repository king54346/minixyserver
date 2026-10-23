// Package xymsrv 是迷你西游真协议的网关：分帧/登录/会话管理，登录后的扩展协议按 PID 分发给 handler。
package xymsrv

import (
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	HandshakeTimeout time.Duration // 连上后多久没登录成功就断（含建角前）
	IdleTimeout      time.Duration // 登录后多久收不到任何包就断；客户端没有心跳包，0 = 不限，只靠 TCP keepalive
	CreateTimeout    time.Duration // 收到 S_CREATE 后等起名的时间（开场剧情 + 起名，可能很久）
	Hotfix           []byte        // 预先编好的 S_SEND_NEW_HOTFIX_CONTENT body，nil 不发
	RecordDir        string        // 战报存档目录（records.go），空 = 不存
	RecordDays       int           // 战报保留天数，0 = 不删
}

func DefaultConfig() Config {
	return Config{HandshakeTimeout: 30 * time.Second, IdleTimeout: 30 * time.Minute, CreateTimeout: 30 * time.Minute}
}

type Server struct {
	cfg      Config
	accounts *Accounts
	roles    *Roles
	players  *Players
	social   *Social    // 好友 / 私信 / 礼物 / 邮件（social.go）
	chat     *chatState // 聊天频道（h_chat.go）
	dongfu   *Dongfus   // 洞府（dongfu.go）
	ladder   *Ladder    // 论道榜（lundao.go）
	starwar  *Starwar   // 星战（starwar.go）
	tianti   *Tianti    // 天梯（tianti.go）
	jiuyou   *Jiuyou    // 九幽试炼排行（jiuyou.go）

	nextID atomic.Uint64

	mu       sync.Mutex
	ln       net.Listener
	closed   bool
	sessions map[*Session]struct{}
	online   map[int32]*Session // uid -> 会话，用于顶号
	wg       sync.WaitGroup
}

func NewServer(cfg Config, accounts *Accounts, roles *Roles, players *Players) *Server {
	return &Server{cfg: cfg, accounts: accounts, roles: roles, players: players, social: openSocial(players.st), chat: newChat(),
		dongfu: openDongfus(players.st), ladder: openLadder(players.st), starwar: openStarwar(players.st), tianti: openTianti(players.st), jiuyou: openJiuyou(players.st),
		sessions: map[*Session]struct{}{}, online: map[int32]*Session{}}
}

// Serve 在 ln 上接受连接，直到 Shutdown。
func (srv *Server) Serve(ln net.Listener) error {
	srv.mu.Lock()
	if srv.closed {
		srv.mu.Unlock()
		return errors.New("xymsrv: server closed")
	}
	srv.ln = ln
	srv.mu.Unlock()
	go srv.regenLoop()
	go srv.starwarLoop()
	go srv.recordsLoop()
	for {
		conn, err := ln.Accept()
		if err != nil {
			srv.mu.Lock()
			closed := srv.closed
			srv.mu.Unlock()
			if closed {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.SetKeepAlivePeriod(time.Minute)
			_ = tc.SetNoDelay(true)
		}
		s := newSession(srv, conn, srv.nextID.Add(1))
		srv.mu.Lock()
		if srv.closed {
			srv.mu.Unlock()
			conn.Close()
			return nil
		}
		srv.sessions[s] = struct{}{}
		srv.wg.Add(1)
		srv.mu.Unlock()
		go func() {
			defer srv.wg.Done()
			s.serve()
		}()
	}
}

// Shutdown 停止监听，断开所有会话并等它们清理完。
func (srv *Server) Shutdown() {
	srv.mu.Lock()
	srv.closed = true
	if srv.ln != nil {
		srv.ln.Close()
	}
	for s := range srv.sessions {
		s.Close()
	}
	srv.mu.Unlock()
	srv.wg.Wait()
}

// bindOnline 把 uid 绑到 s；已有在线会话则顶掉它。
func (srv *Server) bindOnline(uid int32, s *Session) {
	srv.mu.Lock()
	old := srv.online[uid]
	srv.online[uid] = s
	srv.mu.Unlock()
	if old != nil && old != s {
		slog.Info("kick: logged in elsewhere", "uid", uid, "old", old.id, "new", s.id)
		old.sendMsg("您的账号在别处登录，已断开连接。")
		old.Close()
	}
}

func (srv *Server) removeSession(s *Session, uid int32) {
	srv.mu.Lock()
	delete(srv.sessions, s)
	if uid != 0 && srv.online[uid] == s {
		delete(srv.online, uid)
	}
	srv.mu.Unlock()
}

// Online 返回在线人数。
func (srv *Server) Online() int {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return len(srv.online)
}
