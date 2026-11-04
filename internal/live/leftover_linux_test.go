//go:build linux

package live

import (
	"os/exec"
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
