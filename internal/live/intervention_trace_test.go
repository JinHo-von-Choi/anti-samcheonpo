package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
)

func TestDeferredInterventionPreservesBothReceiptTimes(t *testing.T) {
	s, _ := reliabilitySession(t)
	emitted := time.Now().UTC().Add(-time.Second)
	printed := emitted.Add(time.Millisecond)
	v := detect.Signal{ID: "receipt-order", Rule: detect.RuleGraderAccess}
	s.mu.Lock()
	s.actionDelivery = map[string]actionDelivery{v.ID: {emittedAt: emitted, printedAt: printed}}
	s.recordBlock(v)
	s.mu.Unlock()
	traces, err := s.db.InterventionTraces(s.ID, v.ID)
	if err != nil || len(traces) != 1 {
		t.Fatalf("traces %v: %v", traces, err)
	}
	if traces[0].EmittedAt != emitted.Format(time.RFC3339Nano) || traces[0].DeliveredAt != printed.Format(time.RFC3339Nano) {
		t.Fatalf("receipt times collapsed: %+v", traces[0])
	}
}

func TestAcceptedCustomCheckOpensStopUntilInputChanges(t *testing.T) {
	s := agenttimeAcceptedSession(t)
	feedWrite(t, s, "custom-input", "input")
	if res := s.checkpoint(false); len(res) != 1 || !res[0].Pass {
		t.Fatal(res)
	}
	s.mu.Lock()
	verified := s.verified()
	s.mu.Unlock()
	if !verified {
		t.Fatal("fresh accepted custom check did not open stop gate")
	}
	if err := os.WriteFile(filepath.Join(s.Root, "input"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	verified = s.verified()
	s.mu.Unlock()
	if verified {
		t.Fatal("changed input retained custom-check authority")
	}
}

func TestPreDenyTraceRequiresPrintedReceipt(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.Cfg.ProtectedPaths = []string{filepath.Join(s.Root, "grader")}
	s.eng.Cfg.ProtectedPaths = s.Cfg.ProtectedPaths
	s.onPrompt(HookInput{Prompt: "자료 읽어 줘"})
	input, _ := json.Marshal(map[string]string{"file_path": filepath.Join(s.Root, "grader", "answer.txt")})
	a := &ackSlot{}
	out := s.onPreTool(HookInput{ToolUseID: "trace-read", ToolName: "Read", ToolInput: input, Ack: a})
	if !strings.Contains(string(out), `"permissionDecision":"deny"`) {
		t.Fatalf("not denied: %s", out)
	}
	// Exercise receipt-before-deferred-insert as well as ordinary insert order.
	a.emitted()
	a.confirm()
	s.preWrites.Wait()
	traces, err := s.db.InterventionTraces(s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range traces {
		if r.Rule != detect.RuleGraderAccess {
			continue
		}
		found = true
		if r.ID == "" || r.Metadata.Phase != "before_tool" || r.Metadata.EffectiveAction != "pre_deny" || r.Delivery != "delivered" || r.HostReceived != "unknown" || r.DeliveredAt == "" {
			t.Fatalf("trace: %+v", r)
		}
	}
	if !found {
		t.Fatal("missing trace")
	}
}

func TestStopTraceAndUnrelatedPass(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "로그인 기능 구현해"})
	feedWrite(t, s, "w", "auth/login.go")
	feedCheck(t, s, "unrelated", "go test ./unrelated", 0)
	a := &ackSlot{}
	out := s.onStop(HookInput{LastMessage: "구현했습니다. 테스트는 미확인입니다.", Ack: a})
	if !strings.Contains(string(out), `"decision":"block"`) {
		t.Fatalf("unrelated check opened gate: %s", out)
	}
	traces, err := s.db.InterventionTraces(s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range traces {
		if r.Blocked {
			found = true
			if r.Metadata.EffectiveAction != "stop_hold" || r.Delivery != "proposed" {
				t.Fatalf("trace %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("no stop hold")
	}
	feedCheck(t, s, "related", "go test ./auth", 0)
	if out := s.onStop(HookInput{LastMessage: "검증했습니다."}); strings.Contains(string(out), `"decision":"block"`) {
		t.Fatalf("related pass held: %s", out)
	}
}

func TestEstimatedHardPatternCannotEscalate(t *testing.T) {
	s, _ := reliabilitySession(t)
	v := detect.Signal{Rule: "s5.error_hiding", Estimate: true, Facts: map[string]any{"kind": "iron_laws:IL-301", "path": "coverage.go"}}
	s.advised[s.adviceKey(v)] = true
	if route, escalated := s.routeFor(v); route != "advice" || escalated {
		t.Fatalf("static candidate blocked: %s", route)
	}
	v.Estimate = false
	if route, _ := s.routeFor(v); route != "block" {
		t.Fatal("concrete protection was lost")
	}
}
