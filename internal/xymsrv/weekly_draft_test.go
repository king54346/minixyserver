package xymsrv

import (
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func TestDraftPacks(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 17))
	rareCount := 0
	for range 1000 {
		packs, err := draftPacks(rng)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, pack := range packs {
			rare := 0
			for i, id := range pack {
				p := gamedata.Pets[id]
				if p.Star != 5 || p.Race != draftRaces[i] || seen[p.Name] || p.Name == "助人海星" {
					t.Fatalf("invalid or duplicate card: %v", p)
				}
				seen[p.Name] = true
				if p.Special && !xlExcluded(id) {
					rare++
				}
			}
			if rare > 1 {
				t.Fatal("multiple special cards in a single pack")
			}
			rareCount += rare
		}
	}
	if rareCount < 700 || rareCount > 900 {
		t.Fatalf("8000 packs yielded %d rare cards", rareCount)
	}
}

func TestDraftReplacesOnlyUnpickedSeaStar(t *testing.T) {
	packs, err := draftPacks(rand.New(rand.NewPCG(11, 17)))
	if err != nil {
		t.Fatal(err)
	}
	packs[0][1] = 50003
	packs[1][1] = 50003
	player := draftPlayer{Packs: packs, Picked: []int32{50003}, Generation: 3, Score: 1100, Fights: 4}
	if !replaceUnpickedDraftHelper(&player) {
		t.Fatal("legacy unpicked card was not replaced")
	}
	if player.Packs[0][1] != 50003 || player.Picked[0] != 50003 || player.Generation != 3 || player.Score != 1100 || player.Fights != 4 {
		t.Fatal("confirmed draft progress changed")
	}
	replacement := gamedata.Pets[player.Packs[1][1]]
	if replacement == nil || replacement.Name == draftExcludedName || replacement.Race != 3 || replacement.Star != 5 {
		t.Fatalf("invalid replacement: %+v", replacement)
	}
	if replaceUnpickedDraftHelper(&player) {
		t.Fatal("reopening changed the same draft again")
	}
}

func TestDraftWireFlow(t *testing.T) {
	srv, a, b, pa, pb := dfWorld(t)
	type state struct {
		Round      int
		Pack       []int32
		Picked     []int32
		Fights     int
		Skips      int
		Score      int
		Match      *draftMatch
		Generation int32
		Message    string
	}
	send := func(c *client, action, round int, card int32, leader int, cards []int32) {
		rows := make([]xym.Msg, 0, len(cards))
		for _, id := range cards {
			rows = append(rows, xym.Msg{"card": id})
		}
		c.sendExt("DRAFT_PTO.C_DRAFT_ACTION", xym.Msg{"action": action, "round": round, "card": card, "leader": leader, "lineup": rows})
	}
	read := func(c *client) state {
		m := c.expectExt("DRAFT_PTO.S_DRAFT_STATE")
		var s state
		if err := json.Unmarshal([]byte(m["state"].(string)), &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	pickAll := func(c *client) []int32 {
		send(c, draftStart, 0, 0, 0, nil)
		s := read(c)
		first := slices.Clone(s.Pack)
		send(c, draftStart, 0, 0, 0, nil)
		s = read(c)
		if !slices.Equal(first, s.Pack) {
			t.Fatal("repeated start rerolled the pack")
		}
		for round := 1; round <= 8; round++ {
			if s.Round != round || len(s.Pack) != 6 {
				t.Fatalf("round %d: %+v", round, s)
			}
			send(c, draftPick, round, s.Pack[0], 0, nil)
			s = read(c)
		}
		if len(s.Picked) != 8 {
			t.Fatalf("picked: %v", s.Picked)
		}
		bad := slices.Clone(s.Picked)
		bad[1] = bad[0]
		send(c, draftUpload, 0, 0, 1, bad)
		if read(c).Message == "" {
			t.Fatal("accepted duplicate lineup")
		}
		return s.Picked
	}
	gold := srv.roles.Get(pa.UID).Gold
	mine, theirs := pickAll(a), pickAll(b)
	if srv.roles.Get(pa.UID).Gold != gold {
		t.Fatal("first draft charged gold")
	}
	send(a, draftPair, 0, 0, 1, mine)
	first := read(a)
	if first.Match != nil || first.Message == "" {
		t.Fatal("expected saved defense while no opponent exists")
	}
	saved := openWeeklyDraft(srv.players.st)
	if saved.Users[pa.UID].Defense.Cards[0] != mine[0] {
		t.Fatal("matching without opponent did not persist defense")
	}
	send(b, draftPair, 0, 0, 1, theirs)
	if read(b).Match == nil {
		t.Fatal("second player could not match the first uploaded defense")
	}
	send(a, draftPair, 0, 0, 1, mine)
	paired := read(a)
	if paired.Match == nil || paired.Match.UID != pb.UID || paired.Match.Lineup.Cards[0] != theirs[0] {
		t.Fatalf("match %+v", paired.Match)
	}
	theirs[0], theirs[1] = theirs[1], theirs[0]
	send(b, draftUpload, 0, 0, 2, theirs)
	read(b)
	send(a, draftOpen, 0, 0, 0, nil)
	if match := read(a).Match; match == nil || match.Lineup != paired.Match.Lineup {
		t.Fatal("opponent changed locked match")
	}

	uid := pb.UID + 100
	if _, err := srv.roles.Create(uid, 1, "draftthird"); err != nil {
		t.Fatal(err)
	}
	d := srv.draft
	d.mu.Lock()
	d.Users[uid] = &draftPlayer{Defense: d.Users[pb.UID].Defense, Score: 1000}
	d.mu.Unlock()
	send(a, draftSkip, 0, 0, 0, nil)
	skipped := read(a)
	if skipped.Match == nil || skipped.Match.UID != uid || skipped.Fights != 20 || skipped.Skips != 4 {
		t.Fatalf("skip %+v", skipped)
	}

	mine[0], mine[1] = mine[1], mine[0]
	send(a, draftFight, 0, 0, 2, mine)
	a.expectExt("FIGHT_PTO.S_FIGHT_RECORD")
	a.expectMsg(t, "轮抓积分")
	send(a, draftOpen, 0, 0, 0, nil)
	got := read(a)
	if got.Match != nil || got.Fights != 19 || got.Skips != 4 {
		t.Fatalf("fight %+v", got)
	}
	reloaded := openWeeklyDraft(srv.players.st)
	if reloaded.Users[pa.UID].Defense.Cards[0] == mine[0] || reloaded.Users[uid].Score != 1000 {
		t.Fatal("battle modified defense or the opponent's score")
	}

	// Redrafting clears cards and the uploaded defense, but preserves the ranking
	// and daily counters. Replaying a stale purchase cannot charge twice.
	before := reloaded.Users[pa.UID]
	send(a, draftRedraft, 0, before.Generation, 0, nil)
	a.expectExt("ETC_PTO.S_ATTR_UPDATE")
	redrafted := read(a)
	if redrafted.Round != 1 || len(redrafted.Picked) != 0 || redrafted.Generation != 1 || redrafted.Score != got.Score || redrafted.Fights != 19 || redrafted.Skips != 4 || srv.roles.Get(pa.UID).Gold != gold-5000 {
		t.Fatalf("redraft: %+v gold %d", redrafted, srv.roles.Get(pa.UID).Gold)
	}
	send(a, draftRedraft, 0, before.Generation, 0, nil)
	if read(a).Generation != 1 || srv.roles.Get(pa.UID).Gold != gold-5000 {
		t.Fatal("purchase replay charged twice")
	}
	reloaded = openWeeklyDraft(srv.players.st)
	if reloaded.Users[pa.UID].Defense.Leader != 0 || reloaded.Users[pa.UID].Played != before.Played || reloaded.Users[pa.UID].Generation != 1 {
		t.Fatal("redraft not persisted")
	}
	srv.roles.Update(pa.UID, func(ro *Role) bool { ro.Gold = 4999; return true })
	send(a, draftRedraft, 0, 1, 0, nil)
	rejected := read(a)
	if rejected.Generation != 1 || rejected.Message == "" || srv.roles.Get(pa.UID).Gold != 4999 {
		t.Fatal("insufficient gold changed draft")
	}
	old := reloaded.Week
	future := time.Now().In(draftZone).AddDate(0, 0, 7)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := srv.draftRollover(future); err != nil {
		t.Fatal(err)
	}
	if d.Week == old || len(d.Users) != 0 || len(d.Last) != 1 || d.Last[0].UID != pa.UID {
		t.Fatalf("rollover: %+v", d.draftSeason)
	}
}

func TestDraftLoadout(t *testing.T) {
	for _, id := range gamedata.PetsByStar[5] {
		pi := gamedata.Pets[id]
		if pi.Race < 1 || pi.Race > 4 {
			continue
		}
		f := draftFighter(id, 1)
		expected := superFighter(pi, id, 1, f.Talents, nil, true)
		if !reflect.DeepEqual(f.Suits, expected.Suits) || !reflect.DeepEqual(f.Books, expected.Books) || !reflect.DeepEqual(f.BookJX, expected.BookJX) || f.MagicLv != 6 || !f.SkillFull {
			t.Fatalf("draft loadout differs from super-starwar: %d %+v", id, f)
		}
		if len(pi.Relate) > 0 && (len(f.Suits) != 2 || len(f.Books) != 1) {
			t.Fatalf("missing related loadout: %d suits=%v books=%v", id, f.Suits, f.Books)
		}
		wantAtt, wantHP := pi.Att(100)+30, pi.HP(100)+120
		for _, suit := range f.Suits {
			if suit[0] == 1 {
				wantAtt += 706
			}
			if suit[0] == 2 {
				wantHP += 2824
			}
		}
		if f.Att != wantAtt || f.MaxHP != wantHP {
			t.Fatalf("relation multipliers leaked into draft: %d", id)
		}
		d := draftCardDetail(id)
		if d["att"] != f.Att || d["hp"] != f.MaxHP || d["magicLevel"] != 6 {
			t.Fatalf("preview differs from battle: %d %+v", id, d)
		}
	}
}
