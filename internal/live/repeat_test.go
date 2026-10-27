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
		if _, err := s.db.Exec(`INSERT INTO verdict(id,session_id,seq,detector,rule,confidence,evidence_event_ids,level,facts) VALUES(?,?,1,'S5','s5.test_weakening',0.7,'[1]',1,'{"path":"tests/registry.test.ts","kind":"literal_replaced"}')`, vid, sid); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO intervention(verdict_id,session_id,seq,channel,level,blocked,rule,delivery_state,emitted_at,user_choice) VALUES(?,?,1,'agent',1,0,'s5.test_weakening','delivered',?,?)`, vid, sid, at, choice); err != nil {
			t.Fatal(err)
		}
	}
	v := detect.Signal{Rule: "s5.test_weakening", Level: detect.L1, Facts: map[string]any{"path": "tests/registry.test.ts"}}
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
