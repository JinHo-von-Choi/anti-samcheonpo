package detect

import (
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// The functions here judge a call before it runs. The retrospective
// detectors find the same shapes after the fact; these find them while the
// change is still a proposal, so the call can be stopped instead of
// explained afterwards.

// hidingPre reports a check whose failure is swallowed with `|| true`.
//
// A file that hides an error is judged by the repeat check: the pattern is
// keyed by file and kind, so one ignore marker in one file is not yet
// evidence and the same marker in the same file is. A swallowed test result
// has no such key: the command reports success whatever the check said, so
// it is judged while it is still a command.
func (e *Engine) hidingPre(ev *event.Event) *Signal {
	if ev.Tool != event.ToolShell || ev.Category != event.CatVerify {
		return nil
	}
	if !OrTrueRE.MatchString(ev.CmdNorm) {
		return nil
	}
	return &Signal{Detector: "S5", Rule: "s5.error_hiding", Confidence: 0.8, Level: L1,
		Evidence: []int64{ev.Seq}, Facts: map[string]any{"kind": "or_true", "cmd": ev.CmdNorm, "blocked": true}}
}

// scopePre reports a write that lands outside the scope the user accepted.
//
// The scope is the boundary the user set in the contract, so this is a
// decision the user already made. A scope guessed from prose is not: that
// stays an estimate and is reported, not stopped.
func (e *Engine) scopePre(ev *event.Event) *Signal {
	if !isWrite(ev) {
		return nil
	}
	st := e.St
	if !st.scopeKnown || e.Contract == nil {
		return nil
	}
	for _, p := range ev.Paths {
		if p == "" {
			continue
		}
		if in, known := e.inScope(p); known && !in {
			return &Signal{Detector: "S3", Rule: "s3.out_of_scope", Confidence: 0.9, Level: L1,
				Evidence: []int64{ev.Seq}, Facts: map[string]any{"path": p, "count": len(st.outScope) + 1, "blocked": true}}
		}
	}
	return nil
}

// oscillationPre reports a write that would put a file back into a state
// this session already wrote.
//
// The history holds the content each write proposed. A proposal that equals
// an older state and differs from the newest one is the same rollback the
// retrospective detector reports, seen one step earlier.
//
// A check that passed ends the sequence of attempts: a state written before
// that pass is not an oscillation any more, because the agent is returning
// to something that worked rather than repeating a refuted attempt.
func (e *Engine) oscillationPre(ev *event.Event) *Signal {
	if !isWrite(ev) {
		return nil
	}
	st := e.St
	paths := make([]string, 0, len(ev.WriteHashes))
	for p := range ev.WriteHashes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		h := ev.WriteHashes[p]
		if h == "" || strings.HasPrefix(h, "edit:") || strings.HasPrefix(h, "patch:") {
			continue
		}
		hs := st.fileHist[p]
		if len(hs) < 2 {
			continue
		}
		for i := len(hs) - 2; i >= 0; i-- {
			if hs[i].hash != h || hs[len(hs)-1].hash == h {
				continue
			}
			if hs[i].seq < st.oscEnded {
				continue
			}
			return &Signal{Detector: "S2", Rule: "s2.oscillation", Confidence: 0.9, Level: L1,
				Evidence: []int64{hs[i].seq},
				Facts:    map[string]any{"path": p, "back_to_seq": hs[i].seq, "minutes": minutesBetween(st.bySeq[hs[i].seq], ev), "blocked": true}}
		}
	}
	return nil
}

// isWrite reports whether an event proposes a change to a file.
func isWrite(ev *event.Event) bool {
	return ev.Category == event.CatProduce && (ev.Tool == event.ToolWrite || ev.Tool == event.ToolEdit)
}
