package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

// heavyDaemon opens Claude sessions on the given roots in one daemon.
func heavyDaemon(t *testing.T, roots ...string) (*Daemon, []*Session) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	db, err := ledger.Open(filepath.Join(home, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	prices, err := cost.Load("")
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{sessions: map[string]*Session{}, db: db, prices: prices, versions: map[string]string{"claude": "2.1.0"}}
	var out []*Session
	for i, root := range roots {
		if err := os.WriteFile(filepath.Join(root, ".samcheonpo.yml"), []byte("notify: {desktop: false}\niron_laws: {enabled: false}\n"), 0600); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
		s, err := newSession("heavy-"+string(rune('a'+i)), "claude", root, "", db, prices, adapter.Profiles["claude"].Caps)
		if err != nil {
			t.Fatal(err)
		}
		s.daemon = d
		d.sessions[s.ID] = s
		out = append(out, s)
	}
	t.Cleanup(func() {
		for _, s := range out {
			if _, err := s.Finalize(); err != nil {
				t.Error(err)
			}
		}
		db.Close()
	})
	return d, out
}

func heavyCall(t *testing.T, d *Daemon, s *Session, event, id, cmd string, extra map[string]any) string {
	t.Helper()
	m := map[string]any{"session_id": s.ID, "cwd": s.Root, "tool_name": "Bash", "tool_use_id": id,
		"tool_input": map[string]any{"command": cmd}}
	for k, v := range extra {
		m[k] = v
	}
	raw, _ := json.Marshal(m)
	out, _, err := d.handle(Request{Agent: "claude", Event: event, Payload: raw})
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func passed(out string) map[string]any {
	return map[string]any{"tool_response": map[string]any{"stdout": out, "exitCode": 0}}
}

func TestHeavyRunOfAnotherSessionIsAdvised(t *testing.T) {
	root := t.TempDir()
	d, ss := heavyDaemon(t, root, root)
	a, b := ss[0], ss[1]
	if out := heavyCall(t, d, a, "PreToolUse", "a1", "cargo test --workspace", nil); strings.Contains(out, "무거운 시험") {
		t.Fatalf("the first heavy run is free: %s", out)
	}
	out := heavyCall(t, d, b, "PreToolUse", "b1", "cargo test --workspace", nil)
	if !strings.Contains(out, "무거운 시험") || !strings.Contains(out, "heavy-a") {
		t.Fatalf("a second heavy run in the same folder is advised: %s", out)
	}
	// a light, selected test is not heavy
	if out := heavyCall(t, d, b, "PreToolUse", "b2", "cargo test --test todo_test", nil); strings.Contains(out, "무거운 시험") {
		t.Fatalf("a selected test is free: %s", out)
	}
	// once the first run answered, the folder is free again
	heavyCall(t, d, a, "PostToolUse", "a1", "cargo test --workspace", passed("test result: ok"))
	if out := heavyCall(t, d, b, "PreToolUse", "b3", "cargo test --workspace", nil); strings.Contains(out, "무거운 시험") {
		t.Fatalf("released run still advised: %s", out)
	}
}

func TestHeavyRunsInOtherFoldersAreFree(t *testing.T) {
	d, ss := heavyDaemon(t, t.TempDir(), t.TempDir())
	heavyCall(t, d, ss[0], "PreToolUse", "a1", "npm test", nil)
	if out := heavyCall(t, d, ss[1], "PreToolUse", "b1", "npm test", nil); strings.Contains(out, "무거운 시험") {
		t.Fatalf("another folder is another machine budget: %s", out)
	}
}

func TestHeavyBackgroundRunHoldsUntilItsResult(t *testing.T) {
	s, d := mcodeTestSession(t)
	s.mu.Lock()
	s.caps = adapter.Profiles["mcode"].Caps
	s.daemon = d
	s.mu.Unlock()
	heavyNext := func(id string) *heavySignal {
		s.mu.Lock()
		defer s.mu.Unlock()
		ev := s.parser.HookToolUse("Bash", id, json.RawMessage(`{"command":"npm test"}`), time.Now())
		s.classifyEvent(ev)
		if sig := s.heavyPre(ev); sig != nil {
			return &heavySignal{facts: sig.Facts}
		}
		return nil
	}
	send := func(event, id string, extra map[string]any) string {
		m := map[string]any{"hook_event_name": event, "session_id": s.ID, "cwd": s.Root, "tool_name": "bash", "tool_use_id": id,
			"tool_input": map[string]any{"command": "npm test", "run_in_background": true}}
		for k, v := range extra {
			m[k] = v
		}
		raw, _ := json.Marshal(m)
		out, _, err := d.handle(Request{Agent: "mcode", Event: event, Payload: raw})
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	send("PreToolUse", "c1", nil)
	send("PostToolUse", "c1", map[string]any{"tool_response": map[string]any{"content": []any{map[string]any{"type": "text", "text": `<bash_background task_id="bg_1">`}}}})
	// mcode answers a pre-tool advice nowhere, so the advice is recorded and
	// handed over after the next result; the registry is what is checked here
	if sig := heavyNext("c2"); sig == nil || sig.facts["other"] != "이 세션의 백그라운드 실행" {
		t.Fatalf("a heavy run beside the session's own background run is advised: %+v", sig)
	}
	send("PostToolUse", "t1", map[string]any{"tool_name": "task_output", "tool_input": map[string]any{"task_id": "bg_1"},
		"tool_response": map[string]any{"details": map[string]any{"task_id": "bg_1", "status": "succeeded",
			"execution": map[string]any{"status": "succeeded", "reason": "exited", "exitCode": 0}}}})
	if sig := heavyNext("c3"); sig != nil {
		t.Fatalf("the result released the background run: %+v", sig.facts)
	}
}

type heavySignal struct{ facts map[string]any }

func TestHeavyBackgroundRunWithoutJoinIsReleased(t *testing.T) {
	root := t.TempDir()
	d, ss := heavyDaemon(t, root)
	s := ss[0]
	bg := map[string]any{"tool_input": map[string]any{"command": "npm test", "run_in_background": true}}
	heavyCall(t, d, s, "PreToolUse", "c1", "npm test", bg)
	heavyCall(t, d, s, "PostToolUse", "c1", "npm test", map[string]any{"tool_input": bg["tool_input"],
		"tool_response": map[string]any{"stdout": "", "backgroundTaskId": "bash_1"}})
	if out := heavyCall(t, d, s, "PreToolUse", "c2", "npm test", nil); strings.Contains(out, "무거운 시험") {
		t.Fatalf("Claude Code background results are not joined; nothing would release the run: %s", out)
	}
}

func TestHeavyRegistryForgetsRunsWithoutResult(t *testing.T) {
	var h heavyRegistry
	h.releaseSession("k", "a") // a session that never held a run ends
	h.release("k", "a", "x", false)
	old := &heavyRun{session: "a", callID: "x", cmd: "npm test", started: time.Now().Add(-31 * time.Minute)}
	h.hold("k", old)
	if h.conflict("k", "b", time.Now()) != nil {
		t.Fatal("a run past its hold judges nothing")
	}
	long := &heavyRun{session: "a", callID: "y", cmd: "cargo test", started: time.Now().Add(-40 * time.Minute), expect: 25 * time.Minute}
	h.hold("k", long)
	if c := h.conflict("k", "b", time.Now()); c == nil || c.callID != "y" {
		t.Fatalf("a run expected to take 25 minutes holds for 50: %+v", c)
	}
	h.release("k", "a", "y", false)
	if h.conflict("k", "b", time.Now()) != nil {
		t.Fatal("released")
	}
	h.hold("k", &heavyRun{session: "a", callID: "z", started: time.Now()})
	h.releaseSession("k", "a")
	if h.conflict("k", "b", time.Now()) != nil {
		t.Fatal("an ended session holds nothing")
	}
}

// TestCheckpointWaitsForAnotherSessionsHeavyRun covers the daemon's own
// contract checks: they wait while another session runs a heavy test in the
// folder, and take a place in the registry while they run.
func TestCheckpointWaitsForAnotherSessionsHeavyRun(t *testing.T) {
	r := &Runner{Root: t.TempDir(), Timeout: time.Second, Contended: func() bool { return true }}
	res := r.Run([]contract.Check{{ID: "c1", Check: "true"}}, false, nil)
	if !strings.Contains(res[0].Skipped, "다른 세션") {
		t.Fatalf("skipped=%q", res[0].Skipped)
	}
	var held, released int
	r = &Runner{Root: t.TempDir(), Timeout: time.Second, Hold: func(string) func() { held++; return func() { released++ } }}
	res = r.Run([]contract.Check{{ID: "c1", Check: "true"}}, false, nil)
	if !res[0].Pass || held != 1 || released != 1 {
		t.Fatalf("pass=%v held=%d released=%d", res[0].Pass, held, released)
	}
}

// TestMigrationHistoryWriteIsAdvisedBeforeItRuns goes through the daemon:
// the Vellum rollback statement reaches the agent before it runs.
func TestMigrationHistoryWriteIsAdvisedBeforeItRuns(t *testing.T) {
	d, ss := heavyDaemon(t, t.TempDir())
	cmd := "set -a && source .env && set +a && psql \"${DATABASE_URL%%\\?*}\" <<'SQL'\nDELETE FROM vellum._sqlx_migrations WHERE version = 22;\nSQL"
	out := heavyCall(t, d, ss[0], "PreToolUse", "m1", cmd, nil)
	if !strings.Contains(out, "마이그레이션 이력 테이블") {
		t.Fatalf("advice before the run: %s", out)
	}
}
