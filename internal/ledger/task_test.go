package ledger

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

func TestTaskRevisionAtomicReplayAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task, err := intent.NewTask("project", now)
	if err != nil {
		t.Fatal(err)
	}
	r, err := intent.Revise(task.ID, nil, "fix", intent.Source{Origin: intent.User, SessionID: "s", EventID: "e", At: now})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := d.SaveTaskRevision(task, r); err != nil {
			t.Fatal(err)
		}
	}
	conflict := r
	conflict.Goal = "different"
	if err := d.SaveTaskRevision(task, conflict); err == nil {
		t.Fatal("overwrote intent")
	}
	gap := r
	gap.Number = 3
	gap.Parent = 2
	if err := d.SaveTaskRevision(task, gap); err == nil {
		t.Fatal("accepted missing parent")
	}
	link := intent.SessionLink{TaskID: task.ID, Agent: "claude", SessionID: "s", IncludesChildren: true}
	if err := d.LinkTaskSession(link); err != nil {
		t.Fatal(err)
	}
	if err := d.LinkTaskSession(link); err != nil {
		t.Fatal(err)
	}
	child := intent.SessionLink{TaskID: task.ID, Agent: "codex", SessionID: "child", ParentAgent: "claude", ParentSessionID: "s"}
	if err := d.LinkTaskSession(child); err != nil {
		t.Fatal(err)
	}
	child.ParentSessionID = "missing"
	if err := d.LinkTaskSession(child); err == nil {
		t.Fatal("unresolved parent accepted")
	}
	e := verification.Evidence{Version: verification.Version, ID: "evidence", Key: verification.Key{TaskID: task.ID, Revision: 1, CheckID: "check", CommandHash: "cmd", InputHash: "input", EnvironmentHash: "env", RunnerVersion: "runner"}, SessionID: "s", SourceEventID: "check-event", ObservedAt: now, ExpiresAt: now.Add(time.Hour), Pass: true, Complete: true, ResultHash: "pass"}
	for i := 0; i < 2; i++ {
		if err := d.SaveEvidence(e); err != nil {
			t.Fatal(err)
		}
	}
	conflictEvidence := e
	conflictEvidence.ResultHash = "other"
	if err := d.SaveEvidence(conflictEvidence); err == nil {
		t.Fatal("overwrote evidence")
	}
	e.Key.Revision = 2
	if err := d.SaveEvidence(e); err == nil {
		t.Fatal("saved evidence against absent revision")
	}
	e.Key.Revision = 1
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.LatestIntent(task.ID)
	if err != nil || got.Goal != r.Goal {
		t.Fatalf("%+v %v", got, err)
	}
	es, err := d.Evidence(e.Key)
	if err != nil || len(es) != 1 || es[0].ID != e.ID {
		t.Fatalf("%+v %v", es, err)
	}
	// A failure after task insertion must not leave a half-created task.
	orphan := task
	orphan.ID = "orphan"
	bad := r
	bad.TaskID = orphan.ID
	bad.Number = 2
	bad.Parent = 1
	if err := d.SaveTaskRevision(orphan, bad); err == nil {
		t.Fatal("invalid append succeeded")
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM task WHERE id='orphan'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial transaction: %d %v", count, err)
	}
}

func TestUpgradeV2LedgerPreservesAnalysisAndRollsBackFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`CREATE TABLE schema_version(version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"migrations/0001_init.sql", "migrations/0002_live.sql"} {
		b, err := migrations.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = raw.Exec(string(b)); err != nil {
			t.Fatal(err)
		}
	}
	raw.Exec(`INSERT INTO schema_version VALUES(2)`)
	// The current writer also records unsealed observation metadata. Remove
	// that table after seeding to construct the exact v2 schema footprint.
	meta, err := migrations.ReadFile("migrations/0009_session_observation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(string(meta)); err != nil {
		t.Fatal(err)
	}
	snapshotSchema, err := migrations.ReadFile("migrations/0010_receipt_snapshot.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(string(snapshotSchema)); err != nil {
		t.Fatal(err)
	}
	old := &DB{raw}
	r := result(t)
	before, err := old.SaveAnalysis(r, 1, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`DROP TABLE session_observation`); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`DROP TABLE receipt_snapshot`); err != nil {
		t.Fatal(err)
	}
	// Force migration 3 to fail after its first CREATE statement.
	if _, err = raw.Exec(`CREATE TABLE intent_revision(conflict TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(raw); err == nil {
		t.Fatal("migration should fail")
	}
	var count int
	if err = raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='task'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial schema leaked")
	}
	if old.Version() != 2 {
		t.Fatal("failed migration advanced version")
	}
	if _, err = raw.Exec(`DROP TABLE intent_revision`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	after, err := d.Seal(r.Session.ID)
	if err != nil || after.Head != before.Head || after.Spec != before.Spec {
		t.Fatalf("legacy seal changed: %+v %v", after, err)
	}
	if _, err = d.Observation(r.Session.ID); err != sql.ErrNoRows {
		t.Fatalf("legacy metadata invented: %v", err)
	}
	if d.Version() != 10 {
		t.Fatal(d.Version())
	}
}
