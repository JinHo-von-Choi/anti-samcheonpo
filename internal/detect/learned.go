package detect

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
)

func (e *Engine) ConfigureCandidates(pack rulepack.Pack) error {
	m, err := rulepack.New(pack.Definitions)
	if err != nil {
		return err
	}
	e.learned = m
	e.learnedDigest = pack.EvaluationDigest
	e.learnedTier = pack.Tier()
	return nil
}

// candidateSource names the grade of evidence behind a learned rule.
//
// The fact carries the source so that whoever reads it can tell a rule a
// person confirmed from a rule an independent grader derived. The two are
// not the same claim, and a reader that cannot tell them apart reads the
// weaker one as the stronger one.
func (e *Engine) candidateSource() string {
	if e.learnedTier == rulepack.EvidenceDerivedExecution {
		return "derived_candidate"
	}
	return "labeled_candidate"
}

func (e *Engine) observeLearned(ev *event.Event, sigs *[]Signal) {
	if e.learned == nil {
		return
	}
	scope := fp.Hash(e.FirstPrompt)
	if ev.Reliability != nil {
		scope = fp.Hash(scope, ev.Reliability.TaskID, fmt.Sprint(ev.Reliability.Revision))
	}
	for _, match := range e.learned.Observe(ev, scope) {
		d := match.Definition
		e.add(sigs, Signal{Detector: d.Symptom, Rule: d.RuleID(), Level: L1, Confidence: .65, Estimate: true, Evidence: match.Evidence, Facts: map[string]any{"candidate_key": fp.Hash(scope, d.ID, rulepack.Signature(ev)), "target_hash": match.Target, "count": len(match.Evidence), "candidate_id": d.ID, "evaluation_digest": e.learnedDigest, "source": e.candidateSource(), "evidence_tier": e.learnedTier}})
	}
}
