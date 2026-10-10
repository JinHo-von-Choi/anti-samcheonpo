package live

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

func agenttimeAcceptedSession(t *testing.T) *Session {
	t.Helper()
	s, d := reliabilitySession(t)
	if err := os.MkdirAll(contract.Dir(s.Root), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "input"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := `spec: progress-contract/2
goal: finish input
temporal_requirement: {kind: completion}
done:
 - id: ready
   check: test -s input
   pure: true
   reuse:
     inputs: [input]
     environment_files: [input]
     deterministic: true
     max_age_sec: 60
`
	if err := os.WriteFile(contract.Path(s.Root), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.command(CommandInput{Name: "accept", Session: s.ID}); err != nil {
		t.Fatal(err)
	}
	res := s.checkpoint(false)
	if len(res) != 1 || !res[0].Pass || res[0].Evidence == nil {
		t.Fatal(res)
	}
	return s
}
func observedWait(s *Session, n int) *event.Event {
	x := 0
	return &event.Event{Kind: event.KindTool, Tool: event.ToolShell, CallID: fmt.Sprintf("wait-%d", n), Cmd: "sleep 1", CmdNorm: "sleep 1", ExitCode: &x, TS: time.Now(), Category: event.CatExplore, Reliability: s.reliabilityBoundary()}
}
func TestAgentTimeLiveShadowAndInputInvalidation(t *testing.T) {
	s := agenttimeAcceptedSession(t)
	if s.ws.WatchUnavailable() {
		probe := NewWorkspace(s.Root)
		defer probe.Close()
		err := probe.Watch(4000)
		if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EMFILE) {
			t.Skipf("host inotify resources unavailable; deferral covered separately: %v", err)
		}
		t.Fatalf("session observation unavailable (probe: %v)", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for n := 1; n <= 3; n++ {
		ev := observedWait(s, n)
		s.parser.AddEvent(ev)
		s.observe(ev)
		if !ev.Reliability.CriteriaMet || ev.Reliability.Observation != "none" {
			t.Fatal(ev.Reliability)
		}
	}
	found := false
	for _, v := range s.eng.Verdicts {
		if v.Rule == "s1.explicit_waiting" && v.Level == detect.L1 {
			found = true
		}
	}
	if !found || s.lastPrimary != nil || len(s.eng.St.Estimated) > 0 {
		t.Fatal("shadow leaked or costs changed", found, s.lastPrimary)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "input"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ev := observedWait(s, 4)
	s.parser.AddEvent(ev)
	s.observe(ev)
	if ev.Reliability.CriteriaMet {
		t.Fatal("external input change retained completion")
	}
	before := s.eng.St.TotalMicro
	if err := s.recordIntent(intent.Change{Kind: intent.Followup, Goal: "새 조건"}, intent.Source{Origin: intent.User, SessionID: s.ID, EventID: "new", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ev = observedWait(s, 5)
	s.parser.AddEvent(ev)
	s.observe(ev)
	if ev.Reliability.CriteriaMet || s.eng.St.TotalMicro != before {
		t.Fatal("revision retained completion or reset costs")
	}
}
func TestAgentTimeTemporalConflictsAndRestartSegment(t *testing.T) {
	s := agenttimeAcceptedSession(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c.TemporalRequirement = &contract.TemporalRequirement{Kind: "minimum_duration", Seconds: 2}
	now := time.Now()
	s.temporalStart = now
	if s.observationObligation(now) != "pending" || s.observationObligation(now.Add(3*time.Second)) != "fulfilled" {
		t.Fatal("harness duration")
	}
	s.temporalStart = time.Time{}
	if s.observationObligation(now) != "unknown" {
		t.Fatal("restart invented duration")
	}
	s.temporalStart = now.Add(-3 * time.Second)
	s.Cfg.Detectors.S8.CeilingHours = 1.0 / 3600
	if s.observationObligation(now) != "unknown" || !s.goalCard("").TemporalConflict {
		t.Fatal("ceiling conflict absent")
	}
	s.c.TemporalRequirement = &contract.TemporalRequirement{Kind: "observe_until", CheckID: "ready"}
	s.Cfg.Detectors.S8.CeilingHours = 0
	if s.observationObligation(now) != "fulfilled" {
		t.Fatal("independent checkpoint not used")
	}
}
func TestAgentTimeLateResultCannotCrossRevision(t *testing.T) {
	s := agenttimeAcceptedSession(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := observedWait(s, 1)
	if err := s.recordIntent(intent.Change{Kind: intent.Followup, Goal: "다른 조건"}, intent.Source{Origin: intent.User, SessionID: s.ID, EventID: "later", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.parser.AddEvent(ev)
	s.observe(ev)
	if ev.Reliability.Complete || ev.Reliability.CriteriaMet || ev.Reliability.Observation != "unknown" {
		t.Fatal("late result rebound", ev.Reliability)
	}
}

func TestAgentTimeTemporalChangeRevokesAcceptanceAndRouteCannotBlock(t *testing.T) {
	s := agenttimeAcceptedSession(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Cfg.Rollout.ShadowRules = nil
	s.Cfg.Rollout.Mode = "validated"
	s.Cfg.Rollout.ValidatedRules = map[string]string{"s1.explicit_waiting": fmt.Sprintf("%064x", 1), "s8.progress_stall": fmt.Sprintf("%064x", 2)}
	for _, rule := range []string{"s1.explicit_waiting", "s8.progress_stall"} {
		v := detect.Signal{Rule: rule, Level: detect.L1, Facts: map[string]any{"kind": "same"}}
		s.advised[s.adviceKey(v)] = true
		if route, _ := s.routeFor(v); route != "advice" {
			t.Fatal("actual route", route)
		}
	}
	raw, err := os.ReadFile(contract.Path(s.Root))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n# changed temporal authority\n")...)
	// Change a declared field, rather than merely relying on the file hash.
	c, errs := contract.Parse(raw)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	c.TemporalRequirement = &contract.TemporalRequirement{Kind: "minimum_duration", Seconds: 1}
	old := contract.AuthorityDigest(s.c)
	if old == contract.AuthorityDigest(c) {
		t.Fatal("unchanged authority")
	}
	s.setContract(c, contract.Acceptance{State: contract.StateStale})
	if s.eng.Accepted || len(s.checks) != 0 || s.observationObligation(time.Now()) == "none" {
		t.Fatal("changed temporal intent retained acceptance")
	}
}

func TestAgentTimeUnavailableWatchDefersWithoutQueueGap(t *testing.T) {
	s := agenttimeAcceptedSession(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ws.markWatchFailure()
	before := s.queueRejected.Load()
	for n := 1; n <= 4; n++ {
		ev := observedWait(s, n)
		s.parser.AddEvent(ev)
		s.observe(ev)
		if ev.Reliability.Complete {
			t.Fatal("unavailable watcher asserted complete observation")
		}
	}
	if s.queueRejected.Load() != before {
		t.Fatal("filesystem failure invented a tool queue rejection")
	}
	for _, v := range s.eng.Verdicts {
		if v.Rule == "s1.explicit_waiting" && v.Level > detect.L0 {
			t.Fatal("incomplete observation warned", v)
		}
	}
}
func TestAgentTimeNewGoalDoesNotInheritCompletionAuthority(t *testing.T) {
	s := agenttimeAcceptedSession(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.recordIntent(intent.Change{Kind: intent.Redirect, Goal: "different goal"}, intent.Source{Origin: intent.User, SessionID: s.ID, EventID: "redirect", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if s.reliabilityBoundary().AuthorityHash != "" {
		t.Fatal("old contract authority attached to replacement goal")
	}
}
func TestWatchMissingRootIsNotReportedAsStarted(t *testing.T) {
	w := NewWorkspace(filepath.Join(t.TempDir(), "missing"))
	defer w.Close()
	if err := w.Watch(100); err == nil {
		t.Fatal("missing root reported an active watcher")
	}
	if !w.WatchUnavailable() {
		t.Fatal("failed filesystem observation not exposed")
	}
}

func TestWatchCapacityDefersReliability(t *testing.T) {
	w := NewWorkspace(t.TempDir())
	defer w.Close()
	// No directory registrations are permitted; this must never assert
	// complete observation even when creating the watcher itself succeeds.
	_ = w.Watch(0)
	if !w.WatchUnavailable() {
		t.Fatal("exhausted watch capacity reported complete observation")
	}
}
