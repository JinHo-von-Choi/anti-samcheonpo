package detect

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
	"testing"
)

func behaviorFailure(i int, env string) *event.Event {
	exit := 1
	return &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, CmdNorm: []string{"pytest -q -k test_x", "uv run pytest -v -k test_x", "python -m pytest -k test_x"}[i%3], CmdFP: fmt.Sprint(i), ExitCode: &exit, FailedTests: []string{"test_x"}, ResultFP: "same_failure", Reliability: &event.Reliability{TaskID: "t", Revision: 1, EnvironmentHash: env, Complete: true}}
}
func findBehavior(vs []Signal, rule string) *Signal {
	for i := range vs {
		if vs[i].Rule == rule {
			return &vs[i]
		}
	}
	return nil
}
func TestBehaviorDifferentCommandsSameFailureIsAdvisory(t *testing.T) {
	b := newB(t, "버그 구현", nil)
	var last []Signal
	for i := 0; i < 3; i++ {
		last = b.next(behaviorFailure(i, "env"))
	}
	v := findBehavior(last, "s2.behavior_loop")
	if v == nil || !v.Estimate || v.Level != L1 || v.WasteMicro != 0 || !policy.AdviceOnly(v.Rule) {
		t.Fatalf("%+v", last)
	}
}
func TestBehaviorProtectsChangedEnvironmentFailureAndFlaky(t *testing.T) {
	for _, mode := range []string{"environment", "failure", "flaky", "background", "revision", "input", "selection"} {
		t.Run(mode, func(t *testing.T) {
			b := newB(t, "구현", nil)
			for i := 0; i < 5; i++ {
				ev := behaviorFailure(i, "env")
				switch mode {
				case "environment":
					ev.Reliability.EnvironmentHash = fmt.Sprint(i)
				case "failure":
					ev.ResultFP = fmt.Sprint(i)
				case "flaky":
					ev.Reliability.Flaky = true
				case "background":
					ev.Background = true
				case "input":
					ev.Reliability.InputHash = fmt.Sprint(i)
				case "selection":
					ev.CmdNorm = fmt.Sprintf("pytest -k case%d", i)
				case "revision":
					ev.Reliability.Revision = uint64(i + 1)
				}
				if v := findBehavior(b.next(ev), "s2.behavior_loop"); v != nil {
					t.Fatalf("normal retry was flagged: %+v", v)
				}
			}
		})
	}
}
func TestResearchKnowledgeAndRepeatedResults(t *testing.T) {
	b := newB(t, "웹서치 조사 후 플랜 작성", nil)
	for i := 0; i < 4; i++ {
		ev := &event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"paper"}, Text: fmt.Sprint(i)}
		if v := findBehavior(b.next(ev), "s4.research_repeat"); v != nil || !ev.WorkProgress.Knowledge {
			t.Fatalf("new evidence %+v %+v", v, ev)
		}
	}
	for i := 0; i < 3; i++ {
		ev := &event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"paper"}, Text: "duplicate"}
		vs := b.next(ev)
		if i == 2 && findBehavior(vs, "s4.research_repeat") == nil {
			t.Fatalf("%+v", vs)
		}
	}
}
func TestBehaviorBoundedAndIncompleteReviewUnknown(t *testing.T) {
	b := newB(t, "조사", nil)
	for i := 0; i < 700; i++ {
		b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{fmt.Sprint(i)}, Text: "content"})
	}
	if len(b.e.behavior.entries) > 128 || len(b.e.behavior.knowledge) > 512 {
		t.Fatal("unbounded candidate memory")
	}
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolTask, Category: event.CatExplore, Purpose: "review"}
	b.next(ev)
	if findBehavior(b.e.Verdicts, "s1.delegation_loop") != nil {
		t.Fatal("missing result is not a loop")
	}
}

func TestPassingSameSelectionResetsFailureCandidate(t *testing.T) {
	b := newB(t, "구현", nil)
	b.next(behaviorFailure(0, "env"))
	b.next(behaviorFailure(1, "env"))
	pass := behaviorFailure(2, "env")
	exit := 0
	pass.ExitCode = &exit
	pass.FailedTests = nil
	pass.ResultFP = "pass"
	b.next(pass)
	if v := findBehavior(b.next(behaviorFailure(0, "env")), "s2.behavior_loop"); v != nil {
		t.Fatal("loop crossed a passing verification")
	}
}

func TestBehaviorProtectsDeclaredEnvironmentAndTimeoutChanges(t *testing.T) {
	for _, mode := range []string{"environment", "limit", "opaque"} {
		t.Run(mode, func(t *testing.T) {
			b := newB(t, "구현", nil)
			for i := 0; i < 5; i++ {
				ev := behaviorFailure(i, "env")
				switch mode {
				case "environment":
					ev.Cmd = fmt.Sprintf("MODE=%d pytest -k test_x", i)
				case "limit":
					ev.TimeoutMS = int64(i+1) * 1000
				case "opaque":
					ev.Cmd = "source env; pytest -k test_x"
				}
				if v := findBehavior(b.next(ev), "s2.behavior_loop"); v != nil {
					t.Fatal(v)
				}
			}
		})
	}
}
