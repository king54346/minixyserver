package xymsrv

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"turnrpg/internal/xym"
)

func TestSecurityCodeRules(t *testing.T) {
	for _, code := range []string{"abc12345", "ABCDEFG1", "a123456789012345"} {
		if !validSecCode(code) {
			t.Errorf("rejected %q", code)
		}
	}
	for _, code := range []string{"", "1234", "abc1234", "abcdefgh", "12345678", "abc1234!", "中文abc123", " abc12345", "a1234567890123456"} {
		if validSecCode(code) {
			t.Errorf("accepted %q", code)
		}
	}
	a, err := OpenAccounts(openTestStore(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Register("bad", "pw", "1234"); !errors.Is(err, ErrBadCode) {
		t.Fatal(err)
	}
	if _, err := a.Register("same", "ABC12345", "abc12345"); !errors.Is(err, ErrBadCode) {
		t.Fatal(err)
	}
	acc, err := a.Register("legacy", "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	// Existing short codes remain usable, but cannot be used for new accounts.
	acc.PwdSalt = "00112233445566778899aabbccddeeff"
	acc.CodeHash = hashPwd([]byte{0, 17, 34, 51, 68, 85, 102, 119, 136, 153, 170, 187, 204, 221, 238, 255}, "code:1234")
	if _, err := a.Recover("legacy", "1234", ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"legacy", "missing"} {
		if _, err := a.Recover(name, "wrong", ""); !errors.Is(err, ErrBadCode) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestSecuritySIDLifetime(t *testing.T) {
	st := openTestStore(t, t.TempDir())
	a, err := OpenAccounts(st)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := a.Register("alice", "pw", "abc12345")
	if err != nil {
		t.Fatal(err)
	}
	sid, err := a.NewSID(acc)
	if err != nil {
		t.Fatal(err)
	}
	uid := uint32(acc.UID)
	if a.BySID(uid, sid) == nil || a.BySID(uid+1, sid) != nil || a.BySID(uid, "wrong") != nil {
		t.Fatal("SID matching")
	}
	a.sidIssued[acc.UID] = time.Now().Add(-sidTTL + time.Minute)
	if a.BySID(uid, sid) == nil {
		t.Fatal("expired too early")
	}
	a.sidIssued[acc.UID] = time.Now().Add(-sidTTL)
	if a.BySID(uid, sid) != nil {
		t.Fatal("expired SID accepted")
	}
	sid, err = a.NewSID(acc)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAccounts(st)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.BySID(uid, sid) != nil {
		t.Fatal("SID survived restart")
	}
	if _, err := a.Recover("alice", "abc12345", "newpw"); err != nil {
		t.Fatal(err)
	}
	if a.BySID(uid, sid) != nil {
		t.Fatal("SID survived password reset")
	}
}

func TestSecurityGuardDimensionsAndExpiry(t *testing.T) {
	now := time.Now()
	g := failGuard{now: func() time.Time { return now }}
	for n := 0; n < failLimit; n++ {
		done, ok := g.begin(" Alice ", fmt.Sprint(n))
		if !ok {
			t.Fatal("locked early")
		}
		done(true)
	}
	if _, ok := g.begin("ALICE", "new-ip"); ok {
		t.Fatal("normalization or account lock bypass")
	}
	now = now.Add(failLockFor)
	done, ok := g.begin("alice", "new-ip")
	if !ok {
		t.Fatal("lock did not expire")
	}
	done(false)
	for n := 0; n < failLimit; n++ {
		done, ok := g.begin(fmt.Sprint(n), "same-ip")
		if !ok {
			t.Fatal("locked early")
		}
		done(true)
	}
	if _, ok := g.begin("different-account", "same-ip"); ok {
		t.Fatal("IP lock bypass")
	}
	// A successful account authentication does not reset the IP failure budget.
	h := failGuard{}
	done, _ = h.begin("alice", "ip")
	done(true)
	done, _ = h.begin("alice", "ip")
	done(false)
	if h.urss["alice"].count != 0 || h.ips["ip"].count != 1 {
		t.Fatal("success reset wrong counters")
	}
	// Lock duration is measured from the fifth failure, not the first one.
	j := failGuard{now: func() time.Time { return now }}
	for n := 0; n < failLimit; n++ {
		done, _ = j.begin("alice", "ip")
		done(true)
		if n == 0 {
			now = now.Add(14 * time.Minute)
		}
	}
	now = now.Add(2 * time.Minute)
	if _, ok := j.begin("alice", "ip"); ok {
		t.Fatal("lock reset at window boundary")
	}
}

func TestSecurityGuardConcurrent(t *testing.T) {
	var g failGuard
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for n := 0; n < 100; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if done, ok := g.begin("alice", "ip"); ok {
				accepted.Add(1)
				done(true)
			}
		}()
	}
	wg.Wait()
	if got := accepted.Load(); got != failLimit {
		t.Fatalf("concurrent attempts accepted: %d", got)
	}
}

func TestSecurityReconnectRateLimit(t *testing.T) {
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	if _, err := srv.accounts.Register("alice", "pw", "abc12345"); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < failLimit; n++ {
		c := dial(t, addr)
		if n%2 == 0 {
			c.sendRaw(xym.CLogin, loginBody("alice", "wrong"))
			c.expectFail(xym.FailBadPassword)
		} else {
			name := "alice"
			if n == 3 {
				name = "missing"
			}
			c.sendRaw(xym.CRecover, cat(ver, str(name), str("wrong"), str("")))
			c.expectFail(FailRecoverBadCode)
		}
		c.conn.Close()
	}
	for _, main := range []byte{xym.CLogin, xym.CRecover, xym.CRegister} {
		c := dial(t, addr)
		switch main {
		case xym.CLogin:
			c.sendRaw(main, loginBody("alice", "pw"))
		case xym.CRecover:
			c.sendRaw(main, cat(ver, str("alice"), str("abc12345"), str("")))
		case xym.CRegister:
			c.sendRaw(main, registerBody("new", "pw", "abc12345"))
		}
		c.expectFail(xym.FailRefused)
		c.expectClosed()
	}
}
