package live

import (
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

// Called under s.mu. Pre-tool captures only identity, without fingerprint I/O.
func (s *Session) reliabilityBoundary() *event.Reliability {
	r := &event.Reliability{Observation: "unknown"}
	if s.task != nil && s.intentRevision != nil {
		r.TaskID, r.Revision = s.task.ID, s.intentRevision.Number
	}
	if s.c != nil && s.acc.State == contract.StateAccepted && s.acc.RequestedGoal == "" && (s.intentRevision == nil || strings.TrimSpace(s.intentRevision.Goal) == strings.TrimSpace(s.c.Goal)) {
		r.AuthorityHash = contract.AuthorityDigest(s.c)
	}
	return r
}

func (s *Session) observationObligation(now time.Time) string {
	if s.c == nil || s.c.TemporalRequirement == nil || s.c.TemporalConflict(s.Cfg.Detectors.S8.CeilingHours*3600) {
		return "unknown"
	}
	t := s.c.TemporalRequirement
	switch t.Kind {
	case "completion":
		return "none"
	case "minimum_duration":
		if s.temporalStart.IsZero() || now.Before(s.temporalStart) {
			return "unknown"
		}
		// A restart starts a new continuous harness observation segment.
		if now.Sub(s.temporalStart).Seconds() >= float64(t.Seconds) {
			return "fulfilled"
		}
		return "pending"
	case "observe_until":
		if r, ok := s.checks[t.CheckID]; ok && r.Pass && r.Evidence != nil && !r.SideEffect && !r.Truncated && !r.TimedOut {
			return "fulfilled"
		}
		return "pending"
	}
	return "unknown"
}

func (s *Session) observeReliability(ev *event.Event) {
	if ev.Kind != event.KindTool {
		return
	}
	if ev.Reliability == nil {
		ev.Reliability = s.reliabilityBoundary()
	}
	r, current := ev.Reliability, s.reliabilityBoundary()
	if r.TaskID != current.TaskID || r.Revision != current.Revision || r.AuthorityHash != current.AuthorityHash {
		r.Complete = false
		r.Observation = "unknown"
		r.CriteriaMet = false
		return
	}
	r.Complete = s.queueRejected.Load() == 0 && s.storageErr == nil && (s.ws == nil || !s.ws.WatchUnavailable()) && !ev.Unknown && !ev.Background
	r.Wait = event.ExplicitWait(ev)
	if r.Wait != "" {
		r.WaitKind = "sleep"
	}
	if ev.DurationMS > 0 {
		r.DurationSource = "observed_tool"
	}
	if r.Wait != "" {
		for id := range s.awaiting {
			if id != ev.CallID {
				r.Wait = "unknown"
				break
			}
		}
		r.Observation = s.observationObligation(ev.TS)
		r.CriteriaMet = s.reliabilityCriteria(ev.TS)
	}
	// Agent checks run in another environment. Comparable check observations
	// come only from the harness, whose runner provides complete proof keys.
}

func (s *Session) reliabilityCriteria(now time.Time) bool {
	if s.c == nil || s.acc.State != contract.StateAccepted || len(s.c.Done) == 0 || s.runner == nil {
		return false
	}
	task, rev := s.evidenceIdentity()
	for _, check := range s.c.Done {
		if check.Manual != "" {
			return false
		} // manual confirmation is unavailable
		r, ok := s.checks[check.ID]
		if !ok || r.Evidence == nil || r.Truncated || r.TimedOut || r.SideEffect || len(r.Failed) > 0 {
			return false
		}
		key, err := s.runner.CurrentKey(check, task, rev)
		if err != nil {
			return false
		}
		if valid, _ := verification.Reusable(*r.Evidence, key, now); !valid {
			return false
		}
	}
	return true
}

func (s *Session) recordReliabilityCheck(result CheckResult) {
	if result.Reused || result.Skipped != "" || result.Evidence == nil {
		return
	}
	proof := result.Evidence
	r := s.reliabilityBoundary()
	r.CheckID, r.CommandHash, r.InputHash, r.EnvironmentHash = proof.Key.CheckID, proof.Key.CommandHash, proof.Key.InputHash, proof.Key.EnvironmentHash
	r.Complete = proof.Complete && !result.Truncated && !proof.TimedOut && !proof.SideEffect && s.queueRejected.Load() == 0 && s.storageErr == nil && (s.ws == nil || !s.ws.WatchUnavailable()) && !(proof.Pass && len(result.Failed) > 0)
	r.Flaky, r.FailureComparable = proof.Flaky, proof.Complete && !result.Truncated
	r.Phase, r.DurationSource = "checkpoint", "harness_monotonic"
	r.Observation = s.observationObligation(time.Now())
	exit := result.ExitCode
	ev := &event.Event{Kind: event.KindTool, Tool: "checkpoint_check", Category: event.CatVerify, TS: proof.ObservedAt, SourceRef: proof.SourceEventID,
		ExitCode: &exit, FailedTests: append([]string(nil), result.Failed...), ResultFP: result.ResultFP, DurationMS: result.Duration.Milliseconds(), Reliability: r, Priced: true, Basis: "live"}
	ev.ErrFPs = append([]string(nil), result.ErrorFPs...)
	s.parser.AddEvent(ev)
	sigs := s.eng.ObserveReliabilityFact(ev)
	s.persistEvent(ev)
	s.deliver(sigs)
}
