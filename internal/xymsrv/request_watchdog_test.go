package xymsrv

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestStalledRequestLogDoesNotWaitForSessionLock(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	defer slog.SetDefault(previous)
	s := &Session{id: 1, bound: 20001, sendCh: make(chan []byte, 2)}
	start := time.Now()
	s.requestProgress.Store(&requestProgress{started: start, uid: 20001, name: "main:1", phase: "dispatch"})
	s.setRequestPhase("TASK_PTO.C_START_FIGHT", "handler")
	srv := &Server{sessions: map[*Session]struct{}{s: {}}}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	done := make(chan struct{})
	go func() {
		srv.reportStalledRequests(start.Add(6 * time.Second))
		srv.reportStalledRequests(start.Add(7 * time.Second))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watchdog blocked behind the stalled session")
	}
	text := output.String()
	if strings.Count(text, "request still running") != 1 || !strings.Contains(text, "phase=handler") || !strings.Contains(text, "TASK_PTO.C_START_FIGHT") {
		t.Fatal(text)
	}
	output.Reset()
	s.requestProgress.Store(nil)
	srv.reportStalledRequests(start.Add(time.Minute))
	if output.Len() != 0 {
		t.Fatal("completed request reported as stalled")
	}
}
