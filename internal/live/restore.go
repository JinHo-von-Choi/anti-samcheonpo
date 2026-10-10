package live

import (
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// restoreHistory continues a session that a previous daemon already
// recorded (the daemon restarted for an upgrade or after idling while the
// agent kept the session): its stored events are observed again so the
// running totals, repeat counts and waste marks carry over, and new events
// and verdicts are numbered after the stored ones instead of overwriting
// them. Stored events hold no raw command or output, so rules that need
// them see less on the replayed part; nothing replayed is delivered again,
// and the stored verdicts stand for that part.
func (s *Session) restoreHistory() error {
	if s.db == nil {
		return nil
	}
	evs, err := s.db.EventsOf(s.ID)
	if err != nil || len(evs) == 0 {
		return err
	}
	vs, err := s.db.VerdictsOf(s.ID)
	if err != nil {
		return err
	}
	s.eng.Restoring = true
	defer func() { s.eng.Restoring = false }()
	for _, ev := range evs {
		if ev.Tool == event.ToolShell && ev.CmdNorm == "" {
			ev.CmdNorm = strings.TrimPrefix(ev.Summary, "shell: ")
		}
		ev.Basis = "live"
		if ev.Tool == "checkpoint_check" {
			s.eng.ObserveReliabilityFact(ev)
		} else {
			s.eng.Observe(ev)
		}
	}
	s.parser.ContinueAt(evs[len(evs)-1].Seq + 1)
	s.eng.ReserveIDs(len(vs) + len(s.eng.Verdicts) + 1)
	// the receipt lists the verdicts as they were delivered, not as the
	// replay with less evidence would raise them again
	s.eng.Verdicts = vs
	s.restoreIronLaws(vs)
	s.eng.RestoreReliabilityVerdicts(vs)
	var last *event.Reliability
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Reliability != nil {
			last = evs[i].Reliability
			break
		}
	}
	current := s.reliabilityBoundary()
	if last == nil || last.TaskID != current.TaskID || last.Revision != current.Revision || last.AuthorityHash != current.AuthorityHash {
		s.eng.ResetReliability()
	}
	return nil
}
