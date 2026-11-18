package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// Run the actual release executable against a new file store, never production
// accounts. This verifies both inlined price branches in each deployed baseline.
func TestExternalMaterialPrice(t *testing.T) {
	binary := os.Getenv("XYM_MATERIAL_BINARY")
	if binary == "" {
		t.Skip("set XYM_MATERIAL_BINARY to the release executable")
	}
	dir := t.TempDir()
	st, err := store.Open("file", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	accounts, err := OpenAccounts(st)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := accounts.Register("matprobe", "pw", "abcd1234")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := OpenRoles(st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = roles.Create(acc.UID, 1, "matprobe"); err != nil {
		t.Fatal(err)
	}
	if _, err = roles.Update(acc.UID, func(ro *Role) bool { ro.Gold = 2000; return true }); err != nil {
		t.Fatal(err)
	}
	p, err := OpenPlayers(st).Get(acc.UID)
	if err != nil {
		t.Fatal(err)
	}
	x := p.xianling(rand.New(rand.NewPCG(1, 2)))
	var needs []xlNeed
	for _, star := range []int{3, 4, 5} {
		needs = append(needs, xlNeed{Kind: "magic", Type: int(gamedata.BooksByStar[star][0])})
	}
	x.Goods[0].Need = append([]xlNeed(nil), needs...)
	x.Goods[1].Need = append([]xlNeed(nil), needs...)
	if err = p.save(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	log, err := os.Create(filepath.Join(dir, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(binary, "-store", "file", "-data", dir, "-addr", addr, "-admin", "127.0.0.1:0", "-hotfix", "", "-records", "-")
	cmd.Dir = dir
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	ready := false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		conn, e := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if e == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		data, _ := os.ReadFile(filepath.Join(dir, "server.log"))
		t.Fatalf("probe startup failed: %s", data)
	}
	c := dial(t, addr)
	defer c.conn.Close()
	c.sendRaw(xym.CLogin, loginBody("matprobe", "pw"))
	c.until(t, "LOGIN_PTO.S_LOGIN_SUC")
	gold := func() int32 {
		// File stores cache roles per process; read a new store to observe the
		// child executable's committed balance rather than the fixture cache.
		fresh := store.OpenFile(dir)
		rows, e := fresh.LoadRoles()
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range rows {
			if r.UID == acc.UID {
				return r.Gold
			}
		}
		t.Fatal("missing probe role")
		return 0
	}
	remaining := int32(2000)
	for i, cost := range []int32{50, 300, 300} {
		c.sendExt("ETC_PTO.C_XIANLING_SHOP_IGNORE_STUFF", xym.Msg{"pos": 0, "type": needs[i].Type, "stuff_kind": "magic"})
		c.until(t, "ETC_PTO.S_SEND_XIANLING_SHOP_INFO")
		remaining -= cost
		if gold() != remaining {
			t.Fatalf("direct material star %d: gold=%d want %d", i+3, gold(), remaining)
		}
	}
	c.sendExt("ETC_PTO.C_XIANLING_SHOP_EXCHANGE_GOODS", xym.Msg{"pos": 1, "need": []xym.Msg{}})
	ask := c.until(t, "ETC_PTO.S_ASK_BOX")["ETC_PTO.S_ASK_BOX"]
	if !strings.Contains(fmt.Sprint(ask), strconv.Itoa(650)) {
		t.Fatal("bulk missing-material quote", ask)
	}
	c.sendExt("ETC_PTO.C_ASK_BOX", xym.Msg{"session": ask["session"], "opt": 0})
	c.until(t, "ETC_PTO.S_REWARD_MSG")
	if gold() != 700 {
		t.Fatalf("bulk material charge: gold=%d want 700", gold())
	}
	t.Log("release executable verified: 3-star=50, 4/5-star=300, missing-material total=650, actual persisted balances correct")
}
