package detect

import (
	"slices"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

func contractAuthority(e *Engine) string {
	if e.Contract == nil {
		return ""
	}
	return contract.AuthorityDigest(e.Contract)
}

func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

func relatedImprovement(prev, ev *event.Event) bool {
	if prev == nil || !ev.Reliability.Complete || ev.Reliability.Flaky {
		return false
	}
	if !failing(ev) && len(ev.FailedTests) == 0 {
		return true
	}
	if (len(prev.FailedTests) > 0 && len(ev.FailedTests) == 0) || (len(prev.ErrFPs) > 0 && len(ev.ErrFPs) == 0) {
		return false
	}
	return (len(ev.FailedTests) < len(prev.FailedTests) && subset(ev.FailedTests, prev.FailedTests) && subset(ev.ErrFPs, prev.ErrFPs)) ||
		(len(ev.ErrFPs) < len(prev.ErrFPs) && subset(ev.ErrFPs, prev.ErrFPs) && subset(ev.FailedTests, prev.FailedTests))
}

// Related complete failure -> comparable series -> changed inputs -> L1.
// Related progress or changed command/environment -> reset series;
// missing/flaky evidence -> defer without manufacturing progress.
func (e *Engine) progressStall(ev *event.Event, scope string, sigs *[]Signal) {
	r := ev.Reliability
	known := false
	if e.Contract != nil {
		for _, check := range e.Contract.Done {
			if check.ID == r.CheckID && check.Check != "" {
				known = true
				break
			}
		}
	}
	if !known || r.CheckID == "" {
		return
	}
	if !e.Accepted || e.Contract == nil || e.Contract.Explore || scope == "" || r.AuthorityHash != contractAuthority(e) ||
		!r.Complete || r.Flaky || r.CommandHash == "" || r.InputHash == "" || r.EnvironmentHash == "" ||
		!ExecutedVerify(ev) || r.Wait != "" || r.Observation == "pending" || len(e.reliability.background) > 0 || e.reliability.backgroundUnknown {
		e.reliability.Deferred++
		return
	}
	key := fp.Hash(scope, r.CheckID)
	c := e.reliabilityCandidate(key, ev.Seq)
	// A different verifier or environment is not a comparable series.
	if c.last != nil && (c.last.Reliability.CommandHash != r.CommandHash || c.last.Reliability.EnvironmentHash != r.EnvironmentHash) {
		*c = reliabilityCandidate{seq: ev.Seq, inputs: map[string]bool{}, firstTick: e.reliability.tick}
	}
	if (!failing(ev) && len(ev.FailedTests) == 0) || relatedImprovement(c.last, ev) {
		*c = reliabilityCandidate{seq: ev.Seq, inputs: map[string]bool{}, firstTick: e.reliability.tick}
		delete(e.cooldownUntil, "reliability:s8.progress_stall:"+key)
		delete(e.cooldownLevel, "reliability:s8.progress_stall:"+key)
		e.reliability.reviewEpoch++
		return
	}
	if c.last != nil && ((len(c.last.FailedTests) > 0 && len(ev.FailedTests) == 0) || (len(c.last.ErrFPs) > 0 && len(ev.ErrFPs) == 0)) {
		e.reliability.Deferred++
		return
	}
	if !r.FailureComparable || (len(ev.ErrFPs) == 0 && len(ev.FailedTests) == 0) {
		e.reliability.Deferred++
		return
	}
	if c.failures == 0 {
		c.firstTick = e.reliability.tick
	}
	c.failures++
	c.tick = e.reliability.tick
	c.inputs[r.InputHash] = true
	if len(c.inputs) > reliabilityRecentLimit {
		c.inputs = map[string]bool{r.InputHash: true}
		e.reliability.Deferred++
	}
	c.last = ev
	c.evidence = append(c.evidence, ev.Seq)
	if len(c.evidence) > reliabilityRecentLimit {
		c.evidence = c.evidence[1:]
	}
	if c.failures < max(3, e.Cfg.Detectors.S8.StallFailures) || c.tick-c.firstTick+1 < uint64(max(5, e.Cfg.Detectors.S8.StallToolEvents)) || len(c.inputs) < 2 {
		return
	}
	e.add(sigs, Signal{Detector: "S8", Rule: "s8.progress_stall", Confidence: .7, Level: L1, Estimate: true, Evidence: append([]int64(nil), c.evidence...),
		Facts: map[string]any{"candidate_key": key, "kind": "related_failure", "eligibility": "eligible", "check_id": r.CheckID, "failures": c.failures, "input_changed": true, "failed_count": len(ev.FailedTests), "error_fingerprints": append([]string(nil), ev.ErrFPs...), "last_progress": e.St.LastProgress, "tool_events": c.tick - c.firstTick + 1, "next_action": "좁은 재현·환경 복구·입력 변경·인수 조건 확인"}})
}
