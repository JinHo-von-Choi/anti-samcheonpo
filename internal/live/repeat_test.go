package live

import (
	"fmt"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

func TestRepeatedAcrossSessionsMutesOnlyUnconfirmedRepeats(t *testing.T) {
	s, _ := reliabilitySession(t)
	at := time.Now().UTC().Format(time.RFC3339Nano)
	seed := func(i int, choice any) {
		sid := fmt.Sprintf("old-%d", i)
		if err := s.db.UpsertLive(sid, "hermes", s.Root, "", ""); err != nil {
			t.Fatal(err)
		}
		vid := sid + "-1"
		if _, err := s.db.Exec(`INSERT INTO verdict(id,session_id,seq,detector,rule,confidence,evidence_event_ids,level,facts) VALUES(?,?,1,'S4','s4.read_only_streak',0.7,'[1]',1,'{"path":"tests/registry.test.ts","kind":"read_only"}')`, vid, sid); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO intervention(verdict_id,session_id,seq,channel,level,blocked,rule,delivery_state,emitted_at,user_choice) VALUES(?,?,1,'agent',1,0,'s4.read_only_streak','delivered',?,?)`, vid, sid, at, choice); err != nil {
			t.Fatal(err)
		}
	}
	v := detect.Signal{Rule: "s4.read_only_streak", Level: detect.L1, Facts: map[string]any{"path": "tests/registry.test.ts"}}
	seed(0, nil)
	seed(1, ledger.LabelTruePositive)
	seed(2, nil)
	s.mu.Lock()
	muted := s.repeatedAcrossSessions(v)
	s.mu.Unlock()
	if muted {
		t.Fatal("a confirmed delivery counted toward the mute")
	}
	seed(3, nil)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.repeatedAcrossSessions(v) {
		t.Fatal("three unconfirmed deliveries did not mute the fourth")
	}
	other := v
	other.Facts = map[string]any{"path": "tests/other.test.ts"}
	if s.repeatedAcrossSessions(other) {
		t.Fatal("another path was muted")
	}
}

// A core malicious pattern is never muted by repetition. The agent may
// break it again, and a repeated warning is exactly what a silent rule
// would have cost.
func TestRepeatedAcrossSessionsNeverMutesAHardBlockRule(t *testing.T) {
	s, _ := reliabilitySession(t)
	at := time.Now().UTC().Format(time.RFC3339Nano)
	for i := 0; i < 4; i++ {
		sid := fmt.Sprintf("hard-%d", i)
		if err := s.db.UpsertLive(sid, "hermes", s.Root, "", ""); err != nil {
			t.Fatal(err)
		}
		vid := sid + "-1"
		if _, err := s.db.Exec(`INSERT INTO verdict(id,session_id,seq,detector,rule,confidence,evidence_event_ids,level,facts) VALUES(?,?,1,'S5','s5.error_hiding',0.8,'[1]',2,'{"path":"src/auth.go","kind":"empty_catch"}')`, vid, sid); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO intervention(verdict_id,session_id,seq,channel,level,blocked,rule,delivery_state,emitted_at) VALUES(?,?,1,'agent',2,1,'s5.error_hiding','delivered',?)`, vid, sid, at); err != nil {
			t.Fatal(err)
		}
	}
	v := detect.Signal{Rule: "s5.error_hiding", Level: detect.L2, Facts: map[string]any{"path": "src/auth.go"}}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.repeatedAcrossSessions(v) {
		t.Fatal("a core malicious pattern was muted by repetition")
	}
}
