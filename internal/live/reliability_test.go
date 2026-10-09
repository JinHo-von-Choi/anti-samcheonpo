package live

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/codex"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

func reliabilitySession(t *testing.T) (*Session, *Daemon) {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	if err := os.WriteFile(filepath.Join(root, ".samcheonpo.yml"), []byte("notify: {desktop: false}\niron_laws: {enabled: false}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := ledger.Open(filepath.Join(home, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	prices, err := cost.Load("")
	if err != nil {
		t.Fatal(err)
	}
	s, err := newSession("reliability", "claude", root, "", db, prices, adapter.Profiles["claude"].Caps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.Finalize(); db.Close() })
	return s, &Daemon{sessions: map[string]*Session{s.ID: s}, db: db, prices: prices, versions: map[string]string{"claude": "2.1.0"}}
}

func acceptCheck(t *testing.T, s *Session, d *Daemon, command string) {
	t.Helper()
	if err := os.MkdirAll(contract.Dir(s.Root), 0700); err != nil {
		t.Fatal(err)
	}
	raw := "goal: scoped work\ndone:\n  - id: check\n    check: '" + command + "'\nscope:\n  allow: [src/**]\n  protect: [protected/**]\n"
	if err := os.WriteFile(contract.Path(s.Root), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.command(CommandInput{Name: "accept", Session: s.ID}); err != nil {
		t.Fatal(err)
	}
}

func TestContractSkipClearsEngine(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	s.checkpoint(false)
	if _, err := d.command(CommandInput{Name: "skip", Session: s.ID}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng.Accepted || s.eng.Contract != nil {
		t.Error("skipped contract still controls detectors")
	}
	if len(s.checks) != 0 {
		t.Error("skipped contract retained completion evidence")
	}
}

func TestContractReloadAndReacceptInvalidateEvidence(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	s.checkpoint(false)
	if !strings.Contains(s.Statusline(), "진척 1/1") {
		t.Fatal("initial check did not pass")
	}
	raw, err := os.ReadFile(contract.Path(s.Root))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contract.Path(s.Root), []byte(strings.Replace(string(raw), "'true'", "'false'", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.loadContract()
	if s.acc.State != contract.StateStale || s.eng.Accepted || s.eng.Contract != nil {
		t.Error("stale contract still accepted by engine")
	}
	s.mu.Unlock()
	if _, err := d.command(CommandInput{Name: "accept", Session: s.ID}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.Statusline(), "진척 1/1") {
		t.Error("new command reused previous pass with the same criterion ID")
	}
}

func TestLocalJudgeAddress(t *testing.T) {
	for _, u := range []string{"http://localhost:1234/v1", "http://127.0.0.1/v1", "http://[::1]:1234/v1"} {
		if !isLocal(u) {
			t.Errorf("local endpoint rejected: %s", u)
		}
	}
	for _, u := range []string{"https://localhost.example.com", "https://127.0.0.1.example.com", "https://localhost@remote.example", "https://remote.example/path/://localhost", "file://localhost/tmp", ""} {
		if isLocal(u) {
			t.Errorf("non-local endpoint accepted: %s", u)
		}
	}
}

func TestLedgerFailureIsVisibleAndFinalizeKeepsError(t *testing.T) {
	s, _ := reliabilitySession(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	ev := &event.Event{Kind: event.KindMessage, TS: time.Now(), Priced: true}
	s.parser.AddEvent(ev)
	s.observe(ev)
	s.mu.Unlock()
	if !strings.Contains(s.Statusline(), "저장") {
		t.Error("ledger failure not visible in status")
	}
	if _, err := s.Finalize(); err == nil {
		t.Error("finalize succeeded with a closed database")
	}
	if _, err := s.Finalize(); err == nil {
		t.Error("repeated finalize hid the persistence failure")
	}
}

func TestCheckpointDiscardsResultAfterContractChange(t *testing.T) {
	s, d := reliabilitySession(t)
	barrier := t.TempDir()
	started, release := filepath.Join(barrier, "started"), filepath.Join(barrier, "release")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0600) })
	acceptCheck(t, s, d, "touch "+filepath.ToSlash(started)+"; while test ! -e "+filepath.ToSlash(release)+"; do sleep 0.01; done")
	done := make(chan []CheckResult, 1)
	go func() { done <- s.checkpoint(false) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("checkpoint did not start")
		}
		time.Sleep(time.Millisecond)
	}
	acceptCheck(t, s, d, "false")
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-done:
		if len(res) != 1 || res[0].Pass || res[0].Skipped == "" {
			t.Fatalf("old result applied: %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("checkpoint did not finish")
	}
	if strings.Contains(s.Statusline(), "진척 1/1") {
		t.Fatal("old result completed the new contract")
	}
}

func TestFinalizeDrainsQueuedEvents(t *testing.T) {
	s, _ := reliabilitySession(t)
	const count = 100
	for i := 0; i < count; i++ {
		s.mu.Lock()
		ev := &event.Event{Kind: event.KindMessage, TS: time.Now(), Priced: true}
		s.parser.AddEvent(ev)
		s.mu.Unlock()
		s.enqueue(ev, 0)
	}
	if _, err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	var got int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM event WHERE session_id=?", s.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != count || len(s.eng.St.Events) != count {
		t.Fatalf("pending events lost: stored=%d observed=%d", got, len(s.eng.St.Events))
	}
	// Late enqueue and repeated termination must never send on a closed channel.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.enqueue(&event.Event{Kind: event.KindMessage}, 0); _, _ = s.Finalize() }()
	}
	wg.Wait()
}

func TestFinalizeCanRetryReceiptWrite(t *testing.T) {
	s, _ := reliabilitySession(t)
	blocker := filepath.Join(config.Home(), "receipts")
	if err := os.WriteFile(blocker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finalize(); err == nil {
		t.Fatal("receipt failure was hidden")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	p, err := s.Finalize()
	if err != nil || p == "" {
		t.Fatalf("receipt retry failed: %s %v", p, err)
	}
}

func TestFinalizeWithConcurrentHooks(t *testing.T) {
	s, d := reliabilitySession(t)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, _ = d.dispatch("claude", "UserPromptSubmit", HookInput{SessionID: s.ID, Cwd: s.Root, Prompt: "분석해줘"})
		}()
	}
	close(start)
	if _, err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	var got int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM event WHERE session_id=?", s.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if got != len(s.parser.Session.Events) {
		t.Fatalf("accepted hooks not sealed: stored=%d parsed=%d", got, len(s.parser.Session.Events))
	}
}

func TestFinalizeAttributesUsageAfterQueueDrain(t *testing.T) {
	s, _ := reliabilitySession(t)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	s.cx = codex.NewTracker(path)
	s.cx.Model = "gpt-5"
	s.ws.computeMu.Lock()
	var unlock sync.Once
	release := func() { unlock.Do(s.ws.computeMu.Unlock) }
	t.Cleanup(release)
	s.ws.Invalidate()
	ev := &event.Event{Kind: event.KindTool, TS: time.Now(), Tool: event.ToolShell, Cmd: "true"}
	s.mu.Lock()
	s.parser.AddEvent(ev)
	s.mu.Unlock()
	s.enqueue(ev, 0)
	if err := os.WriteFile(path, []byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":90,"output_tokens":10,"total_tokens":100}}}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Finalize(); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finalization did not start")
		}
		time.Sleep(time.Millisecond)
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("finalization did not finish")
	}
	if ev.Usage.Total() != 100 {
		t.Fatalf("final usage missed queued tool: %+v", ev.Usage)
	}
}

func TestEndedSessionRequiresExplicitRestart(t *testing.T) {
	s, d := reliabilitySession(t)
	if _, err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	d.drop(s.ID)
	in := HookInput{SessionID: s.ID, Cwd: s.Root}
	if _, _, err := d.dispatch("claude", "PostToolUse", in); err == nil {
		t.Fatal("late tool hook recreated a closed session")
	}
	_, resumed, err := d.dispatch("claude", "SessionStart", in)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = resumed.Finalize() })
	if resumed == s {
		t.Fatal("explicit restart reused closed session state")
	}
}

func TestStatuslineStateNeedsEvidenceAndAdmitsUnknown(t *testing.T) {
	s, d := reliabilitySession(t)
	if line := s.Statusline(); !strings.HasPrefix(line, "[지켜보는 중]") {
		t.Fatalf("no warning and no progress evidence is not shown as healthy: %q", line)
	}
	acceptCheck(t, s, d, "true")
	s.checkpoint(false)
	if line := s.Statusline(); !strings.HasPrefix(line, "[순조로움]") {
		t.Fatalf("a passing accepted check is progress evidence: %q", line)
	}
	s.mu.Lock()
	s.lastPrimary = &detect.Signal{Rule: "s1.identical_rerun", Level: detect.L1, Seq: s.eng.St.LastProgress + 1}
	s.mu.Unlock()
	if line := s.Statusline(); !strings.HasPrefix(line, "[지켜보는 중]") {
		t.Fatalf("advice after the last progress: %q", line)
	}
	s.mu.Lock()
	s.lastPrimary = &detect.Signal{Rule: "s2.stuck_error", Level: detect.L2, Seq: s.eng.St.LastProgress + 2}
	s.mu.Unlock()
	if line := s.Statusline(); !strings.HasPrefix(line, "[지금 끼어드세요]") {
		t.Fatalf("an escalated finding asks the user to step in: %q", line)
	}
	s.queueRejected.Add(1)
	if line := s.Statusline(); !strings.HasPrefix(line, "[확인 불가]") {
		t.Fatalf("incomplete observation is shown as unknown: %q", line)
	}
}
