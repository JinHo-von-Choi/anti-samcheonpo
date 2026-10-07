package live

import (
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

// pendingEdit is an edit made since the last verification.
type pendingEdit struct {
	path, fp string
	seq      int64
}

// editFP fingerprints one file change by its path and trimmed changed lines,
// so the same change written again matches regardless of indentation.
func editFP(pf event.PatchFile) string {
	norm := func(lines []string) string {
		var out []string
		for _, l := range lines {
			if t := strings.TrimSpace(l); t != "" {
				out = append(out, t)
			}
		}
		return strings.Join(out, "\n")
	}
	added, removed := norm(pf.Added), norm(pf.Removed)
	if added == "" && removed == "" {
		return ""
	}
	return fp.Hash("edit/1", pf.Path, added, removed)
}

// failureFP identifies a failure by its error fingerprints and failing tests.
func failureFP(ev *event.Event) string {
	keys := append([]string(nil), ev.ErrFPs...)
	for _, t := range ev.FailedTests {
		keys = append(keys, "test:"+t)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	return fp.Hash(keys...)
}

// trackAttempts is called with s.mu held for every observed event. Edits
// since the last verification become failed attempts of the current task
// revision when the verification fails; a pass discards them.
func (s *Session) trackAttempts(ev *event.Event) {
	if s.task == nil || s.intentRevision == nil || ev.Kind != event.KindTool {
		return
	}
	if ev.Category == event.CatProduce {
		for _, pf := range ev.Patch {
			if f := editFP(pf); f != "" {
				s.pendingEdits = append(s.pendingEdits, pendingEdit{path: pf.Path, fp: f, seq: ev.Seq})
			}
		}
		return
	}
	if !detect.ExecutedVerify(ev) {
		return
	}
	edits := s.pendingEdits
	s.pendingEdits = nil
	failure := failureFP(ev)
	if ev.ExitCode == nil || *ev.ExitCode <= 0 || failure == "" || len(edits) == 0 {
		return
	}
	var rows []ledger.FailedAttempt
	for _, e := range edits {
		a := ledger.FailedAttempt{TaskID: s.task.ID, Revision: s.intentRevision.Number, EditFP: e.fp, Path: e.path, FailureFP: failure, SessionID: s.ID, Agent: s.Agent, Seq: e.seq}
		rows = append(rows, a)
		if _, seen := s.failedEdits[e.fp]; !seen {
			s.failedEdits[e.fp] = a
		}
	}
	if s.db != nil {
		s.recordStorageError(s.db.SaveFailedAttempts(rows))
	}
}

// loadFailedAttempts is called with s.mu held when the intent revision is
// set. Only the same task revision counts: a new revision is a new goal.
func (s *Session) loadFailedAttempts() {
	s.failedEdits = map[string]ledger.FailedAttempt{}
	s.pendingEdits = nil
	if s.db == nil || s.task == nil || s.intentRevision == nil {
		return
	}
	as, err := s.db.FailedAttempts(s.task.ID, s.intentRevision.Number)
	if err != nil {
		s.recordStorageError(err)
		return
	}
	for _, a := range as {
		if _, seen := s.failedEdits[a.EditFP]; !seen {
			s.failedEdits[a.EditFP] = a
		}
	}
}

// attemptPre is called with s.mu held before a write. It flags an edit that
// already failed in this task revision, in this or an earlier session.
func (s *Session) attemptPre(ev *event.Event) *detect.Signal {
	if ev.Category != event.CatProduce || len(s.failedEdits) == 0 {
		return nil
	}
	for _, pf := range ev.Patch {
		a, ok := s.failedEdits[editFP(pf)]
		if !ok {
			continue
		}
		return &detect.Signal{Detector: "S2", Rule: "s2.attempt_repeat", Confidence: 0.85, Level: detect.L1, Evidence: []int64{ev.Seq},
			Facts: map[string]any{"path": pf.Path, "earlier_session": a.SessionID, "earlier_agent": a.Agent, "same_session": a.SessionID == s.ID}}
	}
	return nil
}
