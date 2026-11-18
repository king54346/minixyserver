package xymsrv

import (
	"math/rand/v2"
	"strconv"
	"time"
	"turnrpg/internal/xym"
)

func (srv *Server) recruitRule(id int) *recruitPool {
	base := recruitPools[id]
	if base == nil || !srv.cfg.EnableGMOps || srv.ops == nil {
		return base
	}
	c := srv.ops.current.Load()
	r, ok := c.Drops.Recruit[id]
	if !ok {
		return base
	}
	p := *base
	p.rates = r.Weights
	p.pity, p.tenMin = r.Pity, r.TenMin
	p.runtimeID, p.version = id, c.Version
	bp := r.RareBP
	p.rareBP = &bp
	return &p
}

func (s *Session) recruitMallMsg(p *Player, now time.Time) xym.Msg {
	m := p.mallMsg(now)
	for _, id := range []int{1, 5} {
		rule := s.srv.recruitRule(id)
		left := 0
		if rule.pity > 0 {
			left = max(1, min(rule.pity, rule.pity-p.Mall.Pity[id]))
		}
		if id == 1 {
			m["highaddon"], m["dfzj_left_2_baodi"] = left, left
		} else {
			m["daily_left_2_baodi"] = left
		}
	}
	return m
}

func (srv *Server) boxRule(typ int) *boxKind {
	base := boxOf(typ)
	if base == nil || !srv.cfg.EnableGMOps || srv.ops == nil {
		return base
	}
	c := srv.ops.current.Load()
	r, ok := c.Drops.Boxes[base.box]
	if !ok {
		return base
	}
	b := *base
	b.again = r.AgainBP
	b.version = c.Version
	b.drops = nil
	for _, d := range r.Drops {
		b.drops = append(b.drops, boxDrop{d.Weight, d.Code, d.Count})
	}
	return &b
}

func (p *recruitPool) rareRoll(rng *rand.Rand) bool {
	if p.rareBP == nil {
		return rng.IntN(100) < rarePct
	}
	return rng.IntN(10000) < *p.rareBP
}

func (d *bagDelta) auditGains() map[string]int64 {
	out := map[string]int64{}
	for _, g := range d.gains {
		out[strconv.Itoa(g.rtype)+":"+strconv.FormatInt(int64(g.typ), 10)] += int64(g.count)
	}
	return out
}
