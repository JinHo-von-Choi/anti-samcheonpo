package detect

import (
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testout"
)

// shellRun builds a shell event classified the way the adapters do it.
func shellRun(cmd string, exit int, out string, dur time.Duration) *event.Event {
	norm, _ := fp.NormalizeCmd(cmd)
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: cmd, CmdNorm: norm, CmdFP: fp.CmdFP(norm), ExitCode: &exit, Text: out,
		DurationMS: dur.Milliseconds()}
	classify.Initial(ev, classify.Options{})
	ev.ErrFPs = fp.ErrorFPs(out)
	ev.FailedTests = testout.FailedTests(out)
	ev.ResultFP = fp.ResultFP(ev.ExitCode, ev.ErrFPs, ev.FailedTests)
	return ev
}

func (b *builder) run(cmd string, exit int, out string, dur time.Duration) []Signal {
	return b.next(shellRun(cmd, exit, out, dur))
}

// pre asks the live checks about a call before it runs.
func (b *builder) pre(cmd string) *Signal {
	ev := shellRun(cmd, 0, "", 0)
	ev.ExitCode = nil
	ev.Seq, ev.TS, ev.WSBefore = b.seq, b.ts.Add(time.Second), b.ws
	withExecFP(ev)
	if deny, s := b.e.PreCheck(ev); deny {
		return s
	}
	return b.e.PreGuard(ev)
}

func TestFullSuiteAfterLocalChangeNeedsASelectedRunFirst(t *testing.T) {
	b := newB(t, "lib/calc.py 고쳐 줘", nil)
	b.run("pytest -q", 1, "FAILED tests/test_calc.py::test_add - assert 1 == 2\n1 failed, 400 passed", 35*time.Minute)
	b.write("lib/calc.py", "h1", []string{"return a + b"}, []string{"return a - b"})
	s := b.pre("pytest -q")
	if s == nil || s.Rule != "s1.full_suite_local_change" || s.Facts["files"] != 1 || s.Facts["minutes"] != 35 || s.Facts["blocked"] != true {
		t.Fatalf("a 35-minute suite after a one-line change is judged: %+v", s)
	}
	if s.Facts["target"] == "" {
		t.Fatal("the judgement names the suite as a stable target")
	}
	b.run("pytest -q tests/test_calc.py", 0, "1 passed", 2*time.Second)
	if s := b.pre("pytest -q"); s != nil {
		t.Fatalf("after the selected test passed, one full run is allowed: %+v", s)
	}
}

func TestFastFullSuiteIsNotJudged(t *testing.T) {
	b := newB(t, "x", nil)
	b.run("pytest", 1, "1 failed", 2*time.Second)
	b.write("lib/calc.py", "h1", []string{"a"}, []string{"b"})
	if s := b.pre("pytest"); s != nil {
		t.Fatalf("a suite that runs in seconds is no waste to rerun: %+v", s)
	}
}

func TestSuiteRunThroughAWrittenRunnerScriptIsRecognized(t *testing.T) {
	b := newB(t, "x", nil)
	script := "cat > /tmp/run-gates-a.py <<'PY'\nimport subprocess, sys\nsys.exit(subprocess.run([sys.executable, 'scripts/gates.py']).returncode)\nPY"
	b.run(script, 0, "", 0)
	ev := shellRun("python3 -I /tmp/run-gates-a.py 313", 1, "", 30*time.Minute)
	b.next(ev)
	if ev.Category != event.CatVerify {
		t.Fatalf("running a remembered runner script is a test run: %s", ev.Category)
	}
	// a copy edited by a Python program runs the same suite
	b.run("python3 - <<'PY'\nfrom pathlib import Path\ns=Path('/tmp/run-gates-a.py').read_text().replace('sys.exit', 'raise SystemExit')\nPath('/tmp/run-gates-b.py').write_text(s)\nPY", 0, "", 0)
	b.write("netwatcher/app.py", "h2", []string{"x = 1"}, nil)
	s := b.pre("python3 -I /tmp/run-gates-b.py 313")
	if s == nil || s.Rule != "s1.full_suite_local_change" {
		t.Fatalf("the copied runner is the same 30-minute suite: %+v", s)
	}
	if b.pre("cat /tmp/run-gates-b.py") != nil {
		t.Fatal("reading a runner script is not running it")
	}
}

func TestLongRunNeedsAShortProbeAndGrowsInSteps(t *testing.T) {
	b := newB(t, "x", nil)
	s := b.pre("k6 run --duration 2h load.js")
	if s == nil || s.Rule != "s1.unprobed_long_run" || s.Facts["kind"] != "explicit" || s.Facts["allowed"] != 300 {
		t.Fatalf("a two-hour load test without a probe is judged: %+v", s)
	}
	b.run("k6 run --duration 30s load.js", 0, "ok", 30*time.Second)
	if s := b.pre("k6 run --duration 2h load.js"); s == nil {
		t.Fatal("a 30-second probe does not license two hours")
	}
	b.run("k6 run --duration 5m load.js", 0, "ok", 5*time.Minute)
	if s := b.pre("k6 run --duration 20m load.js"); s != nil {
		t.Fatalf("after a 5-minute pass, 20 minutes is within six times: %+v", s)
	}
	if s := b.pre("k6 run --duration 2h load.js"); s == nil || s.Facts["allowed"] != 1800 {
		t.Fatalf("two hours is past six times the longest pass: %+v", s)
	}
	b.write("load.js", "h", []string{"x"}, nil)
	if s := b.pre("k6 run --duration 20m load.js"); s == nil {
		t.Fatal("a code change invalidates the probes")
	}
	if s := b.pre("timeout 600 npm run test:int"); s != nil {
		t.Fatalf("a timeout cap on an ordinary test is not a stated run time: %+v", s)
	}
}

func TestSerialTriageOfAFindingsReport(t *testing.T) {
	b := newB(t, "보안 지적 고쳐 줘", nil)
	var got []Signal
	for i := 0; i < 11; i++ {
		got = append(got, b.read("reports/semgrep.json")...)
		b.write("src/f"+string(rune('a'+i))+".py", "h", []string{"x"}, []string{"y"})
	}
	n := 0
	for _, s := range got {
		if s.Rule == "s4.serial_triage" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("one serial-triage finding per report, got %d", n)
	}
}

func TestRepeatedRejectionGoesToTheUser(t *testing.T) {
	b := newB(t, "x", nil)
	out := "docs/README.md:3: numbers changed (rejected)"
	b.run("vale-like docs/README.md", 4, out, time.Second)
	b.write("docs/README.md", "h", []string{"a"}, []string{"b"})
	v := fired(b.run("vale-like docs/README.md", 4, out, time.Second), "s2.verifier_deadlock")
	if v == nil || v.Level != L2 || v.Facts["kind"] != "repeat" {
		t.Fatalf("an edit that does not change the rejection goes to the user: %+v", v)
	}
	if s := b.pre("vale-like docs/README.md"); s == nil || s.Rule != "s2.verifier_deadlock" || s.Facts["kind"] != "unchanged_rerun" {
		t.Fatalf("the same input again is refused before it runs: %+v", s)
	}
	w := fired(b.msg("검사 도구가 계속 거부합니다. 어떻게 진행할까요?"), "s2.verifier_deadlock")
	if w == nil || w.Facts["kind"] != "waiting" {
		t.Fatalf("stopping to ask while rejected is put to the user: %+v", w)
	}
}

func TestReleaseAfterFailedCINeedsLocalReproduction(t *testing.T) {
	b := newB(t, "x", nil)
	b.run("pytest -q", 0, "400 passed", 30*time.Minute)
	b.run("gh run watch 123 --exit-status", 1, "X build failed", time.Minute)
	if s := b.pre("git push origin v1.2.3"); s == nil || s.Rule != "s5.release_without_preflight" || s.Facts["kind"] != "ci_failed" || s.Facts["target"] != "release" {
		t.Fatalf("a release right after a failed CI run is judged: %+v", s)
	}
	b.write("lib/x.py", "h", []string{"a"}, []string{"b"})
	b.run("pytest -q tests/test_x.py", 0, "3 passed", time.Second)
	if s := b.pre("git push origin v1.2.3"); s == nil {
		t.Fatal("a small test unrelated to an unlocated CI failure is no reproduction")
	}
	b.run("pytest -q", 0, "400 passed", 30*time.Minute)
	if s := b.pre("git push origin v1.2.3"); s != nil {
		t.Fatalf("the full suite passing locally covers what CI ran: %+v", s)
	}
}

func TestReleaseAfterCIFailureNamingATestNeedsThatTest(t *testing.T) {
	b := newB(t, "x", nil)
	b.run("gh run view 9 --log-failed", 0, "FAILED tests/test_ui.py::test_banner - assert 'a' == 'b'", time.Second)
	b.write("app/ui.py", "h", []string{"a"}, []string{"b"})
	b.run("pytest tests/test_api.py", 0, "1 passed", time.Second)
	if s := b.pre("gh release create v2.0.0"); s == nil {
		t.Fatal("another test passing does not cover the named failure")
	}
	b.run("pytest tests/test_ui.py", 0, "1 passed", time.Second)
	if s := b.pre("gh release create v2.0.0"); s != nil {
		t.Fatalf("the failed test passing locally is the reproduction: %+v", s)
	}
}

func TestReleaseRateAndUnverifiedRerelease(t *testing.T) {
	b := newB(t, "x", nil)
	b.run("git push origin v1.0.0", 0, "", time.Second)
	b.write("lib/x.py", "h", []string{"a"}, []string{"b"})
	if s := b.pre("git push origin v1.0.1"); s == nil || s.Facts["kind"] != "unverified_change" {
		t.Fatalf("shipping a change no check ran on is judged: %+v", s)
	}
	b.run("pytest tests/test_x.py", 0, "1 passed", time.Second)
	b.run("git push origin v1.0.1", 0, "", time.Second)
	if s := b.pre("git push origin v1.0.2"); s == nil || s.Rule != "s5.release_rate" || s.Facts["count"] != 2 {
		t.Fatalf("a third release within the hour is judged: %+v", s)
	}
}

func TestBrowserTimingRaceAndReleaseWhileItFails(t *testing.T) {
	b := newB(t, "x", nil)
	out := "Error: locator.click: Timeout 5000ms exceeded.\nwaiting for locator('#save')"
	b.run("npx playwright test tests/save.spec.ts", 1, out, 20*time.Second)
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{"tests/save.spec.ts"},
		AddedLines: map[string]int{"tests/save.spec.ts": 1}, RemovedLines: map[string]int{"tests/save.spec.ts": 1},
		Patch: []event.PatchFile{{Path: "tests/save.spec.ts", Added: []string{"await page.click('#save', { timeout: 10000 })"}, Removed: []string{"await page.click('#save', { timeout: 5000 })"}}}}
	b.next(ev)
	v := fired(b.run("npx playwright test tests/save.spec.ts", 1, out, 20*time.Second), "s2.flaky_ui_race")
	if v == nil || v.Facts["kind"] != "tweak" {
		t.Fatalf("raising a timeout and failing on timing again is a race: %+v", v)
	}
	if s := b.pre("git push origin v3.0.0"); s == nil || s.Facts["kind"] != "ui_timing" {
		t.Fatalf("a release while the browser test fails on timing is judged: %+v", s)
	}
}

func TestMaskedExitFailureCountsAsRepeat(t *testing.T) {
	b := newB(t, "x", nil)
	cmd := ".venv/bin/python -m pytest tests/test_a.py > /tmp/o.txt 2>&1; tail -5 /tmp/o.txt"
	out := "FAILED tests/test_a.py::test_x - assert 1 == 2\n1 failed"
	var got []Signal
	for i := 0; i < 3; i++ {
		got = append(got, b.run(cmd, 0, out, time.Second)...)
		b.write("lib/a.py", "h"+string(rune('0'+i)), []string{"x"}, []string{"y"})
	}
	if fired(got, "s2.stuck_error") == nil {
		t.Fatal("a failure hidden behind tail is still the same failure three times")
	}
}

func TestBlindFailuresRepeatWithoutOutput(t *testing.T) {
	b := newB(t, "x", nil)
	var got []Signal
	for i := 0; i < 3; i++ {
		got = append(got, b.run("python scripts/gates.py", 1, "", 10*time.Minute)...)
		b.write("lib/a.py", "h"+string(rune('0'+i)), []string{"x"}, []string{"y"})
	}
	v := fired(got, "s2.stuck_error")
	if v == nil {
		t.Fatal("the same gate failing after each edit with its output in a log is a repeat")
	}
}

func TestSessionLengthTiersAndCeiling(t *testing.T) {
	b := newB(t, "x", nil)
	b.e.Cfg.Detectors.S8.CeilingHours = 5
	var got []Signal
	// 16 minutes between events keeps the session active
	for i := 0; i < 20; i++ {
		b.ts = b.ts.Add(16 * time.Minute)
		got = append(got, b.read("a.go")...)
	}
	levels := map[Level]bool{}
	for _, s := range got {
		if s.Rule == "s8.session_long" {
			levels[s.Level] = true
		}
	}
	if !levels[L0] || !levels[L2] {
		t.Fatalf("two hours is a notice, four asks the user: %v", levels)
	}
	if v := fired(got, "s8.session_ceiling"); v == nil || v.Level != L3 {
		t.Fatalf("the configured ceiling stops the session: %+v", v)
	}
	write := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{"a.go"}}
	if s := b.e.PreGuard(write); s == nil || s.Rule != "s8.session_ceiling" {
		t.Fatalf("past the ceiling a write is refused: %+v", s)
	}
	if s := b.e.PreGuard(&event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore}); s != nil {
		t.Fatal("reading stays allowed so the agent can report")
	}
	if _, ok := b.e.ExtendCeiling(2, 0); !ok {
		t.Fatal("a set ceiling can be extended")
	}
	if s := b.e.PreGuard(write); s != nil {
		t.Fatalf("after the extension the write runs: %+v", s)
	}
}

func TestLongPauseIsNotActiveTime(t *testing.T) {
	b := newB(t, "x", nil)
	b.read("a.go")
	b.ts = b.ts.Add(10 * time.Hour)
	b.read("a.go")
	if active, _, _ := b.e.SessionLength(); active > time.Hour {
		t.Fatalf("a night away is not work: %v", active)
	}
}

// The failed CI run's log names a Go package; running that whole package
// locally until it passes is the reproduction, whatever spelling the local
// command uses for the package.
func TestReleaseAfterCIFailureNamingAGoPackage(t *testing.T) {
	b := newB(t, "x", nil)
	log := "native-package (windows-11-arm)\tSource checks\t2026-10-09T14:28:11.3483757Z --- FAIL: TestSummary (0.93s)\n" +
		"native-package (windows-11-arm)\tSource checks\t2026-10-09T14:28:11.3492061Z FAIL\tgithub.com/x/y/internal/live\t63.846s\n"
	b.run("gh run view 37 --log-failed | tail -20", 0, log, time.Second)
	b.write("internal/live/live_test.go", "h", []string{"a"}, []string{"b"})
	b.run("go test ./internal/adapter", 0, "ok", time.Second)
	if s := b.pre("git push origin v0.5.0"); s == nil || s.Facts["kind"] != "ci_failed" {
		t.Fatalf("another package passing does not cover the failed one: %+v", s)
	}
	b.run("go test -race ./internal/live ./internal/adapter", 0, "ok", 3*time.Minute)
	if s := b.pre("git push origin v0.5.0"); s != nil {
		t.Fatalf("the failed package passing locally is the reproduction: %+v", s)
	}
}

// A call started in the background exits at launch: its exit status is not a
// test result or a CI result.
func TestBackgroundRunIsNotAResult(t *testing.T) {
	b := newB(t, "x", nil)
	b.run("gh run watch 37 --exit-status", 1, "X failed", time.Second)
	ev := shellRun("gh run watch 38 --exit-status", 0, "Command running in background", 0)
	ev.Background = true
	b.next(ev)
	run := shellRun("pytest -q", 0, "Command running in background", 0)
	run.Background = true
	b.next(run)
	if ExecutedVerify(run) {
		t.Fatal("a background test run has no result yet")
	}
	if s := b.pre("git push origin v1.0.0"); s == nil || s.Facts["kind"] != "ci_failed" {
		t.Fatalf("launching a CI watch in the background does not clear the failure: %+v", s)
	}
}

// A release that went out unchecked and then failed CI is waste: the
// release and every call up to the failure (waiting on and polling CI).
func TestUncheckedReleaseThatFailsCIIsWaste(t *testing.T) {
	b := newB(t, "x", nil)
	b.run("gh run watch 1 --exit-status", 1, "X failed", time.Second)
	push := shellRun("git push origin v1.0.1", 0, "", time.Second)
	b.next(push)
	if _, ok := b.e.St.Wasted[push.Seq]; ok {
		t.Fatal("not waste before CI answers")
	}
	poll := shellRun("gh run list --limit 1", 0, "in_progress", time.Second)
	b.next(poll)
	watch := shellRun("gh run watch 2 --exit-status", 1, "X failed again", time.Second)
	b.next(watch)
	for _, ev := range []*event.Event{push, poll, watch} {
		if _, ok := b.e.St.Wasted[ev.Seq]; !ok {
			t.Fatalf("event %d (%s) is waste after the unchecked release failed CI", ev.Seq, ev.CmdNorm)
		}
	}
	_, _, waste, _ := b.e.St.UsageSummary()
	if waste == 0 {
		t.Fatal("the waste share counts it")
	}
}

// An expensive rerun after a small change, run anyway, is waste.
func TestExpensiveRerunAfterSmallChangeIsWaste(t *testing.T) {
	b := newB(t, "x", nil)
	b.run("pytest -q", 1, "1 failed", 30*time.Minute)
	b.write("lib/a.py", "h", []string{"a"}, []string{"b"})
	rerun := shellRun("pytest -q", 1, "1 failed", 30*time.Minute)
	b.next(rerun)
	if _, ok := b.e.St.Wasted[rerun.Seq]; !ok {
		t.Fatal("the full rerun after a one-line change is waste")
	}
}

// Rerunning the same failed CI run without a change and failing again is a
// remote identical rerun: the rerun and the wait for it are waste.
func TestCIRerunThatFailsAgainIsWaste(t *testing.T) {
	b := newB(t, "x", nil)
	b.run("gh run watch 5 --exit-status", 1, "X failed", time.Second)
	rerun := shellRun("gh run rerun 5 --failed", 0, "", time.Second)
	b.next(rerun)
	watch := shellRun("gh run watch 5 --exit-status", 1, "X failed", time.Second)
	b.next(watch)
	for _, ev := range []*event.Event{rerun, watch} {
		if _, ok := b.e.St.Wasted[ev.Seq]; !ok {
			t.Fatalf("event %d (%s) is waste", ev.Seq, ev.CmdNorm)
		}
	}
	c := newB(t, "x", nil)
	c.run("gh run watch 5 --exit-status", 1, "X failed", time.Second)
	again := shellRun("gh run rerun 5 --failed", 0, "", time.Second)
	c.next(again)
	c.run("gh run watch 5 --exit-status", 0, "✓ passed", time.Second)
	if _, ok := c.e.St.Wasted[again.Seq]; ok {
		t.Fatal("a rerun that passes (a flaky failure) is not waste")
	}
}
