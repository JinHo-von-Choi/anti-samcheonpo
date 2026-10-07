package ledger

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

func TestRevisionAndLinkCommitTogetherAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	task, err := intent.NewTask("project", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r, err := intent.Revise(task.ID, nil, "goal", intent.Source{Origin: intent.User, SessionID: "s", EventID: "e", At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	link := intent.SessionLink{TaskID: task.ID, Agent: "claude", SessionID: "s", ParentAgent: "codex", ParentSessionID: "missing"}
	if err := d.SaveTaskRevision(task, r, link); err == nil {
		t.Fatal("unresolved parent accepted")
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM task`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial task after link failure", count, err)
	}
	link.ParentAgent, link.ParentSessionID = "", ""
	for i := 0; i < 2; i++ {
		if err := d.SaveTaskRevision(task, r, link); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task2, r2, link2, err := d.SessionIntent("claude", "s")
	if err != nil || task2 != task || r2.Goal != r.Goal || link2 != link {
		t.Fatalf("%+v %+v %+v %v", task2, r2, link2, err)
	}
	if _, _, _, err := d.SessionIntent("codex", "s"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cross-agent identity collision", err)
	}
	other, _ := intent.NewTask("project", time.Now())
	otherRevision, _ := intent.Revise(other.ID, nil, "other", r.Source)
	otherLink := link
	otherLink.TaskID = other.ID
	if err := d.SaveTaskRevision(other, otherRevision, otherLink); err == nil {
		t.Fatal("session reassigned to another task")
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM task WHERE id=?`, other.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("orphan after link conflict", count, err)
	}
}
