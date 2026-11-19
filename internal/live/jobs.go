package live

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// Rules for a scheduled job's run.
const (
	// RuleJobOverrun reports a run far above the job's usual size.
	RuleJobOverrun = "s8.job_overrun"
	// RuleJobBudget stops a run past a limit the user set.
	RuleJobBudget = "s8.job_budget"
)

// jobBaselineRuns is how many past runs make a baseline, and jobOverrunRatio
// how far above their median a run is reported.
const (
	jobBaselineRuns = 10
	jobOverrunRatio = 3
)

var digits = regexp.MustCompile(`[0-9]+`)

// JobID names the scheduled job a prompt starts, or "" for a prompt that is
// not a scheduled job's. Numbers (dates, counters) do not change the job.
func JobID(agent, root, prompt string) string {
	body, scheduled := detect.ScheduledBody(prompt)
	if !scheduled {
		return ""
	}
	return "job_" + fp.Hash(agent, root, digits.ReplaceAllString(body, "#"))[:16]
}

// setJobBaseline takes the token totals of the job's past runs as its
// baseline once there are enough. Called with s.mu held.
func (s *Session) setJobBaseline(runs []int64) {
	if len(runs) < jobBaselineRuns {
		return
	}
	slices.Sort(runs)
	s.jobMedian, s.jobRuns = runs[len(runs)/2], len(runs)
}

// checkJob compares the run so far with its job's baseline and the user's
// limits. Called with s.mu held.
func (s *Session) checkJob() {
	if s.jobID == "" || len(s.eng.St.Events) == 0 {
		return
	}
	tokens, _, _, _ := s.measuredUsage()
	last := s.eng.St.Events[len(s.eng.St.Events)-1]
	facts := func() map[string]any {
		f := map[string]any{"tokens": tokens, "runs": s.jobRuns, "median": s.jobMedian, "target": s.jobID, "ratio": "-"}
		if s.jobMedian > 0 {
			f["ratio"] = fmt.Sprintf("%.1f", float64(tokens)/float64(s.jobMedian))
		}
		return f
	}
	limits := s.Cfg.Jobs
	if s.jobStop == nil && (limits.BudgetTokens > 0 && tokens > limits.BudgetTokens || limits.StopRatio > 0 && s.jobMedian > 0 && float64(tokens) > limits.StopRatio*float64(s.jobMedian)) {
		f := facts()
		if limits.BudgetTokens > 0 {
			f["budget"] = limits.BudgetTokens
		}
		s.jobStop = &detect.Signal{Detector: "S8", Rule: RuleJobBudget, Confidence: 1, Level: detect.L2, Evidence: []int64{last.Seq}, Facts: f}
		return
	}
	if s.jobNoticed || s.jobMedian == 0 || tokens <= jobOverrunRatio*s.jobMedian {
		return
	}
	s.jobNoticed = true
	s.deliver(s.eng.Emit(last, []detect.Signal{{Detector: "S8", Rule: RuleJobOverrun, Confidence: 0.8, Level: detect.L1, Evidence: []int64{last.Seq}, Facts: facts()}}))
}

// jobPre refuses the calls of a run past the user's limit. Called with s.mu
// held.
func (s *Session) jobPre() *detect.Signal {
	if s.jobStop == nil {
		return nil
	}
	v := *s.jobStop
	return &v
}
