package xymsrv

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

type gatedStore struct {
	store.Store
	saveRole   func(store.Role) error
	loadPlayer func(int32) ([]byte, error)
}

func (s *gatedStore) SaveRole(r store.Role) error {
	if s.saveRole != nil {
		return s.saveRole(r)
	}
	return s.Store.SaveRole(r)
}

func (s *gatedStore) LoadPlayer(uid int32) ([]byte, error) {
	if s.loadPlayer != nil {
		return s.loadPlayer(uid)
	}
	return s.Store.LoadPlayer(uid)
}

// A slow save must not stop everyone from reading roles or updating another UID.
func TestConcurrentRoleSave(t *testing.T) {
	st := openTestStore(t, t.TempDir())
	started, release := make(chan struct{}), make(chan struct{})
	gate := &gatedStore{Store: st, saveRole: func(r store.Role) error {
		if r.UID == 20001 {
			close(started)
			<-release
		}
		return st.SaveRole(r)
	}}
	r := &Roles{st: gate, ByUID: map[int32]*Role{
		20001: {UID: 20001, Gold: 10}, 20002: {UID: 20002, Gold: 20},
	}}
	finished := make(chan error, 1)
	go func() { _, err := r.Update(20001, func(ro *Role) bool { ro.Gold++; return true }); finished <- err }()
	<-started
	defer func() {
		close(release)
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	read := make(chan *Role, 1)
	go func() { read <- r.Get(20001) }()
	select {
	case ro := <-read:
		if ro.Gold != 10 {
			t.Error("uncommitted role published")
		}
	case <-time.After(time.Second):
		t.Fatal("role read blocked behind another save")
	}
	other := make(chan error, 1)
	go func() { _, err := r.Update(20002, func(ro *Role) bool { ro.Gold++; return true }); other <- err }()
	select {
	case err := <-other:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("different role blocked by global lock")
	}
}

func TestConcurrentRoleUpdateNoLostWrites(t *testing.T) {
	r, err := OpenRoles(openTestStore(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create(20001, 1, "alice"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if _, err := r.Update(20001, func(ro *Role) bool { ro.Gold++; return true }); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if got := r.Get(20001).Gold; got != 160 {
		t.Fatalf("lost writes: %d", got)
	}
	fail := errors.New("disk unavailable")
	r.st = &gatedStore{Store: r.st, saveRole: func(store.Role) error { return fail }}
	if _, err := r.Update(20001, func(ro *Role) bool { ro.Gold = 999; return true }); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if got := r.Get(20001).Gold; got != 160 {
		t.Fatalf("failed save changed balance: %d", got)
	}
}

// Loading a new player must not block cached players. Same-UID callers must all
// receive the same shared Player, rather than overwriting each other's saves.
func TestConcurrentPlayerLoad(t *testing.T) {
	st := openTestStore(t, t.TempDir())
	ps := OpenPlayers(st)
	cached, err := ps.Get(20001)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	gate := &gatedStore{Store: st, loadPlayer: func(uid int32) ([]byte, error) {
		if uid == 20002 {
			close(started)
			<-release
		}
		return st.LoadPlayer(uid)
	}}
	ps.st = gate
	finished := make(chan *Player, 1)
	go func() {
		p, err := ps.Get(20002)
		if err != nil {
			t.Error(err)
		}
		finished <- p
	}()
	<-started
	read := make(chan *Player, 1)
	go func() { p, _ := ps.Get(20001); read <- p }()
	select {
	case p := <-read:
		if p != cached {
			t.Error("cached player replaced")
		}
	case <-time.After(time.Second):
		t.Error("cached player blocked behind load")
	}
	same := make(chan *Player, 1)
	go func() {
		p, err := ps.Get(20002)
		if err != nil {
			t.Error(err)
		}
		same <- p
	}()
	close(release)
	loaded := <-finished
	if p := <-same; p != loaded {
		t.Fatal("concurrent load returned a different player")
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := ps.Get(20002)
			if err != nil || p != loaded {
				t.Errorf("inconsistent player: %p, %v", p, err)
			}
		}()
	}
	wg.Wait()
}

func TestConcurrentRegenAndRequests(t *testing.T) {
	srv, a, _ := twoPlayers(t)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			srv.regenOnline()
		}
	}()
	for range 30 {
		a.sendExt("ETC_PTO.C_OPEN_POINT_BUY", xym.Msg{"type": 1})
		a.expectExt("ETC_PTO.S_OPEN_POINT_BUY")
	}
	wg.Wait()
}

func TestConcurrentFriendFightAndLineup(t *testing.T) {
	srv, a, _ := twoPlayers(t)
	other, err := srv.players.Get(20002)
	if err != nil {
		t.Fatal(err)
	}
	stop, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-stop:
				return
			default:
			}
			other.mu.Lock()
			other.FightNo = 1
			other.Zhenfa = &ZhenfaState{Cur: 0, Stair: 1}
			other.mu.Unlock()
			runtime.Gosched()
		}
	}()
	defer func() { close(stop); <-finished }()
	for range 8 {
		a.sendExt("FRIEND_PTO.C_FIGHT_FRIEND", xym.Msg{"fid": 20002})
		a.collect()
	}
}

func TestConcurrentXianlingAndMijingNavigation(t *testing.T) {
	_, addr, _ := startServerSrv(t, DefaultConfig())
	var clients []*client
	for n := range 8 {
		clients = append(clients, enterWorld(t, addr, fmt.Sprintf("nav%d", n)))
	}
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				c.sendExt("ETC_PTO.C_OPEN_XIANLING_SHOP", nil)
				c.expectExt("ETC_PTO.S_SEND_XIANLING_SHOP_INFO")
				// Closing the shop itself sends no client packet. The next
				// navigation request must still complete on the same socket.
				c.sendExt("QUEST_PTO.C_OPEN_MIJING_MAIN", nil)
				c.expectExt("QUEST_PTO.S_OPEN_MIJING_MAIN")
				c.sendExt("ETC_PTO.C_OPEN_POINT_BUY", xym.Msg{"type": 1})
				c.expectExt("ETC_PTO.S_OPEN_POINT_BUY")
			}
		}()
	}
	wg.Wait()
}
