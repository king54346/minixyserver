package xymsrv

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"turnrpg/internal/xym"
)

func TestAuthQueueSuccessfulBurst(t *testing.T) {
	var g failGuard
	var held []func(bool)
	for i := range failLimit {
		done, ok := g.begin(fmt.Sprint(i), "shared-ip")
		if !ok {
			t.Fatal("could not reserve initial authentications")
		}
		held = append(held, done)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan bool, 32)
	for i := range 32 {
		go func() {
			done, blocked := g.beginWait(ctx, nil, fmt.Sprint(100+i), "shared-ip")
			if done != nil {
				done(false)
			}
			finished <- done != nil && !blocked
		}()
	}
	select {
	case <-finished:
		t.Fatal("busy capacity was treated as a failed login")
	case <-time.After(20 * time.Millisecond):
	}
	for _, done := range held {
		done(false)
	}
	for range 32 {
		if !<-finished {
			t.Fatal("a valid queued authentication was refused")
		}
	}
	if g.ips["shared-ip"].count != 0 || g.ips["shared-ip"].pending != 0 {
		t.Fatal("successful burst polluted the failure budget")
	}
}

func TestAuthQueueFailuresStillLimited(t *testing.T) {
	var g failGuard
	var accepted atomic.Int32
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done, blocked := g.beginWait(ctx, nil, fmt.Sprint(i), "attacker-ip")
			if done == nil {
				if !blocked {
					t.Error("timed out instead of enforcing the failure limit")
				}
				return
			}
			accepted.Add(1)
			time.Sleep(time.Millisecond)
			done(true)
		}()
	}
	wg.Wait()
	if accepted.Load() != failLimit {
		t.Fatalf("accepted %d failed attempts, want %d", accepted.Load(), failLimit)
	}
}

func TestAuthQueueCancellation(t *testing.T) {
	var g failGuard
	var held []func(bool)
	for range failLimit {
		done, _ := g.begin("same-account", "ip")
		held = append(held, done)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if done, blocked := g.beginWait(ctx, nil, "same-account", "ip"); done != nil || blocked {
		t.Fatal("cancelled wait was treated as a rate-limit ban")
	}
	stop := make(chan struct{})
	close(stop)
	if done, blocked := g.beginWait(context.Background(), stop, "same-account", "ip"); done != nil || blocked {
		t.Fatal("closed session kept waiting")
	}
	for _, done := range held {
		done(false)
	}
}

func TestObsoleteSDKCallbackDoesNotInvalidateGame(t *testing.T) {
	c, _, _ := actWorld(t, "sdk-callback")
	c.sendRaw(xym.CNewLogin, ver)
	c.sendExt("MALL_PTO.C_OPEN_PET_MALL", nil)
	if got := c.expectExt("MALL_PTO.S_OPEN_PET_MALL"); got == nil {
		t.Fatal("authenticated connection did not survive obsolete SDK callback")
	}
}
