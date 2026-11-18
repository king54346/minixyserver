package xymsrv

import (
	"encoding/json"
	"testing"

	"turnrpg/internal/gamedata"
)

func TestPetHistorySurvivesRemovalAndReload(t *testing.T) {
	p := newPlayer(1)
	const typ int32 = 15031
	for range 4 {
		p.gainPet(typ)
	}
	pet := p.petByType(typ)
	if pet.TLAdd != 3 || p.OwnedCopies[typ] != 4 {
		t.Fatal("initial duplicates counted incorrectly")
	}
	// Historical count must survive even when current copies were consumed.
	pet.TLAdd, pet.TLPoint = 0, 0
	p.removePet(pet)
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var loaded Player
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.migrateOwned() {
		t.Fatal("reload changed the saved history")
	}
	restored := loaded.gainPet(typ)
	if restored == nil || restored.TLAdd != 0 || restored.TLPoint != 0 || loaded.OwnedCopies[typ] != 5 {
		t.Fatal("consumed historical cards were recreated or history was lost")
	}
	loaded.gainPet(typ)
	if restored.TLAdd != 1 || restored.TLPoint != shenTongPoint[5] || loaded.OwnedCopies[typ] != 6 {
		t.Fatal("subsequent duplicate counted twice or lost")
	}
}

func TestLuochengConsumedByUpgradeNotRecreated(t *testing.T) {
	p := newPlayer(1)
	const typ int32 = 13001
	for range 5 {
		p.gainPet(typ)
	}
	pet := p.petByType(typ)
	p.feishengSplit(pet, gamedata.Pets[typ], 5)
	if p.petByType(typ) != nil || pet.Type != 14013 || p.OwnedCopies[typ] != 5 {
		t.Fatal("upgrade did not consume the five low-star cards")
	}
	// Match the reported large lifetime count, with no usable low-star base.
	p.OwnedCopies[typ] = 257
	d := &bagDelta{}
	p.giveCard(typ, d)
	received := p.petByType(typ)
	if received == nil || received.TLAdd != 0 || received.TLPoint != 0 || d.soul != 0 || p.OwnedCopies[typ] != 258 {
		t.Fatal("one Luocheng drop recreated consumed history or conversion soul")
	}
	d = &bagDelta{}
	p.giveCard(typ, d)
	if received.TLAdd != 1 || received.TLPoint != shenTongPoint[3] || d.soul != petSoulPoint[3] {
		t.Fatal("subsequent drop must add exactly one usable duplicate")
	}
}

func TestPetHistoryMigratesLegacyCounts(t *testing.T) {
	p := newPlayer(1)
	pet := p.addPet(15031)
	pet.TLAdd = 7
	p.markOwned(35019) // Legacy history has no count for an absent base.
	if !p.migrateOwned() || p.OwnedCopies[15031] != 8 || p.OwnedCopies[35019] != 1 {
		t.Fatal("legacy history migration lost available evidence")
	}
	if p.migrateOwned() {
		t.Fatal("migration is not idempotent")
	}
	p.removePet(pet)
	if got := p.gainPet(15031); got.TLAdd != 0 || got.TLPoint != 0 || p.OwnedCopies[15031] != 9 {
		t.Fatal("legacy historical cards became usable again")
	}
}

func TestPetHistoryMergeDoesNotDoubleCount(t *testing.T) {
	p := newPlayer(1)
	first := p.addPet(15031)
	first.TLAdd = 2
	second := p.addPet(15031)
	second.TLAdd = 1
	keep, gone := p.feishengMerge(second)
	if keep != first || gone != second || keep.TLAdd != 4 || p.OwnedCopies[15031] != 5 {
		t.Fatal("merging existing copies double-counted history")
	}
	p.removePet(keep)
	if restored := p.gainPet(15031); restored.TLAdd != 0 || p.OwnedCopies[15031] != 6 {
		t.Fatal("removed merged cards were recreated")
	}
}
