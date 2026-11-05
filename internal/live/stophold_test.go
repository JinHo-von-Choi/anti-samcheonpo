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
// it stays held until a check passes or the hold bound runs out. The host
// asking again does not release it; the same work held MaxStopHolds times
// with no change in between ends unverified, and the user is told so.
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
	out = string(s.onStop(HookInput{LastMessage: last}))
	if strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "2번 보류") || !strings.Contains(out, "auth/login.go") {
		t.Fatalf("the hold bound did not let the unchanged work end unverified: %s", out)
	}
	// one intervention for the evidence, however often the agent asks to end
	var blocked int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM intervention WHERE session_id=? AND rule=? AND blocked=1`, s.ID, detect.RuleAbandonedVerification).Scan(&blocked); err != nil || blocked != 1 {
		t.Fatalf("hold not recorded once: %d %v", blocked, err)
	}
	// a new change is new work: the gate holds it afresh
	feedWrite(t, s, "w2", "auth/login.go")
	if out := string(s.onStop(HookInput{LastMessage: last})); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("a new change was not held: %s", out)
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

// MaxStopHolds 0 keeps the strict gate: no bound, held until a check passes.
func TestStopHoldWithoutBoundStaysShut(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.mu.Lock()
	s.Cfg.Levels.MaxStopHolds = 0
	s.mu.Unlock()
	s.onPrompt(HookInput{Prompt: "로그인 기능 만들어 줘"})
	feedWrite(t, s, "w1", "auth/login.go")
	for i := 0; i < 5; i++ {
		if out := string(s.onStop(HookInput{LastMessage: "구현했습니다.", StopHookActive: i > 0})); !strings.Contains(out, `"decision":"block"`) {
			t.Fatalf("stop %d released an unbounded gate: %s", i, out)
		}
	}
}

// A check the agent ran after the change, that samcheonpo cannot relate to
// the changed file (a Gradle build), is not "no verification": the end is
// let through as unverified instead of held.
func TestStopHoldLetsAnUnrelatedPassingCheckEnd(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "회원 서비스 고쳐 줘"})
	feedWrite(t, s, "w1", "src/main/java/app/MemberService.java")
	feedCheck(t, s, "t1", "./gradlew build", 0)
	out := string(s.onStop(HookInput{LastMessage: "수정했고 빌드가 통과합니다."}))
	if strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("a passing build the gate cannot relate held the turn: %s", out)
	}
	if !strings.Contains(out, "연결할 수 없었습니다") || !strings.Contains(out, "MemberService.java") {
		t.Fatalf("the unverified end was not told to the user: %s", out)
	}
}

// A piped run is the same: its exit status is not the check's, so the
// coverage model cannot judge it, and a pass lets the end through unverified.
func TestStopHoldLetsAPipedPassingCheckEnd(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "계산 버그 고쳐 줘"})
	feedWrite(t, s, "w1", "calc.py")
	feedCheck(t, s, "t1", "pytest -q | tail -3", 0)
	if out := string(s.onStop(HookInput{LastMessage: "고쳤습니다."})); strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("a passing piped run held the turn: %s", out)
	}
}

// Editor and build-tool state is not work a check verifies: changing it
// alone neither holds the turn end nor marks the work unverified.
func TestStopDoesNotHoldOnToolState(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "프로젝트 설정 정리해 줘"})
	feedWrite(t, s, "w1", ".idea/modules.xml")
	feedWrite(t, s, "w2", "app/app.iml")
	feedWrite(t, s, "w3", "web/node_modules/x/index.js")
	if out := string(s.onStop(HookInput{LastMessage: "정리했습니다."})); strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("tool state held the turn: %s", out)
	}
}

// keep now lets the next held turn end through, once.
func TestKeepNowReleasesTheNextHeldStopOnce(t *testing.T) {
	s, d := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "로그인 기능 만들어 줘"})
	feedWrite(t, s, "w1", "auth/login.go")
	if out := string(s.onStop(HookInput{LastMessage: "구현했습니다."})); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("not held: %s", out)
	}
	text, err := d.command(CommandInput{Name: "keep", Arg: "now", Root: s.Root, Session: s.ID})
	if err != nil || !strings.Contains(text, "다음 종료 한 번") {
		t.Fatalf("keep now does not state what it releases: %q %v", text, err)
	}
	out := string(s.onStop(HookInput{LastMessage: "구현했습니다."}))
	if strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "보류하지 않았습니다") {
		t.Fatalf("keep now did not let the next end through: %s", out)
	}
	feedWrite(t, s, "w2", "auth/login.go")
	if out := string(s.onStop(HookInput{LastMessage: "구현했습니다."})); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("keep now released more than one end: %s", out)
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
