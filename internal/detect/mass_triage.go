package detect

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// triageMark counts the cycles of going back to one findings report and
// editing one or two files in between.
type triageMark struct {
	cycles int
	edits  map[string]bool // files edited since the report was last read
	seqs   []int64
	fired  bool
}

// findingsReportRe names static-analysis and lint result files: SARIF, and
// JSON, XML or text output named after a scanner or as a findings report.
var findingsReportRe = lazyre.New(`(?i)(?:\.sarif(?:\.json)?$|(?:^|[/_.-])(?:semgrep|bandit|eslint|codeql|sonar|trivy|gosec|sast|findings|scan|lint|audit|issues|report|results)[^/]*\.(?:json|sarif|xml|txt|csv)$)`)

// reportOf returns the findings report a read or a read-only shell call
// looks at, "" when none.
func reportOf(ev *event.Event) string {
	if ev.Tool != event.ToolRead && !(ev.Tool == event.ToolShell && ev.Category == event.CatExplore && !ev.Unknown) && ev.Tool != event.ToolSearch {
		return ""
	}
	for _, p := range ev.Paths {
		if findingsReportRe.MatchString(p) {
			return p
		}
	}
	return ""
}

// trackTriage raises s4.serial_triage when the agent keeps going back to one
// findings report and editing one or two files in between, the pattern of
// fixing a long list of findings one at a time. Larger edits in between are
// batch work and start the count over.
func (e *Engine) trackTriage(ev *event.Event, sigs *[]Signal) {
	m := e.St.mar
	if r := reportOf(ev); r != "" {
		t := m.triage[r]
		if t == nil {
			t = &triageMark{edits: map[string]bool{}}
			m.triage[r] = t
		}
		switch n := len(t.edits); {
		case m.lastRead == r && n > 0 && n <= 2:
			t.cycles++
			t.seqs = append(t.seqs, ev.Seq)
		case n > 2:
			t.cycles, t.seqs = 0, nil
		}
		clear(t.edits)
		m.lastRead = r
		if !t.fired && t.cycles >= e.threshold("s4.serial_triage", e.Cfg.Detectors.S4.TriageCycles) && e.Cfg.Detectors.S4.TriageCycles > 0 {
			t.fired = true
			e.add(sigs, Signal{Detector: "S4", Rule: "s4.serial_triage", Confidence: 0.7, Level: L1, Evidence: append([]int64(nil), t.seqs...),
				Facts: map[string]any{"path": r, "cycles": t.cycles}})
		}
		return
	}
	if ev.Category != event.CatProduce || m.lastRead == "" {
		return
	}
	t := m.triage[m.lastRead]
	for _, p := range changedPaths(ev) {
		if p != m.lastRead {
			t.edits[p] = true
		}
	}
}
