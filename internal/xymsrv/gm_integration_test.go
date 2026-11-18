package xymsrv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"turnrpg/internal/store"
)

func TestGMExpandedRoutesAndRoleFields(t *testing.T) {
	c, p, srv := actWorld(t, "gmexpanded")
	c.conn.Close()
	if err := srv.kickWait(p.UID); err != nil {
		t.Fatal(err)
	}
	h := srv.AdminHandler()
	req := func(method, path string, body any, status int) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(data)))
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w
	}
	for _, path := range []string{"/players", "/catalog", "/accounts", "/dongfus", "/chat", "/gm/operations"} {
		req("GET", path, nil, 200)
	}
	for _, path := range []string{"/player", "/pets", "/bag", "/progress", "/xiuwei"} {
		req("GET", fmt.Sprintf("%s?uid=%d", path, p.UID), nil, 200)
	}
	req("POST", "/role", map[string]any{"uid": p.UID, "set": map[string]int{"grade": 30, "stop_upgrade": 1, "xgrade": 10, "xiuwei": 12345}}, 200)
	ro := srv.roles.Get(p.UID)
	if ro.StopUpgrade != 1 || ro.XGrade != 10 || ro.Xiuwei != 12345 {
		t.Fatal(ro)
	}
	req("POST", "/role", map[string]any{"uid": p.UID, "set": map[string]int{"gold": 100, "stop_upgrade": 2}}, 400)
	if *srv.roles.Get(p.UID) != *ro {
		t.Fatal("invalid role request partially applied")
	}
	req("POST", "/bag_add", map[string]any{"uid": p.UID, "kind": "item", "id": 11487, "n": 3}, 200)
	if p.itemCount(11487) != 3 {
		t.Fatal("GM bag add missing")
	}
	req("POST", "/bag_del", map[string]any{"uid": p.UID, "kind": "item", "type": 11487}, 200)
	if p.itemCount(11487) != 0 {
		t.Fatal("GM bag delete missing")
	}
	req("POST", "/pet_point", map[string]any{"uid": p.UID, "npcnum": p.Pets[0].NpcNum, "attadd": 12, "hpadd": 34}, 200)
	req("POST", "/xiuwei_set", map[string]any{"uid": p.UID, "set": map[string]int{"attack": 300}}, 200)
	req("POST", "/progress_set", map[string]any{"uid": p.UID, "typ": 0, "task": 101}, 200)
	if p.Prog[0].Last != 101 || p.Prog[1].Last != 0 {
		t.Fatal("GM progress affected another mode")
	}
}

func TestGMAccountPasswordDeleteAndUIDFloor(t *testing.T) {
	_, _, srv := actWorld(t, "gmaccounts")
	acc, err := srv.accounts.Register("gmfixture", "oldpass", "abc12345")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.roles.Create(acc.UID, 1, "DeleteMe"); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.players.Get(acc.UID); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.accounts.SetPassword(acc.URS, "newpass"); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.accounts.Login(acc.URS, "oldpass", ""); err == nil {
		t.Fatal("old password accepted")
	}
	if _, err = srv.accounts.Login(acc.URS, "newpass", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.accounts.Recover(acc.URS, "abc12345", ""); err != nil {
		t.Fatal("reset broke security code", err)
	}
	if _, err = srv.gmDeleteAccount(acc.URS, "wrong"); err == nil {
		t.Fatal("missing delete confirmation")
	}
	if uid, err := srv.gmDeleteAccount(acc.URS, acc.URS); err != nil || uid != acc.UID {
		t.Fatal(uid, err)
	}
	if srv.roles.Get(acc.UID) != nil {
		t.Fatal("deleted role remains cached")
	}
	if _, err = srv.players.st.LoadPlayer(acc.UID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted player remains durable", err)
	}
	accounts, err := OpenAccounts(srv.accounts.st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = accounts.Login(acc.URS, "newpass", ""); err == nil {
		t.Fatal("deleted account remains durable")
	}
	next, err := accounts.Register("newfixture", "pw", "")
	if err != nil || next.UID <= acc.UID {
		t.Fatal("deleted UID reused", next, err)
	}
}

func TestGMDongfuRejectedBatchDoesNotMutate(t *testing.T) {
	_, _, srv := actWorld(t, "gmdongfu")
	srv.dongfu.do(func() bool { srv.dongfu.List[1] = &Dongfu{ID: 1, Lv: 1, Builds: map[string]*DFBuild{}}; return true })
	if _, err := srv.gmSetDongfuBuild(1, map[string]int{"attack": 2, "hp": 6}); err == nil {
		t.Fatal("over-cap building accepted")
	}
	srv.dongfu.do(func() bool {
		if len(srv.dongfu.List[1].Builds) != 0 {
			t.Fatal("rejected batch partially mutated")
		}
		return false
	})
}
