package xymsrv

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func (srv *Server) gmOpsHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /gm/operations", func(w http.ResponseWriter, r *http.Request) {
		if srv.ops.startupErr != nil {
			writeJSON(w, 500, map[string]string{"error": srv.ops.startupErr.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"settings": srv.ops.current.Load(), "seven_day_template": sevenDayRule(), "conservative_drops": conservativeDropSettings(), "base_drops": defaultOpsSettings().Drops, "scheduler_running": srv.cfg.EnableGMOps, "server_time": time.Now().Format(time.RFC3339), "full_activity": 125})
	})
	mux.HandleFunc("POST /gm/settings", func(w http.ResponseWriter, r *http.Request) {
		var c OpsSettings
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if err := srv.ops.update(c); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, srv.ops.current.Load())
	})
	mux.HandleFunc("POST /gm/preview", func(w http.ResponseWriter, r *http.Request) {
		var rule RewardRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		c := *srv.ops.current.Load()
		c.Rules = []RewardRule{rule}
		if err := validateOpsSettings(c); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		rows := srv.runRewardRule(rule, time.Now(), true)
		writeJSON(w, 200, map[string]any{"rows": rows, "dry_run": true})
	})
	mux.HandleFunc("POST /gm/run", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if !srv.ops.runMu.TryLock() {
			writeJSON(w, 409, map[string]string{"error": "发奖任务执行中，请稍后"})
			return
		}
		defer srv.ops.runMu.Unlock()
		for _, rule := range srv.ops.current.Load().Rules {
			if rule.ID == req.ID {
				if !rule.Enabled {
					writeJSON(w, 400, map[string]string{"error": "请先启用规则"})
					return
				}
				if rule.Mode == "once" && rule.Date != today() {
					writeJSON(w, 400, map[string]string{"error": "单次规则只能在指定日期执行"})
					return
				}
				if err := srv.ops.audit(OpsEvent{Kind: "manual_run", Rule: rule.ID, Status: "requested"}); err != nil {
					writeJSON(w, 500, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, 200, map[string]any{"rows": srv.runRewardRule(rule, time.Now(), false)})
				return
			}
		}
		writeJSON(w, 404, map[string]string{"error": "规则不存在"})
	})
	mux.HandleFunc("GET /gm/logs", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 500 {
			limit = 200
		}
		day := r.URL.Query().Get("day")
		if day == "" {
			day = today()
		}
		if _, err := time.Parse("2006-01-02", day); err != nil {
			writeJSON(w, 400, map[string]string{"error": "日期应为YYYY-MM-DD"})
			return
		}
		uid, _ := strconv.Atoi(r.URL.Query().Get("uid"))
		kind := r.URL.Query().Get("kind")
		matches := func(e OpsEvent) bool { return (uid <= 0 || e.UID == int32(uid)) && (kind == "" || e.Kind == kind) }
		path := srv.ops.logFile(day)
		var rows []OpsEvent
		if srv.ops.path != "" {
			f, err := os.Open(path)
			if err != nil && !os.IsNotExist(err) {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			if f != nil {
				defer f.Close()
				stat, err := f.Stat()
				if err != nil {
					writeJSON(w, 500, map[string]string{"error": err.Error()})
					return
				}
				offset := max(int64(0), stat.Size()-(2<<20))
				if uid > 0 || kind != "" {
					offset = 0
				} // History searches cover the full day's audit file.
				if _, err = f.Seek(offset, io.SeekStart); err != nil {
					writeJSON(w, 500, map[string]string{"error": err.Error()})
					return
				}
				scanner := bufio.NewScanner(io.LimitReader(f, stat.Size()-offset))
				scanner.Buffer(make([]byte, 4096), 2<<20)
				if offset > 0 {
					scanner.Scan()
				} // Skip an incomplete first line.
				for scanner.Scan() {
					if uid > 0 && !strings.Contains(scanner.Text(), fmt.Sprintf("\"uid\":%d", uid)) {
						continue
					}
					var e OpsEvent
					if json.Unmarshal(scanner.Bytes(), &e) == nil && matches(e) {
						rows = append(rows, e)
						if len(rows) > limit {
							rows = rows[1:]
						}
					}
				}
				if err := scanner.Err(); err != nil {
					writeJSON(w, 500, map[string]string{"error": err.Error()})
					return
				}
			}
		} else {
			srv.ops.journalMu.Lock()
			for _, e := range srv.ops.recent {
				if matches(e) {
					rows = append(rows, e)
				}
			}
			srv.ops.journalMu.Unlock()
			if len(rows) > limit {
				rows = rows[len(rows)-limit:]
			}
		}
		writeJSON(w, 200, map[string]any{"events": rows, "file": path})
	})
	srv.gmBroadcastRoutes(mux)
}

func (srv *Server) gmBroadcastRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /broadcast", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text  string `json:"text"`
			Popup *bool  `json:"popup"`
			Chat  *bool  `json:"chat"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		text := cleanBroadcast(req.Text)
		popup, chat := req.Popup == nil || *req.Popup, req.Chat == nil || *req.Chat
		if text == "" || len(text) > 3000 || !popup && !chat {
			writeJSON(w, 400, map[string]string{"error": "公告内容不能为空且必须至少选择一个通道（最多3000字节）"})
			return
		}
		if err := srv.ops.audit(OpsEvent{Kind: "broadcast", Status: "requested", Data: map[string]any{"text": text, "popup": popup, "chat": chat}}); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		n := srv.gmBroadcast(text, popup, chat)
		if err := srv.ops.audit(OpsEvent{Kind: "broadcast", Status: "sent", Data: map[string]any{"text": text, "recipients": n}}); err != nil {
			writeJSON(w, 500, map[string]string{"error": fmt.Sprintf("已发送%d人，日志写入失败: %v", n, err)})
			return
		}
		writeJSON(w, 200, map[string]any{"sent": n, "ok": true})
	})
}
