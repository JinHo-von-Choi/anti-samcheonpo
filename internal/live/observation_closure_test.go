package live

import (
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestObservationClosureDoesNotInventSessionEnd(t *testing.T) {
	s, _ := reliabilitySession(t)
	activity := time.Now().Add(-time.Minute)
	s.mu.Lock()
	ev := &event.Event{Seq: 1, TS: activity, Kind: event.KindTool, Tool: event.ToolRead}
	s.parser.AddEvent(ev)
	s.observe(ev)
	s.mu.Unlock()
	if _, err := s.CloseObservation(); err != nil {
		t.Fatal(err)
	}
	var end string
	if err := s.db.QueryRow(`SELECT COALESCE(ended_at,'') FROM session WHERE id=?`, s.ID).Scan(&end); err != nil {
		t.Fatal(err)
	}
	if end != "" {
		t.Fatalf("daemon closure invented agent end: %s", end)
	}
	o, err := s.db.Observation(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if o.CloseReason != "daemon_shutdown" || o.ClosedAt.IsZero() || !o.LastActivity.Equal(activity) {
		t.Fatalf("observation not recorded: %+v", o)
	}
	stored, err := s.db.EventsOf(s.ID)
	if err != nil || len(stored) == 0 || stored[0].SourceRef == "" {
		t.Fatalf("missing safe event reference: %+v %v", stored, err)
	}
	again, err := newSession(s.ID, s.Agent, s.Root, "", s.db, s.prices, s.caps)
	if err != nil {
		t.Fatal(err)
	}
	defer again.CloseObservation()
	o, err = s.db.Observation(s.ID)
	if err != nil || o.CloseReason != "open" || !o.ClosedAt.IsZero() {
		t.Fatalf("resume retained observation closure: %+v %v", o, err)
	}
	if _, err = again.Finalize(); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT COALESCE(ended_at,'') FROM session WHERE id=?`, s.ID).Scan(&end); err != nil || end == "" {
		t.Fatalf("actual SessionEnd missing: %s %v", end, err)
	}
	o, err = s.db.Observation(s.ID)
	if err != nil || o.CloseReason != "session_end" {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestObservedReferenceDoesNotStoreCallIdentifier(t *testing.T) {
	s, _ := reliabilitySession(t)
	ev := &event.Event{Seq: 1, TS: time.Now(), Kind: event.KindTool, CallID: "fixture-secret-call-id", SourceRef: "fixture-private-transcript-path"}
	s.persistEvent(ev)
	rows, err := s.db.EventsOf(s.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%+v %v", rows, err)
	}
	if !strings.HasPrefix(rows[0].SourceRef, "source-hash:") || strings.Contains(rows[0].SourceRef, "fixture-") {
		t.Fatalf("unsafe source ref: %s", rows[0].SourceRef)
	}
	ref := ev.SourceRef
	s.persistEvent(ev)
	if ref != ev.SourceRef {
		t.Fatal("duplicate persistence changed reference")
	}
}
