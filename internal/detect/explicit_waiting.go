package detect

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// Completed wait -> eligibility -> consecutive count -> L1 candidate.
// Unknown/linked wait or any non-wait action -> reset consecutive count.
func (e *Engine) explicitWaiting(ev *event.Event, scope string, sigs *[]Signal) {
	r := ev.Reliability
	key := fp.Hash(scope, "waiting", r.CheckID, r.WaitTarget)
	if r.Wait == "" {
		// Any distinct non-wait action interrupts consecutive waits.
		for _, c := range e.reliability.targets {
			c.waits = 0
		}
		return
	}
	if ev.ExitCode == nil || *ev.ExitCode != 0 || ev.Background {
		return
	}
	c := e.reliabilityCandidate(key, ev.Seq)
	for k, other := range e.reliability.targets {
		if k != key {
			other.waits = 0
			if other.failures == 0 {
				other.evidence = nil
			}
		}
	}
	reason := "eligible"
	switch {
	case r.Wait == "linked":
		reason = "legitimate_wait"
	case r.Wait != "explicit":
		reason = "wait_relation_unknown"
	case !e.Accepted || e.Contract == nil || scope == "":
		reason = "accepted_context_missing"
	case e.Contract.TemporalRequirement == nil || e.Contract.TemporalConflict(e.Cfg.Detectors.S8.CeilingHours*3600):
		reason = "temporal_context_unknown"
	case r.AuthorityHash != contractAuthority(e):
		reason = "authority_changed"
	case !r.Complete || r.Flaky:
		reason = "incomplete_observation"
	case !r.CriteriaMet:
		reason = "criteria_unconfirmed"
	case !e.Contract.WaitingObservationSatisfied(r.Observation):
		reason = "observation_" + r.Observation
	}
	level := L0
	if reason == "eligible" {
		c.waits++
		c.evidence = append(c.evidence, ev.Seq)
		if len(c.evidence) > reliabilityRecentLimit {
			c.evidence = c.evidence[1:]
		}
		if c.waits >= max(3, e.Cfg.Detectors.S1.ExplicitWaitingRepeats) {
			level = L1
		}
	} else {
		c.waits = 0
		c.evidence = nil
	}
	evidence := append([]int64(nil), c.evidence...)
	if len(evidence) == 0 {
		evidence = []int64{ev.Seq}
	}
	e.add(sigs, Signal{Detector: "S1", Rule: "s1.explicit_waiting", Confidence: .7, Level: level, Estimate: true,
		Evidence: evidence, Facts: map[string]any{"candidate_key": key, "kind": "explicit_wait", "eligibility": reason, "count": c.waits, "wait": r.Wait, "duration_ms": ev.DurationMS, "duration_source": r.DurationSource, "observation": r.Observation}})
}
