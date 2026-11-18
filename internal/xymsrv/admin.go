package xymsrv

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"turnrpg/internal/xym"
)

// 管理接口（xymserver -admin 127.0.0.1:28418，只应监听本机）：
//
//	GET  /players                 角色列表 [{uid, name, grade, online}]
//	POST /mail  {"to": 20001, "title": "补偿", "text": "...", "gold": 100, "cash": 50000, "soul": 0,
//	             "items": {"WP10317": 2, "DZ25001": 1}, "star": true}
//	            to 为 0 时发给全服所有角色；返回 {"sent": 封数}
//
//	POST /perfect {"urs": "test2", "pwd": "123456", "code": "abcd1234", "name": "test2"}
//	            建（或重置）测试用完美账号（perfect.go），角色须离线；返回 {"uid": …}
//
//	POST /perfect_uid {"uid": 20005} 或 {"urs": "账号名"}
//	            把已有角色直接改成完美账号（在线先踢下线；护法 / 装备 / 法术书整体替换）；返回 {"uid": …}
//	            本地脚本：server/tools/perfect_remote.py
//
//	GET /catalog、GET /player、POST /role、POST /kick：GM 工具用（gm.go，Desktop/mnxy/GM）
//
//	POST /starwar/start          今天的星战 / 超级星战立刻截止报名、开赛（测试用，starwar.go）
//
//	GET  /records?day=20261021&uid=20022   某天（空 = 今天）的战报文件列表，uid 可省（records.go）
//	GET  /records/file?f=20261021/113245_20022_7.rec   下载战报文件（本地用 go run ./cmd/xymrec 查看）
//	POST /records/play {"uid": 20022, "file": "20261021/113245_20022_7.rec"}   把战报发给在线玩家回放
//
// 例：curl -X POST localhost:28418/mail --data-binary @mail.json（mail.json 存成 UTF-8；Windows 命令行里直接写中文会乱码）

type mailReq struct {
	Items map[string]int32 `json:"items"`
	UIDs  []int32          `json:"uids,omitempty"`
	Title string           `json:"title"`
	Text  string           `json:"text"`
	To    int32            `json:"to"`
	Gold  int32            `json:"gold"`
	Cash  int32            `json:"cash"`
	Soul  int32            `json:"soul"`
	Star  bool             `json:"star"`
}

// AdminHandler 是管理接口的 HTTP 处理器。
func (srv *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /chat", func(w http.ResponseWriter, r *http.Request) {
		srv.chat.Lock()
		out := []map[string]any{}
		for ch, list := range srv.chat.Hist {
			for _, m := range list {
				out = append(out, map[string]any{
					"channel": ch,
					"sn":      m.SN,
					"uid":     m.UID,
					"name":    m.Name,
					"msg":     m.Msg,
					"time":    m.TS,
				})
			}
		}
		srv.chat.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"count": len(out), "list": out})
	})
	mux.HandleFunc("GET /players", func(w http.ResponseWriter, r *http.Request) {
		type row struct {
			Name   string `json:"name"`
			Grade  int    `json:"grade"`
			UID    int32  `json:"uid"`
			Online bool   `json:"online"`
		}
		var out []row
		srv.roles.mu.Lock()
		for _, ro := range srv.roles.ByUID {
			out = append(out, row{UID: ro.UID, Name: ro.Name, Grade: ro.Grade})
		}
		srv.roles.mu.Unlock()
		for i := range out {
			out[i].Online = srv.sessionOf(out[i].UID) != nil
		}
		sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
		writeJSON(w, http.StatusOK, out)
	})
	srv.gmRecordHandlers(mux)
	mux.HandleFunc("GET /records", func(w http.ResponseWriter, r *http.Request) {
		uid, _ := strconv.Atoi(r.URL.Query().Get("uid"))
		writeJSON(w, http.StatusOK, srv.listRecords(r.URL.Query().Get("day"), int32(uid)))
	})
	mux.HandleFunc("GET /records/file", func(w http.ResponseWriter, r *http.Request) {
		b, err := srv.readRecord(r.URL.Query().Get("f"))
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(b)
	})
	mux.HandleFunc("POST /records/play", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID  int32  `json:"uid"`
			File string `json:"file"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		b, err := srv.readRecord(req.File)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		s := srv.sessionOf(req.UID)
		if s == nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "player offline"})
			return
		}
		s.send(xym.MainExt, b)
		writeJSON(w, http.StatusOK, map[string]int{"bytes": len(b)})
	})
	mux.HandleFunc("POST /starwar/start", func(w http.ResponseWriter, r *http.Request) {
		srv.StarwarStartNow()
		writeJSON(w, http.StatusOK, map[string]string{"ok": "started"})
	})
	mux.HandleFunc("POST /mail", func(w http.ResponseWriter, r *http.Request) {
		var req mailReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// JSON 解码会把非 UTF-8 字节换成 U+FFFD，所以查替换符
		if strings.ContainsRune(req.Title, utf8.RuneError) || strings.ContainsRune(req.Text, utf8.RuneError) {
			// Windows 命令行里直接写中文会按系统代码页传，改用 --data-binary @文件（UTF-8）
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title/text must be UTF-8 (use curl --data-binary @mail.json)"})
			return
		}
		if req.Gold < 0 || req.Cash < 0 || req.Soul < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "negative amount"})
			return
		}
		for code, n := range req.Items {
			if !mailItemOK(code) || n <= 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item " + code})
				return
			}
		}
		if (req.To == -1) != (len(req.UIDs) > 0) || len(req.UIDs) > 10000 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid mail recipients"})
			return
		}
		spec := MailSpec{Title: req.Title, Text: req.Text, Gold: req.Gold, Cash: req.Cash,
			Soul: req.Soul, Items: req.Items, Star: req.Star}
		var n int
		var err error
		if req.To == -1 {
			n, err = srv.sendMailToUIDs(req.UIDs, spec)
		} else {
			n, err = srv.SendMail(req.To, spec)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"sent": n})
	})
	mux.HandleFunc("POST /perfect", func(w http.ResponseWriter, r *http.Request) {
		var req PerfectSpec
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		uid, err := srv.MakePerfect(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int32{"uid": uid})
	})
	mux.HandleFunc("POST /perfect_uid", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID int32  `json:"uid"`
			URS string `json:"urs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		uid, err := srv.PerfectExisting(req.UID, req.URS)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int32{"uid": uid})
	})
	srv.gmHandlers(mux) // GM 工具：/catalog /player /role /kick（gm.go）
	srv.gmRegistrationHandlers(mux)
	srv.gmOpsHandlers(mux)
	srv.gmGameRuleHandlers(mux)
	return auditWrap(srv.cfg.GMLog, mux)
}

// auditWrap 是 GM 操作审计：把每个管理接口请求（方法、路径、请求体、响应码）写进
// slog 和 gm.log（JSON Lines，每行一条），供事后追溯谁改了什么。请求体里的 pwd
// 会打码，其余原样记录（截断到 8KB）。
func auditWrap(logPath string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
			if len(body) > 1<<20 {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request exceeds 1 MiB"})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		aw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(aw, r)
		clean := sanitizeBody(body)
		if runes := []rune(clean); len(runes) > 8192 {
			clean = string(runes[:8192]) + "…"
		}
		logGMEvent(logPath, r.Method+" "+r.URL.Path, r.RemoteAddr, aw.status, clean)
	})
}

// logGMEvent 写一条 GM 相关审计流水（slog + gm.log JSON Lines）：管理接口操作、
// 福利邮件被玩家领取（h_mail.go）等，供追溯谁发的、谁领的。
func logGMEvent(logPath, op, remote string, status int, body string) {
	slog.Info("gm op", "op", op, "remote", remote, "status", status, "body", body)
	if logPath == "" {
		return
	}
	rec := map[string]any{"t": time.Now().Format(time.RFC3339), "op": op, "remote": remote, "status": status}
	if body != "" {
		rec["body"] = body
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Error("gm audit log", "path", logPath, "err", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		slog.Error("gm audit log", "path", logPath, "err", err)
	}
}

// sanitizeBody 把请求体整理成审计文本：JSON 里的 pwd 打码，非 JSON 截断到 2KB（按字符切）。
func sanitizeBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(body, &m) == nil {
		for _, k := range []string{"pwd", "password", "code"} {
			if _, ok := m[k]; ok {
				m[k] = "***"
			}
		}
		if b, err := json.Marshal(m); err == nil {
			return string(b)
		}
	}
	s := string(body)
	if r := []rune(s); len(r) > 2048 {
		s = string(r[:2048]) + "…"
	}
	return s
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
