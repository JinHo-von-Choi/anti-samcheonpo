package detect

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// flakeState is what s5.flake_masking remembers: the tests that failed in
// the session and whether a run failed on time (a timeout or a browser
// test racing the page).
type flakeState struct {
	failed     map[string]bool // failing test ids and their file names
	timingFail bool            // a run failed on time
}

var (
	// a retry count set or raised in a test file, a test config or a test
	// command: Playwright/Vitest retries, --retries, jest.retryTimes,
	// pytest --reruns and flaky markers, nextest retries
	retryAddRe = lazyre.New(`(?i)\bretries\s*[:=]\s*[1-9]|--retries[= ]\s*[1-9]|\bretryTimes\s*\(\s*[1-9]|\bretry\s*:\s*[1-9]|--reruns[= ]\s*[1-9]|@pytest\.mark\.flaky|@flaky\b`)
	// a loop bounded by a number around an attempt counter
	retryLoopRe = lazyre.New(`(?i)\b(?:for|while)\b[^\n]*\b(?:attempt|retr(?:y|ies)|tries)\w*\b[^\n]*\d|\b(?:attempt|retr(?:y|ies)|tries)\w*\s*(?:<|<=|in\s+range\s*\()\s*\d`)
	// a time limit stated as a number
	timeoutNumRe = regexp.MustCompile(`(?i)\b(?:test\.)?(?:set)?timeout\w*\s*[:=(]\s*([\d_]+(?:\.\d+)?)\s*(ms|s|m)?\b|--timeout[= ]\s*([\d_]+)\s*(ms|s|m)?`)
	timingFailRe = lazyre.New(`(?i)timed?\s?out|timeout of \d+|exceeded timeout|Test timeout of`)
	// configuration files that set how tests run
	testConfigRe = lazyre.New(`(?i)^(?:playwright|vitest|jest|cypress|karma|mocha)\.config\.\w+$|^\.mocharc|^pytest\.ini$|^conftest\.py$|^setup\.cfg$|^tox\.ini$|^config\.toml$`)
)

// timeouts reads the time limits in lines, in milliseconds.
func timeouts(lines []string) []float64 {
	var out []float64
	for _, l := range lines {
		for _, m := range timeoutNumRe.FindAllStringSubmatch(l, -1) {
			num, unit := m[1], m[2]
			if num == "" {
				num, unit = m[3], m[4]
			}
			v, err := strconv.ParseFloat(strings.ReplaceAll(num, "_", ""), 64)
			if err != nil {
				continue
			}
			switch strings.ToLower(unit) {
			case "s":
				v *= 1000
			case "m":
				v *= 60_000
			}
			out = append(out, v)
		}
	}
	return out
}

func maxOf(v []float64) float64 {
	m := 0.0
	for _, x := range v {
		m = max(m, x)
	}
	return m
}

// testRunFile reports whether a file decides how tests run: a test file,
// a test runner config, or a script whose changed lines run tests.
func testRunFile(p string, added []string) bool {
	if classify.IsTestPath(p) || testConfigRe.MatchString(path.Base(p)) {
		return true
	}
	for _, l := range added {
		if len(classify.TestCommands(l)) > 0 {
			return true
		}
	}
	return false
}

// trackFlaky records failing runs for s5.flake_masking. Any shell call
// that ran and failed counts, not only checks: a deploy script runs the
// end-to-end tests whose time-outs are then masked.
func (e *Engine) trackFlaky(ev *event.Event) {
	if ev.Tool != event.ToolShell || ev.Background || ev.ExitCode == nil || *ev.ExitCode <= 0 {
		return
	}
	f := &e.St.flake
	if f.failed == nil {
		f.failed = map[string]bool{}
	}
	for _, t := range ev.FailedTests {
		f.failed[t] = true
		file := t
		if i := strings.Index(t, "::"); i > 0 {
			file = t[:i]
		}
		f.failed[path.Base(file)] = true
	}
	if ev.Execution != nil && ev.Execution.FailureType == "timeout" || timingFailRe.MatchString(ev.Text) {
		f.timingFail = true
	}
}

// flakeMasking raises s5.flake_masking when a change to how tests run adds
// retries, raises a time limit or wraps a step in a counted retry loop.
// After a failure of the same test file (or any timing failure, for a
// runner config or script) it is advised; without one it is recorded only.
// A file created in this session, and a contract that allows simplifying
// tests, are not judged. The judgment is per file: the lines are not tied
// to a test. A loop over table cases is not a retry loop.
func (e *Engine) flakeMasking(ev *event.Event, sigs *[]Signal) {
	if e.Contract != nil && e.Contract.SimplifyTests {
		return
	}
	f := e.St.flake
	for _, pf := range ev.Patch {
		p := pf.Path
		if pf.Deleted || e.St.created[p] || contains(ev.Created, p) || !testRunFile(p, pf.Added) {
			continue
		}
		var kinds []string
		retry := false
		for _, l := range pf.Added {
			retry = retry || retryAddRe.MatchString(l)
		}
		if retry {
			kinds = append(kinds, "retries")
		}
		before, after := maxOf(timeouts(pf.Removed)), maxOf(timeouts(pf.Added))
		if before > 0 && after > before {
			kinds = append(kinds, "timeout")
		}
		for _, l := range pf.Added {
			if retryLoopRe.MatchString(l) {
				kinds = append(kinds, "retry_loop")
				break
			}
		}
		if len(kinds) == 0 {
			continue
		}
		related := f.failed[path.Base(p)] || !classify.IsTestPath(p) && f.timingFail
		level, conf := L0, 0.5
		if related {
			level, conf = L1, 0.7
		}
		facts := map[string]any{"path": p, "kind": kinds[0], "kinds": strings.Join(kinds, ","), "target": p, "target_hash": p}
		if before > 0 && after > before {
			facts["timeout_from"], facts["timeout_to"] = int(before), int(after)
		}
		e.add(sigs, Signal{Detector: "S5", Rule: "s5.flake_masking", Confidence: conf, Level: level, Evidence: []int64{ev.Seq}, Estimate: true, Facts: facts})
	}
}
