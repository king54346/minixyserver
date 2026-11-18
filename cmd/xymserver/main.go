// xymserver 是迷你西游（再续前缘）真协议服务端，client2 直连 127.0.0.1:28417。
//
//	go run ./cmd/xymserver -hotfix ../analysis/data/hotfix,hotfix-local
//	adb reverse tcp:28417 tcp:28417   # 手机经 USB 连电脑
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof"
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
	storeKind := flag.String("store", "sqlite", "存档方式：mysql（DATABASE_URL）| sqlite | file")
	dbPath := flag.String("db", "", "SQLite 库文件，空 = <data>/xym.db")
	hotfixDir := flag.String("hotfix", "", "登录时下发的 hotfix 目录，逗号分隔多个（*.lua，文件名 NN_<id>.lua），空 = 不发")
	hotfixID := flag.String("hotfix-id", "1.5.403", "文件名不带 id 时用的 hotfix_id")
	idle := flag.Duration("idle", 30*time.Minute, "登录后读空闲超时（客户端无心跳），0 = 不限")
	debug := flag.Bool("debug", false, "输出 debug 日志")
	recDir := flag.String("records", "", "战报存档目录（每场战报一个文件，便于排查 / 回放），空 = <data>/records，\"-\" = 不存")
	recDays := flag.Int("record-days", 7, "战报保留天数，过期整天删除，0 = 不删")
	admin := flag.String("admin", "127.0.0.1:28418", "管理接口（发邮件等，见 internal/xymsrv/admin.go）监听地址，空 = 不开")
	checkConfig := flag.Bool("check-config", false, "验证存储连接与热更新配置后退出，不监听游戏端口")
	profileAddr := flag.String("pprof", "", "性能诊断 HTTP 地址，仅允许本机，空 = 不开")
	prestigePath := flag.String("prestige-rules", os.Getenv("XYM_PRESTIGE_RULES"), "排行榜威望分档 JSON，空 = 暂不增加额外榜单威望")
	draftRewardsPath := flag.String("draft-rewards", "", "轮抓第1至10名奖励 JSON 数组，空 = 不发奖")
	newRoleGold := flag.Int("new-role-gold", 2_000_000, "新建角色赠送元宝，0 = 不赠送")
	newRoleChests := flag.Int("new-role-chests", 2600, "新建角色赠送黄金宝箱及同量钥匙，0 = 不赠送")
	flag.Parse()
	if *newRoleGold < 0 || *newRoleGold > 2_000_000_000 || *newRoleChests < 0 || *newRoleChests > 100_000 {
		fatal("new role gift", fmt.Errorf("new-role-gold must be 0..2000000000 and new-role-chests 0..100000"))
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	prestigeRules, err := xymsrv.LoadPrestigeRules(*prestigePath)
	if err != nil {
		fatal("load prestige rules", err)
	}
	var draftRewards [10]xymsrv.MailSpec
	if *draftRewardsPath != "" {
		data, err := os.ReadFile(*draftRewardsPath)
		if err != nil {
			fatal("load draft rewards", err)
		}
		var ranks []xymsrv.MailSpec
		if err := json.Unmarshal(data, &ranks); err != nil {
			fatal("decode draft rewards", err)
		}
		if len(ranks) > len(draftRewards) {
			fatal("decode draft rewards", fmt.Errorf("at most %d rank rewards", len(draftRewards)))
		}
		copy(draftRewards[:], ranks)
	}

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataDir, "xym.db")
	}
	st, err := store.Open(*storeKind, *dataDir, *dbPath)
	if err != nil {
		fatal("open store", err)
	}
	defer st.Close()
	if *checkConfig {
		_, n, err := xymsrv.LoadHotfixBatches(*hotfixDir, *hotfixID)
		if err != nil {
			fatal("load hotfix", err)
		}
		slog.Info("configuration verified", "store", *storeKind, "hotfix", n)
		return
	}
	if *profileAddr != "" {
		host, _, err := net.SplitHostPort(*profileAddr)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			fatal("pprof address", fmt.Errorf("pprof must listen on a loopback address"))
		}
		go func() {
			if err := http.ListenAndServe(*profileAddr, http.DefaultServeMux); err != nil {
				slog.Error("pprof", "err", err)
			}
		}()
	}
	accounts, err := xymsrv.OpenAccounts(st)
	if err != nil {
		fatal("open accounts", err)
	}
	roles, err := xymsrv.OpenRoles(st)
	if err != nil {
		fatal("open roles", err)
	}
	cfg := xymsrv.DefaultConfig()
	cfg.NewRoleGold, cfg.NewRoleChests = int32(*newRoleGold), int32(*newRoleChests)
	cfg.PrestigeRules = prestigeRules
	cfg.DraftRewards = draftRewards
	cfg.IdleTimeout = *idle
	cfg.RecordDir, cfg.RecordDays = *recDir, *recDays
	if cfg.RecordDir == "" {
		cfg.RecordDir = filepath.Join(*dataDir, "records")
	} else if cfg.RecordDir == "-" {
		cfg.RecordDir = ""
	}
	cfg.GMLog = filepath.Join(*dataDir, "gm.log")
	cfg.EnableGMOps = true
	cfg.GMOpsLog = filepath.Join(*dataDir, "gm-ops.jsonl")
	var n int
	if cfg.HotfixBatches, n, err = xymsrv.LoadHotfixBatches(*hotfixDir, *hotfixID); err != nil {
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
	} else if *storeKind == "mysql" {
		where = "MySQL (private environment configuration)"
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
