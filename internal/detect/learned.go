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
	return nil
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
		e.add(sigs, Signal{Detector: d.Symptom, Rule: d.RuleID(), Level: L1, Confidence: .65, Estimate: true, Evidence: match.Evidence, Facts: map[string]any{"candidate_key": fp.Hash(scope, d.ID, rulepack.Signature(ev)), "target_hash": match.Target, "count": len(match.Evidence), "candidate_id": d.ID, "evaluation_digest": e.learnedDigest, "source": "labeled_candidate"}})
	}
}
