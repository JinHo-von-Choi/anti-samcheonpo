package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
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
}
