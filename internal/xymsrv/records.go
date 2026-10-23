package xymsrv

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// 战报存档：每场发给客户端的 FIGHT_PTO.S_FIGHT_RECORD（编码后的整包：双方名字、阵容、战报字节）像日志一样
// 落盘，方便排查和回放。文件：<RecordDir>/<YYYYMMDD>/<HHMMSS>_<uid>_<序号>.rec（原样的扩展协议 body）。
// 按天分目录，超过 RecordDays 天的整天目录在启动时和之后每小时删掉。
//
// 查看：go run ./cmd/xymrec <文件>（双方阵容 + 逐条战报命令）；回放：管理接口 POST /records/play
// 把文件原样发给在线玩家（与「重播」C_PLAY_RECORD 同一条协议）。

var recordFileSeq atomic.Uint64

// saveRecord 把一场战报写进存档目录（异步，失败只记日志）。
func (srv *Server) saveRecord(uid int32, body []byte) {
	dir := srv.cfg.RecordDir
	if dir == "" {
		return
	}
	now := time.Now()
	name := filepath.Join(dir, now.Format("20060102"),
		fmt.Sprintf("%s_%d_%d.rec", now.Format("150405"), uid, recordFileSeq.Add(1)))
	data := append([]byte(nil), body...)
	go func() {
		err := os.MkdirAll(filepath.Dir(name), 0o755)
		if err == nil {
			err = os.WriteFile(name, data, 0o644)
		}
		if err != nil {
			slog.Warn("save record", "file", name, "err", err)
		}
	}()
}

// pruneRecords 删掉超过 RecordDays 天的整天目录。
func (srv *Server) pruneRecords() {
	dir, days := srv.cfg.RecordDir, srv.cfg.RecordDays
	if dir == "" || days <= 0 {
		return
	}
	cut := time.Now().AddDate(0, 0, -days).Format("20060102")
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if n := e.Name(); e.IsDir() && len(n) == 8 && n < cut {
			if err := os.RemoveAll(filepath.Join(dir, n)); err != nil {
				slog.Warn("prune records", "dir", n, "err", err)
			} else {
				slog.Info("pruned records", "dir", n)
			}
		}
	}
}

// recordsLoop 启动时清一次，之后每小时清一次，直到服务器关闭。
func (srv *Server) recordsLoop() {
	srv.pruneRecords()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for range t.C {
		srv.mu.Lock()
		closed := srv.closed
		srv.mu.Unlock()
		if closed {
			return
		}
		srv.pruneRecords()
	}
}

// listRecords 列出某天（YYYYMMDD，空 = 今天）的战报文件，uid 非 0 时只列该玩家的；按时间倒序。
func (srv *Server) listRecords(day string, uid int32) []string {
	if srv.cfg.RecordDir == "" {
		return nil
	}
	if day == "" {
		day = time.Now().Format("20060102")
	}
	ents, _ := os.ReadDir(filepath.Join(srv.cfg.RecordDir, filepath.Base(day)))
	var out []string
	for _, e := range ents {
		if n := e.Name(); strings.HasSuffix(n, ".rec") && (uid == 0 || strings.Contains(n, fmt.Sprintf("_%d_", uid))) {
			out = append(out, day+"/"+n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// readRecord 读存档里的一个战报文件（rel 形如 20261021/113245_20022_7.rec，不许跳出存档目录）。
func (srv *Server) readRecord(rel string) ([]byte, error) {
	if srv.cfg.RecordDir == "" {
		return nil, fmt.Errorf("records disabled")
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 2 || parts[0] != filepath.Base(parts[0]) || parts[1] != filepath.Base(parts[1]) ||
		strings.HasPrefix(parts[0], ".") || strings.HasPrefix(parts[1], ".") {
		return nil, fmt.Errorf("bad record path %q", rel)
	}
	return os.ReadFile(filepath.Join(srv.cfg.RecordDir, parts[0], parts[1]))
}
