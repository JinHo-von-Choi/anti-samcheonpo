package live

import (
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

// observeOnce gives the session one observed event, the way a hook does.
func observeOnce(t *testing.T, s *Session) {
	t.Helper()
	s.mu.Lock()
	ev := &event.Event{Seq: 1, TS: time.Now(), Kind: event.KindTool, Tool: event.ToolRead, Summary: "read"}
	s.parser.AddEvent(ev)
	s.observe(ev)
	s.mu.Unlock()
}

// The receipt is issued when the session ends. If the daemon dies before
// that, the analysis has to be stored already, or the session leaves no
// receipt behind at all.
func TestStopHookStoresAnalysisSnapshot(t *testing.T) {
	s, _ := reliabilitySession(t)
	observeOnce(t, s)

	s.onStop(HookInput{LastMessage: "done"})

	var payload string
	if err := s.db.QueryRow(`SELECT payload FROM receipt_snapshot WHERE session_id=?`, s.ID).Scan(&payload); err != nil {
		t.Fatalf("stop hook stored no analysis snapshot: %v", err)
	}
	// The session is still being observed, so the snapshot must not make it
	// look finished.
	o, err := s.db.Observation(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if o.CloseReason != ledger.ObservationOpen {
		t.Fatalf("snapshot closed the observation: %q", o.CloseReason)
	}
}

// A daemon that dies leaves the observation open, and an open observation is
// refused a receipt. The next daemon closes what the dead one left behind,
// so the stored analysis becomes reachable without running it again.
func TestDaemonLossMakesStoredAnalysisReachable(t *testing.T) {
	s, _ := reliabilitySession(t)
	observeOnce(t, s)
	s.onStop(HookInput{LastMessage: "done"})

	if _, _, err := s.db.StoredResult(s.ID); err == nil {
		t.Fatal("stored receipt issued for a session that is still observed")
	}
	n, err := s.db.CloseLostObservations(ledger.ObservationDaemonLost, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("closed %d lost observations, want 1", n)
	}
	r, sl, err := s.db.StoredResult(s.ID)
	if err != nil {
		t.Fatalf("stored analysis unreachable after the daemon was lost: %v", err)
	}
	if sl.Spec == "" || r.Session == nil || r.Session.ID != s.ID {
		t.Fatal("stored analysis did not come back with its seal")
	}
}

// A session that never reports the end of its session keeps everything it
// holds. The daemon releases it, and the record says the observation stopped
// without claiming that the agent ended.
func TestReapIdleSessionsReleasesSilentSession(t *testing.T) {
	s, d := reliabilitySession(t)
	s.mu.Lock()
	s.lastActive = time.Now().Add(-sessionIdleLimit - time.Minute)
	s.mu.Unlock()

	d.reapIdleSessions()

	d.mu.Lock()
	_, held := d.sessions[s.ID]
	d.mu.Unlock()
	if held {
		t.Fatal("silent session still held by the daemon")
	}
	o, err := s.db.Observation(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if o.CloseReason != ledger.ObservationIdleTimeout {
		t.Fatalf("reaped observation reason = %q, want %q", o.CloseReason, ledger.ObservationIdleTimeout)
	}
	var ended string
	if err := s.db.QueryRow(`SELECT COALESCE(ended_at,'') FROM session WHERE id=?`, s.ID).Scan(&ended); err != nil {
		t.Fatal(err)
	}
	if ended != "" {
		t.Fatalf("reaping invented an agent end: %s", ended)
	}
}

// A session that is still working must not be released. The limit is about
// silence, not about age.
func TestReapIdleSessionsKeepsActiveSession(t *testing.T) {
	s, d := reliabilitySession(t)
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()

	d.reapIdleSessions()

	d.mu.Lock()
	_, held := d.sessions[s.ID]
	d.mu.Unlock()
	if !held {
		t.Fatal("active session was released")
	}
}
