package xymsrv

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func TestLineupSuitPositionWireRoundTrip(t *testing.T) {
	for _, pos := range []int{0, 1, 127, 128, 255, 256, 300, 511, 512, 768, 1000, 65535} {
		t.Run(strconv.Itoa(pos), func(t *testing.T) {
			sl := &Slot{Pet: 1, Suit: [3]int{pos, pos, pos}}
			spec := xym.ByName("LINEUP_PTO.C_SET_LINEUPS")
			body := spec.MustEncode(xym.Msg{"fno": 1, "lineups": []xym.Msg{{"lineup_no": 1, "leader": 1, "info": []xym.Msg{lineupSlotMsg(1, sl)}}}})
			_, _, decoded, err := xym.DecodeExt(xym.C2S, body)
			if err != nil {
				t.Fatal(err)
			}
			r := decoded["lineups"].([]xym.Msg)[0]["info"].([]xym.Msg)[0]
			for i := range sl.Suit {
				if got := lineupSuitPos(r, i); got != pos {
					t.Fatalf("slot %d position %d want %d", i, got, pos)
				}
			}
		})
	}
	// Exact old failure: 300 was sent as 44 without an extension.
	if got := lineupSuitPos(xym.Msg{"spos1": int64(byte(300 % 256))}, 0); got != 44 {
		t.Fatal(got)
	}
}

func TestSwitchLineupRefineHighPositionGear(t *testing.T) {
	c, p, srv := actWorld(t, "highgear")
	srv.roles.Update(p.UID, func(r *Role) bool { r.Cash = 1000000; return true })
	p.mu.Lock()
	p.ensureLineups()
	var types [3]int
	for typ, si := range gamedata.Suits {
		k, ok := suitSlot[si.Kind]
		if ok && si.Star == 2 && !isSuitMaterial(typ) && (types[k] == 0 || typ < types[k]) {
			types[k] = typ
		}
	}
	a, b := p.lineup(1), p.lineup(2)
	for i := range b.Slots {
		b.Slots[i] = &Slot{}
	}
	b.Leader = 1
	var target *Suit
	for fp := 0; fp < 2; fp++ {
		if a.Slots[fp].Pet == 0 {
			t.Fatal("fixture needs two pets")
		}
		b.Slots[fp].Pet = a.Slots[fp].Pet
		for k, pos := range []int{300 + fp, 511 + fp, 768 + fp} {
			su := p.addSuit(types[k])
			su.Pos = pos
			a.Slots[fp].Suit[k] = pos
			b.Slots[1-fp].Suit[k] = pos
			if fp == 0 && k == 0 {
				target = su
			}
		}
	}
	// A real lower-position item must never silently replace item 300.
	low := p.addSuit(types[0])
	low.Pos = 44
	p.addIron(target.Type, 5, &bagDelta{})
	want1, _ := json.Marshal(a)
	want2, _ := json.Marshal(b)
	if err := p.save(); err != nil {
		t.Fatal(err)
	}
	p.mu.Unlock()
	c.sendExt("LINEUP_PTO.C_GET_LINEUPS", nil)
	cached := c.expectExt("LINEUP_PTO.S_SEND_LINEUP")
	switchTo := func(no int) {
		t.Helper()
		cached["fno"] = no
		c.sendRaw(xym.MainExtLarge, xym.ByName("LINEUP_PTO.C_SET_LINEUPS").MustEncode(cached))
		got := c.until(t, "LINEUP_PTO.S_SEND_LINEUP")
		cached = got["LINEUP_PTO.S_SEND_LINEUP"]
		wear := got["ITEM_PTO.S_WEAR_SUIT"]["wear"].([]xym.Msg)
		p.mu.Lock()
		defer p.mu.Unlock()
		for fp, sl := range p.lineup(no).Slots {
			for k, key := range []string{"weapon_item", "shirt_item", "shoe_item"} {
				want := int64(0)
				if su := p.suitByPos(sl.Suit[k]); su != nil {
					want = int64(su.ItemNum)
				}
				if wear[fp][key] != want {
					t.Fatalf("lineup %d pos %d %s: %v want %d", no, fp+1, key, wear[fp][key], want)
				}
			}
		}
	}
	switchTo(2)
	c.sendExt("ITEM_PTO.C_REFINE_SUITS", xym.Msg{"itemnum": target.ItemNum, "from_xiuwei": 0, "suit_irons": []xym.Msg{{"type": target.Type}}})
	c.until(t, "ITEM_PTO.S_UPDATE_SUIT")
	for _, no := range []int{1, 2, 1} {
		switchTo(no)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if target.Grade == 1 && target.Exp == 0 {
		t.Fatal("fixture did not refine gear")
	}
	got1, _ := json.Marshal(p.lineup(1))
	got2, _ := json.Marshal(p.lineup(2))
	if !reflect.DeepEqual(got1, want1) || !reflect.DeepEqual(got2, want2) {
		t.Fatalf("lineups changed after refine/switch: %s / %s", got1, got2)
	}
	raw, err := srv.players.st.LoadPlayer(p.UID)
	if err != nil {
		t.Fatal(err)
	}
	var saved Player
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	got1, _ = json.Marshal(saved.lineup(1))
	got2, _ = json.Marshal(saved.lineup(2))
	if string(got1) != string(want1) || string(got2) != string(want2) {
		t.Fatal("persisted lineup gear differs")
	}
}

func TestRejectTruncatedLineupSave(t *testing.T) {
	c, p, _ := actWorld(t, "stalegear")
	p.mu.Lock()
	w := p.addSuit(1001)
	w.Pos = 300
	p.fightSlot(1).Suit[0] = 300
	cached := p.lineupsMsg()
	rows := cached["lineups"].([]xym.Msg)[0]["info"].([]xym.Msg)
	delete(rows[0], "s_talent_1") // unpatched client only sends the truncated byte
	before, _ := json.Marshal(p.Lineups)
	p.mu.Unlock()
	c.sendRaw(xym.MainExtLarge, xym.ByName("LINEUP_PTO.C_SET_LINEUPS").MustEncode(cached))
	c.expectMsg(t, "重新登录")
	c.expectExt("LINEUP_PTO.S_SEND_LINEUP")
	p.mu.Lock()
	defer p.mu.Unlock()
	after, _ := json.Marshal(p.Lineups)
	if string(before) != string(after) {
		t.Fatal("rejected request changed lineups")
	}
}
