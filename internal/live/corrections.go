package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/alignment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/correction"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

// Rules for the user's own corrections.
const (
	// RuleCorrectionRepeat breaks a rule the user approved; it is the
	// user's limit, so it blocks.
	RuleCorrectionRepeat = "s5.correction_repeat"
	// RuleCorrectionCandidate breaks a rule not approved yet; it is counted.
	RuleCorrectionCandidate = "s5.correction_candidate"
)

// requestedNow reports whether the current request asks for act. The
// current request wins over a rule.
func (s *Session) requestedNow(act string) bool {
	for _, d := range s.book.Items {
		if d.Seq == s.lastRequestSeq && d.Action == act && !d.Forbidden {
			return true
		}
	}
	return false
}

// correctionPre checks a tool call against the project's rules. A broken
// approved rule is returned to be refused; a broken candidate is counted.
// Called with s.mu held.
func (s *Session) correctionPre(ev *event.Event) *detect.Signal {
	if len(s.corrections) == 0 {
		return nil
	}
	call := correction.Call{Cmd: ev.Cmd, Paths: ev.Paths, Requested: s.requestedNow}
	if ev.Tool == event.ToolShell {
		call.Full = s.eng.IsFullCheck(ev)
		call.Verify = ev.Category == event.CatVerify
		call.DocsOnly = call.Verify && s.docsOnlySincePass(ev)
	}
	for i := range s.corrections {
		r := &s.corrections[i]
		if !correction.Breaks(r.Correction, call) {
			continue
		}
		return s.correctionSignal(r, ev.Seq)
	}
	return nil
}

func (s *Session) correctionSignal(r *ledger.CorrectionRow, seq int64) *detect.Signal {
	statement := correction.Statement(r.Correction, s.language())
	if r.State != correction.StateActive {
		return &detect.Signal{Detector: "S5", Rule: RuleCorrectionCandidate, Confidence: 0.6, Level: detect.L0, Evidence: []int64{seq},
			Facts: map[string]any{"statement": statement, "correction": r.ID, "target": r.ID}}
	}
	// counted in memory: a ledger write here would hold the pre-tool
	// answer past its deadline, and the call would run unrefused
	r.Violations++
	n := r.Violations
	if s.db != nil {
		id := r.ID
		s.storeAfterAnswer(func() {
			_, err := s.db.NoteCorrectionViolation(id)
			s.recordStorageError(err)
		})
	}
	return &detect.Signal{Detector: "S5", Rule: RuleCorrectionRepeat, Confidence: 0.95, Level: detect.L2, Evidence: []int64{seq},
		Facts: map[string]any{"statement": statement, "correction": r.ID, "target": r.ID, "count": len(r.Evidence) + n}}
}

// correctionsAtStop checks the rules a turn end can break: an owed action
// and the answer's language. Called with s.mu held.
func (s *Session) correctionsAtStop(msg *event.Event) []detect.Signal {
	var sigs []detect.Signal
	for i := range s.corrections {
		r := &s.corrections[i]
		broken := false
		switch {
		case r.Kind == correction.KindWriting && r.Action == correction.ActKoreanAnswer:
			broken = !alignment.WantsEnglish(s.lastRequestText) && alignment.EnglishAnswer(msg.Text)
		case r.Kind == correction.KindRequire:
			broken = detect.IsCompletionClaim(msg.Text) && s.producedSince(s.lastRequestSeq) && !s.didAction(r.Action)
		}
		if broken {
			if v := s.correctionSignal(r, msg.Seq); v != nil {
				sigs = append(sigs, *v)
			}
		}
	}
	return sigs
}

// producedSince reports a change in the current request: a rule to finish
// work with a release applies to work, not to a question.
func (s *Session) producedSince(seq int64) bool {
	for _, ev := range s.eng.St.Events {
		if ev.Seq > seq && ev.Kind == event.KindTool && ev.Category == event.CatProduce {
			return true
		}
	}
	return false
}

// didAction reports a successful command of act in the current request.
func (s *Session) didAction(act string) bool {
	for _, ev := range s.eng.St.Events {
		if ev.Seq <= s.lastRequestSeq || ev.Kind != event.KindTool || ev.Tool != event.ToolShell || ev.Cmd == "" || ev.ExitCode == nil || *ev.ExitCode != 0 {
			continue
		}
		for _, c := range alignment.Classify(ev.Cmd, s.deployCommands()) {
			if c.Action == act && !c.Wrapper {
				return true
			}
		}
	}
	return false
}
