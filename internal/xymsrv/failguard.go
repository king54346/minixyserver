package xymsrv

import (
	"context"
	"net"
	"sync"
	"time"
)

const (
	failLimit   = 5
	failWindow  = 15 * time.Minute
	failLockFor = 15 * time.Minute
)

type failStat struct {
	count, pending int
	first, lock    time.Time
}

// failGuard belongs to the server, not a TCP connection. Pending attempts reserve
// capacity so concurrent connections cannot all pass the check before failing.
type failGuard struct {
	mu        sync.Mutex
	urss, ips map[string]*failStat
	nextSweep time.Time
	now       func() time.Time
	changed   chan struct{}
}

func (g *failGuard) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *failGuard) begin(urs, ip string) (func(bool), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	done, allowed, _ := g.beginLocked(urs, ip)
	return done, allowed
}

// Pending verifications are capacity reservations, not failed credentials.
// Wait for capacity without letting concurrent failures exceed either budget.
// A nil done with blocked=false means cancellation/timeout, not a 15-minute ban.
func (g *failGuard) beginWait(ctx context.Context, stop <-chan struct{}, urs, ip string) (done func(bool), blocked bool) {
	for {
		select {
		case <-ctx.Done():
			return nil, false
		case <-stop:
			return nil, false
		default:
		}
		g.mu.Lock()
		done, allowed, busy := g.beginLocked(urs, ip)
		if allowed || !busy {
			g.mu.Unlock()
			return done, !allowed
		}
		if g.changed == nil {
			g.changed = make(chan struct{})
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, false
		case <-stop:
			return nil, false
		case <-changed:
		}
	}
}

// Caller holds mu; busy distinguishes in-flight verifications from real locks.
func (g *failGuard) beginLocked(urs, ip string) (func(bool), bool, bool) {
	now := g.clock()
	if g.urss == nil {
		g.urss, g.ips = make(map[string]*failStat), make(map[string]*failStat)
	}
	if !now.Before(g.nextSweep) {
		for _, m := range []map[string]*failStat{g.urss, g.ips} {
			for k, s := range m {
				if s.pending == 0 && !now.Before(s.lock) && !now.Before(s.first.Add(failWindow)) {
					delete(m, k)
				}
			}
		}
		g.nextSweep = now.Add(time.Minute)
	}
	get := func(m map[string]*failStat, k string) *failStat {
		s := m[k]
		if s == nil {
			s = &failStat{first: now}
			m[k] = s
		}
		if !now.Before(s.lock) && !now.Before(s.first.Add(failWindow)) {
			s.count, s.first, s.lock = 0, now, time.Time{}
		}
		return s
	}
	// Reject blocked sources before allocating entries for arbitrary account names.
	for _, s := range []*failStat{g.urss[key(urs)], g.ips[ip]} {
		if s != nil && now.Before(s.lock) {
			return nil, false, false
		}
		if s != nil && s.pending >= failLimit {
			return nil, false, true
		}
	}
	// Use exactly the same normalization as the account store (including spaces).
	a, i := get(g.urss, key(urs)), get(g.ips, ip)
	for _, s := range []*failStat{a, i} {
		if now.Before(s.lock) || s.count+s.pending >= failLimit {
			return nil, false, !now.Before(s.lock) && s.pending > 0
		}
	}
	a.pending++
	i.pending++
	var once sync.Once
	return func(failed bool) {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			now := g.clock()
			for _, s := range []*failStat{a, i} {
				s.pending--
				if failed {
					s.count++
					if s.count >= failLimit {
						s.lock = now.Add(failLockFor)
					}
				}
			}
			// A good credential must not erase failures from this IP against other users.
			if !failed && !now.Before(a.lock) {
				a.count = 0
				a.first = now
			}
			if g.changed != nil {
				close(g.changed)
				g.changed = make(chan struct{})
			}
		})
	}, true, false
}

func (s *Session) ipOf() string {
	if s.conn == nil {
		return "?"
	}
	addr := s.conn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
