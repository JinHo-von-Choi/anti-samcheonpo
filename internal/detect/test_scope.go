package detect

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// scopeState is the source change since the last whole-suite run.
type scopeState struct {
	base     bool  // a whole-suite run was seen
	baseSeq  int64 // that run
	files    map[string]int
	lines    int
	unsized  bool // a change of unknown size (a script wrote the file)
	targeted bool // a selected run passed after the last change
}

// trackScope keeps the change since the last whole-suite run and whether a
// selected run passed after it.
func (e *Engine) trackScope(ev *event.Event) {
	sc := &e.St.mar.scope
	switch {
	case ev.Category == event.CatProduce:
		changed := false
		for _, p := range changedPaths(ev) {
			if IsDocPath(p) || strings.HasPrefix(p, ".samcheonpo/") {
				continue
			}
			if sc.files == nil {
				sc.files = map[string]int{}
			}
			n := ev.AddedLines[p] + ev.RemovedLines[p]
			if _, sized := ev.AddedLines[p]; !sized && ev.RemovedLines[p] == 0 && ev.Tool == event.ToolShell {
				sc.unsized = true
			}
			sc.files[p] += n
			sc.lines += n
			changed = true
		}
		if changed {
			sc.targeted = false
		}
	case ExecutedVerify(ev):
		c := e.checkOf(ev)
		e.trackCheckCost(ev, c)
		switch {
		case e.wide(c):
			*sc = scopeState{base: true, baseSeq: ev.Seq}
		case c.scope != classify.ScopeUnknown:
			if !failing(ev) && len(sc.files) > 0 {
				sc.targeted = true
			}
		}
	}
}

// changedPaths lists the files a write touched.
func changedPaths(ev *event.Event) []string {
	paths := append([]string(nil), ev.Paths...)
	for p := range ev.AddedLines {
		if !contains(paths, p) {
			paths = append(paths, p)
		}
	}
	for p := range ev.RemovedLines {
		if !contains(paths, p) {
			paths = append(paths, p)
		}
	}
	return paths
}

// fullSuiteCheck raises s1.full_suite_local_change for an expensive test run
// (the same tests took expensive_check_sec or longer last time) after a change
// smaller than the local-change limits, when no selected run passed since
// that change. The first whole-suite run of a session is never judged,
// and neither is a run that is a completion check of the accepted contract.
func (e *Engine) fullSuiteCheck(ev *event.Event, live bool) *Signal {
	if ev.Tool != event.ToolShell || ev.Category != event.CatVerify {
		return nil
	}
	c := e.checkOf(ev)
	if !e.expensive(c) {
		return nil
	}
	d := e.Cfg.Detectors.S1
	sc := &e.St.mar.scope
	if !sc.base || sc.targeted || len(sc.files) == 0 || d.LocalChangeFiles <= 0 || d.LocalChangeLines <= 0 {
		return nil
	}
	if len(sc.files) >= d.LocalChangeFiles || sc.lines >= d.LocalChangeLines || e.contractCheck(ev) {
		return nil
	}
	paths := make([]string, 0, len(sc.files))
	for p := range sc.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	cmd := ev.CmdNorm
	if len(c.tests) > 0 {
		cmd = strings.Join(c.tests, " && ")
	}
	size := fmt.Sprintf("파일 %d개 %d줄", len(paths), sc.lines)
	if sc.unsized {
		size = fmt.Sprintf("파일 %d개(스크립트로 고쳐 줄 수 미확인)", len(paths))
	}
	facts := map[string]any{"cmd": cmd, "files": len(paths), "lines": sc.lines, "size": size, "paths": strings.Join(paths, ", "), "target": c.key}
	if d := e.St.mar.checkCost[c.key]; d > 0 {
		facts["minutes"] = int(d.Minutes() + 0.5)
	}
	if s := suggestTargeted(cmd, paths); s != "" {
		facts["suggest"] = s
	}
	if live {
		facts["blocked"] = true
	}
	return &Signal{Detector: "S1", Rule: "s1.full_suite_local_change", Confidence: 0.85, Level: L1, Evidence: []int64{sc.baseSeq, ev.Seq}, Facts: facts}
}

// contractCheck reports whether a run is one of the accepted contract's
// completion checks, which the evidence plan already governs.
func (e *Engine) contractCheck(ev *event.Event) bool {
	if e.Contract == nil || !e.Accepted || ev.ExecFP == "" {
		return false
	}
	for _, c := range e.Contract.MachineChecks() {
		if id, ok := fp.ExecFP(c.Check, ""); ok && id == ev.ExecFP {
			return true
		}
	}
	return false
}

// suggestTargeted names a selected run that covers the changed files, only
// from what the change itself shows: Go packages of changed Go files, and
// changed test files for pytest and the JavaScript runners. Nothing is guessed.
func suggestTargeted(suite string, paths []string) string {
	var tests []string
	pkgs := map[string]bool{}
	for _, p := range paths {
		if strings.HasSuffix(p, ".go") {
			pkgs["./"+path.Dir(p)] = true
		}
		if classify.IsTestPath(p) {
			tests = append(tests, p)
		}
	}
	switch {
	case strings.HasPrefix(suite, "go test") && len(pkgs) > 0:
		var list []string
		for p := range pkgs {
			list = append(list, strings.TrimSuffix(p, "/."))
		}
		sort.Strings(list)
		return "go test " + strings.Join(list, " ")
	case strings.Contains(suite, "pytest") && len(tests) > 0:
		return "pytest " + strings.Join(tests, " ")
	case len(tests) > 0:
		return "변경된 시험 파일만: " + strings.Join(tests, ", ")
	}
	return ""
}
