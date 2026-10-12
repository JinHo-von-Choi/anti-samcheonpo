package ledger

import (
	"context"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"path/filepath"
	"testing"
	"time"
)

func TestLinkedWindowIsReadOnlyBoundedAndRevisionChecked(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	task, err := intent.NewTask("project", now)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := intent.Revise(task.ID, nil, "fix", intent.Source{Origin: intent.User, SessionID: "s", EventID: "e", At: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveTaskRevision(task, rev, intent.SessionLink{TaskID: task.ID, Agent: "claude", SessionID: rev.Source.SessionID}); err != nil {
		t.Fatal(err)
	}
	roPath, err := db.FilePath()
	if err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(roPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	_, _, err = ro.LinkedWindow(context.Background(), task.ID, rev.Number+1, 32, 256)
	if err == nil {
		t.Fatal("stale revision accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = ro.LinkedWindow(ctx, task.ID, rev.Number, 32, 256); err == nil {
		t.Fatal("cancel ignored")
	}
	if _, _, err = ro.LinkedWindow(context.Background(), task.ID, rev.Number, 0, 256); err == nil {
		t.Fatal("unbounded request accepted")
	}
}
