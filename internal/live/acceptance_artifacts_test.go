package live

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

func TestAcceptedArtifactIsRequiredAndInvalidatesReuse(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"input", "result.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("ok"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ws := NewWorkspace(root)
	defer ws.Close()
	r := Runner{Root: root, WS: ws, Timeout: time.Second}
	c := contract.Check{ID: "result", Check: "test -s input", Pure: true,
		Evidence: &contract.EvidenceSpec{Kind: "data", Artifacts: []string{"result.json"}},
		Reuse:    &contract.ReuseScope{Inputs: []string{"input"}, EnvironmentFiles: []string{"input"}, Deterministic: true, MaxAgeSec: 60}}
	run := func() CheckResult { return r.RunRevision([]contract.Check{c}, false, nil, "task", 1)[0] }
	first := run()
	if !first.Pass || first.Observation == nil || first.Observation.Kind != "data" || len(first.Observation.Artifacts) != 1 || first.Evidence == nil {
		t.Fatalf("%+v", first)
	}
	if next := run(); !next.Pass || !next.Reused {
		t.Fatalf("unchanged evidence: %+v", next)
	}
	if err := os.WriteFile(filepath.Join(root, "result.json"), []byte("new result"), 0600); err != nil {
		t.Fatal(err)
	}
	if next := run(); !next.Pass || next.Reused {
		t.Fatalf("artifact change retained old proof: %+v", next)
	}
	if err := os.Remove(filepath.Join(root, "result.json")); err != nil {
		t.Fatal(err)
	}
	if next := run(); next.Pass || next.Observation == nil || next.Observation.State != "missing_evidence" {
		t.Fatalf("exit 0 without artifact passed: %+v", next)
	}
}

func TestFreshTypedAcceptanceOpensStopWithoutReusableEvidence(t *testing.T) {
	s, d := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "화면과 API 구현해"})
	if err := os.MkdirAll(contract.Dir(s.Root), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "response.json"), []byte(`{"status":"ok"}`), 0600); err != nil {
		t.Fatal(err)
	}
	raw := "spec: progress-contract/2\ngoal: 화면과 API 구현해\ndone:\n - id: api\n   check: test -s response.json\n   pure: true\n   evidence: {kind: api, artifacts: [response.json]}\n"
	if err := os.WriteFile(contract.Path(s.Root), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.command(CommandInput{Name: "accept", Session: s.ID}); err != nil {
		t.Fatal(err)
	}
	feedWrite(t, s, "api-write", "api/main.go")
	out := s.onStop(HookInput{LastMessage: "수락된 API 검사와 결과를 확인했습니다."})
	if strings.Contains(string(out), `"decision":"block"`) {
		t.Fatalf("fresh typed check held: %s", out)
	}
	if s.checks["api"].Evidence != nil {
		t.Fatal("non-reusable check became reusable proof")
	}
	if err := os.Remove(filepath.Join(s.Root, "response.json")); err != nil {
		t.Fatal(err)
	}
	results := s.checkpoint(false)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.freshTypedAcceptance(results) || results[0].Pass {
		t.Fatal("missing API result released stop")
	}
}

func TestTypedCheckObservationPersistsWithoutReuseAuthority(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.mu.Lock()
	s.recordReliabilityCheck(CheckResult{ID: "visual", ExitCode: 0, Pass: true,
		Observation: &verification.Observation{Version: "observed-check/1", Kind: "ui", Source: "accepted_contract_check", State: "passed", CheckID: "visual", Artifacts: []verification.Artifact{{Path: "screen.png", Digest: "observed-digest"}}}})
	s.mu.Unlock()
	events, err := s.db.EventsOf(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Verification != nil && ev.Verification.Kind == "ui" {
			if ev.Reliability.Complete || ev.Verification.Independent {
				t.Fatal("observation invented reuse authority")
			}
			return
		}
	}
	t.Fatal("UI acceptance evidence not persisted")
}
