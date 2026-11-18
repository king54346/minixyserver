package xymsrv

import (
	"testing"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

type countedPlayerStore struct {
	store.Store
	saves int
}

func (s *countedPlayerStore) SavePlayer(uid int32, data []byte) error {
	s.saves++
	return s.Store.SavePlayer(uid, data)
}

func TestXianlingOpenOnlySavesChanges(t *testing.T) {
	st := &countedPlayerStore{Store: openTestStore(t, t.TempDir())}
	p := newPlayer(20001)
	p.st = st
	s := &Session{role: &Role{UID: 20001}, sendCh: make(chan []byte, 10), closing: make(chan struct{})}
	h := handlers[xym.ByName("ETC_PTO.C_OPEN_XIANLING_SHOP").PID]
	h(s, p, nil)
	if st.saves != 1 {
		t.Fatalf("initial shop saves %d", st.saves)
	}
	h(s, p, nil)
	h(s, p, nil)
	if st.saves != 1 {
		t.Fatalf("unchanged shop saved %d times", st.saves)
	}
	p.Xianling.Period--
	h(s, p, nil)
	if st.saves != 2 {
		t.Fatal("period change not persisted")
	}
	p.Xianling.Day = "2000-01-01"
	p.Xianling.Refreshed = 10
	// Persist yesterday's state first. Restoring an already durable snapshot
	// after only an in-memory test mutation should no longer cause an I/O.
	if err := p.save(); err != nil {
		t.Fatal(err)
	}
	h(s, p, nil)
	if st.saves != 4 || p.Xianling.Refreshed != 0 {
		t.Fatal("daily reset not persisted")
	}
	loaded, err := st.LoadPlayer(20001)
	if err != nil || len(loaded) == 0 {
		t.Fatalf("saved shop unavailable: %v", err)
	}
}
