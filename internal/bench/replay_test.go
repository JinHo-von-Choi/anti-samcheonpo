package bench

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

// replayFixture writes a git project committed before the session and a
// Claude Code transcript that writes, fails, edits and passes.
func replayFixture(t *testing.T, editOld string, pass bool) (root, transcript string) {
	t.Helper()
	root = t.TempDir()
	git := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_AUTHOR_DATE=2026-10-01T08:00:00Z", "GIT_COMMITTER_DATE=2026-10-01T08:00:00Z")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	_ = os.MkdirAll(filepath.Join(root, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "src", "a.py"), []byte("x = 1\n"), 0o644)
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "base")
	type m = map[string]any
	var lines []m
	add := func(typ, ts string, msg m, extra m) {
		l := m{"type": typ, "uuid": ts, "sessionId": "sess-replay-1", "cwd": root, "timestamp": "2026-10-01T09:00:" + ts + ".000Z", "message": msg}
		for k, v := range extra {
			l[k] = v
		}
		lines = append(lines, l)
	}
	use := func(ts, id, name string, input m) {
		add("assistant", ts, m{"id": "msg_" + id, "model": "claude-sonnet-5-5", "role": "assistant", "content": []m{{"type": "tool_use", "id": id, "name": name, "input": input}}, "usage": m{"input_tokens": 1, "output_tokens": 1}}, nil)
	}
	result := func(ts, id, text string, isErr bool, tur m) {
		add("user", ts, m{"role": "user", "content": []m{{"type": "tool_result", "tool_use_id": id, "content": text, "is_error": isErr}}}, m{"toolUseResult": tur})
	}
	add("user", "00", m{"role": "user", "content": "src/a.py 값을 3으로 고쳐 줘"}, nil)
	use("01", "t1", "Write", m{"file_path": filepath.Join(root, "src", "a.py"), "content": "x = 2\n"})
	result("02", "t1", "File created successfully", false, m{"type": "update"})
	use("03", "t2", "Bash", m{"command": "pytest -q"})
	result("04", "t2", "Exit code 1\nFAILED tests/test_a.py::test_x - assert 2 == 3\n1 failed", true, m{"stdout": "FAILED tests/test_a.py::test_x - assert 2 == 3\n1 failed", "stderr": ""})
	use("05", "t3", "Edit", m{"file_path": filepath.Join(root, "src", "a.py"), "old_string": editOld, "new_string": "x = 3"})
	result("06", "t3", "The file has been updated", false, m{"originalFile": "x = 2\n", "oldString": editOld, "newString": "x = 3"})
	use("07", "t4", "Bash", m{"command": "pytest -q"})
	if pass {
		result("08", "t4", "1 passed", false, m{"stdout": "1 passed", "stderr": ""})
	} else {
		result("08", "t4", "Exit code 1\n1 failed", true, m{"stdout": "1 failed", "stderr": ""})
	}
	transcript = filepath.Join(t.TempDir(), "sess-replay-1.jsonl")
	var b strings.Builder
	for _, l := range lines {
		j, _ := json.Marshal(l)
		b.Write(append(j, '\n'))
	}
	if err := os.WriteFile(transcript, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, transcript
}

func seqOf(t *testing.T, transcript, callID string) (int64, *sources.File) {
	t.Helper()
	f := &sources.File{Path: transcript, Agent: "claude"}
	s, err := sources.Parse(*f)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range s.Events {
		if ev.CallID == callID {
			return ev.Seq, f
		}
	}
	t.Fatalf("no event for %s", callID)
	return 0, nil
}

func TestBuildReplayRestoresStateBeforeTheSpan(t *testing.T) {
	_, transcript := replayFixture(t, "x = 2", true)
	at, f := seqOf(t, transcript, "t3")
	s, _ := sources.Parse(*f)
	out := t.TempDir()
	r, err := BuildReplay(s, transcript, at, out)
	if err != nil || !r.Recoverable || r.Verified != 1 || r.Replayed != 1 {
		t.Fatalf("the span is restorable: %+v %v", r, err)
	}
	b, _ := os.ReadFile(filepath.Join(r.Dir, "repo", "src", "a.py"))
	if string(b) != "x = 2\n" {
		t.Fatalf("the repository holds the state before the span: %q", b)
	}
	task, _ := os.ReadFile(filepath.Join(r.Dir, "task.yml"))
	for _, want := range []string{"group: sess-replay-1", "pytest -q", "src/a.py 값을 3으로"} {
		if !strings.Contains(string(task), want) {
			t.Fatalf("task.yml lacks %q:\n%s", want, task)
		}
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "grader")); err == nil {
		t.Fatal("no grader is generated; the task stays non-independent until reviewed")
	}
	tasks, err := LoadLiveTasks(out)
	if err != nil || len(tasks) != 1 || tasks[0].Group != "sess-replay-1" {
		t.Fatalf("the generated task loads as a bench task with its group: %+v %v", tasks, err)
	}
}

func TestBuildReplayRejectsUnrestorableSpans(t *testing.T) {
	// the session edited text that the base commit never had
	_, transcript := replayFixture(t, "y = 9", true)
	at, f := seqOf(t, transcript, "t4")
	s, _ := sources.Parse(*f)
	out := t.TempDir()
	if r, err := BuildReplay(s, transcript, at, out); err != nil || r.Recoverable || r.Reason == "" {
		t.Fatalf("an edit that does not apply is unrestorable: %+v %v", r, err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatal("an unrestorable span leaves nothing behind")
	}
	// no verification passed after the branch point
	_, transcript = replayFixture(t, "x = 2", false)
	at, f = seqOf(t, transcript, "t3")
	s, _ = sources.Parse(*f)
	if r, _ := BuildReplay(s, transcript, at, t.TempDir()); r.Recoverable || !strings.Contains(r.Reason, "통과한 검증") {
		t.Fatalf("without a later pass there is no check: %+v", r)
	}
}
