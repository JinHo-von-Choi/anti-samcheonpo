package detect

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// uiMark follows one browser-test command failing on timing.
type uiMark struct {
	cmd   string
	count int
	tweak bool // a test edit in between only changed literals, timeouts or sleeps
	seqs  []int64
	fired bool
}

// trackUITiming raises s2.flaky_ui_race when a browser-test command keeps
// failing on timing (fp.FailUITiming) and the edits in between only adjust
// expected text, timeout numbers or sleeps: the test races the page, and
// such edits move the race instead of waiting for the state.
func (e *Engine) trackUITiming(ev *event.Event, sigs *[]Signal) {
	m := e.St.mar
	if ev.Category == event.CatProduce {
		if len(m.ui) > 0 && tweakOnly(ev) {
			for _, u := range m.ui {
				u.tweak = true
			}
		}
		return
	}
	if !ExecutedVerify(ev) || runID(ev) == "" {
		return
	}
	id := runID(ev)
	if !failing(ev) || fp.DiagnoseFailure(*ev.ExitCode, ev.Text).Class != fp.FailUITiming {
		delete(m.ui, id)
		return
	}
	u := m.ui[id]
	if u == nil {
		u = &uiMark{cmd: ev.CmdNorm}
		m.ui[id] = u
	}
	u.count++
	u.seqs = append(u.seqs, ev.Seq)
	need := e.threshold("s2.flaky_ui_race", e.Cfg.Detectors.S2.UITimingRepeats)
	if u.fired || need <= 0 || !(u.count >= need && u.tweak || u.count > need) {
		return
	}
	u.fired = true
	waste := e.markWaste(u.seqs[1:], "S2")
	kind := "repeat"
	if u.tweak {
		kind = "tweak"
	}
	e.add(sigs, Signal{Detector: "S2", Rule: "s2.flaky_ui_race", Confidence: 0.75, Level: L1, WasteMicro: waste, Evidence: append([]int64(nil), u.seqs...),
		Facts: map[string]any{"cmd": u.cmd, "count": u.count, "kind": kind}})
}

// openUITiming returns a browser-test command whose last run failed on
// timing and has not passed since.
func (e *Engine) openUITiming() (cmd string, seq int64, ok bool) {
	for _, u := range e.St.mar.ui {
		if last := u.seqs[len(u.seqs)-1]; !ok || last > seq {
			cmd, seq, ok = u.cmd, last, true
		}
	}
	return cmd, seq, ok
}

var (
	literalRe  = lazyre.New("\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'|`[^`]*`|\\b\\d+(?:\\.\\d+)?\\b")
	waitCallRe = lazyre.New(`(?i)\b(?:waitForTimeout|sleep|setTimeout|cy\.wait|Thread\.sleep|time\.sleep)\s*\(|\btimeout\s*[:=]`)
)

// tweakOnly reports whether a write changes only test files, and in them
// only string or number literals line for line, or adds a fixed wait.
func tweakOnly(ev *event.Event) bool {
	if len(ev.Patch) == 0 {
		return false
	}
	for _, pf := range ev.Patch {
		if pf.Deleted || !classify.IsTestPath(pf.Path) {
			return false
		}
		waits := false
		for _, l := range pf.Added {
			if waitCallRe.MatchString(l) {
				waits = true
				break
			}
		}
		if waits {
			continue
		}
		if len(pf.Added) == 0 || len(pf.Added) != len(pf.Removed) {
			return false
		}
		for i := range pf.Added {
			if literalRe.ReplaceAllString(pf.Added[i], "_") != literalRe.ReplaceAllString(pf.Removed[i], "_") {
				return false
			}
		}
	}
	return true
}
