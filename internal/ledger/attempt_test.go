package ledger

import (
	"path/filepath"
	"testing"
)

func TestFailedAttemptsPerTaskRevision(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := FailedAttempt{TaskID: "t", Revision: 1, EditFP: "e1", Path: "a.py", FailureFP: "f", SessionID: "s1", Agent: "claude", Seq: 4}
	if err := db.SaveFailedAttempts([]FailedAttempt{a, a, {TaskID: "t", Revision: 2, EditFP: "e2", Path: "a.py", FailureFP: "f", SessionID: "s1", Agent: "claude"}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.FailedAttempts("t", 1)
	if err != nil || len(got) != 1 || got[0] != a {
		t.Fatalf("one revision, stored once: %+v %v", got, err)
	}
	if got, _ := db.FailedAttempts("t", 3); len(got) != 0 {
		t.Fatal("another revision has its own attempts")
	}
}
