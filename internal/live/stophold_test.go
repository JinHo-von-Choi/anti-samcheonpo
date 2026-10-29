package live

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

func feedWrite(t *testing.T, s *Session, id, path string) {
	t.Helper()
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolWrite, Category: event.CatProduce, Paths: []string{path}, CallID: id, TS: time.Now(), Basis: "live"}
	s.mu.Lock()
	s.parser.AddEvent(ev)
	s.mu.Unlock()
	s.enqueue(ev, time.Second)
}

// feedCheck runs a check in the session and records its exit status.
func feedCheck(t *testing.T, s *Session, id, cmd string, exit int) {
	t.Helper()
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, Cmd: cmd, CmdNorm: cmd, ExitCode: &exit, CallID: id, TS: time.Now(), Basis: "live"}
	s.mu.Lock()
	s.parser.AddEvent(ev)
	s.mu.Unlock()
	s.enqueue(ev, time.Second)
}

// A turn that ends on an unverified change is held with the next step, and
// it stays held until a check passes. The hold is a gate, not a warning:
// neither the host asking again nor the same evidence coming back releases
// it, because the state the hold names is still true.
func TestStopHoldsUnverifiedTurnEndUntilACheckPasses(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "로그인 기능 만들어 줘"})
	feedWrite(t, s, "w1", "auth/login.go")
	last := "로그인 기능을 구현했습니다. 검증은 하지 못했습니다."

	out := string(s.onStop(HookInput{LastMessage: last}))
	if !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "마지막 코드 변경 뒤에 실행한 검사가 없다") || !strings.Contains(out, "가장 작은 검사") {
		t.Fatalf("stop not held with the next step: %s", out)
	}
	if out := string(s.onStop(HookInput{LastMessage: last, StopHookActive: true})); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("the host asking again released the gate: %s", out)
	}
	if out := string(s.onStop(HookInput{LastMessage: last})); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("the same evidence released the gate: %s", out)
	}
	// one intervention for the evidence, however often the agent asks to end
	var blocked int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM intervention WHERE session_id=? AND rule=? AND blocked=1`, s.ID, detect.RuleAbandonedVerification).Scan(&blocked); err != nil || blocked != 1 {
		t.Fatalf("hold not recorded once: %d %v", blocked, err)
	}
	// a check that fails keeps the gate shut
	feedCheck(t, s, "t1", "go test ./...", 1)
	if out := string(s.onStop(HookInput{LastMessage: last})); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("a failing check released the gate: %s", out)
	}
	// a check that passes opens it
	feedCheck(t, s, "t2", "go test ./...", 0)
	if out := string(s.onStop(HookInput{LastMessage: last, StopHookActive: true})); strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("held after the check passed: %s", out)
	}
}

// A false completion is the same gate seen from the claim: the turn ends
// while the check that failed is still failing.
func TestStopHoldsAFalseCompletionUntilACheckPasses(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "로그인 기능 만들어 줘"})
	feedWrite(t, s, "w1", "auth/login.go")
	feedCheck(t, s, "t1", "go test ./...", 1)
	claim := "모든 작업이 완료되었습니다."

	out := string(s.onStop(HookInput{LastMessage: claim}))
	if !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("a false completion was not held: %s", out)
	}
	if !strings.Contains(out, "go test ./...") {
		t.Fatalf("the hold does not name the failing check: %s", out)
	}
	if out := string(s.onStop(HookInput{LastMessage: claim, StopHookActive: true})); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("the host asking again released the gate: %s", out)
	}
	// a check that passes opens the gate
	feedCheck(t, s, "t2", "go test ./...", 0)
	if out := string(s.onStop(HookInput{LastMessage: claim})); strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("held after the check passed: %s", out)
	}
}

// Shadow mode records the verdict and never holds the turn.
func TestStopHoldRespectsShadow(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.mu.Lock()
	s.Cfg.Rollout.Mode = "shadow"
	s.mu.Unlock()
	s.onPrompt(HookInput{Prompt: "로그인 기능 만들어 줘"})
	feedWrite(t, s, "w1", "auth/login.go")
	if out := string(s.onStop(HookInput{LastMessage: "구현했습니다. 검증은 하지 못했습니다."})); strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("shadow held the turn: %s", out)
	}
}

// The effect of a hold is settled at the next turn end: the agent ran the
// check, the verdict did not come back, so the hold had no recurrence.
func TestStopHoldEffectSettlesAtNextTurnEnd(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "로그인 기능 만들어 줘"})
	feedWrite(t, s, "w1", "auth/login.go")
	out := s.onStop(HookInput{LastMessage: "구현했습니다. 테스트는 돌리지 않았습니다."})
	if !strings.Contains(string(out), `"decision":"block"`) {
		t.Fatalf("not held: %s", out)
	}
	s.markRecoveryOutput(out, recovery.Emitted)
	s.markRecoveryOutput(out, recovery.Delivered)
	exit := 0
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, Cmd: "go test ./...", CmdNorm: "go test ./...", ExitCode: &exit, CallID: "t1", TS: time.Now(), Basis: "live"}
	s.mu.Lock()
	s.parser.AddEvent(ev)
	s.mu.Unlock()
	s.enqueue(ev, time.Second)
	if out := string(s.onStop(HookInput{LastMessage: "go test가 통과합니다.", StopHookActive: true})); strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("held after the check passed: %s", out)
	}
	var outcome string
	if err := s.db.QueryRow(`SELECT COALESCE(outcome,'') FROM intervention WHERE session_id=? AND rule=? AND blocked=1`, s.ID, detect.RuleAbandonedVerification).Scan(&outcome); err != nil || outcome != "no_recurrence_observed" {
		t.Fatalf("outcome %q %v", outcome, err)
	}
}

// A host that continues a held stop by sending the hold reason as the next
// prompt (Copilot) does not turn samcheonpo's text into a user request.
func TestHoldReasonEchoedAsPromptIsNotARequest(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "로그인 기능 만들어 줘"})
	feedWrite(t, s, "w1", "auth/login.go")
	out := s.onStop(HookInput{LastMessage: "구현했습니다."})
	var held struct{ Reason string }
	if err := json.Unmarshal(out, &held); err != nil || held.Reason == "" {
		t.Fatalf("not held: %s", out)
	}
	s.onPrompt(HookInput{Prompt: held.Reason})
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.eng.Requests) != 0 || strings.Contains(s.msgContext().Goal, "[삼천포]") {
		t.Fatalf("hold reason became a request: %q", s.eng.Requests)
	}
	if s.intentRevision != nil && strings.Contains(s.intentRevision.Message, "[삼천포]") {
		t.Fatal("hold reason was recorded as an intent revision")
	}
}

func TestCopilotPromptInjectionOutput(t *testing.T) {
	out := string(copilotOut("userPromptSubmitted", nil, map[string]any{"additionalContext": "삼천포 안내"}))
	if !strings.Contains(out, `"additionalContext":"삼천포 안내"`) {
		t.Fatalf("prompt context not translated: %s", out)
	}
}

// A read of a protected path is denied in the answer itself, and the block
// record is stored after the answer.
func TestProtectedReadIsDeniedAndRecorded(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.mu.Lock()
	s.Cfg.ProtectedPaths = []string{filepath.Join(s.Root, "grader")}
	s.eng.Cfg.ProtectedPaths = s.Cfg.ProtectedPaths
	s.mu.Unlock()
	s.onPrompt(HookInput{Prompt: "grader 읽어 줘"})
	input, _ := json.Marshal(map[string]string{"file_path": filepath.Join(s.Root, "grader", "answer.txt")})
	out := string(s.onPreTool(HookInput{ToolUseID: "r1", ToolName: "Read", ToolInput: input}))
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("protected read not denied: %s", out)
	}
	s.preWrites.Wait()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM intervention WHERE session_id=? AND rule='s10.grader_access' AND blocked=1`, s.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("block not stored: %d %v", n, err)
	}
}

// A call refused by Claude Code's permission check gets no post-tool hook.
// Once a later call has answered, the next call closes it, so it does not
// look like a call still running next to every later wait.
func TestRefusedCallIsClosedAfterALaterResult(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "go test 돌려 줘"})
	s.onPreTool(HookInput{ToolUseID: "refused", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"go test ./... ; echo $?"}`)})
	time.Sleep(2 * time.Millisecond)
	s.onPreTool(HookInput{ToolUseID: "ok", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"go test ./..."}`)})
	s.mu.Lock()
	if s.awaiting["refused"] == nil {
		s.mu.Unlock()
		t.Fatal("a call of the same batch was closed before any result")
	}
	s.mu.Unlock()
	s.onPostTool(HookInput{ToolUseID: "ok", ToolName: "Bash", ToolResponse: json.RawMessage(`{"stdout":"ok calc","exit_code":0}`)}, false)
	time.Sleep(2 * time.Millisecond)
	s.onPreTool(HookInput{ToolUseID: "wait", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"sleep 2"}`)})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.awaiting["refused"] != nil {
		t.Fatal("the refused call is still waiting after a later result")
	}
	if s.awaiting["wait"] == nil {
		t.Fatal("the new call is not waiting")
	}
}
