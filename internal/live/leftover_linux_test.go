//go:build linux

package live

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sockpath"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLeftoverProcessIsToldAtStop: a server a test run started in the
// workspace is still running when the turn ends.
func TestLeftoverProcessIsToldAtStop(t *testing.T) {
	root := t.TempDir()
	d, ss := heavyDaemon(t, root)
	s := ss[0]
	heavyCall(t, d, s, "PreToolUse", "c1", "bash scripts/e2e.sh", nil)
	server := exec.Command("sleep", "60")
	server.Dir = root
	if err := server.Start(); err != nil {
		t.Skip(err)
	}
	defer func() { _ = server.Process.Kill(); _, _ = server.Process.Wait() }()
	time.Sleep(20 * time.Millisecond)
	heavyCall(t, d, s, "PostToolUse", "c1", "bash scripts/e2e.sh", passed("ok"))
	s.mu.Lock()
	s.leftoverAtStop(s.eng.St.Events[len(s.eng.St.Events)-1])
	first := strings.Join(s.userMsg, "\n")
	n := len(s.userMsg)
	s.leftoverAtStop(s.eng.St.Events[len(s.eng.St.Events)-1])
	again := len(s.userMsg)
	s.mu.Unlock()
	if !strings.Contains(first, "sleep 60") || !strings.Contains(first, "아직 실행 중") {
		t.Fatalf("advice: %q", first)
	}
	_, user := s.takePendingFor("Stop", nil)
	if !strings.Contains(user, "sleep 60") {
		t.Fatal("notice did not reach ordinary Stop output")
	}
	if again != n {
		t.Fatal("the same processes are told once")
	}
}

func TestLeftoverTellsEachProcessOnceAndSkipsTheHostsOwn(t *testing.T) {
	root := t.TempDir()
	d, ss := heavyDaemon(t, root)
	s := ss[0]
	start := func(arg string) *exec.Cmd {
		heavyCall(t, d, s, "PreToolUse", "c"+arg, "bash scripts/e2e.sh "+arg, nil)
		p := exec.Command("sleep", arg)
		p.Dir = root
		if err := p.Start(); err != nil {
			t.Skip(err)
		}
		t.Cleanup(func() { _ = p.Process.Kill(); _, _ = p.Process.Wait() })
		time.Sleep(20 * time.Millisecond)
		heavyCall(t, d, s, "PostToolUse", "c"+arg, "bash scripts/e2e.sh "+arg, passed("ok"))
		return p
	}
	stop := func() string {
		s.mu.Lock()
		defer s.mu.Unlock()
		before := len(s.userMsg)
		s.leftoverAtStop(s.eng.St.Events[len(s.eng.St.Events)-1])
		return strings.Join(s.userMsg[before:], "\n")
	}
	s.mu.Lock()
	cooldown := s.eng.Cfg.Levels.NudgeCooldownEvents
	s.mu.Unlock()
	start("61")
	s.mu.Lock()
	s.hostPID = os.Getpid()
	s.mu.Unlock()
	if got := stop(); got != "" {
		t.Fatalf("a child of the host: %q", got)
	}
	s.mu.Lock()
	s.hostPID = 0
	s.mu.Unlock()
	if got := stop(); !strings.Contains(got, "sleep 61") {
		t.Fatalf("first report: %q", got)
	}
	start("62")
	if got := stop(); got != "" {
		t.Fatalf("the cooldown holds the next report: %q", got)
	}
	for i := 0; i <= cooldown; i++ {
		heavyCall(t, d, s, "PreToolUse", fmt.Sprintf("r%d", i), "ls", nil)
		heavyCall(t, d, s, "PostToolUse", fmt.Sprintf("r%d", i), "ls", passed("ok"))
	}
	got := stop()
	if !strings.Contains(got, "sleep 62") || strings.Contains(got, "sleep 61") {
		t.Fatalf("a process held back by the cooldown is told after it, alone: %q", got)
	}
	if got := stop(); got != "" {
		t.Fatalf("nothing new: %q", got)
	}
}

func TestHookMissesBecomeObservationGaps(t *testing.T) {
	d, _ := heavyDaemon(t, t.TempDir())
	path := sockpath.MissLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lines := `{"agent":"claude","event":"Stop","session_id":"lost"}` + "\n" + `{"agent":"claude","event":"PreToolUse","session_id":"lost"}` + "\n" + `{"agent":"codex","event":"Stop","session_id":""}` + "\nnot json\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	ingestHookMisses(d.db)
	if n, err := d.db.ObservationGap("claude", "lost"); err != nil || n != 2 {
		t.Fatalf("gap: %d %v", n, err)
	}
	if n, err := d.db.ObservationGap("codex", unknownSession); err != nil || n != 1 {
		t.Fatalf("no session id: %d %v", n, err)
	}
	if n, err := d.db.UnobservedSessions(); err != nil || n != 2 {
		t.Fatalf("sessions known only from a gap: %d %v", n, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the record is consumed")
	}
	ingestHookMisses(d.db)
	if n, _ := d.db.ObservationGap("claude", "lost"); n != 2 {
		t.Fatalf("read twice: %d", n)
	}
}

func TestTrimPassIsAskedOncePerDocumentAndRequest(t *testing.T) {
	_, ss := heavyDaemon(t, t.TempDir())
	s := ss[0]
	s.mu.Lock()
	defer s.mu.Unlock()
	write := func(seq int64, path string) {
		s.eng.St.Events = append(s.eng.St.Events, &event.Event{Kind: event.KindTool, Tool: event.ToolWrite, Seq: seq, Paths: []string{path}})
	}
	write(1000, "docs/guide.md")
	s.queueTrimPass()
	if len(s.pending) != 0 {
		t.Fatalf("off by default: %q", s.pending)
	}
	s.Cfg.Trim.Enabled = true
	s.trimThrough = 0
	s.queueTrimPass()
	if len(s.pending) != 1 || !strings.Contains(s.pending[0], "docs/guide.md") {
		t.Fatalf("asked: %q", s.pending)
	}
	write(1001, "docs/guide.md")
	write(1002, "docs/plans/next.md")
	s.queueTrimPass()
	if len(s.pending) != 1 {
		t.Fatalf("the trimmed result and a plan note: %q", s.pending)
	}
}
