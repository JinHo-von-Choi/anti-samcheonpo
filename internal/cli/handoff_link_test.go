package cli

import (
	"bytes"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"testing"
	"time"
)

func TestHandoffLinkCLIRecordsExplicitParentAndRejectsReassignment(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := intent.NewTask("project", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r, err := intent.Revise(task.ID, nil, "goal", intent.Source{Origin: intent.User, SessionID: "parent", EventID: "request", At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveTaskRevision(task, r, intent.SessionLink{TaskID: task.ID, Agent: "claude", SessionID: "parent"}); err != nil {
		t.Fatal(err)
	}
	cmd := handoffCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"link", "parent", "--from", "claude", "--agent", "codex", "--session", "child"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	_, _, link, err := db.SessionIntent("codex", "child")
	if err != nil || link.ParentAgent != "claude" || link.ParentSessionID != "parent" {
		t.Fatalf("%+v %v", link, err)
	}
	link.ParentSessionID = "missing"
	if err := db.LinkTaskSession(link); err == nil {
		t.Fatal("existing child reassigned to unknown parent")
	}
}
