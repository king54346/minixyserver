package xymsrv

import (
	"log/slog"
	"time"
)

// The existing slow-request log runs only after dispatch returns. A stuck
// handler never reaches it, so sample progress without taking session/player
// locks. Do not record request bodies or authentication credentials.
type requestProgress struct {
	started time.Time
	uid     int32
	name    string
	phase   string
}

func (s *Session) setRequestPhase(name, phase string) {
	if previous := s.requestProgress.Load(); previous != nil {
		s.requestProgress.Store(&requestProgress{started: previous.started, uid: s.bound, name: name, phase: phase})
	}
}

func (srv *Server) reportStalledRequests(now time.Time) {
	srv.mu.Lock()
	sessions := make([]*Session, 0, len(srv.sessions))
	for s := range srv.sessions {
		sessions = append(sessions, s)
	}
	srv.mu.Unlock()
	for _, s := range sessions {
		r := s.requestProgress.Load()
		if r == nil || now.Sub(r.started) < 5*time.Second {
			continue
		}
		last := s.lastStallWarning.Load()
		if last != 0 && now.UnixNano()-last < int64(30*time.Second) {
			continue
		}
		s.lastStallWarning.Store(now.UnixNano())
		slog.Warn("request still running", "sess", s.id, "uid", r.uid, "request", r.name,
			"phase", r.phase, "elapsed", now.Sub(r.started), "send_queue", len(s.sendCh))
	}
}

func (srv *Server) watchRequests() {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for now := range timer.C {
		srv.mu.Lock()
		closed := srv.closed
		srv.mu.Unlock()
		if closed {
			return
		}
		srv.reportStalledRequests(now)
	}
}
