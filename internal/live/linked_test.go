package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func awaitLinked(t *testing.T, s *Session) {
	t.Helper()
	s.mu.Lock()
	done := s.linkedDone
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("linked worker failed to close")
		}
	}
}
func linkedSessions(t *testing.T, recommend bool) (*Session, *Session) {
	t.Helper()
	return linkedSessionsChecks(t, recommend, "")
}
func linkedSessionsChecks(t *testing.T, recommend bool, extra string) (*Session, *Session) {
	t.Helper()
	s, d := reliabilitySession(t)
	if err := os.MkdirAll(contract.Dir(s.Root), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "input"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := `spec: progress-contract/2
goal: task-bound evidence
done:
  - id: check
    check: test "$(cat input)" = pass
    pure: true
    reuse:
      inputs: [input]
      environment_files: [input]
      deterministic: true
      max_age_sec: 60
`
	if err := os.WriteFile(contract.Path(s.Root), []byte(raw+extra), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.command(CommandInput{Name: "accept", Session: s.ID}); err != nil {
		t.Fatal(err)
	}
	link := intent.SessionLink{TaskID: s.task.ID, Agent: "codex", SessionID: "linked-codex", ParentAgent: s.Agent, ParentSessionID: s.ID}
	if err := s.db.LinkTaskSession(link); err != nil {
		t.Fatal(err)
	}
	if recommend {
		if err := os.WriteFile(filepath.Join(filepath.Dir(s.ledgerPath), "config.yml"), []byte("rollout: {recommend_rules: [s1.linked_loop]}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	child, err := newSession(link.SessionID, link.Agent, s.Root, "", s.db, s.prices, adapter.Profiles["codex"].Caps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Finalize() })
	return s, child
}
func TestLinkedRuntimeFindsActualChecksAcrossAgentsWithOptInAndDedup(t *testing.T) {
	for _, recommend := range []bool{false, true} {
		t.Run(map[bool]string{false: "shadow", true: "recommend"}[recommend], func(t *testing.T) {
			s, child := linkedSessions(t, recommend)
			for i := 0; i < 2; i++ {
				s.mu.Lock()
				s.linkedAt = time.Time{}
				s.mu.Unlock()
				res := s.checkpoint(false)
				if len(res) != 1 || res[0].Evidence == nil || res[0].Pass {
					t.Fatalf("real failing proof missing: %+v", res)
				}
				awaitLinked(t, s)
			}
			res := child.checkpoint(false)
			if len(res) != 1 || res[0].Evidence == nil {
				t.Fatalf("child evidence %+v", res)
			}
			awaitLinked(t, child)
			child.mu.Lock()
			count := 0
			for _, v := range child.eng.Verdicts {
				if v.Rule == "s1.linked_loop" {
					count++
					if v.Level != 1 || !v.Estimate || len(v.Evidence) != 1 {
						child.mu.Unlock()
						t.Fatal(v)
					}
				}
			}
			if count != 1 || child.linkedSummary == nil || len(child.linkedSummary.Cycles) != 1 || child.unresolved != nil {

				summary, unavailable := child.linkedSummary, child.linkedUnavailable
				child.mu.Unlock()
				t.Fatalf("runtime linked result count=%d summary=%+v unavailable=%v", count, summary, unavailable)
			}
			messages := strings.Join(child.pending, "\n")
			if recommend && !strings.Contains(messages, "세션") {
				child.mu.Unlock()
				t.Fatal("opted-in linked advice not queued")
			}
			if !recommend && messages != "" {
				child.mu.Unlock()
				t.Fatal("shadow advice injected")
			}
			child.linkedAt = time.Time{}
			child.mu.Unlock()
			child.checkpoint(false)
			awaitLinked(t, child)
			child.mu.Lock()
			defer child.mu.Unlock()
			again := 0
			for _, v := range child.eng.Verdicts {
				if v.Rule == "s1.linked_loop" {
					again++
				}
			}
			if again != 1 {
				t.Fatal("same linked finding repeatedly emitted")
			}
		})
	}
}
func TestLinkedSnapshotCancellationAndFinalization(t *testing.T) {
	s, child := linkedSessions(t, false)
	s.checkpoint(false)
	awaitLinked(t, s)
	child.checkpoint(false)
	if _, err := child.Finalize(); err != nil {
		t.Fatal(err)
	}
	child.mu.Lock()
	closed, busy := child.closed, child.linkedBusy
	child.mu.Unlock()
	if !closed || busy {
		t.Fatal("background reader outlived session")
	}
}

func TestLinkedFindingIsNotReissuedAfterSessionRestart(t *testing.T) {
	s, child := linkedSessions(t, false)
	for i := 0; i < 2; i++ {
		s.mu.Lock()
		s.linkedAt = time.Time{}
		s.mu.Unlock()
		s.checkpoint(false)
		awaitLinked(t, s)
	}
	child.checkpoint(false)
	awaitLinked(t, child)
	child.mu.Lock()
	seen := len(child.linkedSeen)
	child.mu.Unlock()
	if seen != 1 {
		t.Fatalf("expected durable linked finding, got %d", seen)
	}
	if _, err := child.Finalize(); err != nil {
		t.Fatal(err)
	}
	restarted, err := newSession(child.ID, child.Agent, child.Root, "", child.db, child.prices, adapter.Profiles[child.Agent].Caps)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Finalize()
	restarted.checkpoint(false)
	awaitLinked(t, restarted)
	restarted.mu.Lock()
	defer restarted.mu.Unlock()
	count := 0
	for _, v := range restarted.eng.Verdicts {
		if v.Rule == "s1.linked_loop" {
			count++
		}
	}
	if count != 1 || len(restarted.linkedSeen) != 1 {
		t.Fatalf("restart duplicated finding: count=%d seen=%d", count, len(restarted.linkedSeen))
	}
}

func TestLinkedWorkerRetainsMultipleCheckArrivalsWithinThrottle(t *testing.T) {
	extra := `  - id: second
    check: test "$(cat input)" = good
    pure: true
    reuse:
      inputs: [input]
      environment_files: [input]
      deterministic: true
      max_age_sec: 60
`
	s, child := linkedSessionsChecks(t, true, extra)
	for i := 0; i < 2; i++ {
		if results := s.checkpoint(false); len(results) != 2 {
			t.Fatal(results)
		}
	}
	awaitLinked(t, s)
	if results := child.checkpoint(false); len(results) != 2 {
		t.Fatal(results)
	}
	awaitLinked(t, child)
	// A completion and a new arrival can cross the worker's final unlock.
	awaitLinked(t, child)
	child.mu.Lock()
	var count, delivered int
	for _, v := range child.eng.Verdicts {
		if v.Rule == "s1.linked_loop" {
			count++
			if v.Primary && !v.Suppressed {
				delivered++
			}
		}
	}
	pending := len(child.pending)
	recoveries := append([]recovery.Attempt(nil), child.recoveries...)
	verdicts := append([]detect.Signal(nil), child.eng.Verdicts...)
	child.mu.Unlock()
	if count != 2 || delivered != 2 || pending != 1 {
		t.Fatalf("lost distinct target: count=%d primary=%d pending=%d recoveries=%+v verdicts=%+v", count, delivered, pending, recoveries, verdicts)
	}
}
