package xymsrv

// teamSummary is an immutable public projection of the active lineup. Busy
// players expose their last completed state without waiting on another player.
type teamSummary struct {
	Power  int
	Leader int
	Skill  int32
	Grade  int
}

// refreshTeamSummary is called with p.mu held (or before publication).
func (p *Player) refreshTeamSummary() *teamSummary {
	summary := &teamSummary{Leader: 1}
	if p.role == nil {
		return summary
	}
	var pets []*Pet
	if lu := p.lineup(p.FightNo); lu != nil {
		summary.Leader = lu.Leader
		for _, sl := range lu.Slots {
			if sl != nil {
				if pet := p.petAtBag(sl.Pet); pet != nil {
					pets = append(pets, pet)
				}
			}
		}
	}
	summary.Power, summary.Skill, summary.Grade = p.teamCap(pets), p.leaderSkill(), p.grade()
	p.publicTeam.Store(summary)
	return summary
}

func (srv *Server) teamSummaryOf(uid int32, me *Player) *teamSummary {
	if me != nil && me.UID == uid {
		return me.refreshTeamSummary()
	}
	ro := srv.roles.Get(uid)
	if ro == nil {
		return nil
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return nil
	}
	if p.mu.TryLock() {
		defer p.mu.Unlock()
		p.role = ro
		return p.refreshTeamSummary()
	}
	return p.publicTeam.Load()
}

func (srv *Server) fightPointOf(uid int32, me *Player) int {
	if summary := srv.teamSummaryOf(uid, me); summary != nil {
		return summary.Power
	}
	return 0
}
