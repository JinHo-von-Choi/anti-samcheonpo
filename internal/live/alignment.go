package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/alignment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func (s *Session) requirementGraph() alignment.Graph {
	goal := s.firstPrompt
	if s.intentRevision != nil {
		goal = s.intentRevision.Goal
	}
	accepted := s.c != nil && s.acc.State == contract.StateAccepted
	if accepted {
		goal = s.c.Goal
	}
	return alignment.Build(goal, s.c, accepted, s.eng.St.Events, s.reliabilityBoundary())
}

// A current failed accepted check and a later full completion claim form a
// concrete contradiction candidate without requiring an external judge.
func (s *Session) groundedClaimSignals(ev *event.Event) []detect.Signal {
	if ev.Kind != event.KindMessage || s.closed || s.queueRejected.Load() != 0 || s.alignmentGraph == nil {
		return nil
	}
	g := s.alignmentGraph
	var requirements []string
	var results []int64
	for _, r := range g.Requirements {
		if r.Status == "failure_observed" && len(r.Evidence) == 1 && g.ValidIssue("partial_completion", []string{r.ID}, []int64{ev.Seq}, r.Evidence, nil) {
			requirements = append(requirements, r.ID)
			results = append(results, r.Evidence[0])
		}
	}
	if len(requirements) == 0 {
		return nil
	}
	evidence := append(append([]int64(nil), results...), ev.Seq)
	return s.eng.Emit(ev, []detect.Signal{{Detector: "S5", Rule: "s5.partial_done", Level: detect.L1, Confidence: .7, Estimate: true, Evidence: evidence, Facts: map[string]any{"requirements": requirements, "claims": []int64{ev.Seq}, "results": results, "source": "requirement_graph"}}})
}
func (s *Session) annotateRequirements(ev *event.Event) {
	if ev.Kind == event.KindMessage || ev.Category == event.CatProduce || ev.Category == event.CatVerify {
		g := s.requirementGraph()
		ev.Requirements = g.Progress()
		s.alignmentGraph = &g
	}
}
