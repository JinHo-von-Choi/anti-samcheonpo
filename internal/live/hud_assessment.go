package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/assessment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

type hudAssessmentCache struct {
	events, verdicts int
	prompt, config   string
	summary          assessment.Summary
	work             intent.Work
	gaps             assessment.GapTracker
}

// Append-only diagnostic facts are folded once. Late usage and classifications
// use the engine's invalidated usage cache; polling does not scan all events.
func (s *Session) currentAssessment(met, total int) (assessment.Summary, intent.Work) {
	st := s.eng.St
	c := s.assessmentCache
	if c == nil || c.events > len(st.Events) || c.verdicts > len(s.eng.Verdicts) || c.prompt != s.firstPrompt || c.config != s.CfgHash {
		c = &hudAssessmentCache{prompt: s.firstPrompt, config: s.CfgHash, summary: assessment.Build(nil, nil, s.Cfg, 0, 0), work: intent.InferWork(s.firstPrompt)}
		s.assessmentCache = c
	}
	next := assessment.Build(st.Events[c.events:], s.eng.Verdicts[c.verdicts:], s.Cfg, 0, 0)
	if len(next.Requirements) > 0 {
		c.summary.Requirements = next.Requirements
	}
	c.summary.Findings += next.Findings
	c.summary.Candidates += next.Candidates
	c.summary.Shadow += next.Shadow
	c.summary.Coverage.ShellCalls += next.Coverage.ShellCalls
	c.summary.Coverage.CompletedShellResults += next.Coverage.CompletedShellResults
	c.summary.Coverage.WorkspaceKnown += next.Coverage.WorkspaceKnown
	c.summary.Coverage.ResultKnown += next.Coverage.ResultKnown
	c.summary.Progress.Activity = c.summary.Progress.Activity || next.Progress.Activity
	c.summary.Progress.Knowledge = c.summary.Progress.Knowledge || next.Progress.Knowledge
	c.summary.Progress.Verification = c.summary.Progress.Verification || next.Progress.Verification
	for key, n := range next.Reasons {
		if key != "completion_unobserved" && key != "usage_unobserved" {
			c.summary.Reasons[key] += n
		}
	}
	for _, ev := range st.Events[c.events:] {
		c.gaps.Observe(ev)
		if ev.Kind == event.KindTool && !ev.IsError && (ev.Category == event.CatProduce || ev.Category == event.CatVerify) {
			w := intent.DescribeWork(s.firstPrompt, []*event.Event{ev})
			if w.Phase != "research" && w.Phase != "discovery" {
				c.work.Phase = w.Phase
			}
		}
	}
	c.events = len(st.Events)
	c.summary.Coverage.Gaps = c.gaps.Snapshot()
	c.verdicts = len(s.eng.Verdicts)
	tokens, unpriced, waste, _ := st.UsageSummary()
	c.summary.Confirmed = max(0, len(st.Wasted)-len(st.Estimated))
	c.summary.ApplyMeasurement(tokens, unpriced, st.TotalMicro, waste, st.WasteTokens(), met, total)
	if s.acc.State != "accepted" {
		c.summary.Reasons["contract_unaccepted"] = 1
	} else {
		delete(c.summary.Reasons, "contract_unaccepted")
	}
	if s.judgeBudgetReason != "" {
		c.summary.Reasons["semantic_deferred"] = 1
	} else {
		delete(c.summary.Reasons, "semantic_deferred")
	}
	result := c.summary
	result.Reasons = make(map[string]int, len(c.summary.Reasons))
	for k, v := range c.summary.Reasons {
		result.Reasons[k] = v
	}
	return result, c.work
}
