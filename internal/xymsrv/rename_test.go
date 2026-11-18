package xymsrv

import (
	"errors"
	"sync"
	"testing"

	"turnrpg/internal/xym"
)

func TestRenameToken(t *testing.T) {
	c, p, srv := actWorld(t, "rename")
	if _, err := srv.roles.Create(p.UID+100, 1, "Occupied"); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.addItem(renameToken, 1)
	p.mu.Unlock()
	old := srv.roles.Get(p.UID).Name
	for name, tip := range map[string]string{"occupied": "已被使用", "bad name": "名字需", old: "当前名字"} {
		c.sendExt("ITEM_PTO.C_CHANGE_NAME", xym.Msg{"name": name})
		c.expectMsg(t, tip)
		if p.itemCount(renameToken) != 1 || srv.roles.Get(p.UID).Name != old {
			t.Fatal("refused rename changed name/token")
		}
	}
	c.sendExt("ITEM_PTO.C_CHANGE_NAME", xym.Msg{"name": "新名字"})
	got := c.until(t, "ITEM_PTO.S_UPDATE_NAME")
	if got["ITEM_PTO.S_UPDATE_NAME"]["name"] != "新名字" || p.itemCount(renameToken) != 0 {
		t.Fatal("rename did not consume exactly one token")
	}
	if got["ITEM_PTO.S_REMOVE_ITEM"] == nil {
		t.Fatal("missing item removal notification")
	}
	reloaded, err := OpenRoles(srv.roles.st)
	if err != nil || reloaded.Get(p.UID).Name != "新名字" {
		t.Fatal("new name was not persisted")
	}
	c.sendExt("ITEM_PTO.C_CHANGE_NAME", xym.Msg{"name": "另一个名"})
	c.expectMsg(t, "缺少改名令")
	if srv.roles.Get(p.UID).Name != "新名字" {
		t.Fatal("rename without a token succeeded")
	}
}

func TestConcurrentRenameNameReservation(t *testing.T) {
	r, err := OpenRoles(openTestStore(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	for i := int32(1); i <= 2; i++ {
		if _, err = r.Create(i, 1, string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := int32(1); i <= 2; i++ {
		wg.Add(1)
		go func(uid int32) { defer wg.Done(); _, e := r.Rename(uid, "SameName"); errs <- e }(i)
	}
	wg.Wait()
	close(errs)
	ok, used := 0, 0
	for e := range errs {
		if e == nil {
			ok++
		} else if errors.Is(e, ErrNameUsed) {
			used++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || used != 1 {
		t.Fatalf("success=%d duplicate=%d", ok, used)
	}
}
