// xymserver 是迷你西游（细水长流）真协议服务端，client2 直连 127.0.0.1:28417。
//
//	go run ./cmd/xymserver -hotfix ../analysis/data/hotfix,hotfix-local
//	adb reverse tcp:28417 tcp:28417   # 手机经 USB 连电脑
package main

import (
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"turnrpg/internal/store"
	"turnrpg/internal/xymsrv"
)

func main() {
	addr := flag.String("addr", ":28417", "监听地址")
	dataDir := flag.String("data", "data/xym", "存档目录（sqlite 首次启动从这里导入旧 JSON 存档；file 模式的存档位置）")
	storeKind := flag.String("store", "sqlite", "存档方式：sqlite | file（JSON 文件，兜底用）")
	dbPath := flag.String("db", "", "SQLite 库文件，空 = <data>/xym.db")
	hotfixDir := flag.String("hotfix", "", "登录时下发的 hotfix 目录，逗号分隔多个（*.lua，文件名 NN_<id>.lua），空 = 不发")
	hotfixID := flag.String("hotfix-id", "1.5.403", "文件名不带 id 时用的 hotfix_id")
	idle := flag.Duration("idle", 30*time.Minute, "登录后读空闲超时（客户端无心跳），0 = 不限")
	debug := flag.Bool("debug", false, "输出 debug 日志")
	recDir := flag.String("records", "", "战报存档目录（每场战报一个文件，便于排查 / 回放），空 = <data>/records，\"-\" = 不存")
	recDays := flag.Int("record-days", 7, "战报保留天数，过期整天删除，0 = 不删")
	admin := flag.String("admin", "127.0.0.1:28418", "管理接口（发邮件等，见 internal/xymsrv/admin.go）监听地址，空 = 不开")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataDir, "xym.db")
	}
	st, err := store.Open(*storeKind, *dataDir, *dbPath)
	if err != nil {
		fatal("open store", err)
	}
	defer st.Close()
	accounts, err := xymsrv.OpenAccounts(st)
	if err != nil {
		fatal("open accounts", err)
	}
	roles, err := xymsrv.OpenRoles(st)
	if err != nil {
		fatal("open roles", err)
	}
	cfg := xymsrv.DefaultConfig()
	cfg.IdleTimeout = *idle
	cfg.RecordDir, cfg.RecordDays = *recDir, *recDays
	if cfg.RecordDir == "" {
		cfg.RecordDir = filepath.Join(*dataDir, "records")
	} else if cfg.RecordDir == "-" {
		cfg.RecordDir = ""
	}
	var n int
	if cfg.Hotfix, n, err = xymsrv.LoadHotfix(*hotfixDir, *hotfixID); err != nil {
		fatal("load hotfix", err)
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fatal("listen", err)
	}
	srv := xymsrv.NewServer(cfg, accounts, roles, xymsrv.OpenPlayers(st))
	where := *dbPath
	if *storeKind == "file" {
		where = *dataDir
	}
	slog.Info("xymserver listening", "addr", ln.Addr().String(), "store", *storeKind, "at", where, "hotfix", n)

	if *admin != "" {
		go func() {
			slog.Info("admin listening", "addr", *admin)
			if err := http.ListenAndServe(*admin, srv.AdminHandler()); err != nil {
				slog.Error("admin", "err", err)
			}
		}()
	}

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		slog.Info("shutting down")
		srv.Shutdown()
	}()
	if err := srv.Serve(ln); err != nil {
		fatal("serve", err)
	}
	slog.Info("bye")
}

func fatal(what string, err error) {
	slog.Error(what, "err", err)
	os.Exit(1)
}
