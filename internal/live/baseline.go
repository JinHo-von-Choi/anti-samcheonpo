package live

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/mutants"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testout"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testsel"
)

// A baseline comparison runs the agent's passing test command once more on
// the code with the session's feature changes undone and its test changes
// kept. Undoing the whole change is the largest single mutation: a test that
// still passes without it does not check it.

// Baseline outcomes.
const (
	BaselineVerified    = "verified"           // a changed test fails without the change
	BaselinePass        = "baseline_pass"      // the tests pass without the change
	BaselinePreserved   = "behavior_preserved" // a refactor: passing without the change is the point
	BaselineWeak        = "build_failure"      // fails only because new code is missing
	BaselineEnv         = "environment"        // fails for a cause outside the code
	BaselineUndecided   = "undetermined"       // fails, but not in the changed tests
	BaselineTimeout     = "timeout"
	BaselineNotCompared = "not_compared"
)

const (
	baselineMinTimeout = 10 * time.Second
	baselineMaxTimeout = 120 * time.Second
	baselineGitTimeout = 30 * time.Second
	// baselineConflictWait bounds the wait for another heavy run in the
	// folder to end before a comparison starts
	baselineConflictWait = 30 * time.Second
)

// BaselineResult is the outcome of one comparison.
type BaselineResult struct {
	Seq     int64     `json:"seq"`
	Cmd     string    `json:"cmd"`
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason,omitempty"`
	Tests   []string  `json:"tests,omitempty"`  // changed test files the command ran
	Failed  []string  `json:"failed,omitempty"` // changed tests that failed without the change
	At      time.Time `json:"at"`
}

// baselineState is the session's comparison state, under s.mu.
type baselineState struct {
	start   string // tree of the workspace when the session began ("" unknown)
	startOK bool
	running bool
	results []BaselineResult
	done    map[string]bool // commands already compared at one workspace state
	// uncomparedNoted is set once the session recorded why its checks
	// cannot be compared at all
	uncomparedNoted bool
	// snapping is set while the start tree is taken; a write seen meanwhile
	// sets startDirty, and the tree is then not the state before the session
	// (startLost)
	snapping, startDirty, startLost bool
}

// noteUncompared records, once, why no passing check of the session can be
// compared (the project is not a git work tree, the start state is unknown):
// only when the check ran a test the session changed and the session changed
// feature code. The test files are found off the hook path. Called with s.mu
// held.
func (s *Session) noteUncompared(ev *event.Event, cmd, reason string) {
	changedTests := map[string]bool{}
	for _, c := range s.eng.TestChanges() {
		changedTests[c.Path] = true
	}
	feature := false
	for _, e := range s.byTool {
		if e.Category != event.CatProduce {
			continue
		}
		for _, p := range e.Paths {
			if r := s.projectRel(p); r != "" && featureSide(r) {
				feature = true
			}
		}
	}
	if len(changedTests) == 0 || !feature {
		return
	}
	s.baseline.uncomparedNoted = true
	seq, root := ev.Seq, s.Root
	go func() {
		var tests []string
		for _, f := range testsel.Resolve(root, cmd).Files {
			if changedTests[f] {
				tests = append(tests, f)
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(tests) == 0 {
			s.baseline.uncomparedNoted = false
			return
		}
		s.baseline.results = append(s.baseline.results, BaselineResult{Seq: seq, Cmd: short(cmd, 200), Outcome: BaselineNotCompared,
			Reason: reason, Tests: tests, At: time.Now()})
	}()
}

// projectRel turns a path an event names into a project-relative slash
// path, or "" outside the project.
func (s *Session) projectRel(p string) string {
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(filepath.Clean(p))
	}
	r, err := filepath.Rel(s.Root, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(r)
}

// baselineDir holds the comparison work trees and the session start trees.
func baselineDir() string { return filepath.Join(config.Home(), "baseline") }

func sessionStartFile(id string) string {
	h := sha256.Sum256([]byte(id))
	return filepath.Join(baselineDir(), "start-"+hex.EncodeToString(h[:8]))
}

// SessionStartTree returns the tree recorded for the workspace when the
// session began, when one was recorded.
func SessionStartTree(id string) (string, bool) {
	b, err := os.ReadFile(sessionStartFile(id))
	if err != nil {
		return "", false
	}
	t := string(bytes.TrimSpace(b))
	return t, t != ""
}

// recordSessionStart keeps the tree of the workspace as it was when the
// session began, tracked and untracked files alike. A session the daemon
// resumes keeps the tree recorded the first time. It runs off the hook path.
func (s *Session) recordSessionStart() {
	s.mu.Lock()
	eligible := s.baselineEligible() && !s.baseline.startOK && !s.baseline.snapping
	if eligible {
		s.baseline.snapping, s.baseline.startDirty, s.baseline.startLost = true, false, false
	}
	s.mu.Unlock()
	if !eligible {
		return
	}
	tree, recorded, ok := s.takeSessionStart()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.baseline.snapping = false
	switch {
	case !ok:
	case s.baseline.startDirty && !recorded:
		// a write may have reached the tree: it is not the state before
		s.baseline.startLost = true
	default:
		if !recorded {
			if err := os.MkdirAll(baselineDir(), 0o700); err == nil {
				_ = os.WriteFile(sessionStartFile(s.ID), []byte(tree+"\n"), 0o600)
			}
		}
		s.baseline.start, s.baseline.startOK = tree, true
	}
}

// takeSessionStart reads the start tree a resumed session recorded
// (recorded), or takes it.
func (s *Session) takeSessionStart() (tree string, recorded, ok bool) {
	if s.Root == "" || !s.ws.IsGit() {
		return "", false, false
	}
	if b, err := os.ReadFile(sessionStartFile(s.ID)); err == nil && len(bytes.TrimSpace(b)) > 0 {
		return string(bytes.TrimSpace(b)), true, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), baselineGitTimeout)
	defer cancel()
	tree, err := snapshotTree(ctx, s.Root)
	return tree, false, err == nil
}

// noteStartWrite marks the start tree being taken as possibly holding the
// session's own change. Called with s.mu held for each call that may write.
func (s *Session) noteStartWrite(ev *event.Event) {
	if s.baseline.snapping && (ev.Category == event.CatProduce || ev.Mutating || ev.Unknown) {
		s.baseline.startDirty = true
	}
}

// runGit runs one git command in root and returns its trimmed output.
func runGit(ctx context.Context, root string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// tempIndex returns a path for a throwaway git index outside the project.
func tempIndex() (string, func(), error) {
	f, err := os.CreateTemp("", "samcheonpo-index-*")
	if err != nil {
		return "", func() {}, err
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return name, func() { os.Remove(name); os.Remove(name + ".lock") }, nil
}

// snapshotTree writes the workspace, tracked and untracked files that git
// does not ignore, as a tree object without touching the index or the work
// tree.
func snapshotTree(ctx context.Context, root string) (string, error) {
	idx, cleanup, err := tempIndex()
	if err != nil {
		return "", err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + idx}
	// a copy of the project's index keeps the file stats, so adding the work
	// tree hashes only the files that changed instead of every file
	if p, err := runGit(ctx, root, nil, "rev-parse", "--git-path", "index"); err == nil {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if b, err := os.ReadFile(p); err == nil && os.WriteFile(idx, b, 0o600) == nil {
			if _, err := runGit(ctx, root, env, "add", "-A", "--", "."); err == nil {
				if tree, err := runGit(ctx, root, env, "write-tree"); err == nil {
					return tree, nil
				}
			}
			os.Remove(idx)
		}
	}
	if _, err := runGit(ctx, root, nil, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		if _, err := runGit(ctx, root, env, "read-tree", "HEAD"); err != nil {
			return "", err
		}
	}
	if _, err := runGit(ctx, root, env, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	return runGit(ctx, root, env, "write-tree")
}

// changedFiles lists the paths that differ between two trees.
func changedFiles(ctx context.Context, root, a, b string) ([]string, error) {
	out, err := runGit(ctx, root, nil, "diff-tree", "-r", "--no-renames", "--name-only", a, b)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// testSide reports a path that belongs to the tests: a test file, a test
// collection file, or a file under a test folder (fixtures, snapshots).
func testSide(p string) bool {
	if classify.IsTestPath(p) || classify.IsTestExpectationPath(p) {
		return true
	}
	for _, d := range strings.Split(path.Dir(p), "/") {
		if d == "testdata" || d == "fixtures" {
			return true
		}
	}
	n := path.Base(p)
	return testsel.InfraNames[n] || (testsel.SectionFiles[n] && !strings.Contains(p, "/"))
}

// featureSide reports a changed path whose old content the baseline needs:
// everything that is not a test or a document.
func featureSide(p string) bool {
	return !testSide(p) && !detect.IsDocPath(p)
}

// baselineTree is the pass tree with every feature file put back to the
// start tree: present files take their start content, new ones are removed.
func baselineTree(ctx context.Context, root, start, pass string, feature []string) (string, error) {
	idx, cleanup, err := tempIndex()
	if err != nil {
		return "", err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err := runGit(ctx, root, env, "read-tree", pass); err != nil {
		return "", err
	}
	for _, p := range feature {
		entry, err := runGit(ctx, root, nil, "ls-tree", start, "--", p)
		if err != nil {
			return "", err
		}
		if entry == "" {
			if _, err := runGit(ctx, root, env, "update-index", "--force-remove", "--", p); err != nil {
				return "", err
			}
			continue
		}
		// <mode> SP <type> SP <object> TAB <path>
		meta, _, _ := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		if len(f) != 3 || f[1] != "blob" {
			continue
		}
		if _, err := runGit(ctx, root, env, "update-index", "--add", "--cacheinfo", f[0]+","+f[2]+","+p); err != nil {
			return "", err
		}
	}
	return runGit(ctx, root, env, "write-tree")
}

// refactorRe finds a request whose point is to keep behaviour unchanged.
var refactorRe = regexp.MustCompile(`(?i)리팩터|리팩토|구조\s*(?:변경|개선|정리)|이름\s*(?:변경|바꾸)|동작\s*(?:유지|보존|은\s*그대로)|\brefactor|\brename\b|\bbehavio(?:u)?r[- ]preserving\b|\bwithout changing (?:the )?behavio`)

// testNameRe finds test names in test source, one language per group.
var testNameRe = regexp.MustCompile(`(?m)^\s*func\s+(Test\w+)\s*\(|^\s*(?:async\s+)?def\s+(test_\w+)\s*\(|\b(?:it|test)\s*\(\s*['"` + "`" + `]([^'"` + "`" + `]{1,200})['"` + "`" + `]|#\[(?:tokio::)?test\][^\n]*\n\s*(?:pub\s+)?(?:async\s+)?fn\s+(\w+)`)

func testNames(src string) []string {
	var out []string
	for _, m := range testNameRe.FindAllStringSubmatch(src, -1) {
		for _, g := range m[1:] {
			if g != "" {
				out = append(out, g)
			}
		}
	}
	return out
}

// buildFailureRe finds output of a run that never reached the tests because
// code they need does not exist yet.
var buildFailureRe = regexp.MustCompile(`(?m)\[build failed\]|^\S+\.go:\d+:\d+: undefined: |ERROR collecting|ImportError while importing test module|cannot import name|error\[E\d+\]|Cannot find module '\.|has no exported member|is not exported by|SyntaxError: |TS2305|TS2307`)

// netnsAvailable reports whether a command can run without network access:
// an unprivileged user and network namespace.
var netnsOnce sync.Once
var netnsOK bool

func netnsAvailable() bool {
	netnsOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		netnsOK = exec.CommandContext(ctx, "unshare", "-rn", "true").Run() == nil
	})
	return netnsOK
}

// maybeBaseline schedules a comparison for a passing check; called with s.mu
// held, returns at once.
func (s *Session) maybeBaseline(ev *event.Event) {
	mode := s.Cfg.Verify.Baseline
	if mode == "off" || mode == "" || ev.Tool != event.ToolShell || ev.Category != event.CatVerify || ev.ExitCode == nil || *ev.ExitCode != 0 || ev.Background {
		return
	}
	cmd := ev.Cmd
	if mode == "contract" {
		check, ok := s.contractCheckCommand(ev.Cmd)
		if !ok {
			return
		}
		// the check itself, not the agent's command around it, which may
		// also have written files
		cmd = check
	}
	if !s.baseline.startOK && !s.baseline.uncomparedNoted && s.Root != "" {
		switch {
		case s.ws != nil && !s.ws.IsGit():
			s.noteUncompared(ev, cmd, "not_git_repository")
			return
		case s.baseline.startLost:
			s.noteUncompared(ev, cmd, "start_not_recorded")
			return
		}
	}
	if !s.baseline.startOK || s.baseline.running || cmd == "" {
		return
	}
	request := s.eng.RequestText()
	key := ev.CmdNorm + "|" + ev.WSAfter
	if s.baseline.done[key] {
		return
	}
	if s.baseline.done == nil {
		s.baseline.done = map[string]bool{}
	}
	s.baseline.done[key] = true
	s.baseline.running = true
	s.baselineWG.Add(1)
	job := baselineJob{seq: ev.Seq, cmd: cmd, dur: time.Duration(ev.DurationMS) * time.Millisecond, start: s.baseline.start,
		refactor: refactorRe.MatchString(request), netns: mode == "auto"}
	go s.runBaseline(job)
}

// baselineEligible reports whether a comparison can happen in this session:
// any test command in auto mode, an accepted contract with a done check
// marked free of side effects in contract mode. The start tree is kept
// only then, since writing it hashes the changed files of the workspace.
// Called with s.mu held.
func (s *Session) baselineEligible() bool {
	switch s.Cfg.Verify.Baseline {
	case "auto":
		return true
	case "contract":
		if s.c == nil || s.acc.State != contract.StateAccepted {
			return false
		}
		for _, d := range s.c.Done {
			if d.Check != "" && (d.Pure || d.Isolation == "worktree") {
				return true
			}
		}
	}
	return false
}

// contractCheckCommand finds the accepted contract's done check, marked free
// of side effects, that a command ran: the command itself or its last command
// after a `;`, `&&` or new line, whose exit is the command's exit, run in the
// project folder. It returns the check as the contract spells it.
func (s *Session) contractCheckCommand(cmd string) (string, bool) {
	if s.c == nil || s.acc.State != contract.StateAccepted {
		return "", false
	}
	last, ok := lastCommand(cmd)
	if !ok {
		return "", false
	}
	atRoot := func(dir string) bool {
		if dir == "" {
			return true
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(s.Root, dir)
		}
		return filepath.Clean(dir) == filepath.Clean(s.Root)
	}
	norm, dir := fp.NormalizeCmd(last)
	if !atRoot(dir) {
		return "", false
	}
	// a folder change earlier in the line moves the check out of the project
	for _, st := range fp.SplitStages(strings.TrimSuffix(strings.TrimRight(cmd, " \t\n;"), last)) {
		if f := strings.Fields(st); len(f) > 0 && f[0] == "cd" && (len(f) != 2 || !atRoot(strings.Trim(f[1], `'"`))) {
			return "", false
		}
	}
	for _, d := range s.c.Done {
		if d.Check == "" || !(d.Pure || d.Isolation == "worktree") {
			continue
		}
		if n, _ := fp.NormalizeCmd(d.Check); n != "" && n == norm {
			return d.Check, true
		}
	}
	return "", false
}

// lastCommand returns the last command of a command line: what follows the
// last top-level `;`, `&&` or new line. It reports false when that command
// may not have run or does not give the exit: after `||`, in a pipe, run in
// the background, or continued from the line above.
func lastCommand(cmd string) (string, bool) {
	cmd = strings.TrimRight(cmd, " \t\n;")
	line := cmd
	if i := strings.LastIndexByte(cmd, '\n'); i >= 0 {
		line = cmd[i+1:]
		prev := strings.TrimRight(cmd[:i], " \t")
		if strings.HasSuffix(prev, "\\") || strings.HasSuffix(prev, "|") {
			return "", false
		}
	}
	inS, inD := false, false
	start, ok := 0, true
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\'' && !inD:
			inS = !inS
		case c == '"' && !inS:
			inD = !inD
		case inS || inD:
		case c == ';':
			start, ok = i+1, true
		case c == '&' && (i > 0 && (line[i-1] == '>' || line[i-1] == '<') || i+1 < len(line) && line[i+1] == '>'):
			// a redirection such as 2>&1
		case c == '&' && i+1 < len(line) && line[i+1] == '&':
			i++
			start, ok = i+1, true
		case c == '|' && i+1 < len(line) && line[i+1] == '|':
			i++
			start, ok = i+1, false
		case c == '|' || c == '&':
			start, ok = i+1, false
		}
	}
	if inS || inD {
		return "", false
	}
	return strings.TrimSpace(line[start:]), ok
}

type baselineJob struct {
	seq      int64
	cmd      string
	dur      time.Duration
	start    string
	refactor bool
	netns    bool
}

// runBaseline does one comparison off the hook path and records it.
func (s *Session) runBaseline(j baselineJob) {
	defer s.baselineWG.Done()
	r := s.compareBaseline(j)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.baseline.running = false
	if r == nil {
		return
	}
	s.baseline.results = append(s.baseline.results, *r)
	s.eng.NoteBaseline(j.seq, r.Outcome, r.Tests)
}

// changedSince reports a call after seq that writes or may write files;
// called with s.mu held.
func (s *Session) changedSince(seq int64) bool {
	for _, ev := range s.byTool {
		if ev.Seq > seq && (ev.Category == event.CatProduce || ev.Mutating || ev.Unknown) {
			return true
		}
	}
	return false
}

// compareBaseline builds the baseline, runs the command and judges the
// result. nil means there was nothing to compare: the command ran no test
// the session changed, or the session changed no feature code.
func (s *Session) compareBaseline(j baselineJob) *BaselineResult {
	res := &BaselineResult{Seq: j.seq, Cmd: short(j.cmd, 200), At: time.Now()}
	notCompared := func(reason string) *BaselineResult {
		res.Outcome, res.Reason = BaselineNotCompared, reason
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), baselineGitTimeout)
	defer cancel()
	pass, err := snapshotTree(ctx, s.Root)
	if err != nil {
		return notCompared("snapshot_failed")
	}
	s.mu.Lock()
	moved := s.changedSince(j.seq)
	s.mu.Unlock()
	if moved {
		// a write after the passing run: the snapshot may not be what passed
		return notCompared("time_mismatch")
	}
	changed, err := changedFiles(ctx, s.Root, j.start, pass)
	if err != nil {
		return notCompared("diff_failed")
	}
	sel := testsel.Resolve(s.Root, j.cmd)
	ran := map[string]bool{}
	for _, f := range sel.Files {
		ran[f] = true
	}
	var tests, feature []string
	for _, p := range changed {
		switch {
		case ran[p]:
			tests = append(tests, p)
		case featureSide(p):
			feature = append(feature, p)
		}
	}
	if len(tests) == 0 || len(feature) == 0 {
		return nil
	}
	res.Tests = tests
	if j.netns && !netnsAvailable() {
		return notCompared("network_isolation_unavailable")
	}
	tree, err := baselineTree(ctx, s.Root, j.start, pass, feature)
	if err != nil {
		return notCompared("baseline_failed")
	}
	wt, cleanup, err := baselineWorktree(ctx, s.Root, tree)
	if err != nil {
		return notCompared("worktree_failed")
	}
	defer cleanup()
	linkDependencies(s.Root, wt, append(append([]string{}, tests...), "."))
	timeout := 3 * j.dur
	if timeout < baselineMinTimeout {
		timeout = baselineMinTimeout
	}
	if timeout > baselineMaxTimeout {
		timeout = baselineMaxTimeout
	}
	cmd := j.cmd
	if j.netns {
		cmd = "unshare -rn -- /bin/sh -c " + shellQuote(cmd)
	}
	h := sha256.Sum256([]byte(s.Root))
	cmd = "CARGO_TARGET_DIR=" + shellQuote(filepath.Join(baselineDir(), "target-"+hex.EncodeToString(h[:6]))) + " " + cmd
	var stop <-chan struct{}
	if s.daemon != nil {
		key := s.heavyKey()
		var release func()
		stop, release = s.daemon.heavy.holdBaseline(key)
		defer release()
		// a run still holding the folder may be a call the host refused,
		// closed at the end of the turn, or one about to finish: wait for it
		// a while instead of giving up at once
		deadline := time.Now().Add(baselineConflictWait)
		for s.daemon.heavy.conflict(key, "", time.Now()) != nil {
			select {
			case <-stop:
				if s.daemon.stopping() {
					return notCompared("daemon_stopped")
				}
				return notCompared("agent_check_started")
			default:
			}
			if time.Now().After(deadline) {
				return notCompared("agent_check_running")
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	code, out, timedOut, _, stopped := runGroupUntil(wt, cmd, timeout, stop)
	if stopped {
		if s.daemon != nil && s.daemon.stopping() {
			return notCompared("daemon_stopped")
		}
		return notCompared("agent_check_started")
	}
	var names []string
	for _, p := range tests {
		if b, err := os.ReadFile(filepath.Join(wt, filepath.FromSlash(p))); err == nil {
			names = append(names, testNames(string(b))...)
		}
	}
	res.Outcome = judgeBaseline(code, out, timedOut, names, j.refactor, &res.Failed)
	return res
}

// judgeBaseline classifies one baseline run.
func judgeBaseline(code int, out string, timedOut bool, names []string, refactor bool, failed *[]string) string {
	switch {
	case timedOut:
		return BaselineTimeout
	case code == 0 && refactor:
		return BaselinePreserved
	case code == 0:
		return BaselinePass
	}
	for _, f := range testout.FailedTests(out) {
		for _, n := range names {
			if f == n || strings.HasSuffix(f, "::"+n) || strings.HasSuffix(f, "/"+n) || strings.HasSuffix(f, " "+n) || strings.Contains(f, n+" ") || strings.HasSuffix(f, "."+n) {
				*failed = append(*failed, f)
				break
			}
		}
	}
	if len(*failed) > 0 {
		return BaselineVerified
	}
	if buildFailureRe.MatchString(out) {
		return BaselineWeak
	}
	if f := fp.DiagnoseFailure(code, out); fp.External(f.Class) || f.Class == fp.FailTransient {
		return BaselineEnv
	}
	return BaselineUndecided
}

// baselineWorktree checks out a tree in a detached work tree outside the
// project and returns a cleanup that removes it.
func baselineWorktree(ctx context.Context, root, tree string) (string, func(), error) {
	env := []string{"GIT_AUTHOR_NAME=samcheonpo", "GIT_AUTHOR_EMAIL=samcheonpo@localhost", "GIT_COMMITTER_NAME=samcheonpo", "GIT_COMMITTER_EMAIL=samcheonpo@localhost"}
	commit, err := runGit(ctx, root, env, "commit-tree", tree, "-m", "samcheonpo baseline")
	if err != nil {
		return "", func() {}, err
	}
	if err := os.MkdirAll(baselineDir(), 0o700); err != nil {
		return "", func() {}, err
	}
	dir, err := os.MkdirTemp(baselineDir(), "wt-")
	if err != nil {
		return "", func() {}, err
	}
	if _, err := runGit(ctx, root, nil, "worktree", "add", "--detach", dir, commit); err != nil {
		os.RemoveAll(dir)
		return "", func() {}, err
	}
	return dir, func() {
		c, cancel := context.WithTimeout(context.Background(), baselineGitTimeout)
		defer cancel()
		_, _ = runGit(c, root, nil, "worktree", "remove", "--force", dir)
		_ = os.RemoveAll(dir)
		_, _ = runGit(c, root, nil, "worktree", "prune")
	}, nil
}

// dependencyDirs are installed dependency folders git ignores; a work tree
// reuses the project's own through a link.
var dependencyDirs = []string{"node_modules", ".venv", "venv"}

// linkDependencies links the dependency folders next to the given paths, and
// in every folder above them, from the project into the work tree.
func linkDependencies(root, wt string, paths []string) {
	seen := map[string]bool{}
	for _, p := range paths {
		for d := path.Dir(p); ; d = path.Dir(d) {
			if !seen[d] {
				seen[d] = true
				for _, name := range dependencyDirs {
					src := filepath.Join(root, filepath.FromSlash(d), name)
					dst := filepath.Join(wt, filepath.FromSlash(d), name)
					if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
						continue
					}
					if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
						continue
					}
					_ = os.MkdirAll(filepath.Dir(dst), 0o755)
					_ = os.Symlink(src, dst)
				}
			}
			if d == "." || d == "/" || d == "" {
				break
			}
		}
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// testSection is the test changes section of the receipt and the summary;
// called with s.mu held.
func (s *Session) testSection() []string {
	in := receipt.TestInput{Files: s.eng.TestChanges(), Trivial: s.eng.TrivialTests(), Verdicts: s.eng.Verdicts}
	for _, r := range s.baseline.results {
		in.Baselines = append(in.Baselines, receipt.BaselineLine{Outcome: r.Outcome, Tests: r.Tests, Failed: r.Failed, Reason: r.Reason})
	}
	if rep, ok := mutants.Load(s.ID); ok {
		in.Mutants = MutantLines(rep)
	}
	return receipt.TestSection(in)
}

// MutantLines turns a mutation report into receipt lines.
func MutantLines(rep mutants.Report) []receipt.MutantLine {
	var out []receipt.MutantLine
	for _, r := range rep.Results {
		out = append(out, receipt.MutantLine{Tool: r.Tool, Ran: r.Ran, Total: r.Total, Missed: r.Missed, Survivors: r.Survivors, Note: r.Note})
	}
	return out
}
