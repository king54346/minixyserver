package battle

import "testing"

func TestTiantiTimeoutDrawDoesNotChangeOtherModes(t *testing.T) {
	team1 := []*Fighter{fu(1, 1, 1000000, 1)}
	team2 := []*Fighter{fu(101, 1, 2000000, 1)}
	regular := SimulateWith(CloneTeam(team1), CloneTeam(team2), Options{}, nil)
	ladder := SimulateWith(CloneTeam(team1), CloneTeam(team2), Options{DrawOnTimeout: true}, nil)
	if regular.Draw || regular.Win || !ladder.Draw || ladder.Win || ladder.Rounds != maxRounds {
		t.Fatal("timeout draw changed non-ladder outcomes or failed to activate")
	}
}
