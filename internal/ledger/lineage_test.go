package ledger

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

// TestLineageLinksJudgmentsAcrossSessions checks that a task's chain says
// which judgment came from which session, that a handed-over session
// points back at the session that handed it over, and that an enforced
// judgment is marked apart from an announced one.
func TestLineageLinksJudgmentsAcrossSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := time.Now().UTC()
	task, err := intent.NewTask("/tmp/project", now)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := intent.Revise(task.ID, nil, "첫 목표", intent.Source{Origin: intent.User, SessionID: "parent", EventID: "e1", At: now})
	if err != nil {
		t.Fatal(err)
	}
	parent := intent.SessionLink{TaskID: task.ID, Agent: "claude", SessionID: "parent", IncludesChildren: true}
	child := intent.SessionLink{TaskID: task.ID, Agent: "codex", SessionID: "child", ParentAgent: "claude", ParentSessionID: "parent"}
	if err := d.SaveTaskRevision(task, rev, parent); err != nil {
		t.Fatal(err)
	}
	if err := d.LinkTaskSession(child); err != nil {
		t.Fatal(err)
	}

	base := now.Add(-time.Minute)
	seedTimingSession(t, d, "parent", 10, 8, base)
	seedTimingSession(t, d, "child", 10, 3, base)
	blocked := detect.Signal{ID: "child-v2", Detector: "S5", Rule: "s5.false_done", Seq: 9, Confidence: 0.8, Level: detect.L3}
	if err := d.InsertLiveVerdict("child", blocked, "user", "agent"); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordIntervention(blocked, "child", "agent", true); err != nil {
		t.Fatal(err)
	}

	lineage, err := d.Lineage(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lineage.Revisions) != 1 || lineage.Revisions[0].Goal != "첫 목표" {
		t.Fatalf("revisions %+v", lineage.Revisions)
	}
	if len(lineage.Sessions) != 2 {
		t.Fatalf("sessions %d, want 2", len(lineage.Sessions))
	}
	bySession := map[string]LineageSession{}
	for _, s := range lineage.Sessions {
		bySession[s.Session] = s
	}
	parentSession, ok := bySession["parent"]
	if !ok {
		t.Fatal("parent session missing from lineage")
	}
	if parentSession.Verdicts != 1 {
		t.Fatalf("parent verdicts %d, want 1", parentSession.Verdicts)
	}
	if parentSession.Judgments[0].Rule != "s2.oscillation" {
		t.Fatalf("parent judgment %+v", parentSession.Judgments[0])
	}
	if parentSession.Judgments[0].Blocked {
		t.Fatal("an announced judgment must not be marked as blocked")
	}
	if parentSession.Position <= 0 || !parentSession.InLastThird {
		t.Fatalf("parent position %v last third %v", parentSession.Position, parentSession.InLastThird)
	}
	childSession, ok := bySession["child"]
	if !ok {
		t.Fatal("child session missing from lineage")
	}
	if childSession.ParentAgent != "claude" || childSession.ParentSession != "parent" {
		t.Fatalf("handoff parent lost: %+v", childSession)
	}
	if childSession.Verdicts != 2 {
		t.Fatalf("child verdicts %d, want 2", childSession.Verdicts)
	}
	if !childSession.Judgments[1].Blocked {
		t.Fatal("an enforced judgment must be marked as blocked")
	}
	if childSession.FirstVerdict.IsZero() {
		t.Fatal("first verdict time missing")
	}
	if childSession.LeadMS <= 0 {
		t.Fatalf("lead %dms, want positive", childSession.LeadMS)
	}
}

// TestLineageWithoutSessionsReportsNothing checks that a task nobody
// worked on is an empty chain, not an error.
func TestLineageWithoutSessionsReportsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	lineage, err := d.Lineage("no-such-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(lineage.Revisions) != 0 || len(lineage.Sessions) != 0 {
		t.Fatalf("expected empty lineage, got %+v", lineage)
	}
}

// TestLineageKeepsEventOrder checks that judgments are listed in the order
// they were raised, because the order is the chain.
func TestLineageKeepsEventOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := time.Now().UTC()
	task, err := intent.NewTask("/tmp/project", now)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := intent.Revise(task.ID, nil, "목표", intent.Source{Origin: intent.User, SessionID: "s", EventID: "e", At: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SaveTaskRevision(task, rev, intent.SessionLink{TaskID: task.ID, Agent: "claude", SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	base := now.Add(-time.Minute)
	seedTimingSession(t, d, "s", 10, 5, base)
	for _, seq := range []int64{7, 9} {
		v := detect.Signal{ID: "s-v" + string(rune('a'+seq)), Detector: "S2", Rule: "s2.oscillation", Seq: seq, Level: detect.L1}
		if err := d.InsertLiveVerdict("s", v, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	lineage, err := d.Lineage(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lineage.Sessions) != 1 {
		t.Fatalf("sessions %d", len(lineage.Sessions))
	}
	seqs := []int64{}
	for _, j := range lineage.Sessions[0].Judgments {
		seqs = append(seqs, j.Seq)
	}
	if len(seqs) != 3 || seqs[0] != 5 || seqs[1] != 7 || seqs[2] != 9 {
		t.Fatalf("judgment order %v", seqs)
	}
}
