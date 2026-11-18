package xymsrv

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// Exercise the actual per-server release using isolated accounts and file saves.
func TestExternalSpecialRelate(t *testing.T) {
	binary := os.Getenv("XYM_SPECIAL_BINARY")
	if binary == "" {
		t.Skip("set XYM_SPECIAL_BINARY")
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
	roles, err := OpenRoles(st)
	if err != nil {
		t.Fatal(err)
	}
	players := OpenPlayers(st)
	var ids []int32
	for _, name := range []string{"relateprobe", "relatefoe"} {
		a, e := accounts.Register(name, "pw", "abcd1234")
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, a.UID)
		if _, e = roles.Create(a.UID, 1, name); e != nil {
			t.Fatal(e)
		}
		if _, e = roles.Update(a.UID, func(r *Role) bool { r.Grade = 100; return true }); e != nil {
			t.Fatal(e)
		}
		p, e := players.Get(a.UID)
		if e != nil {
			t.Fatal(e)
		}
		p.Pets = nil
		lu := p.lineup(p.FightNo)
		for i := range lu.Slots {
			lu.Slots[i].Pet = 0
		}
		if name == "relateprobe" {
			receiver := p.addPet(15023)
			receiver.Grade = 100
			receiver.HPAdd = 100000
			receiver.AttAdd = 1000
			source := p.addPet(15031)
			source.TLAdd = 10
			lu.Slots[0].Pet = receiver.BagPos // source is deliberately absent from every team slot.
		} else {
			for i := 0; i < 2; i++ {
				pet := p.addPet(int32(13010 + i))
				pet.Grade = 100
				pet.HPAdd = 1000000
				lu.Slots[i].Pet = pet.BagPos
			}
		}
		if e = p.save(); e != nil {
			t.Fatal(e)
		}
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
	for i := 0; i < 100; i++ {
		c, e := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if e == nil {
			c.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		b, _ := os.ReadFile(log.Name())
		t.Fatalf("startup: %s", b)
	}
	c := dial(t, addr)
	defer c.conn.Close()
	c.sendRaw(xym.CLogin, loginBody("relateprobe", "pw"))
	c.until(t, "LOGIN_PTO.S_LOGIN_SUC")
	c.collect()
	c.sendExt("FRIEND_PTO.C_FIGHT_FRIEND", xym.Msg{"fid": ids[1]})
	record := c.until(t, "FIGHT_PTO.S_FIGHT_RECORD")["FIGHT_PTO.S_FIGHT_RECORD"]
	commands, err := battle.DecodeRecord(record["record"].([]byte))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, command := range commands {
		if command.ID == battle.CmdActDo && command.Magic == 20260 {
			found = true
		}
	}
	want := os.Getenv("XYM_SPECIAL_EXPECT") != "absent"
	if found != want {
		t.Fatalf("bench source granted HuiSao10=%v, want=%v", found, want)
	}
}
