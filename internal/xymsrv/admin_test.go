package xymsrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAudit：GM 操作审计——每次管理接口请求都落 gm.log（JSON Lines），pwd 打码。
func TestAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gm.log")
	h := auditWrap(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "x"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"sent": 1})
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mail",
		strings.NewReader(`{"to":20001,"title":"补偿","gold":100}`)))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/perfect",
		strings.NewReader(`{"urs":"test2","pwd":"s3cret!"}`)))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/players", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/bad", nil))

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines %d != 4: %s", len(lines), b)
	}
	var recs []map[string]any
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad json %q: %v", l, err)
		}
		recs = append(recs, m)
	}
	if recs[0]["op"] != "POST /mail" || recs[0]["status"] != float64(http.StatusOK) {
		t.Fatalf("rec0 %v", recs[0])
	}
	if body, _ := recs[0]["body"].(string); !strings.Contains(body, "20001") {
		t.Fatalf("rec0 body %v", recs[0]["body"])
	}
	// pwd 打码：不落明文
	body, _ := recs[1]["body"].(string)
	if strings.Contains(body, "s3cret!") || !strings.Contains(body, `"pwd":"***"`) {
		t.Fatalf("pwd not masked: %q", body)
	}
	if recs[2]["op"] != "GET /players" {
		t.Fatalf("rec2 %v", recs[2])
	}
	if _, ok := recs[2]["body"]; ok {
		t.Fatalf("GET should have no body: %v", recs[2])
	}
	if recs[3]["status"] != float64(http.StatusBadRequest) {
		t.Fatalf("rec3 %v", recs[3])
	}
}

// TestAuditNoFile：GMLog 为空时只走 slog，不应写任何文件、也不应出错。
func TestAuditNoFile(t *testing.T) {
	h := auditWrap("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]int{"ok": 1})
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mail", strings.NewReader(`{"to":1}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
}
