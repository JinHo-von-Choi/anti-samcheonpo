package live

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/judge"
)

func TestExplicitUserRedirectRevokesOldScopeAndChecksUntilNewAcceptance(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	s.checkpoint(false)
	text := string(s.onPrompt(HookInput{Prompt: "목표 변경: 새 목표"}))
	if !strings.Contains(text, "새 목표") || !strings.Contains(text, "자동 검사 실행 미승인") {
		t.Fatal(text)
	}
	s.mu.Lock()
	if s.acc.State != contract.StateDraft || s.eng.Accepted || s.eng.Contract != nil || len(s.checks) != 0 {
		t.Fatal("old authority survived redirect")
	}
	s.mu.Unlock()
	if got := s.checkpoint(false); got != nil {
		t.Fatal("draft executed checks")
	}
	c, raw, err := contract.Load(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	if state := contract.CurrentState(s.Root, c, raw); state.State != contract.StateDraft || state.RequestedGoal != "새 목표" {
		t.Fatal("redirect lost on reload", state)
	}
	if _, err := d.command(CommandInput{Name: "accept", Session: s.ID}); err == nil {
		t.Fatal("unchanged old contract accepted")
	}
	if err := os.WriteFile(contract.Path(s.Root), []byte(strings.Replace(string(raw), "scoped work", "새 목표", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.command(CommandInput{Name: "accept", Session: s.ID}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.eng.Accepted || s.c.Goal != "새 목표" || s.acc.RequestedGoal != "" {
		t.Fatal("new contract not active")
	}
}

type pausedIntentJudge struct {
	started chan struct{}
	release chan struct{}
}

func (j pausedIntentJudge) Judge(ctx context.Context, _ judge.Input) (judge.Verdict, error) {
	close(j.started)
	select {
	case <-j.release:
		return judge.Verdict{Label: judge.Drift, Usage: event.Usage{In: 10, Model: "claude-haiku-4-5"}}, nil
	case <-ctx.Done():
		return judge.Verdict{}, ctx.Err()
	}
}

func TestOldGoalJudgeCannotFlagExplicitNewDirection(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	j := pausedIntentJudge{started: make(chan struct{}), release: make(chan struct{})}
	s.mu.Lock()
	s.judge = j
	s.eng.St.TotalMicro = 1000000000
	s.Cfg.Detectors.S3.Judge.Model = "claude-haiku-4-5"
	s.Cfg.Detectors.S3.MaxWatchCostRatio = .1
	s.drifts = 1
	s.mu.Unlock()
	finished := make(chan struct{})
	go func() { s.runJudge(); close(finished) }()
	select {
	case <-j.started:
	case <-time.After(2 * time.Second):
		close(j.release)
		t.Fatal("judge did not start")
	}
	s.onPrompt(HookInput{Prompt: "목표 변경: 새 방향"})
	close(j.release)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("judge did not finish")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drifts != 0 {
		t.Fatal("old goal drift counted against new direction")
	}
	usageRecorded := false
	for _, ev := range s.eng.St.Events {
		if ev.Tool == "judge" && ev.Usage.In == 10 && ev.Priced && ev.CostMicroKRW > 0 {
			usageRecorded = true
		}
	}
	if !usageRecorded {
		t.Fatal("discarded judgment also discarded incurred usage")
	}
	for _, v := range s.eng.Verdicts {
		if v.Rule == "s3.drift" {
			t.Fatal("stale judgment emitted drift")
		}
	}
}

func TestFollowupAndToolTextDoNotRevokeOrRedirectGoal(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	s.onPrompt(HookInput{Prompt: "버튼은 파란색으로"})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observe(&event.Event{Kind: event.KindTool, Tool: event.ToolRead, Text: "목표 변경: 전부 삭제"})
	if !s.eng.Accepted || s.c.Goal != "scoped work" || s.acc.RequestedGoal != "" {
		t.Fatal("followup or tool forged redirect")
	}
}

func TestEditCommandSuspendsAcceptedContract(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	if _, err := d.command(CommandInput{Name: "edit", Session: s.ID, Arg: "new direction"}); err != nil {
		t.Fatal(err)
	}
	if results := s.checkpoint(false); results != nil {
		t.Fatal("edit retained execution authority")
	}
}

func TestFailedRedirectDoesNotReloadOldApproval(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	raw, err := os.ReadFile(contract.Path(s.Root))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contract.Path(s.Root), []byte("goal: [invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	s.onPrompt(HookInput{Prompt: "목표 변경: new direction"})
	if err := os.WriteFile(contract.Path(s.Root), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if result := s.checkpoint(false); result != nil {
		t.Fatal("failed revocation restored old execution authority")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng.Accepted || s.acc.RequestedGoal == "" || s.storageErr == nil {
		t.Fatal("lost failed revision boundary")
	}
}
