package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func TestRefineLargeBagProtectsSavedLineups(t *testing.T) {
	c, p, srv := actWorld(t, "refine-large")
	srv.roles.Update(p.UID, func(r *Role) bool { r.Cash = 1000000; return true })
	p.mu.Lock()
	p.ensureLineups()
	typ := gamedata.SuitsByStar[1][0]
	target := p.addSuit(gamedata.SuitsByStar[5][0])
	start := p.NextItemNum
	before := len(p.Suits)
	const n = 2048
	for i := range n {
		p.Suits = append(p.Suits, &Suit{ItemNum: start + int32(i), Pos: 5000 + i, Type: typ, Grade: 1})
	}
	p.NextItemNum += n
	// An item in a different saved lineup is not a valid material.
	saved := p.lineup(2)
	saved.Slots[0] = &Slot{Pet: p.lineup(1).Slots[0].Pet, Suit: [3]int{5000}}
	p.Suits[before+1].Locked = true
	reqs := []xym.Msg{{"itemnum": start}, {"itemnum": start + 1}, {"itemnum": target.ItemNum}, {"itemnum": -1}}
	for i := 2; i < 42; i++ {
		reqs = append(reqs, xym.Msg{"itemnum": start + int32(i)})
	}
	reqs = append(reqs, reqs[4]) // duplicate: consume only once
	p.mu.Unlock()
	c.sendExt("ITEM_PTO.C_REFINE_SUITS", xym.Msg{"itemnum": target.ItemNum, "from_xiuwei": 0, "suits": reqs})
	got := c.until(t, "ITEM_PTO.S_UPDATE_SUIT")
	if len(got["ITEM_PTO.S_REMOVE_SUIT"]["suits"].([]xym.Msg)) != 40 {
		t.Fatal("wrong number of removed materials")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.Suits) != before+n-40 || p.suitByNum(start) == nil || p.suitByNum(start+1) == nil || saved.Slots[0].Suit[0] != 5000 {
		t.Fatal("batch consumption damaged protected items or saved lineup")
	}
	for i := 2; i < 42; i++ {
		if p.suitByNum(start+int32(i)) != nil {
			t.Fatal("selected material survived batch consumption")
		}
	}
	grade, exp := upTable(suitUpExp).add(5, 1, 0, 40)
	if target.Grade != grade || target.Exp != exp || srv.roles.Get(p.UID).Cash != 1000000-40*100*5 {
		t.Fatal("experience or charge changed")
	}
	// Stable compaction preserves the order and pointers of remaining items.
	if p.Suits[before+2].ItemNum != start+42 {
		t.Fatal("remaining inventory order changed")
	}
}

func BenchmarkSuitMaterials(b *testing.B) {
	p := &Player{}
	typ := gamedata.SuitsByStar[1][0]
	for i := range 4000 {
		p.Suits = append(p.Suits, &Suit{ItemNum: int32(i + 1), Pos: i + 1, Type: typ, Grade: 1})
	}
	var reqs []xym.Msg
	for i := range 40 {
		reqs = append(reqs, xym.Msg{"itemnum": int64(4000 - i)})
	}
	m := xym.Msg{"suits": reqs}
	b.Run("previous-linear-lookups", func(b *testing.B) {
		for range b.N {
			mt := &materials{}
			seen := map[*Suit]bool{}
			for _, r := range reqs {
				x := p.suitByNum(int32(r["itemnum"].(int64)))
				if x == nil || seen[x] || x.Locked || p.suitWorn(x.Pos) {
					continue
				}
				seen[x] = true
				mt.suits = append(mt.suits, x)
				mt.exp += x.expValue()
			}
			if len(mt.suits) != 40 {
				b.Fatal("missing materials")
			}
		}
	})
	b.Run("request-index", func(b *testing.B) {
		for range b.N {
			if mt := p.suitMaterials(m, nil); len(mt.suits) != 40 {
				b.Fatal("missing materials")
			}
		}
	})
}
