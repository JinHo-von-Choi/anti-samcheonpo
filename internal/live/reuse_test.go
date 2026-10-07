package live

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestRunnerReusesPassAndInvalidatesInputEnvironmentAndRevision(t *testing.T) {
	root := t.TempDir()
	counter := filepath.Join(t.TempDir(), "calls")
	for _, name := range []string{"input", "runtime"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("ok"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ws := NewWorkspace(root)
	defer ws.Close()
	r := &Runner{Root: root, WS: ws, Timeout: time.Second}
	c := contract.Check{ID: "c", Check: "echo call >> '" + counter + "'; test -s input", Pure: true, Reuse: &contract.ReuseScope{Inputs: []string{"input"}, EnvironmentFiles: []string{"runtime"}, Deterministic: true, MaxAgeSec: 60}}
	var wg sync.WaitGroup
	results := make(chan CheckResult, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- r.RunRevision([]contract.Check{c}, false, nil, "task", 1)[0] }()
	}
	wg.Wait()
	close(results)
	reused := 0
	for res := range results {
		if !res.Pass || res.Evidence == nil {
			t.Fatalf("%+v", res)
		}
		if res.Reused {
			reused++
		}
	}
	if reused != 1 {
		t.Fatal("simultaneous calls did not share pass", reused)
	}
	assertCalls := func(want int) {
		t.Helper()
		b, err := os.ReadFile(counter)
		if err != nil || strings.Count(string(b), "call\n") != want {
			t.Fatalf("calls=%q want=%d err=%v", b, want, err)
		}
	}
	assertCalls(1)
	os.WriteFile(filepath.Join(root, "input"), []byte("changed"), 0o600)
	if res := r.RunRevision([]contract.Check{c}, false, nil, "task", 1)[0]; res.Reused || !res.Pass {
		t.Fatalf("input: %+v", res)
	}
	assertCalls(2)
	os.WriteFile(filepath.Join(root, "runtime"), []byte("changed"), 0o600)
	if res := r.RunRevision([]contract.Check{c}, false, nil, "task", 1)[0]; res.Reused || !res.Pass {
		t.Fatalf("environment file: %+v", res)
	}
	assertCalls(3)
	t.Setenv("SAMCHEONPO_TEST_ENV", "changed")
	if res := r.RunRevision([]contract.Check{c}, false, nil, "task", 1)[0]; res.Reused || !res.Pass {
		t.Fatalf("environment variable: %+v", res)
	}
	assertCalls(4)
	if res := r.RunRevision([]contract.Check{c}, false, nil, "task", 2)[0]; res.Reused || !res.Pass {
		t.Fatalf("revision: %+v", res)
	}
	assertCalls(5)
	c.Check = "echo call >> '" + counter + "'; false"
	for i := 0; i < 2; i++ {
		if res := r.RunRevision([]contract.Check{c}, false, nil, "task", 2)[0]; res.Reused || res.Pass {
			t.Fatalf("failure cached: %+v", res)
		}
	}
	assertCalls(7)
}

func TestCheckpointSkipAndMutationDoNotRetainPass(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	s.checkpoint(false)
	s.mu.Lock()
	if met, _ := s.criteria(); met != 1 {
		t.Fatal("no initial pass")
	}
	s.observe(&event.Event{Kind: event.KindTool, Tool: event.ToolWrite, Category: event.CatProduce, Mutating: true})
	if met, _ := s.criteria(); met != 0 {
		t.Fatal("pass survived mutation")
	}
	s.mu.Unlock()
	s.checkpoint(false)
	s.runner.Busy = func() bool { return true }
	s.checkpoint(false)
	s.mu.Lock()
	defer s.mu.Unlock()
	if met, _ := s.criteria(); met != 0 {
		t.Fatal("skipped check retained stale pass")
	}
	s.c.Done = append(s.c.Done, contract.Check{ID: "manual", Manual: "visual confirmation"})
	if _, total := s.criteria(); total != 2 {
		t.Fatal("manual criterion disappeared")
	}
}

func TestRunnerRejectsChangedInputAndTruncatedOutput(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "input"), []byte("old"), 0o600)
	ws := NewWorkspace(root)
	defer ws.Close()
	r := &Runner{Root: root, WS: ws, Timeout: time.Second}
	c := contract.Check{ID: "c", Check: "echo changed > input", Pure: true, Reuse: &contract.ReuseScope{Inputs: []string{"input"}, EnvironmentFiles: []string{"input"}, Deterministic: true, MaxAgeSec: 60}}
	res := r.Run([]contract.Check{c}, false, nil)[0]
	if res.Pass || !res.SideEffect || res.Reused {
		t.Fatalf("%+v", res)
	}
	res = r.Run([]contract.Check{{ID: "big", Check: "head -c 1048600 /dev/zero"}}, false, nil)[0]
	if res.Pass || !res.Truncated {
		t.Fatalf("truncated output counted as complete: %+v", res)
	}
}

func TestGitWorkspaceIgnoresOwnStateDirectory(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o600)
	git(t, root, "init", "-q")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "init")
	ws := NewWorkspace(root)
	defer ws.Close()
	r := &Runner{Root: root, WS: ws, Timeout: 5 * time.Second}
	own := r.Run([]contract.Check{{ID: "own", Check: "mkdir -p .samcheonpo/handoff && echo s > .samcheonpo/contract.state.json && echo h > .samcheonpo/handoff/x.md"}}, false, nil)[0]
	if !own.Pass || own.SideEffect {
		t.Fatalf("samcheonpo state writes counted as a check side effect: %+v", own)
	}
	user := r.Run([]contract.Check{{ID: "user", Check: "echo b > a.txt"}}, false, nil)[0]
	if !user.SideEffect {
		t.Fatalf("workspace change not detected: %+v", user)
	}
}
