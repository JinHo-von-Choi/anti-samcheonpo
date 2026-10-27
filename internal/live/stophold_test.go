package live

import (
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

// A turn that ends on an unverified change is held once with the next
// step, then released on the same evidence so the agent never loops.
func TestStopHoldsAbandonedVerificationOnce(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "로그인 기능 만들어 줘"})
	feedWrite(t, s, "w1", "auth/login.go")
	last := "로그인 기능을 구현했습니다. 검증은 하지 못했습니다."

	out := string(s.onStop(HookInput{LastMessage: last}))
	if !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "마지막 코드 변경 뒤에 실행한 검사가 없다") || !strings.Contains(out, "가장 작은 검사") {
		t.Fatalf("stop not held with the next step: %s", out)
	}
	if out := string(s.onStop(HookInput{LastMessage: last, StopHookActive: true})); strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("held a continued turn again: %s", out)
	}
	if out := string(s.onStop(HookInput{LastMessage: last})); strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("held twice on the same evidence: %s", out)
	}
	var blocked int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM intervention WHERE session_id=? AND rule=? AND blocked=1`, s.ID, detect.RuleAbandonedVerification).Scan(&blocked); err != nil || blocked != 1 {
		t.Fatalf("hold not recorded once: %d %v", blocked, err)
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
