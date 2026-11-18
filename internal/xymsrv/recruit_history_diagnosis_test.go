package xymsrv

import (
	"encoding/json"
	"testing"

	"turnrpg/internal/xym"
)

// Exercise the reported four-star Erlang case through the real recruit handler.
func TestRecruitMissingFourStarErlangRestoresBase(t *testing.T) {
	c, p, srv := actWorld(t, "erlhistory")
	const typ int32 = 34001
	original := recruitPools[3]
	forced := *original
	forced.rates = map[int]int{4: 100}
	forced.pity, forced.tenMin = 0, 0
	savedCards, hadStar := poolPets[forced.name][4]
	recruitPools[3] = &forced
	poolPets[forced.name][4] = []int32{typ}
	t.Cleanup(func() {
		recruitPools[3] = original
		if hadStar {
			poolPets[forced.name][4] = savedCards
		} else {
			delete(poolPets[forced.name], 4)
		}
	})
	p.mu.Lock()
	if pet := p.petByType(typ); pet != nil {
		p.removePet(pet)
	}
	p.markOwned(typ)
	count := len(p.Pets)
	if err := p.save(); err != nil {
		t.Fatal(err)
	}
	p.mu.Unlock()
	before := srv.roles.Get(p.UID).SoulExp
	c.sendExt("MALL_PTO.C_BUY_PET", xym.Msg{"type": 3, "count": 1})
	got := c.until(t, "ETC_PTO.S_REWARD_MSG")
	reward := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg)
	if len(reward) != 1 || num(reward[0], "type") != int(typ) {
		t.Fatalf("draw result %v", reward)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pet := p.petByType(typ)
	if pet == nil || len(p.Pets) != count+1 || pet.TLAdd != 0 {
		t.Fatal("historical card and duplicate count were not restored")
	}
	if delta := srv.roles.Get(p.UID).SoulExp - before; delta != 0 {
		t.Fatalf("restored base also awarded soul=%d", delta)
	}
	if got["PET_PTO.S_UPDATE_PET"] == nil || got["PET_PTO.S_PET_LIST"] == nil {
		t.Fatal("restored base was not pushed to client")
	}
	data, err := p.st.LoadPlayer(p.UID)
	var saved Player
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.petByType(typ) == nil || saved.petByType(typ).TLAdd != 0 || saved.OwnedCopies[typ] != 2 {
		t.Fatal("restored base and count were not persisted")
	}
}
