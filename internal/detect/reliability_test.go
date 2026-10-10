package detect

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
)

func reliabilityEngine() *Engine {
	c := &contract.Contract{Goal: "finish", Done: []contract.Check{{ID: "test", Check: "pytest"}}, TemporalRequirement: &contract.TemporalRequirement{Kind: "completion"}}
	cfg := config.Default()
	cfg.Experiment.Enabled = false
	return NewEngine(cfg, c, true, "live", "", "session", "")
}
func reliabilityEvent(e *Engine, n int64) *event.Event {
	exit := 0
	return &event.Event{SessionID: "session", Seq: n, CallID: fmt.Sprint(n), Kind: event.KindTool, Tool: event.ToolShell, ExitCode: &exit, Cmd: "sleep 1", TS: time.Unix(n, 0), Priced: true, CostMicroKRW: 1000,
		Reliability: &event.Reliability{TaskID: "task", Revision: 1, AuthorityHash: contract.AuthorityDigest(e.Contract), Complete: true, CriteriaMet: true, Observation: "none", Phase: "result", Wait: "explicit"}}
}
func findings(s []Signal, rule string, eligible bool) int {
	n := 0
	for _, x := range s {
		if x.Rule == rule && (!eligible || x.Level > 0) {
			n++
		}
	}
	return n
}
func TestExplicitWaitingEligibilityAndAccounting(t *testing.T) {
	for _, reason := range []string{"eligible", "pending", "unknown", "manual", "incomplete", "linked", "background", "blocked", "unaccepted", "missing_identity"} {
		t.Run(reason, func(t *testing.T) {
			e := reliabilityEngine()
			var sigs []Signal
			for n := int64(1); n <= 3; n++ {
				ev := reliabilityEvent(e, n)
				switch reason {
				case "pending", "unknown":
					ev.Reliability.Observation = reason
				case "manual":
					ev.Reliability.CriteriaMet = false
				case "incomplete":
					ev.Reliability.Complete = false
				case "linked":
					ev.Reliability.Wait = "linked"
				case "background":
					ev.Background = true
				case "blocked":
					x := -1
					ev.ExitCode = &x
				case "unaccepted":
					e.Accepted = false
				case "missing_identity":
					ev.CallID = ""
				}
				sigs = append(sigs, e.Observe(ev)...)
			}
			want := 0
			if reason == "eligible" {
				want = 1
			}
			if got := findings(sigs, "s1.explicit_waiting", true); got != want {
				t.Fatalf("eligible warnings %d want %d: %+v", got, want, sigs)
			}
			if len(e.St.Estimated) != 0 || len(e.St.Wasted) != 0 || e.St.TotalMicro != 3000 {
				t.Fatalf("accounting changed: %+v", e.St)
			}
		})
	}
}
func TestReliabilityDedupPhaseAndReplay(t *testing.T) {
	e := reliabilityEngine()
	input := reliabilityEvent(e, 1)
	input.Reliability.Phase = "input"
	input.ExitCode = nil
	e.Observe(input)
	result := reliabilityEvent(e, 2)
	result.CallID = input.CallID
	e.Observe(result)
	e.Observe(result)
	if got := findings(e.Observe(reliabilityEvent(e, 3)), "s1.explicit_waiting", true); got != 0 {
		t.Fatal("duplicate counted")
	}
	vs := e.Observe(reliabilityEvent(e, 4))
	if findings(vs, "s1.explicit_waiting", true) != 1 {
		t.Fatal(vs)
	}
	restarted := reliabilityEngine()
	restarted.Restoring = true
	for _, ev := range e.St.Events {
		b, _ := json.Marshal(ev)
		var stored event.Event
		if err := json.Unmarshal(b, &stored); err != nil {
			t.Fatal(err)
		}
		restarted.Observe(&stored)
	}
	for _, s := range restarted.Verdicts {
		if policy.AdviceOnly(s.Rule) {
			t.Fatal("restore emitted new verdict", s)
		}
	}
	restarted.Restoring = false
	restarted.RestoreReliabilityVerdicts(e.Verdicts)
	next := reliabilityEvent(restarted, 5)
	sigs := restarted.Observe(next)
	for _, s := range sigs {
		if s.Rule == "s1.explicit_waiting" && !s.Suppressed {
			t.Fatal("restored delivery cooldown lost", s)
		}
	}
}
func TestReliabilityLevelRouteAndPrimaryCaps(t *testing.T) {
	e := reliabilityEngine()
	for n := int64(1); n < 60; n++ {
		ev := reliabilityEvent(e, n)
		for _, s := range e.Observe(ev) {
			if s.Rule == "s1.explicit_waiting" && s.Level > L1 {
				t.Fatal(s)
			}
		}
	}
	for _, rule := range []string{"s1.explicit_waiting", "s8.progress_stall"} {
		if policy.RolloutRoute("validated", rule, map[string]string{rule: fmt.Sprintf("%064x", 1)}) != "advice" || policy.Escalable(rule, "", true) || policy.ExplicitGuardrail(rule) {
			t.Fatal("blocking", rule)
		}
	}
	sigs := e.Emit(reliabilityEvent(e, 100), []Signal{{Rule: "s1.explicit_waiting", Detector: "S1", Level: L4, Confidence: 1, Facts: map[string]any{"candidate_key": "new"}}, {Rule: "s2.environment", Detector: "S2", Level: L1, Confidence: 1}})
	if !sigs[1].Primary || sigs[0].Primary || sigs[0].Level != L1 {
		t.Fatal(sigs)
	}
}
func failureEvent(e *Engine, n int64, input, check string, failed []string) *event.Event {
	ev := reliabilityEvent(e, n)
	x := 1
	ev.ExitCode = &x
	ev.Cmd = ""
	ev.Category = event.CatVerify
	ev.Tool = "checkpoint_check"
	ev.FailedTests = failed
	r := ev.Reliability
	r.Wait = ""
	r.CheckID = check
	r.InputHash = input
	r.EnvironmentHash = "env"
	r.CommandHash = "check-command"
	r.FailureComparable = true
	return ev
}
func filler(e *Engine, n int64) {
	ev := reliabilityEvent(e, n)
	ev.Cmd = ""
	ev.Tool = event.ToolRead
	ev.Category = event.CatExplore
	ev.Reliability.Wait = ""
	e.Observe(ev)
}
func TestProgressStallRequiresChangedInputsAndRelatedProgress(t *testing.T) {
	for _, variant := range []string{"changed", "same", "improved", "environment", "truncated", "unrelated_pass", "related_pass", "flaky"} {
		t.Run(variant, func(t *testing.T) {
			e := reliabilityEngine()
			e.Observe(failureEvent(e, 1, "a", "test", []string{"A", "B"}))
			filler(e, 2)
			second := failureEvent(e, 3, "b", "test", []string{"A", "B"})
			switch variant {
			case "same":
				second.Reliability.InputHash = "a"
			case "improved":
				second.FailedTests = []string{"A"}
			case "environment":
				second.Reliability.EnvironmentHash = "other"
			case "truncated":
				second.Reliability.Complete = false
			case "flaky":
				second.Reliability.Flaky = true
			}
			e.Observe(second)
			if variant == "unrelated_pass" || variant == "related_pass" {
				check := "another"
				if variant == "related_pass" {
					check = "test"
				}
				pass := failureEvent(e, 4, "b", check, nil)
				x := 0
				pass.ExitCode = &x
				e.Observe(pass)
			} else {
				filler(e, 4)
			}
			third := failureEvent(e, 5, "c", "test", []string{"A", "B"})
			if variant == "same" {
				third.Reliability.InputHash = "a"
			}
			sigs := e.Observe(third)
			want := 0
			if variant == "changed" || variant == "unrelated_pass" {
				want = 1
			}
			if got := findings(sigs, "s8.progress_stall", true); got != want {
				t.Fatalf("got %d want %d: %+v", got, want, sigs)
			}
			if len(e.St.Estimated) > 0 {
				t.Fatal("estimated costs assigned")
			}
		})
	}
}
func TestReliabilityRevisionBoundsAndCeilingPreserved(t *testing.T) {
	e := reliabilityEngine()
	e.Cfg.Detectors.S8.CeilingTokens = 1
	for n := int64(1); n <= 70; n++ {
		ev := reliabilityEvent(e, n)
		ev.Reliability.CheckID = fmt.Sprint(n)
		e.Observe(ev)
	}
	evicted, deferred := e.ReliabilityStats()
	if len(e.reliability.targets) > 64 || evicted == 0 || deferred == 0 {
		t.Fatal(evicted, deferred)
	}
	before := e.St.TotalMicro
	e.ResetReliability()
	if e.St.TotalMicro != before || e.Cfg.Detectors.S8.CeilingTokens != 1 {
		t.Fatal("reset widened budget")
	}
	if len(e.reliability.targets) != 0 {
		t.Fatal("revision retained candidates")
	}
	for n := int64(100); n < 104; n++ {
		ev := reliabilityEvent(e, n)
		ev.Reliability.Revision = uint64(n - 99)
		if findings(e.Observe(ev), "s1.explicit_waiting", true) > 0 {
			t.Fatal("mixed revisions")
		}
	}
}

func TestReviewRepeatRevisionInputAndCheckpointIsolation(t *testing.T) {
	e := reliabilityEngine()
	review := func(seq int64, revision uint64, input string) []Signal {
		ev := reliabilityEvent(e, seq)
		ev.Tool = event.ToolTask
		ev.Cmd = ""
		ev.Purpose = "code review"
		ev.WSBefore = "same"
		ev.Reliability.Wait = ""
		ev.Reliability.Revision = revision
		ev.Reliability.InputHash = input
		return e.Observe(ev)
	}
	if findings(review(1, 1, "a"), "s1.review_repeat", false) != 0 || findings(review(2, 1, "a"), "s1.review_repeat", false) != 1 {
		t.Fatal("setup")
	}
	if findings(review(3, 2, "a"), "s1.review_repeat", false) != 0 || findings(review(4, 2, "b"), "s1.review_repeat", false) != 0 {
		t.Fatal("review crossed revision or inputs")
	}
	pass := failureEvent(e, 5, "b", "test", nil)
	pass.Reliability.Revision = 2
	x := 0
	pass.ExitCode = &x
	e.ObserveReliabilityFact(pass)
	if findings(review(6, 2, "b"), "s1.review_repeat", false) != 0 {
		t.Fatal("new independent evidence retained review repeat")
	}
}

func TestUnresolvedBackgroundDefersWaitingAndPreservesCeiling(t *testing.T) {
	e := reliabilityEngine()
	exit := 0
	e.Observe(&event.Event{SessionID: e.Session, Seq: 0, Kind: event.KindTool, Tool: event.ToolShell, CallID: "job", Background: true, ExitCode: &exit, Usage: event.Usage{Out: 2}})
	for n := int64(1); n <= 3; n++ {
		if findings(e.Observe(reliabilityEvent(e, n)), "s1.explicit_waiting", true) > 0 {
			t.Fatal("unobserved background completion")
		}
	}
	e.Cfg.Detectors.S8.CeilingTokens = 1
	guard := e.PreGuard(&event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, Cmd: "go test ./...", Unknown: true})
	if guard == nil || guard.Rule != "s8.session_ceiling" {
		t.Fatal("legitimate/unknown waiting widened ceiling", guard)
	}
	e.ResetReliability()
	if findings(e.Observe(reliabilityEvent(e, 4)), "s1.explicit_waiting", true) > 0 {
		t.Fatal("revision invented background completion")
	}
}

func TestReliabilityExperimentArmsPreserveDetection(t *testing.T) {
	arms := map[string]bool{}
	for i := 0; i < 40; i++ {
		e := reliabilityEngine()
		e.Session = fmt.Sprintf("arm-%d", i)
		e.Cfg.Experiment.Enabled = true
		e.Cfg.Rollout.ShadowRules = nil
		var sigs []Signal
		for n := int64(1); n <= 3; n++ {
			ev := reliabilityEvent(e, n)
			ev.SessionID = e.Session
			sigs = e.Observe(ev)
		}
		for _, s := range sigs {
			if s.Rule == "s1.explicit_waiting" {
				if s.Level != L1 || !s.Primary || s.Arm != Arm(e.Session, s.Rule) {
					t.Fatal(s)
				}
				arms[s.Arm] = true
			}
		}
	}
	if len(arms) != 3 {
		t.Fatal("all experiment arms were not exercised", arms)
	}
}
