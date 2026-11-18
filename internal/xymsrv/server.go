// Package xymsrv 是迷你西游真协议的网关：分帧/登录/会话管理，登录后的扩展协议按 PID 分发给 handler。
package xymsrv

import (
	"errors"
	"fmt"
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
	HotfixBatches    [][]byte      // 多帧补丁，按文件顺序在登录成功前发完
	PrestigeRules    PrestigeRules // 额外排行榜威望，空分档不加分
	RecordDir        string        // 战报存档目录（records.go），空 = 不存
	RecordDays       int           // 战报保留天数，0 = 不删
	GMLog            string        // GM 操作审计日志文件（admin.go），空 = 只进 slog
	NewRoleGold      int32         // New character's initial balance; never applied to existing roles.
	NewRoleChests    int32         // Golden chests and matching keys for new inventories.
	EnableGMOps      bool
	GMOpsLog         string
	DraftRewards     [10]MailSpec // 按周榜名次发放，空配置不发奖。
}

func DefaultConfig() Config {
	return Config{HandshakeTimeout: 30 * time.Second, IdleTimeout: 30 * time.Minute, CreateTimeout: 30 * time.Minute}
}

type Server struct {
	cfg       Config
	accounts  *Accounts
	guards    failGuard // 全部连接共享账号/IP失败计数
	roles     *Roles
	players   *Players
	ops       *GMOps
	gameRules *gameRules
	opsStop   chan struct{}
	social    *Social    // 好友 / 私信 / 礼物 / 邮件（social.go）
	chat      *chatState // 聊天频道（h_chat.go）
	dongfu    *Dongfus   // 洞府（dongfu.go）
	ladder    *Ladder    // 论道榜（lundao.go）
	starwar   *Starwar   // 星战（starwar.go）
	tianti    *Tianti    // 天梯（tianti.go）
	draft     *weeklyDraft
	jiuyou    *Jiuyou // 九幽试炼排行（jiuyou.go）
	liexi     *Liexi  // 时空竞技场积分榜（liexi_jjc.go）
	qjRankMu  sync.Mutex
	lrCache   lrCache      // 关卡排行（level_rank.go）
	qjBoard   *qjRankBoard // 西天取经进度榜（qujing.go，首次用时从存档读）

	nextID atomic.Uint64

	mu       sync.Mutex
	ln       net.Listener
	closed   bool
	sessions map[*Session]struct{}
	online   map[int32]*Session // uid -> 会话，用于顶号
	wg       sync.WaitGroup
}

func NewServer(cfg Config, accounts *Accounts, roles *Roles, players *Players) *Server {
	roles.newRoleGold, players.newRoleChests = max(cfg.NewRoleGold, 0), max(cfg.NewRoleChests, 0)
	srv := &Server{cfg: cfg, accounts: accounts, roles: roles, players: players, social: openSocial(players.st), chat: newChat(players.st),
		dongfu: openDongfus(players.st), ladder: openLadder(players.st), starwar: openStarwar(players.st), tianti: openTianti(players.st), jiuyou: openJiuyou(players.st), liexi: openLiexi(players.st),
		sessions: map[*Session]struct{}{}, online: map[int32]*Session{}, opsStop: make(chan struct{})}
	srv.ops = newGMOps(players.st, cfg.GMOpsLog)
	srv.gameRules = newGameRules(players.st)
	srv.draft = openWeeklyDraft(players.st)
	srv.tianti.srv = srv
	return srv
}

// Serve 在 ln 上接受连接，直到 Shutdown。
func (srv *Server) Serve(ln net.Listener) error {
	if srv.gameRules.err != nil {
		return fmt.Errorf("GM gameplay configuration: %w", srv.gameRules.err)
	}
	if srv.cfg.EnableGMOps && srv.ops.startupErr != nil {
		return fmt.Errorf("GM operations configuration: %w", srv.ops.startupErr)
	}
	srv.mu.Lock()
	if srv.closed {
		srv.mu.Unlock()
		return errors.New("xymsrv: server closed")
	}
	srv.ln = ln
	if srv.cfg.EnableGMOps {
		srv.wg.Add(1)
	}
	srv.mu.Unlock()
	go srv.regenLoop()
	go srv.starwarLoop()
	go srv.ttLoop()
	go srv.draftLoop()
	go srv.recordsLoop()
	go srv.watchRequests()
	if srv.cfg.EnableGMOps {
		go srv.gmOpsLoop()
	}
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
	if !srv.closed {
		srv.closed = true
		close(srv.opsStop)
	}
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
