package xymsrv

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"turnrpg/internal/xym"
)

func TestExplicitHotfixResourceVersionCompatibility(t *testing.T) {
	dir := t.TempDir()
	code := "-- @compat 1.5.353 1.5.403 bad.version 1.5.353\nVALUE=1\n"
	if err := os.WriteFile(filepath.Join(dir, "13_1.5.403.lua"), []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	batches, n, err := LoadHotfixBatches(dir, "1.5.403")
	if err != nil || n != 2 || len(batches) != 1 {
		t.Fatalf("frames=%d entries=%d err=%v", len(batches), n, err)
	}
	_, _, m, err := xym.DecodeExt(xym.S2C, batches[0])
	if err != nil {
		t.Fatal(err)
	}
	rows := m["contents"].([]xym.Msg)
	ids := []string{rows[0]["hotfix_id"].(string), rows[1]["hotfix_id"].(string)}
	if !reflect.DeepEqual(ids, []string{"1.5.403", "1.5.353"}) {
		t.Fatal(ids)
	}
	for _, r := range rows {
		if r["hotfix_content"] != "VALUE=1\n" {
			t.Fatalf("content changed: %v", r)
		}
	}
	// Official/other patches without opt-in retain their original version only.
	if ids := compatibleHotfixVersions([]byte("VALUE=1"), "1.5.403"); len(ids) != 1 || ids[0] != "1.5.403" {
		t.Fatal(ids)
	}
}

func TestHotfixBatchesLoginOrder(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		src := strings.Repeat("x=1\n", 7500) + "part=" + strconv.Itoa(i) + "\n"
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(i)+"_1.5.403.lua"), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	batches, n, err := LoadHotfixBatches(dir, "1.5.403")
	if err != nil || n != 3 || len(batches) != 2 {
		t.Fatalf("batches=%d files=%d err=%v", len(batches), n, err)
	}
	cfg := DefaultConfig()
	cfg.HotfixBatches = batches
	addr, _ := startServer(t, cfg)
	c := dial(t, addr)
	c.sendRaw(xym.CRegister, registerBody("hfbatch", "pw", "abcd1234"))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	c.recv()
	c.sendRaw(xym.CCreate, cat([]byte{1, 0}, str("沙僧")))
	seen := 0
	for _, body := range batches {
		if len(body) > 65535 {
			t.Fatal("oversized frame")
		}
		m := c.expectExt("LOGIN_PTO.S_SEND_NEW_HOTFIX_CONTENT")
		for _, row := range m["contents"].([]xym.Msg) {
			if !strings.HasSuffix(row["hotfix_content"].(string), "part="+strconv.Itoa(seen)+"\n") {
				t.Fatal("patch order changed")
			}
			seen++
		}
	}
	if seen != 3 {
		t.Fatal(seen)
	}
	c.expectExt("LOGIN_PTO.S_NEW_USER")
	c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
}

func TestHotfixRejectOversizedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "large.lua"), []byte(strings.Repeat("x=1\n", 17000)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadHotfixBatches(dir, "1.5.403"); err == nil {
		t.Fatal("single oversized patch accepted")
	}
}
