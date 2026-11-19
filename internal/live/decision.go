package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"slices"
)

// Called under s.mu. Derived reasons never change canonical event/seal data.
func (s *Session) recordDecision(v detect.Signal, code string) {
	for i := len(s.eng.St.Events) - 1; i >= 0; i-- {
		ev := s.eng.St.Events[i]
		if ev.Seq != v.Seq {
			continue
		}
		for _, d := range ev.Decisions {
			if d.Rule == v.Rule && d.Code == code {
				return
			}
		}
		ev.Decisions = append(ev.Decisions, event.Decision{Rule: v.Rule, Code: code, Evidence: append([]int64(nil), v.Evidence...)})
		if s.db != nil {
			s.recordStorageError(s.db.SaveEventObservation(s.ID, ev))
		}
		s.assessmentCache = nil
		return
	}
	// The verdict names an event the worker has not observed yet (a turn
	// end judged before its message is queued). Keep the decision until the
	// event arrives; dropping it leaves the verdict with no reason at all.
	if s.lateDecisions == nil {
		s.lateDecisions = map[int64][]event.Decision{}
	}
	for _, d := range s.lateDecisions[v.Seq] {
		if d.Rule == v.Rule && d.Code == code {
			return
		}
	}
	s.lateDecisions[v.Seq] = append(s.lateDecisions[v.Seq], event.Decision{Rule: v.Rule, Code: code, Evidence: append([]int64(nil), v.Evidence...)})
}

// takeLateDecisions moves the decisions recorded before ev was observed onto
// it. Called with s.mu held.
func (s *Session) takeLateDecisions(ev *event.Event) {
	late := s.lateDecisions[ev.Seq]
	if len(late) == 0 {
		return
	}
	delete(s.lateDecisions, ev.Seq)
	for _, d := range late {
		if !slices.ContainsFunc(ev.Decisions, func(x event.Decision) bool { return x.Rule == d.Rule && x.Code == d.Code }) {
			ev.Decisions = append(ev.Decisions, d)
		}
	}
	if s.db != nil {
		s.recordStorageError(s.db.SaveEventObservation(s.ID, ev))
	}
}
