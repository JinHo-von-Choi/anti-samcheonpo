package eval

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

func behaviorApplicability(rr *CorpusRunReport, ev *event.Event, goal string) {
	for _, rule := range []string{"s2.behavior_loop", "s4.research_repeat", "s1.delegation_loop", "s3.requirement_substitution", "s3.repeated_permission", "s5.partial_done"} {
		if rr.Applicability[rule] == "" {
			rr.Applicability[rule] = "unknown"
		}
	}
	r := ev.Reliability
	if r == nil || !r.Complete || r.Flaky || r.SourceMissing || ev.Background || ev.BehaviorTarget == "" || ev.BehaviorResult == "" {
		return
	}
	eligible := func(rule string) { rr.Eligible[rule] = true; rr.Applicability[rule] = "eligible" }
	if detect.ExecutedVerify(ev) && r.EnvironmentHash != "" && r.InputHash != "" && (r.CheckID != "" || len(ev.FailedTests) > 0 || len(ev.ErrFPs) > 0) {
		eligible("s2.behavior_loop")
	}
	if ev.Category == event.CatExplore && intent.ResearchRequest(goal) && !ev.IsError {
		eligible("s4.research_repeat")
	}
	if detect.IsReview(ev) && !ev.IsError {
		eligible("s1.delegation_loop")
	}
	// Semantic rules need independent claim/requirement evidence and a permitted
	// budgeted judge. Offline absence of that evaluation remains unknown.
}
