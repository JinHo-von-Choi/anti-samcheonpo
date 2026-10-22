package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/patch"
)

// forkFixture prepares a project with a recorded passing point, a file that
// drifted after it, and one live session.
func forkFixture(t *testing.T) (root, file string) {
	t.Helper()
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	root = t.TempDir()
	file = filepath.Join(root, "math.go")
	if err := os.WriteFile(file, []byte("package math\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := patch.NewGoldenStateManager(root)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Capture(patch.SnapshotMeta{Reason: "검증 통과", Session: "s1", Seq: 7},
		map[string]patch.File{"math.go": {Data: data, Mode: 0o644}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package math\n\nfunc Add(a, b int) int { return a + b + 0 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLive("s1", "claude", root, filepath.Join(root, "transcript.jsonl"), "claude-sonnet"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return root, file
}

func TestForkPreviewsWithoutRestoring(t *testing.T) {
	root, file := forkFixture(t)
	cmd := forkCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("fork 미리보기 실패: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "math.go") {
		t.Fatalf("the preview names the file:\n%s", text)
	}
	if !strings.Contains(text, "--apply") {
		t.Fatalf("the preview says how to apply:\n%s", text)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "a + b + 0") {
		t.Fatal("the preview leaves the workspace alone")
	}
	_ = root
}

func TestForkRestoresAndBranchesTheLedger(t *testing.T) {
	root, file := forkFixture(t)
	cmd := forkCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--apply"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("fork 실행 실패: %v", err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "a + b + 0") {
		t.Fatalf("the file is back at the passing point:\n%s", got)
	}
	if !strings.Contains(string(got), "return a + b") {
		t.Fatalf("the recorded content is restored:\n%s", got)
	}
	text := out.String()
	if !strings.Contains(text, "새 세션: s1-fork-") {
		t.Fatalf("the new session is named:\n%s", text)
	}
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, err := db.SessionBranchState("s1")
	if err != nil {
		t.Fatal(err)
	}
	if state != ledger.BranchStateDiverged {
		t.Fatalf("the failed session records the branch: %q", state)
	}
	newID := strings.TrimSpace(strings.SplitN(strings.SplitN(text, "새 세션: ", 2)[1], "\n", 2)[0])
	evs, err := db.EventsOf(newID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 {
		t.Fatal("the new session starts with the recovery note")
	}
	if !strings.Contains(evs[0].Summary, "분기") {
		t.Fatalf("the recovery note is stored: %q", evs[0].Summary)
	}
	// the old session keeps its own record
	oldEvents, err := db.EventsOf("s1")
	if err != nil {
		t.Fatal(err)
	}
	_ = oldEvents
	_ = root
}

func TestForkWithoutAPassingPointIsRefused(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	root := t.TempDir()
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLive("s2", "claude", root, filepath.Join(root, "t.jsonl"), "claude-sonnet"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	cmd := forkCmd()
	cmd.SetArgs([]string{"--apply"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("a project without a passing point cannot be forked")
	}
}
