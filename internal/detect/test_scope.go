package detect

import (
	"fmt"
	"path"
	"regexp"
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

// fullBudget counts the whole-suite runs of the current user request.
type fullBudget struct {
	runs         int
	failed       bool // the last whole-suite run failed
	targetedPass bool // a selected run passed since that failure
	// passed holds, per command, the last write seen when it passed
	passed map[string]int64
	// failedAt holds, per command, the last write seen when it failed;
	// unsteady marks a command that both failed and passed on one state of
	// the code, whose result the code alone does not decide
	failedAt map[string]int64
	unsteady map[string]bool
	// started holds the calls counted when live mode let them through
	started map[string]bool
}

var (
	fullRequestRe = regexp.MustCompile(`(?i)(전체|풀)\s*(시험|테스트|검사)|\bfull[- ](test|suite|check)|\ball tests\b|\./\.\.\.`)
	// a request to test under several settings asks for one run per setting
	multiRunRequestRe = regexp.MustCompile(`(?i)(경우|조건|환경|플래그|설정|옵션|버전)[^.\n]{0,24}(모두|각각|둘 ?다|별로|마다)[^.\n]{0,12}(시험|테스트|검사|돌려)|(시험|테스트|검사)[^.\n]{0,12}(경우|조건|환경|플래그|설정|옵션|버전)\s*(별로|마다)|\b(test|run)\b[^.\n]{0,40}\b(with|under|for) (both|each)\b|\b(both|each|every) (flag|setting|config(uration)?|environment|version|option)s?\b`)
	fullRequestNotRe  = regexp.MustCompile(`(?i)((전체|풀)\s*(시험|테스트|검사)|full[- ](test|suite|check)|all tests)[^.\n]{0,16}(말|마$|지 ?마|금지|그만|안 ?돌|don'?t|stop|no more)|(don'?t|do not|stop|no more|never)[^.\n]{0,16}(full[- ](test|suite|check)|all tests)`)
)

// mentionOnly are programs that print or search text: naming a gate to them
// runs nothing.
var mentionOnly = map[string]bool{"echo": true, "printf": true, "grep": true, "rg": true, "sed": true, "awk": true, "cat": true, "head": true, "tail": true, "ls": true, "git": true}

// fullCheck reports a call that runs the whole test suite or a project gate
// named in the settings, however it is wrapped.
func (e *Engine) fullCheck(ev *event.Event) bool {
	if ev.Tool != event.ToolShell {
		return false
	}
	for _, stage := range fp.SplitStages(ev.CmdNorm) {
		stage = strings.TrimLeft(stage, "( ")
		if word, _, _ := strings.Cut(stage, " "); mentionOnly[word] {
			continue
		}
		for _, f := range e.Cfg.Detectors.S1.FullCheckCommands {
			if f != "" && strings.Contains(stage, f) {
				return true
			}
		}
	}
	if ev.Category != event.CatVerify {
		return false
	}
	return e.wide(e.checkOf(ev))
}

// fullBudgetCheck refuses a whole-suite run once the request has used its
// allowance. Two things open it again: the user asking for one in the latest
// message, and a failed whole run whose fix a selected run has confirmed.
func (e *Engine) fullBudgetCheck(ev *event.Event, live bool) *Signal {
	limit := e.Cfg.Detectors.S1.FullChecksPerRequest
	b := &e.St.mar.budget
	if limit <= 0 || b.runs < limit || !e.fullCheck(ev) {
		return nil
	}
	if b.failed && b.targetedPass {
		return nil
	}
	if text := e.St.mar.promptText; (fullRequestRe.MatchString(text) || multiRunRequestRe.MatchString(text)) && !fullRequestNotRe.MatchString(text) {
		return nil
	}
	sc := &e.St.mar.scope
	paths := make([]string, 0, len(sc.files))
	for p := range sc.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	facts := map[string]any{"cmd": ev.CmdNorm, "runs": b.runs, "limit": limit, "target": "full_budget"}
	if s := suggestTargeted(ev.CmdNorm, paths); s != "" {
		facts["suggest"] = s
	}
	if live {
		facts["blocked"] = true
	}
	return &Signal{Detector: "S1", Rule: RuleFullBudget, Confidence: 0.95, Level: L1, Evidence: []int64{ev.Seq}, Facts: facts}
}

// trackFullBudget counts a whole-suite run that started. A background run
// has no exit code yet and counts all the same.
func (e *Engine) trackFullBudget(ev *event.Event) {
	if ev.Tool != event.ToolShell || (ev.ExitCode == nil && !ev.Background) || (ev.ExitCode != nil && *ev.ExitCode == -1) {
		return
	}
	b := &e.St.mar.budget
	if ev.Category == event.CatVerify && ev.ExitCode != nil {
		if b.passed == nil {
			b.passed, b.failedAt, b.unsteady = map[string]int64{}, map[string]int64{}, map[string]bool{}
		}
		w := e.St.lastWriteSeq
		if failing(ev) {
			if at, ok := b.passed[ev.CmdNorm]; ok && at == w {
				b.unsteady[ev.CmdNorm] = true
			}
			b.failedAt[ev.CmdNorm] = w
		} else {
			if at, ok := b.failedAt[ev.CmdNorm]; ok && at == w {
				b.unsteady[ev.CmdNorm] = true
			}
			b.passed[ev.CmdNorm] = w
		}
	}
	if e.fullCheck(ev) {
		if !b.started[ev.CallID] {
			b.runs++
		}
		b.failed, b.targetedPass = ev.ExitCode != nil && failing(ev), false
		return
	}
	if b.failed && ExecutedVerify(ev) && !failing(ev) && e.checkOf(ev).scope == classify.ScopeTargeted {
		b.targetedPass = true
	}
}

// NoteAllowed counts a whole-suite run when the live harness lets it
// through. Its result reaches the engine later, possibly after the next call
// has been judged, and is then not counted again.
func (e *Engine) NoteAllowed(ev *event.Event) {
	if ev.CallID == "" || e.Cfg.Detectors.S1.FullChecksPerRequest <= 0 || !e.fullCheck(ev) {
		return
	}
	b := &e.St.mar.budget
	if b.started[ev.CallID] {
		return
	}
	if b.started == nil {
		b.started = map[string]bool{}
	}
	b.started[ev.CallID] = true
	b.runs++
	b.failed, b.targetedPass = false, false
}

// BeginRequest starts the allowance of a user request once per prompt. The
// live harness calls it when the prompt arrives, because the calls that
// follow can be judged before the prompt is observed.
func (e *Engine) BeginRequest(ev *event.Event) {
	if e.St.mar.request == ev {
		return
	}
	e.St.mar.request = ev
	e.newUserRequest(ev.Text)
}

// inCurrentRequest reports a call that started after the prompt of the
// current request. The result of an earlier call can be observed after the
// next prompt has opened a new allowance, and does not count against it.
func (e *Engine) inCurrentRequest(ev *event.Event) bool {
	r := e.St.mar.request
	return r == nil || ev.Seq == 0 || ev.Seq > r.Seq
}

// newUserRequest starts a new allowance.
func (e *Engine) newUserRequest(text string) {
	m := e.St.mar
	m.budget, m.promptText, m.quality = fullBudget{}, text, qualityState{}
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

// RuleFullBudget is a whole-suite run beyond the allowance of one request.
const RuleFullBudget = "s1.full_suite_budget"

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
