package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
)

func (h *harness) declareTestRules(rules ...string) {
	h.t.Helper()
	body := "rollout:\n  mode: validated\n  validated_rules:\n"
	for _, rule := range rules {
		body += "    " + rule + ": '" + strings.Repeat("a", 64) + "'\n"
	}
	if err := os.WriteFile(filepath.Join(h.home, "config.yml"), []byte(body), 0600); err != nil {
		h.t.Fatal(err)
	}
}

func TestShadowRecordsButDoesNotDeliver(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Cfg.Rollout.Mode = "shadow"
	s.deliver([]detect.Signal{{ID: "shadow-verdict", Seq: 1, Rule: "s2.stuck_error", Detector: "S2", Level: detect.L3, Primary: true, Facts: map[string]any{}}})
	if len(s.pending) != 0 || len(s.userMsg) != 0 || len(s.recoveries) != 0 || s.unresolved != nil {
		t.Fatal("shadow emitted an intervention")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM verdict WHERE session_id=?`, s.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("missing shadow observation %d %v", count, err)
	}
}

func TestExplicitProtectionSurvivesShadowMode(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	s.mu.Lock()
	s.Cfg.Rollout.Mode = "shadow"
	s.mu.Unlock()
	out := s.onPreTool(HookInput{ToolUseID: "protected", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"protected/file","content":"x"}`)})
	if !strings.Contains(string(out), `"deny"`) {
		t.Fatalf("explicit protection was disabled: %s", out)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev := s.byTool["protected"]; ev == nil || ev.ExitCode == nil || *ev.ExitCode != -1 {
		t.Fatal("explicit guardrail not recorded")
	}
}

func TestShadowStopDoesNotExecuteChecks(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "touch shadow-unwanted")
	s.mu.Lock()
	s.Cfg.Rollout.Mode = "shadow"
	s.mu.Unlock()
	out := s.onStop(HookInput{LastMessage: "done"})
	if strings.Contains(string(out), `"block"`) {
		t.Fatalf("shadow blocked: %s", out)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "shadow-unwanted")); !os.IsNotExist(err) {
		t.Fatal("shadow launched an automatic check")
	}
}
