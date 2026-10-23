package xymsrv

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

// TestRecordsSaved：打一关后战报落盘（按天目录、文件名带 uid），文件能原样解码并通过回放检查；
// 管理接口能列出、下载、发给在线玩家回放；过期的整天目录被清掉。
func TestRecordsSaved(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RecordDir, cfg.RecordDays = t.TempDir(), 3
	srv, addr, _ := startServerSrv(t, cfg)
	c := enterWorld(t, addr, "recorder")
	c.sendExt("TASK_PTO.C_START_FIGHT", xym.Msg{"gid": 10101, "type": 0, "pos1": 1, "pos2": 2, "pos3": 3, "pos4": 4, "pos5": 5})
	sent := c.expectExt("FIGHT_PTO.S_FIGHT_RECORD")

	var files []string
	for range 100 { // 异步写
		if files = srv.listRecords("", 0); len(files) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	uid := srv.onlineSessions()[0].role.UID
	if len(files) != 1 || len(srv.listRecords("", uid)) != 1 || len(srv.listRecords("", uid+1)) != 0 {
		t.Fatalf("files %v", files)
	}
	body, err := srv.readRecord(files[0])
	if err != nil {
		t.Fatal(err)
	}
	_, _, m, err := xym.DecodeExt(xym.S2C, body)
	if err != nil || !bytes.Equal(m["record"].([]byte), sent["record"].([]byte)) {
		t.Fatalf("decode %v", err)
	}
	if _, err := battle.DecodeRecord(m["record"].([]byte)); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.readRecord("../" + filepath.Base(files[0])); err == nil {
		t.Fatal("path escape")
	}

	h := srv.AdminHandler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/records/file?f="+files[0], nil))
	if !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatal("download")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/records/play",
		strings.NewReader(`{"uid": `+strconv.Itoa(int(uid))+`, "file": "`+files[0]+`"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("play %d %s", w.Code, w.Body)
	}
	if got := c.until(t, "FIGHT_PTO.S_FIGHT_RECORD")["FIGHT_PTO.S_FIGHT_RECORD"]; !bytes.Equal(got["record"].([]byte), sent["record"].([]byte)) {
		t.Fatal("replayed record differs")
	}

	old := filepath.Join(cfg.RecordDir, time.Now().AddDate(0, 0, -4).Format("20060102"))
	keep := filepath.Join(cfg.RecordDir, time.Now().AddDate(0, 0, -2).Format("20060102"))
	for _, d := range []string{old, keep} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	srv.pruneRecords()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old dir kept")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("recent dir removed")
	}
	if len(srv.listRecords("", 0)) != 1 {
		t.Fatal("today removed")
	}
}
