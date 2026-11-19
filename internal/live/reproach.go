package live

import (
	"slices"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/correction"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/reproach"
)

// scolding is a reproach the user gave in this session. The message is not
// kept; the span and the themes are.
type scolding struct {
	seq, start int64
	themes     []string
	hash       string
}

// noteRequest marks the start of a user request and, when the message
// scolds the agent, the span before it. It never judges or blocks anything.
// Called with s.mu held.
func (s *Session) noteRequest(ev *event.Event) {
	start := s.lastRequestSeq
	s.lastRequestSeq = ev.Seq
	s.requestSeqs = append(s.requestSeqs, ev.Seq)
	s.userPrompts++
	s.noteOutcomeRequest(ev)
	var sc *scolding
	if r := reproach.Detect(ev.Text); r.Scolding {
		sc = &scolding{seq: ev.Seq, start: start, themes: r.Themes, hash: r.Hash}
		s.scoldings = append(s.scoldings, *sc)
	}
	found := correction.Extract(ev.Text)
	hash := fp.Hash("correction", ev.Text)
	job := ""
	if s.jobID == "" && s.userPrompts == 1 {
		job = JobID(s.Agent, s.Root, ev.Text)
		s.jobID = job
	}
	if s.db == nil {
		return
	}
	// The ledger work runs after the answer and outside the lock: a write
	// here would hold the prompt hook, and a call that follows would wait.
	db, id, agent, root := s.db, s.ID, s.Agent, s.Root
	s.preWrites.Add(1)
	go func() {
		defer s.preWrites.Done()
		var errs []error
		if sc != nil {
			errs = append(errs, db.SaveReproach(ledger.Reproach{SessionID: id, Seq: sc.seq, StartSeq: sc.start, Agent: agent, Project: root,
				Basis: "live", Themes: sc.themes, TextHash: sc.hash}))
		}
		repeats := 0
		for _, c := range found {
			row, err := db.NoteCorrection(root, id, hash, c, time.Now())
			errs = append(errs, err)
			if err == nil && len(row.Evidence) >= 2 && row.Evidence[len(row.Evidence)-1] == hash {
				// the user gave this correction before
				repeats++
			}
		}
		// read again on every request: the user approves a rule outside
		// the session, and the next request is the first to keep it
		rows, err := db.Corrections(root, correction.StateCandidate, correction.StateActive)
		errs = append(errs, err)
		var runs []int64
		if job != "" {
			errs = append(errs, db.SetJobID(id, job))
			r, err := db.JobRuns(job, id, 30)
			errs = append(errs, err)
			runs = r
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, e := range errs {
			s.recordStorageError(e)
		}
		s.correctionRepeats += repeats
		if err == nil {
			s.corrections = rows
		}
		s.setJobBaseline(runs)
	}()
}

// settleReproaches fills in, once every verdict of the session is known,
// what each scolded span contained: the rules that fired, the themes none
// of them matched, its cost, and the cost of redoing its files in the next
// request. Called with s.mu held at finalization.
func (s *Session) settleReproaches(verdicts []detect.Signal, events []*event.Event) *receipt.Outcome {
	o := &receipt.Outcome{Observed: s.userPrompts > 0, Reproaches: len(s.scoldings), Repeats: s.correctionRepeats, RepeatsKnown: s.db != nil}
	if s.db != nil {
		s.recordStorageError(s.db.SaveReproachSession(s.ID, s.Agent, "live", s.userPrompts, s.userPrompts > 0, s.parser.Session.StartedAt))
	}
	for _, sc := range s.scoldings {
		r := SpanReproach(sc.start, sc.seq, s.nextRequest(sc.seq), sc.themes, verdicts, events)
		r.SessionID, r.Seq, r.StartSeq, r.Agent, r.Project, r.Basis, r.TextHash = s.ID, sc.seq, sc.start, s.Agent, s.Root, "live", sc.hash
		o.SpanMicro += r.SpanMicro
		o.SpanTokens += r.SpanTokens
		o.ReworkMicro += r.ReworkMicro
		o.ReworkTokens += r.ReworkTok
		if s.db != nil {
			s.recordStorageError(s.db.SaveReproach(r))
		}
	}
	o.Priced = o.SpanTokens == 0 || o.SpanMicro > 0
	for _, c := range s.corrections {
		if c.State == correction.StateCandidate {
			o.Candidates = append(o.Candidates, receipt.Candidate{ID: c.ID, Statement: correction.Statement(c.Correction, s.language()), Times: len(c.Evidence)})
		}
	}
	return o
}

// nextRequest is the seq of the request after seq, or 0 for the last one.
func (s *Session) nextRequest(seq int64) int64 {
	for _, r := range s.requestSeqs {
		if r > seq {
			return r
		}
	}
	return 0
}

// SpanReproach measures a scolded span (start, seq) and the request after it
// (seq, next; next 0 runs to the end). It is shared with the transcript
// replay, so a replayed reproach is measured as a live one.
func SpanReproach(start, seq, next int64, themes []string, verdicts []detect.Signal, events []*event.Event) ledger.Reproach {
	r := ledger.Reproach{Themes: themes}
	var fired []string
	for _, v := range verdicts {
		if v.Seq > start && v.Seq < seq && !slices.Contains(fired, v.Rule) {
			fired = append(fired, v.Rule)
		}
	}
	r.Matched, r.Missed = reproach.Split(themes, fired)
	edited := map[string]bool{}
	for _, ev := range events {
		switch {
		case ev.Seq > start && ev.Seq < seq:
			r.SpanMicro += ev.CostMicroKRW
			r.SpanTokens += ev.Usage.Total()
			if ev.Kind == event.KindMessage {
				r.DoneClaimed = detect.IsCompletionClaim(ev.Text)
			}
			if ev.Kind == event.KindTool && (ev.Mutating || ev.Category == event.CatProduce) {
				for _, p := range ev.Paths {
					edited[p] = true
				}
			}
		case ev.Seq > seq && (next == 0 || ev.Seq < next) && ev.Kind == event.KindTool && (ev.Mutating || ev.Category == event.CatProduce):
			if slices.ContainsFunc(ev.Paths, func(p string) bool { return edited[p] }) {
				r.ReworkMicro += ev.CostMicroKRW
				r.ReworkTok += ev.Usage.Total()
			}
		}
	}
	return r
}
