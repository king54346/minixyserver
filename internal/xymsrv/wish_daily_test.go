package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// Exercise a persisted, unfinished board across the day boundary through the
// wire handlers: yesterday's target must not lock today's first free draw.
func TestWishNewDayReopensSelection(t *testing.T) {
	c, p, _ := actWorld(t, "wish-new-day")
	p.mu.Lock()
	pet := p.addPet(25001)
	oldTarget := gamedata.Wish[pet.Type][0]
	p.Wish = &WishState{Day: "2000-01-01", From: pet.Type, To: oldTarget,
		Current: []wishGrid{{Pos: 0, Type: wishISoul}}, Deck: []int{wishSoul1}}
	p.mu.Unlock()

	// The rechoose button may have been pressed on yesterday's cached UI.
	c.sendExt("WISH_PTO.C_RECHOOSE_WISH", nil)
	m := c.expectExt("WISH_PTO.S_OPEN_WISH")
	if num(m, "from_type") != 0 || num(m, "to_type") != 0 || num(m, "free_left") != 1 || len(m["current"].([]xym.Msg)) != 0 {
		t.Fatalf("new day did not reopen selection: %v", m)
	}
	// A cached card click must not spend the daily free draw on yesterday's target.
	c.sendExt("WISH_PTO.C_OPEN_GRID", xym.Msg{"pos": 1})
	c.expectExt("ETC_PTO.S_RET_MSG")
	m = c.expectExt("WISH_PTO.S_OPEN_WISH")
	if num(m, "free_left") != 1 {
		t.Fatal("old target consumed today's free draw")
	}
	to := gamedata.Wish[pet.Type][1]
	c.sendExt("WISH_PTO.C_OPEN_WISH", xym.Msg{"from_npc": pet.NpcNum, "to_type": to})
	m = c.expectExt("WISH_PTO.S_OPEN_WISH")
	if num(m, "to_type") != int(to) || num(m, "can_rechoose") != 1 {
		t.Fatalf("new target rejected: %v", m)
	}
	c.sendExt("WISH_PTO.C_OPEN_GRID", xym.Msg{"pos": 0})
	m = c.until(t, "WISH_PTO.S_OPEN_WISH")["WISH_PTO.S_OPEN_WISH"]
	if num(m, "free_left") != 0 || num(m, "can_rechoose") != 0 || len(m["current"].([]xym.Msg)) != 1 {
		t.Fatalf("new day's board did not start correctly: %v", m)
	}
	c.sendExt("WISH_PTO.C_RECHOOSE_WISH", nil)
	c.expectExt("ETC_PTO.S_RET_MSG")
	m = c.expectExt("WISH_PTO.S_OPEN_WISH")
	if num(m, "to_type") != int(to) || num(m, "free_left") != 0 {
		t.Fatal("same-day rechoose bypassed board lock")
	}
}
