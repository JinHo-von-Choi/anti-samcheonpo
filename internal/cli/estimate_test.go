package cli

import (
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

// estimateFixture prepares a live session with one stopped repetition and
// two turns of usage.
func estimateFixture(t *testing.T, closeReason, rule string) (*ledger.DB, *event.Session) {
	t.Helper()
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := db.UpsertLive("s9", "claude", root, root+"/t.jsonl", "claude-sonnet"); err != nil {
		t.Fatal(err)
	}
	for i, usage := range []event.Usage{{In: 1000, Out: 500}, {In: 2000, Out: 1000}} {
		ev := &event.Event{SessionID: "s9", Seq: int64(i + 1), TS: time.Now().UTC(), Kind: event.KindTool,
			Tool: "Edit", Usage: event.Usage{Model: "claude-sonnet", In: usage.In, Out: usage.Out}}
		if err := db.InsertLiveEvent("s9", ev); err != nil {
			t.Fatal(err)
		}
	}
	sig := detect.Signal{ID: "v1", Seq: 2, Detector: "S2", Rule: rule, Confidence: 0.85, Level: detect.L1}
	if err := db.InsertLiveVerdict("s9", sig, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordIntervention(sig, "s9", "agent", true); err != nil {
		t.Fatal(err)
	}
	if closeReason != "" {
		if _, err := db.Exec(`UPDATE session_observation SET close_reason=? WHERE session_id='s9'`, closeReason); err != nil {
			t.Fatal(err)
		}
	}
	return db, &event.Session{ID: "s9", Agent: "claude", Model: "claude-sonnet"}
}

func TestEstimateCountsTheStoppedRepetition(t *testing.T) {
	db, s := estimateFixture(t, "", "s2.semantic_oscillation")
	defer db.Close()
	est := estimateStoppedRepetitions(db, s, nil, time.Now())
	if est == nil {
		t.Fatal("the stopped repetition is estimated")
	}
	if est.Count != 1 {
		t.Fatalf("the count is one: %d", est.Count)
	}
	// two turns of 1500 and 3000 tokens, average 2250
	if est.Tokens != 2250 {
		t.Fatalf("the estimate uses the session's own turns: %d", est.Tokens)
	}
	if est.Basis == "" || est.Assumption == "" {
		t.Fatalf("the estimate names what it counted and what it assumes: %+v", est)
	}
	if est.Kind != "" && est.Kind != "estimate" {
		t.Fatalf("the estimate is labelled: %q", est.Kind)
	}
}

// An observation that was cut short cannot carry an estimate.
func TestEstimateIsSkippedWhenTheObservationWasCutShort(t *testing.T) {
	for _, reason := range []string{ledger.ObservationDaemonLost, ledger.ObservationIdleTimeout} {
		db, s := estimateFixture(t, reason, "s2.semantic_oscillation")
		if est := estimateStoppedRepetitions(db, s, nil, time.Now()); est != nil {
			t.Fatalf("%s: a cut short observation carries no estimate: %+v", reason, est)
		}
		db.Close()
	}
}

// A session whose only stopped call was not a repetition has nothing to
// estimate.
func TestEstimateIsEmptyWithoutStoppedRepetitions(t *testing.T) {
	db, s := estimateFixture(t, "", "s1.verify_after_docs")
	defer db.Close()
	if est := estimateStoppedRepetitions(db, s, nil, time.Now()); est != nil {
		t.Fatalf("a rule that is not a repetition is not estimated: %+v", est)
	}
}
