package xymsrv

import (
	"errors"
	"testing"

	"turnrpg/internal/xym"
)

func TestNewRoleGiftPersistsAndDoesNotRepeat(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NewRoleGold, cfg.NewRoleChests = 2_000_000, 2600
	srv, addr, _ := startServerSrv(t, cfg)
	c := dial(t, addr)
	c.sendRaw(xym.CRegister, registerBody("newgift", "pw", "abcd1234"))
	sid := c.expectExt("LOGIN_PTO.S_SEND_SID")
	c.recv() // S_CREATE
	c.sendRaw(xym.CCreate, cat([]byte{1, 0}, str("newgift")))
	c.expectExt("LOGIN_PTO.S_NEW_USER")
	if m := c.expectExt("LOGIN_PTO.S_LOGIN_SUC"); num(m, "gold") != 2_000_000 {
		t.Fatal(m)
	}
	c.collect()
	uid := int32(num(sid, "uid"))
	p, err := srv.players.Get(uid)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	if p.itemCount(10002) != 2600 || p.itemCount(10003) != 2600 {
		p.mu.Unlock()
		t.Fatal("new character did not receive boxes and keys")
	}
	p.mu.Unlock()
	// Duplicate create and normal relog must never refill a spent gift.
	c.sendRaw(xym.CCreate, cat([]byte{1, 0}, str("newgift")))
	c.collect()
	if _, err := srv.roles.Create(uid, 1, "newgift"); !errors.Is(err, ErrRoleExists) {
		t.Fatal(err)
	}
	_, err = srv.roles.Update(uid, func(ro *Role) bool { ro.Gold = 123; return true })
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.takeItems(10002, 100)
	p.takeItems(10003, 100)
	err = p.save()
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	c.conn.Close()
	c = dial(t, addr)
	c.sendRaw(xym.CLogin, loginBody("newgift", "pw"))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	if m := c.expectExt("LOGIN_PTO.S_LOGIN_SUC"); num(m, "gold") != 123 {
		t.Fatal("relogin granted gold again", m)
	}
	c.collect()
	// Reopen the durable store through fresh caches, with gifts still enabled.
	roles, err := OpenRoles(srv.players.st)
	if err != nil {
		t.Fatal(err)
	}
	players := OpenPlayers(srv.players.st)
	_ = NewServer(cfg, srv.accounts, roles, players)
	reloaded, err := players.Get(uid)
	if err != nil {
		t.Fatal(err)
	}
	if roles.Get(uid).Gold != 123 || reloaded.itemCount(10002) != 2500 || reloaded.itemCount(10003) != 2500 {
		t.Fatal("reopening store refilled an existing character")
	}
}
