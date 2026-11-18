package xymsrv

import (
	"errors"
	"testing"

	"turnrpg/internal/store"
)

type noOpPlayerStore struct {
	store.Store
	calls int
	fail  bool
}

func (s *noOpPlayerStore) SavePlayer(uid int32, data []byte) error {
	s.calls++
	if s.fail {
		return errors.New("write failed")
	}
	return s.Store.SavePlayer(uid, data)
}
func TestUnchangedSaveSkipsIOAndRetriesFailures(t *testing.T) {
	st := &noOpPlayerStore{Store: openTestStore(t, t.TempDir())}
	p := newPlayer(20001)
	p.st = st
	if err := p.save(); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if err := p.save(); err != nil {
			t.Fatal(err)
		}
	}
	if st.calls != 1 {
		t.Fatalf("unchanged state wrote %d times", st.calls)
	}
	p.NextNpc++
	st.fail = true
	if p.save() == nil || p.save() == nil || st.calls != 3 {
		t.Fatal("failed save was incorrectly cached")
	}
	st.fail = false
	if err := p.save(); err != nil {
		t.Fatal(err)
	}
	data, err := st.LoadPlayer(p.UID)
	if err != nil || len(data) == 0 || st.calls != 4 {
		t.Fatal("changed player not committed")
	}
	if err := p.save(); err != nil || st.calls != 4 {
		t.Fatal("committed snapshot was written twice")
	}
}
func TestUnchangedRoleSkipsIO(t *testing.T) {
	calls := 0
	r := &Roles{st: &gatedStore{saveRole: func(store.Role) error { calls++; return nil }}, ByUID: map[int32]*Role{20001: {UID: 20001, Gold: 10}}}
	if _, err := r.Update(20001, func(*Role) bool { return true }); err != nil || calls != 0 {
		t.Fatal("unchanged role performed I/O")
	}
	if _, err := r.Update(20001, func(x *Role) bool { x.Gold++; return true }); err != nil || calls != 1 || r.Get(20001).Gold != 11 {
		t.Fatal("changed role was not saved")
	}
}
