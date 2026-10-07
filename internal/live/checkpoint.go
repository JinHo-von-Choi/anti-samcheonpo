package live

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testout"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

// CheckResult is the outcome of one contract check.
type CheckResult struct {
	StopReason  string                 `json:"stop_reason,omitempty"`
	Reused      bool                   `json:"reused,omitempty"`
	Evidence    *verification.Evidence `json:"evidence,omitempty"`
	Truncated   bool                   `json:"truncated,omitempty"`
	ReuseReason string                 `json:"reuse_reason,omitempty"`
	ID          string                 `json:"id"`
	Command     string                 `json:"command"`
	Pass        bool                   `json:"pass"`
	ExitCode    int                    `json:"exit_code"`
	TimedOut    bool                   `json:"timed_out"`
	SideEffect  bool                   `json:"side_effect"`
	Skipped     string                 `json:"skipped,omitempty"`
	Failed      []string               `json:"failed_tests,omitempty"`
	Errors      int                    `json:"errors"`
	ResultFP    string                 `json:"result_fp"`
	Duration    time.Duration          `json:"duration"`
}

// Runner executes accepted contract checks.
type Runner struct {
	runMu   sync.Mutex
	cache   *verification.Cache
	Root    string
	WS      *Workspace
	Timeout time.Duration
	// Busy reports whether the agent's shell tool is running.
	Busy func() bool
}

// Run executes checks. mid restricts to checks safe to run during work
// (isolation: worktree or pure: true). sideEffect lists ids already excluded.
func (r *Runner) Run(checks []contract.Check, mid bool, sideEffect map[string]bool) []CheckResult {
	return r.RunRevision(checks, mid, sideEffect, fp.Hash("local-check-task", r.Root), 1)
}

func (r *Runner) RunRevision(checks []contract.Check, mid bool, sideEffect map[string]bool, taskID string, revision uint64) []CheckResult {
	return r.RunTaskRevision(checks, mid, sideEffect, taskID, revision, taskID)
}

func (r *Runner) RunTaskRevision(checks []contract.Check, mid bool, sideEffect map[string]bool, taskID string, revision uint64, sessionID string) []CheckResult {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	if r.cache == nil {
		r.cache = verification.NewCache(256)
	}
	var out []CheckResult
	for _, c := range checks {
		res := CheckResult{ID: c.ID, Command: c.Check}
		switch {
		case sideEffect[c.ID]:
			res.Skipped = "작업 폴더를 바꾸는 검사라 자동 실행에서 뺐다"
		case mid && c.Isolation != "worktree" && !c.Pure:
			res.Skipped = "작업 중 실행은 격리되거나 부작용 없는 검사만 한다"
		case r.Busy != nil && r.Busy():
			res.Skipped = "에이전트 셸 실행 중이라 다음 기회로 미뤘다"
		default:
			res = r.reusable(c, taskID, revision, sessionID)
		}
		out = append(out, res)
	}
	return out
}

func (r *Runner) proofKey(c contract.Check, taskID string, revision uint64) (verification.Key, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.proofKeyContext(ctx, c, taskID, revision)
}

func (r *Runner) proofKeyContext(ctx context.Context, c contract.Check, taskID string, revision uint64) (verification.Key, error) {
	key := verification.Key{TaskID: taskID, Revision: revision, CheckID: c.ID, RunnerVersion: "samcheonpo-check/2"}
	if c.Reuse == nil || !c.Pure || !c.Reuse.Deterministic || r.WS == nil {
		return key, fmt.Errorf("완전한 로컬 입력 선언이 없는 검사는 재사용하지 않는다")
	}
	b, _ := json.Marshal(c)
	key.CommandHash = fp.Hash(string(b))
	var err error
	key.InputHash, err = verification.Fingerprint(ctx, r.Root, c.Reuse.Inputs)
	if err != nil {
		return key, err
	}
	environmentFiles, err := verification.Fingerprint(ctx, r.Root, c.Reuse.EnvironmentFiles)
	if err != nil {
		return key, err
	}
	env := os.Environ()
	sort.Strings(env)
	envJSON, _ := json.Marshal(env)
	key.EnvironmentHash = fp.Hash(runtime.GOOS, runtime.GOARCH, string(envJSON), environmentFiles)
	return key, nil
}

// CurrentKey reads declared local inputs/environment only. It never executes
// the check; callers must separately establish receiver-side acceptance.
func (r *Runner) CurrentKey(c contract.Check, taskID string, revision uint64) (verification.Key, error) {
	return r.proofKey(c, taskID, revision)
}

// CurrentKeyContext is CurrentKey bounded by the caller's deadline; the
// pre-tool hook uses it so a slow read lets the run through.
func (r *Runner) CurrentKeyContext(ctx context.Context, c contract.Check, taskID string, revision uint64) (verification.Key, error) {
	return r.proofKeyContext(ctx, c, taskID, revision)
}

func (r *Runner) reusable(c contract.Check, taskID string, revision uint64, sessionID string) CheckResult {
	key, err := r.proofKey(c, taskID, revision)
	if err != nil {
		res := r.one(c)
		if c.Reuse != nil {
			res.ReuseReason = "입력·환경을 확인하지 못해 새로 실행했다"
		}
		return res
	}
	var fresh CheckResult
	e, reused, err := r.cache.Do(context.Background(), key, func(context.Context) (verification.Evidence, error) {
		fresh = r.one(c)
		after, e := r.proofKey(c, taskID, revision)
		if e != nil || after != key {
			fresh.Pass = false
			fresh.ReuseReason = "검사 중 입력·환경이 바뀌어 통과 근거를 적용하지 않았다"
			return verification.Evidence{}, fmt.Errorf("inputs changed during check")
		}
		now := time.Now().UTC()
		proof := verification.Evidence{Version: verification.Version, ID: fp.Hash(key.Digest(), now.Format(time.RFC3339Nano)), Key: key, SessionID: sessionID, SourceEventID: fp.Hash("check", key.Digest(), now.Format(time.RFC3339Nano)), ObservedAt: now, ExpiresAt: now.Add(time.Duration(c.Reuse.MaxAgeSec) * time.Second), Pass: fresh.Pass && !fresh.SideEffect, Complete: fresh.Skipped == "" && !fresh.Truncated, SideEffect: fresh.SideEffect, ExitCode: fresh.ExitCode, TimedOut: fresh.TimedOut, ResultHash: fresh.ResultFP}
		return proof, nil
	})
	if err != nil {
		return fresh
	}
	stopReason := ""
	plan := verification.PlanChecks([]verification.Candidate{{CheckID: c.ID, Evidence: &e, CurrentKey: &key}}, time.Now())
	if plan.StopMachineChecks {
		stopReason = plan.Items[0].Reason + " 새 변경·실패·유효기간 만료가 있으면 다시 확인한다."
	}
	if reused {
		return CheckResult{ID: c.ID, Command: c.Check, Pass: e.Pass, ExitCode: e.ExitCode, ResultFP: e.ResultHash, Reused: true, Evidence: &e, ReuseReason: "같은 목표 개정·명령·입력·환경에서 이미 통과했다", StopReason: stopReason}
	}
	fresh.Evidence = &e
	fresh.StopReason = stopReason
	return fresh
}

func (r *Runner) one(c contract.Check) CheckResult {
	res := CheckResult{ID: c.ID, Command: c.Check}
	timeout := r.Timeout
	if c.TimeoutSec > 0 {
		timeout = time.Duration(c.TimeoutSec) * time.Second
	}
	dir := r.Root
	cleanup := func() {}
	if c.Isolation == "worktree" {
		d, cl, err := r.worktree()
		if err != nil {
			res.Skipped = "격리 작업 폴더를 만들지 못했다: " + err.Error()
			return res
		}
		dir, cleanup = d, cl
	}
	defer cleanup()
	var before string
	if c.Isolation != "worktree" && r.WS != nil {
		ctx, cancel := withTimeout(30 * time.Second)
		before, _ = r.WS.Compute(ctx)
		cancel()
	}
	start := time.Now()
	code, outText, timedOut, truncated := runGroup(dir, c.Check, timeout)
	res.Duration = time.Since(start)
	res.ExitCode = code
	res.TimedOut = timedOut
	res.Truncated = truncated
	res.Pass = code == 0 && !timedOut && !truncated
	res.Failed = testout.FailedTests(outText)
	res.Errors = len(fp.ErrorFPs(outText))
	ec := code
	res.ResultFP = fp.ResultFP(&ec, fp.ErrorFPs(outText), res.Failed)
	if c.Isolation != "worktree" && r.WS != nil && before != "" {
		ctx, cancel := withTimeout(30 * time.Second)
		after, err := r.WS.Compute(ctx)
		cancel()
		if err == nil && after != before {
			res.SideEffect = true
		}
	}
	return res
}

// runGroup runs a shell command in its own process group and kills the whole
// group on timeout. Output is used only for fingerprints, then discarded.
func runGroup(dir, command string, timeout time.Duration) (int, string, bool, bool) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var buf bytes.Buffer
	lw := &limitWriter{w: &buf, n: 1 << 20}
	cmd.Stdout, cmd.Stderr = lw, lw
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return 127, err.Error(), false, false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			code = 1
		}
		return code, buf.String(), false, lw.truncated
	case <-time.After(timeout):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return -1, buf.String(), true, lw.truncated
	}
}

type limitWriter struct {
	w         io.Writer
	n         int
	truncated bool
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		if len(p) > 0 {
			l.truncated = true
		}
		return len(p), nil
	}
	q := p
	if len(q) > l.n {
		l.truncated = true
		q = q[:l.n]
	}
	l.n -= len(q)
	_, _ = l.w.Write(q)
	return len(p), nil
}

// worktree creates a detached git worktree with the current tracked changes
// and untracked files, returning its path and a cleanup function.
func (r *Runner) worktree() (string, func(), error) {
	if r.WS == nil || !r.WS.IsGit() {
		return "", func() {}, fmt.Errorf("git 저장소가 아니다")
	}
	ctx, cancel := withTimeout(60 * time.Second)
	defer cancel()
	commit, err := gitStashCommit(ctx, r.Root)
	if err != nil {
		return "", func() {}, err
	}
	dir, err := os.MkdirTemp("", "samcheonpo-wt-")
	if err != nil {
		return "", func() {}, err
	}
	os.Remove(dir)
	if out, err := exec.CommandContext(ctx, "git", "-C", r.Root, "worktree", "add", "--detach", dir, commit).CombinedOutput(); err != nil {
		return "", func() {}, fmt.Errorf("git worktree add: %s", bytes.TrimSpace(out))
	}
	for _, p := range untracked(ctx, r.Root) {
		src := filepath.Join(r.Root, p)
		dst := filepath.Join(dir, p)
		if b, err := os.ReadFile(src); err == nil {
			_ = os.MkdirAll(filepath.Dir(dst), 0o755)
			_ = os.WriteFile(dst, b, 0o644)
		}
	}
	cleanup := func() {
		c2, cancel2 := withTimeout(30 * time.Second)
		defer cancel2()
		_ = exec.CommandContext(c2, "git", "-C", r.Root, "worktree", "remove", "--force", dir).Run()
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup, nil
}
