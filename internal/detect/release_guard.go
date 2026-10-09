package detect

import (
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testout"
)

// ciMark is the latest remote CI result the agent read that failed.
type ciMark struct {
	seq   int64
	ws    string
	cmd   string
	tests []string // failing tests the CI output named, when it named any
	pkgs  []string // failing Go packages the CI output named
}

// trackRelease records remote CI results the agent read and the releases
// that went out.
func (e *Engine) trackRelease(ev *event.Event) {
	// a call started in the background exits when it is launched; its exit
	// status says nothing about what it runs
	if ev.Tool != event.ToolShell || ev.ExitCode == nil || *ev.ExitCode == -1 || ev.Background {
		return
	}
	m := e.St.mar
	log := classify.CILog(ev.Text)
	switch classify.CIResult(cmdOf(ev), *ev.ExitCode, ev.Text) {
	case classify.CIPassed:
		m.ci = nil
		return
	case classify.CIFailed:
		m.ci = &ciMark{seq: ev.Seq, ws: ev.WSBefore, cmd: ev.CmdNorm, tests: testout.FailedTests(log), pkgs: classify.FailedPackages(log)}
		return
	}
	if m.ci != nil && ev.Category != event.CatVerify && ev.Seq > m.ci.seq {
		// failing tests read back from a saved CI log after the failure
		m.ci.tests = append(m.ci.tests, testout.FailedTests(log)...)
		m.ci.pkgs = append(m.ci.pkgs, classify.FailedPackages(log)...)
	}
	if k := classify.Release(cmdOf(ev)); k != classify.ReleaseNone && k != classify.ReleaseTag && *ev.ExitCode == 0 {
		m.lastRelease, m.released = ev.Seq, true
		if !ev.TS.IsZero() {
			m.releases = append(m.releases, ev.TS)
		}
	}
}

// releaseCheck judges a call that ships a version (pushes tags, creates a
// release, publishes a package). After a failed remote CI result it needs a
// local check that passed on a changed workspace; while a browser test is
// failing on timing it needs that test to pass first; a release after code
// changed since the previous one needs a check passing on that change; and
// releases are bounded per hour. Remote CI is known only from commands the
// agent ran (gh run watch/view/list, gh pr checks, glab ci): with none, its
// state is unknown and nothing is judged on it.
func (e *Engine) releaseCheck(ev *event.Event, live bool) *Signal {
	if ev.Tool != event.ToolShell {
		return nil
	}
	k := classify.Release(cmdOf(ev))
	if k == classify.ReleaseNone || k == classify.ReleaseTag {
		return nil
	}
	m := e.St.mar
	// every release has a new tag; the rule judges releasing as such
	facts := map[string]any{"cmd": ev.CmdNorm, "target": "release"}
	if live {
		facts["blocked"] = true
	}
	if ci := m.ci; ci != nil && !e.reproduced(ci) {
		facts["kind"], facts["ci_cmd"] = "ci_failed", ci.cmd
		return &Signal{Detector: "S5", Rule: "s5.release_without_preflight", Confidence: 0.9, Level: L1, Evidence: []int64{ci.seq}, Facts: facts}
	}
	// shipping again after code changed since the last release, with no check
	// passing on that change: the remote run is being used to find out
	if last := m.lastRelease; m.released && e.St.lastCodeWriteSeq > last && !e.passedSince(e.St.lastCodeWriteSeq, "") {
		facts["kind"] = "unverified_change"
		return &Signal{Detector: "S5", Rule: "s5.release_without_preflight", Confidence: 0.85, Level: L1, Evidence: []int64{last, e.St.lastCodeWriteSeq}, Facts: facts}
	}
	if cmd, seq, ok := e.openUITiming(); ok {
		facts["kind"], facts["failing_cmd"] = "ui_timing", cmd
		return &Signal{Detector: "S5", Rule: "s5.release_without_preflight", Confidence: 0.8, Level: L1, Evidence: []int64{seq}, Facts: facts}
	}
	limit := e.Cfg.Detectors.S5.ReleasesPerHour
	now := ev.TS
	if limit <= 0 || now.IsZero() {
		return nil
	}
	n := 0
	for _, t := range m.releases {
		if now.Sub(t) < time.Hour {
			n++
		}
	}
	if n < limit {
		return nil
	}
	facts["count"], facts["per_hour"] = n, limit
	return &Signal{Detector: "S5", Rule: "s5.release_rate", Confidence: 0.9, Level: L1, Evidence: []int64{ev.Seq}, Facts: facts}
}

// reproduced reports whether the remote failure was checked locally: after
// it, on a changed workspace, a test run passed that covers it. When the CI
// output named failing tests, the run has to name each of their files (or be
// an expensive run); otherwise only an expensive run (the whole suite, or one
// as long as a full run) covers what CI ran. A small selected test passing is
// no evidence about a failure nobody located.
func (e *Engine) reproduced(ci *ciMark) bool {
	evs := e.St.Events
	for i := len(evs) - 1; i >= 0 && evs[i].Seq > ci.seq; i-- {
		ev := evs[i]
		if !ExecutedVerify(ev) || failing(ev) || (ci.ws != "" && ev.WSBefore == ci.ws) {
			continue
		}
		c := e.checkOf(ev)
		if e.wide(c) {
			return true
		}
		cmd := strings.Join(c.tests, " ") + " " + ev.CmdNorm
		if len(ci.pkgs) > 0 && coversPackages(cmd, ci.pkgs) {
			return true
		}
		if len(ci.tests) > 0 && coversTests(cmd, ci.tests) {
			return true
		}
	}
	return false
}

// coversPackages reports whether a command runs every failed Go package:
// a package argument whose path ends the package's import path
// (./internal/live for github.com/x/y/internal/live).
func coversPackages(cmd string, pkgs []string) bool {
	var args []string
	for _, w := range strings.Fields(cmd) {
		w = strings.TrimSuffix(strings.TrimPrefix(w, "./"), "/")
		if w != "" && w != "..." && !strings.HasPrefix(w, "-") {
			args = append(args, w)
		}
	}
	for _, p := range pkgs {
		found := false
		for _, a := range args {
			if p == a || strings.HasSuffix(p, "/"+a) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// coversTests reports whether a command names the file of every test.
func coversTests(cmd string, tests []string) bool {
	for _, t := range tests {
		file := t
		if i := strings.Index(t, "::"); i > 0 {
			file = t[:i]
		}
		if !strings.Contains(cmd, file) {
			return false
		}
	}
	return true
}

// passedSince reports whether a check passed after seq on a workspace other
// than ws.
func (e *Engine) passedSince(seq int64, ws string) bool {
	evs := e.St.Events
	for i := len(evs) - 1; i >= 0 && evs[i].Seq > seq; i-- {
		ev := evs[i]
		if ExecutedVerify(ev) && !failing(ev) && (ws == "" || ev.WSBefore != ws) {
			return true
		}
	}
	return false
}
