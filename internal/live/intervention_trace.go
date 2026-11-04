package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
	"time"
)

type actionDelivery struct {
	emittedAt time.Time
	printedAt time.Time
}

// All helpers except emitted are called with the session lock held.
func (s *Session) interventionMetadata(v detect.Signal, channel string, blocked bool, phase string) ledger.InterventionMetadata {
	route, _ := s.routeFor(v)
	effective, reason := "post_feedback", "rollout"
	switch {
	case blocked && phase == "before_tool":
		effective = "pre_deny"
	case blocked && phase == "stop":
		effective = "stop_hold"
	case channel == "shadow" || channel == "observe":
		effective, reason = "observe", "observation_only"
	case channel == "user":
		effective = "user_notice"
	case phase == "before_tool":
		effective = "pre_feedback"
	}
	if policy.ExplicitGuardrail(v.Rule) {
		reason = "explicit_guardrail"
	} else if detect.IsHardBlock(v.Rule) {
		reason = "hard_pattern"
	}
	if route == "block" && !blocked {
		reason = "phase_or_capability_downgrade"
	}
	model := ""
	if s.parser != nil {
		model = s.parser.Session.Model
	}
	return ledger.InterventionMetadata{PolicyVersion: policy.Version, Experiment: policy.Experiment, RequestedAction: route, EffectiveAction: effective, Phase: phase, Reason: reason, HostVersion: s.hostVersion, AdapterVersion: "hook-shapes/1", DetectorVersion: analyze.EvaluatorVersion, ConfigDigest: s.CfgHash, Capabilities: s.caps, Model: model, Evidence: v.Evidence}
}

func (s *Session) noteIntervention(a *ackSlot, id string) {
	if a == nil {
		return
	}
	a.s = s
	a.interventions = append(a.interventions, id)
}

func (s *Session) markActionDelivery(ids []string, printed bool) {
	if s.actionDelivery == nil {
		s.actionDelivery = map[string]actionDelivery{}
	}
	for _, id := range ids {
		r := s.actionDelivery[id]
		if !r.printedAt.IsZero() || !r.emittedAt.IsZero() && !printed {
			continue
		}
		now := time.Now().UTC()
		if r.emittedAt.IsZero() {
			r.emittedAt = now
		}
		if printed {
			r.printedAt = now
		}
		s.actionDelivery[id] = r
		if s.db != nil {
			s.recordStorageError(s.db.MarkInterventionOutput(s.ID, id, printed, now))
		}
	}
}

func (a *ackSlot) emitted() {
	if !a.pending() {
		return
	}
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	a.s.markActionDelivery(a.interventions, false)
}

// Pre-tool guards can produce a verdict without Engine.Emit. Keep it in the
// analysis as well as the live table; otherwise finalization rewrites the
// verdict table and orphans the intervention that actually denied the call.
// Called with s.mu held by storeAfterAnswer.
func (s *Session) persistPreVerdict(v detect.Signal) {
	found := false
	for _, existing := range s.eng.Verdicts {
		if existing.ID == v.ID {
			found = true
			break
		}
	}
	if !found {
		s.eng.Verdicts = append(s.eng.Verdicts, v)
	}
	s.persistVerdict(v)
}
