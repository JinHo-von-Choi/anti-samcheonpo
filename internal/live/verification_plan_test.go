package live

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestRepeatWarningLinksRealPassWithoutAssertingCurrentValidity(t *testing.T) {
	s, _ := reliabilitySession(t)
	if err := os.WriteFile(filepath.Join(s.Root, "input"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	c := contract.Check{ID: "check", Check: "test -s input", Pure: true, Reuse: &contract.ReuseScope{Inputs: []string{"input"}, EnvironmentFiles: []string{"input"}, Deterministic: true, MaxAgeSec: 60}}
	r := s.runner.RunRevision([]contract.Check{c}, false, nil, s.ID, 1)[0]
	if r.Evidence == nil || r.StopReason == "" {
		t.Fatalf("actual pass has no conclusion: %+v", r)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c = &contract.Contract{Done: []contract.Check{c, {ID: "visual", Manual: "look"}}}
	s.acc.State = contract.StateAccepted
	s.contractRevision = 1
	s.checks[c.ID] = r
	plan := s.verificationCandidates()
	if plan.StopMachineChecks || plan.ManualPending != 1 {
		t.Fatalf("%+v", plan)
	}
	v := detect.Signal{ID: "repeat-proof", Seq: 1, Rule: "s1.identical_rerun", Detector: "S1", Primary: true, Level: detect.L1, Facts: map[string]any{"count": 3, "cmd": c.Check}}
	s.deliver([]detect.Signal{v})
	message := strings.Join(s.pending, "\n")
	if !strings.Contains(message, r.Evidence.ID) || !strings.Contains(message, "미확인") || !strings.Contains(message, "수동 완료 조건") {
		t.Fatal(message)
	}
	if _, ok := v.Facts["verification_plan"]; ok {
		t.Fatal("engine facts mutated")
	}
	if err := os.WriteFile(filepath.Join(s.Root, "input"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if s.verificationCandidates().StopMachineChecks {
		t.Fatal("unobserved mutation was declared safe")
	}
	s.observe(&event.Event{Kind: event.KindTool, Tool: event.ToolWrite, Mutating: true})
	if hint := verificationHint(s.verificationCandidates()); hint != "" {
		t.Fatal("mutation retained proof", hint)
	}
	s.checks[c.ID] = r
	s.contractRevision++
	if hint := verificationHint(s.verificationCandidates()); hint != "" {
		t.Fatal("revision retained proof", hint)
	}
	s.contractRevision = 1
	r.Evidence.ExpiresAt = time.Now().Add(-time.Second)
	if hint := verificationHint(s.verificationCandidates()); hint != "" {
		t.Fatal("expired proof", hint)
	}
}

func TestCheckCommandKeepsManualOnlyContractPending(t *testing.T) {
	s, d := reliabilitySession(t)
	if err := os.MkdirAll(contract.Dir(s.Root), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contract.Path(s.Root), []byte("goal: visual review\ndone:\n  - id: visual\n    manual: inspect output\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.command(CommandInput{Name: "accept", Session: s.ID}); err != nil {
		t.Fatal(err)
	}
	text, err := d.command(CommandInput{Name: "check", Session: s.ID})
	if err != nil || !strings.Contains(text, "수동 완료 조건 1개") || strings.Contains(text, "수락된 계약이 없어") {
		t.Fatalf("%s %v", text, err)
	}
}
