package detect

import (
	"sort"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// cmdOf is a shell call's command as the agent wrote it when known (here-
// documents and trailing pipes intact), else the normalized command.
func cmdOf(ev *event.Event) string {
	if ev.Cmd != "" {
		return ev.Cmd
	}
	return ev.CmdNorm
}

// checkInfo identifies the test run a shell call makes: its scope (the whole
// suite or a selection) and a key that stays the same when the same tests run
// through a different wrapper (a here-document, a generated runner script).
type checkInfo struct {
	scope string
	key   string
	tests []string
}

// checkOf reads the tests a call runs: its test stages, the ones its here-
// documents run, and those of a runner script written earlier in the session.
func (e *Engine) checkOf(ev *event.Event) checkInfo {
	if ev.Tool != event.ToolShell {
		return checkInfo{}
	}
	// a remembered runner script is identified by the tests it runs, not by
	// its own (usually fresh) file name
	tests := e.scriptTests(ev)
	scripts := map[string]bool{}
	for _, p := range classify.ExecutedScripts(ev.CmdNorm) {
		if _, ok := e.St.mar.scripts[p]; ok {
			scripts[p] = true
		}
	}
	for _, t := range classify.TestCommands(cmdOf(ev)) {
		run := classify.ExecutedScripts(t)
		if len(run) == 1 && scripts[run[0]] {
			continue
		}
		tests = append(tests, t)
	}
	if len(tests) == 0 {
		return checkInfo{key: runID(ev)}
	}
	scope := classify.ScopeUnknown
	for _, t := range tests {
		switch classify.CommandScope(t) {
		case classify.ScopeFull:
			scope = classify.ScopeFull
		case classify.ScopeTargeted:
			if scope == classify.ScopeUnknown {
				scope = classify.ScopeTargeted
			}
		}
	}
	keys := append([]string(nil), tests...)
	sort.Strings(keys)
	return checkInfo{scope: scope, key: strings.Join(keys, " && "), tests: tests}
}

// scriptTests returns the tests of runner scripts this call runs that were
// written earlier in the session from a here-document.
func (e *Engine) scriptTests(ev *event.Event) []string {
	m := e.St.mar
	if len(m.scripts) == 0 {
		return nil
	}
	var out []string
	for _, p := range classify.ExecutedScripts(ev.CmdNorm) {
		if sc, ok := m.scripts[p]; ok {
			out = append(out, sc.tests...)
		}
	}
	return out
}

// scriptMark is a runner script's content as last written in the session
// and the tests it runs.
type scriptMark struct {
	body  string
	tests []string
}

// maxScriptBody bounds a remembered script.
const maxScriptBody = 256 << 10

func isScriptPath(p string) bool {
	for _, ext := range []string{".py", ".sh", ".bash", ".js", ".mjs", ".ts"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

func (e *Engine) rememberScript(path, body string) {
	m := e.St.mar
	if len(body) > maxScriptBody {
		delete(m.scripts, path)
		return
	}
	m.scripts[path] = scriptMark{body: body, tests: classify.ScriptChecks(path, body)}
}

// trackScripts remembers runner scripts a call writes from a here-document,
// with the tests they run, so running one later is judged by its content.
func (e *Engine) trackScripts(ev *event.Event) {
	if ev.Tool != event.ToolShell || ev.Cmd == "" {
		return
	}
	for path, body := range classify.HeredocFiles(ev.Cmd) {
		if isScriptPath(path) {
			e.rememberScript(path, body)
		}
	}
	// runner scripts written, copied or edited by a Python program
	for _, body := range classify.PythonHeredocs(ev.Cmd) {
		for _, w := range classify.PythonScriptWrites(body) {
			if !isScriptPath(w.Path) {
				continue
			}
			base := w.Content
			if !w.Literal {
				src, ok := e.St.mar.scripts[w.From]
				if !ok {
					delete(e.St.mar.scripts, w.Path) // content no longer known
					continue
				}
				base = src.body
			}
			for _, r := range w.Replaces {
				base = strings.ReplaceAll(base, r[0], r[1])
			}
			e.rememberScript(w.Path, base)
		}
	}
}

// refine makes a call that runs a remembered runner script with tests a
// verification, whatever its command text looks like.
func (e *Engine) refine(ev *event.Event) {
	if ev.Tool == event.ToolShell && ev.Category != event.CatVerify && len(e.scriptTests(ev)) > 0 {
		ev.Category = event.CatVerify
	}
}

// expensive reports whether a test run is costly enough that rerunning it
// after a small change wastes time: the same tests took at least the
// configured time when they last ran. A whole suite that ran in seconds is
// no waste to rerun, so scope alone is not enough.
func (e *Engine) expensive(c checkInfo) bool {
	limit := e.Cfg.Detectors.S1.ExpensiveCheckSec
	if limit <= 0 || c.key == "" {
		return false
	}
	return e.St.mar.checkCost[c.key] >= time.Duration(limit)*time.Second
}

// wide reports whether a test run checks everything a selected run would:
// the whole suite by scope, or a run as long as one.
func (e *Engine) wide(c checkInfo) bool { return c.scope == classify.ScopeFull || e.expensive(c) }

// trackCheckCost records how long a test run took.
func (e *Engine) trackCheckCost(ev *event.Event, c checkInfo) {
	if ev.DurationMS > 0 && c.key != "" {
		e.St.mar.checkCost[c.key] = time.Duration(ev.DurationMS) * time.Millisecond
	}
}
