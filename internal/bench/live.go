package bench

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

// LiveTask is a real task run by an agent: a starting repository, a prompt and
// a check command whose exit status decides verified completion.
type LiveTask struct {
	Name             string `yaml:"name"`
	Prompt           string `yaml:"prompt"`
	Check            string `yaml:"check"`
	TimeoutSec       int    `yaml:"timeout_sec"`
	Dir              string `yaml:"-"`
	Language         string `yaml:"language"`
	Family           string `yaml:"family"`
	InitiallyCorrect bool   `yaml:"initially_correct"`
	// Group is the source event a task was derived from; variants of one
	// event share it and count as one independent task. Empty means the task
	// is its own group.
	Group string `yaml:"group"`
}

// Arm is one way of running the agent. "{prompt}" in Cmd is replaced by the
// task prompt and "{task}" by the task directory.
type Arm struct {
	Cmd           []string          `yaml:"cmd"`
	Env           map[string]string `yaml:"env"`
	Agent         string            `yaml:"agent"`
	AgentVersion  string            `yaml:"agent_version"`
	Model         string            `yaml:"model"`
	PolicyVersion string            `yaml:"policy_version"`
	Monitor       string            `yaml:"monitor"` // none | samcheonpo; absent/other means unmeasured
}

// ABConfig is the live comparison configuration.
type ABConfig struct {
	Seed       int64          `yaml:"seed"`
	Trials     int            `yaml:"trials"`
	TimeoutSec int            `yaml:"timeout_sec"`
	Arms       map[string]Arm `yaml:"arms"`
}

// TrialResult is one result row. Other tools submit rows in the same format.
type TrialResult struct {
	PriceVersion      string `json:"price_version,omitempty"`
	EvaluatorVersion  string `json:"evaluator_version,omitempty"`
	GraderDurationMS  int64  `json:"grader_duration_ms"`
	ProtocolVersion   string `json:"protocol_version,omitempty"`
	RunID             string `json:"run_id,omitempty"`
	Seed              int64  `json:"seed"`
	Order             int    `json:"order"`
	Agent             string `json:"agent,omitempty"`
	AgentVersion      string `json:"agent_version,omitempty"`
	Model             string `json:"model,omitempty"`
	PolicyVersion     string `json:"policy_version,omitempty"`
	TaskDigest        string `json:"task_digest,omitempty"`
	GraderDigest      string `json:"grader_digest,omitempty"`
	IndependentGrader bool   `json:"independent_grader"`
	SessionID         string `json:"session_id,omitempty"`
	CostSource        string `json:"cost_source,omitempty"`
	CostError         string `json:"cost_error,omitempty"`
	Arm               string `json:"arm"`
	Task              string `json:"task"`
	Group             string `json:"group,omitempty"`
	Trial             int    `json:"trial"`
	Verified          bool   `json:"verified"`
	TotalKRW          int64  `json:"total_krw"`
	WasteKRW          int64  `json:"waste_krw"`
	CostKnown         bool   `json:"cost_known"`
	MonitorKRW        *int64 `json:"monitor_krw,omitempty"` // nil: monitoring cost was not measured
	MonitorCostError  string `json:"monitor_cost_error,omitempty"`
	DurationMS        int64  `json:"duration_ms"`
	Exit              int    `json:"agent_exit"`
	TimedOut          bool   `json:"timed_out,omitempty"`
	Error             string `json:"error,omitempty"`
}

// LoadLiveTasks reads <dir>/*/task.yml; each task has a repo/ directory.
func LoadLiveTasks(dir string) ([]LiveTask, error) {
	ms, _ := filepath.Glob(filepath.Join(dir, "*", "task.yml"))
	sort.Strings(ms)
	var out []LiveTask
	seen := map[string]bool{}
	for _, m := range ms {
		b, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		var t LiveTask
		if err := yaml.Unmarshal(b, &t); err != nil {
			return nil, fmt.Errorf("%s: %w", m, err)
		}
		t.Dir, err = filepath.Abs(filepath.Dir(m))
		if err != nil {
			return nil, err
		}
		if t.Name == "" {
			t.Name = filepath.Base(t.Dir)
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("duplicate task name: %s", t.Name)
		}
		seen[t.Name] = true
		if t.Prompt == "" || t.Check == "" {
			return nil, fmt.Errorf("%s: prompt와 check가 필요하다", m)
		}
		if st, err := os.Stat(filepath.Join(t.Dir, "repo")); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("%s: repo 폴더가 없다", t.Dir)
		}
		out = append(out, t)
	}
	return out, nil
}

// LoadABConfig reads the arm configuration.
func LoadABConfig(p string) (ABConfig, error) {
	var c ABConfig
	b, err := os.ReadFile(p)
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if len(c.Arms) == 0 {
		return c, errors.New("arms가 비어 있다")
	}
	for n, a := range c.Arms {
		if len(a.Cmd) == 0 {
			return c, fmt.Errorf("arm %s에 cmd가 없다", n)
		}
	}
	if c.Trials <= 0 {
		c.Trials = 1
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = 900
	}
	return c, nil
}

// RunTrial runs one task under one arm in a fresh copy of the repository.
// claudeDir is where the agent writes transcripts (found by session id).
func RunTrial(t LiveTask, armName string, a Arm, trial int, c ABConfig, prices *cost.Table, claudeDir, work string) TrialResult {
	r := TrialResult{ProtocolVersion: "2", Seed: c.Seed, Arm: armName, Task: t.Name, Group: t.Group, Trial: trial, Agent: a.Agent, AgentVersion: a.AgentVersion, Model: a.Model, PolicyVersion: a.PolicyVersion}
	if len(a.Cmd) == 0 || prices == nil {
		r.Error = "agent command and price table required"
		return r
	}
	r.PriceVersion = prices.Version
	r.EvaluatorVersion = analyze.EvaluatorVersion
	if r.Agent == "" {
		r.Agent = "claude"
	}
	dir, err := os.MkdirTemp(work, t.Name+"-"+armName+"-")
	if err != nil {
		r.Error = err.Error()
		return r
	}
	repo := filepath.Join(dir, "repo")
	r.TaskDigest, err = TreeDigest(t.Dir)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	grader := filepath.Join(dir, "grader")
	graderSource := filepath.Join(t.Dir, "grader")
	if st, e := os.Stat(graderSource); e == nil && st.IsDir() {
		if err = copyTree(graderSource, grader); err != nil {
			r.Error = err.Error()
			return r
		}
		r.GraderDigest, err = TreeDigest(grader)
		if err != nil {
			r.Error = err.Error()
			return r
		}
		r.IndependentGrader = true
	}
	if err := copyTree(filepath.Join(t.Dir, "repo"), repo); err != nil {
		r.Error = err.Error()
		return r
	}
	for _, g := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=bench", "-c", "user.email=bench@localhost", "commit", "-qm", "start"}} {
		if out, err := run(context.Background(), repo, nil, "git", g...); err != nil {
			r.Error = "git " + g[0] + ": " + strings.TrimSpace(string(out))
			return r
		}
	}
	timeout := c.TimeoutSec
	if t.TimeoutSec > 0 {
		timeout = t.TimeoutSec
	}
	argv := make([]string, len(a.Cmd))
	for i, s := range a.Cmd {
		s = strings.ReplaceAll(s, "{task}", t.Dir)
		argv[i] = strings.ReplaceAll(s, "{prompt}", t.Prompt)
	}
	var env []string
	for k, v := range a.Env {
		env = append(env, k+"="+v)
	}
	env = append(env, "SAMCHEONPO_HOME="+filepath.Join(dir, "home"))
	codexDir := sources.CodexDir()
	if home := a.Env["CODEX_HOME"]; home != "" {
		codexDir = filepath.Join(home, "sessions")
	}
	if home := a.Env["CLAUDE_CONFIG_DIR"]; home != "" {
		claudeDir = filepath.Join(home, "projects")
	}
	preexisting := map[string]bool{}
	for _, file := range sources.Find(r.Agent, time.Time{}, claudeDir, codexDir) {
		preexisting[file.Path] = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	start := time.Now()
	out, err := run(ctx, repo, env, argv[0], argv[1:]...)
	cancel()
	r.DurationMS = time.Since(start).Milliseconds()
	monitorStopped := stopDaemon(filepath.Join(dir, "home"))
	monitorPath := filepath.Join(dir, "home", "ledger.db")
	switch a.Monitor {
	case "none":
		if _, err := os.Stat(monitorPath); os.IsNotExist(err) {
			zero := int64(0)
			r.MonitorKRW = &zero
		} else {
			r.MonitorCostError = "monitor declared disabled but ledger exists or is inaccessible"
		}
	case "samcheonpo":
		if !monitorStopped {
			r.MonitorCostError = "monitor process termination not confirmed"
			break
		}
		if micro, err := ledger.MonitoringCost(monitorPath); err == nil {
			amount := cost.Won(micro)
			r.MonitorKRW = &amount
		} else {
			r.MonitorCostError = err.Error()
		}
	default:
		r.MonitorCostError = "monitor cost source not declared or unsupported"
	}
	_ = os.WriteFile(filepath.Join(dir, "agent.out"), out, 0o600)
	if ctx.Err() == context.DeadlineExceeded {
		r.TimedOut = true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.Exit = ee.ExitCode()
	} else if err != nil {
		r.Error = err.Error()
		r.Exit = -1
	}
	beforeGrade, digestErr := TreeDigest(t.Dir)
	if digestErr != nil || beforeGrade != r.TaskDigest {
		r.Error = "benchmark source changed during trial"
	}
	checkDir := repo
	if r.IndependentGrader {
		checkDir = grader
		digest, e := TreeDigest(grader)
		if e != nil || digest != r.GraderDigest {
			r.Error = "grader changed during trial"
		}
	}
	cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Minute)
	gradeStart := time.Now()
	var cerr error
	if r.Error == "" {
		_, cerr = run(cctx, checkDir, []string{"BENCH_REPO=" + repo, "BENCH_GRADER=" + grader, "PYTHONDONTWRITEBYTECODE=1"}, "sh", "-c", t.Check)
	}
	ccancel()
	r.GraderDurationMS = time.Since(gradeStart).Milliseconds()
	if r.IndependentGrader {
		digest, e := TreeDigest(grader)
		if e != nil || digest != r.GraderDigest {
			r.Error = "grader changed during grading"
		}
	}
	r.Verified = cerr == nil && !r.TimedOut && r.Error == ""
	r.SessionID = sessionID(out)
	s, resolveErr := ResolveTranscript(r.Agent, r.SessionID, repo, claudeDir, codexDir, start)
	if resolveErr != nil {
		r.CostError = resolveErr.Error()
	} else if preexisting[s.SourcePath] {
		r.CostError = "preexisting session transcript cannot supply whole-trial cost"
	} else {
		cfg := config.Default()
		if res, err := analyze.Run(s, analyze.Options{Config: cfg, ConfigHash: config.Hash(cfg), Prices: prices}); err == nil {
			if res.FormatOK && res.Totals.PriceCoverage() == 1 {
				r.TotalKRW = cost.Won(res.Totals.Micro)
				r.WasteKRW = cost.Won(res.Totals.SymptomTotal())
				r.CostKnown = true
				r.CostSource = "transcript_api_equivalent"
			} else {
				r.CostError = "incomplete price coverage"
			}
		} else {
			r.CostError = err.Error()
		}
	}
	return r
}

// stopDaemon ends the trial's samcheonpo daemon, if the arm started one, so
// trials do not leave idle daemons behind. The lock file holds its pid.
func stopDaemon(home string) bool {
	b, err := os.ReadFile(filepath.Join(home, "run", "daemon.lock"))
	if err != nil {
		return os.IsNotExist(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return false
	}
	// A socket disappearing does not prove finalization completed: listeners
	// unlink before queue drain. Until native process verification exists for
	// other OSes, conservatively withhold the monitoring total there.
	if runtime.GOOS != "linux" {
		return false
	}
	// only signal a process that is a samcheonpo daemon using this home
	env, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if os.IsNotExist(err) {
		return true
	}
	if err != nil || !strings.Contains(string(env), "SAMCHEONPO_HOME="+home+"\x00") {
		return false
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return errors.Is(err, syscall.ESRCH)
	}
	for i := 0; i < 100; i++ {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	return cmd.CombinedOutput()
}

// sessionID finds a session id in agent output (claude -p --output-format
// json or stream-json).
func sessionID(out []byte) string {
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	id := ""
	for sc.Scan() {
		var m struct {
			SessionID string `json:"session_id"`
			ThreadID  string `json:"thread_id"`
			Type      string `json:"type"`
		}
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			candidate := m.SessionID
			if m.Type == "thread.started" {
				candidate = m.ThreadID
			}
			if candidate != "" {
				if id != "" && id != candidate {
					return ""
				}
				id = candidate
			}
		}
	}
	if sc.Err() != nil {
		return ""
	}
	return id
}

func findTranscript(claudeDir, id string) string {
	if id == "" || strings.ContainsAny(id, "/*?[") {
		return ""
	}
	ms, _ := filepath.Glob(filepath.Join(claudeDir, "*", id+".jsonl"))
	if len(ms) == 0 {
		return ""
	}
	return ms[0]
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(t, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		st, _ := d.Info()
		return os.WriteFile(t, b, st.Mode().Perm())
	})
}

// ArmSummary aggregates one arm.
type ArmSummary struct {
	Arm                  string        `json:"arm"`
	Trials               int           `json:"trials"`
	Verified             int           `json:"verified"`
	Rate                 float64       `json:"success_rate"`
	CI                   eval.Interval `json:"success_ci95"`
	CostPerVerified      float64       `json:"krw_per_verified"` // NaN-free: 0 when nothing verified
	WasteShare           float64       `json:"waste_share"`
	CostKnown            int           `json:"cost_known"`
	CostPerVerifiedKnown bool          `json:"cost_per_verified_known"`
	CostMissingRate      float64       `json:"cost_missing_rate"`
	TotalCostPerVerified *float64      `json:"krw_per_verified_including_monitor,omitempty"`
	// Comparison against the baseline arm
	DiffLo         float64            `json:"success_diff_lo,omitempty"`
	NonInferior    *bool              `json:"non_inferior,omitempty"`
	CostReduction  float64            `json:"cost_reduction,omitempty"`
	MeetsCostGoal  *bool              `json:"meets_cost_goal,omitempty"`
	IsBaseline     bool               `json:"baseline,omitempty"`
	SampleTooSmall bool               `json:"sample_too_small,omitempty"`
	Comparison     *ClusterComparison `json:"comparison,omitempty"`
}

// Report gates: non-inferior success rate with a 5 point
// margin, and 20% lower cost per verified completion.
const (
	NonInferiorMargin = 0.05
	CostGoal          = 0.20
	MinTrials         = 30
)

// Summarize groups result rows by arm and compares each to baseline.
func Summarize(rows []TrialResult, baseline string) []ArmSummary {
	by := map[string][]TrialResult{}
	for _, r := range rows {
		by[r.Arm] = append(by[r.Arm], r)
	}
	var out []ArmSummary
	for arm, rs := range by {
		s := ArmSummary{Arm: arm, Trials: len(rs), IsBaseline: arm == baseline}
		var total, waste, monitor float64
		monitorKnown := true
		for _, r := range rs {
			if r.Verified {
				s.Verified++
			}
			if r.CostKnown {
				s.CostKnown++
				total += float64(r.TotalKRW)
				waste += float64(r.WasteKRW)
			}
			if r.MonitorKRW == nil || *r.MonitorKRW < 0 {
				monitorKnown = false
			} else {
				monitor += float64(*r.MonitorKRW)
			}
		}
		s.Rate = float64(s.Verified) / float64(s.Trials)
		s.CI = eval.Wilson(s.Verified, s.Trials)
		if s.Verified > 0 && s.CostKnown == s.Trials {
			s.CostPerVerified = float64(total) / float64(s.Verified)
			s.CostPerVerifiedKnown = true
			if monitorKnown {
				amount := (total + monitor) / float64(s.Verified)
				s.TotalCostPerVerified = &amount
			}
		}
		s.CostMissingRate = 1 - float64(s.CostKnown)/float64(s.Trials)
		if total > 0 {
			s.WasteShare = float64(waste) / float64(total)
		}
		s.SampleTooSmall = s.Trials < MinTrials
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsBaseline != out[j].IsBaseline {
			return out[i].IsBaseline
		}
		return out[i].Arm < out[j].Arm
	})
	var base *ArmSummary
	for i := range out {
		if out[i].IsBaseline {
			base = &out[i]
		}
	}
	if base == nil {
		return out
	}
	for i := range out {
		s := &out[i]
		if s.IsBaseline {
			continue
		}
		// Newcombe's Wilson-score difference bound remains non-degenerate at
		// 0% and 100%. A small pilot never establishes non-inferiority.
		s.DiffLo = (s.Rate - base.Rate) - math.Hypot(s.Rate-s.CI.Lo, base.CI.Hi-base.Rate)
		comparison := compareClusters(rows, baseline, s.Arm, len(out)-1)
		s.Comparison = &comparison
		if comparison.SuccessDifference != nil {
			ni := comparison.SuccessDifference.Lo >= -NonInferiorMargin
			s.NonInferior = &ni
		}
		if comparison.CostReduction != nil {
			goal := comparison.CostReduction.Lo >= CostGoal
			s.MeetsCostGoal = &goal
		}
		if base.CostPerVerifiedKnown && s.CostPerVerifiedKnown && base.CostPerVerified > 0 {
			s.CostReduction = 1 - s.CostPerVerified/base.CostPerVerified
			// A point estimate alone does not establish savings. The experiment
			// protocol must supply an uncertainty-aware decision separately.
		}
		if base.TotalCostPerVerified != nil && s.TotalCostPerVerified != nil && *base.TotalCostPerVerified > 0 {
			s.CostReduction = 1 - *s.TotalCostPerVerified / *base.TotalCostPerVerified
		}
	}
	return out
}

// Markdown renders the comparison table for publication.
func Markdown(ss []ArmSummary) string {
	var b strings.Builder
	b.WriteString("| 실행 방식 | 시행 | 검증 완료 | 성공률 (95% 구간) | 완료당 비용 (감시 포함) | 에이전트 헛짓 비율 | 성공률 비열등 | 비용 20% 절감 |\n")
	b.WriteString("|-|-|-|-|-|-|-|-|\n")
	yn := func(p *bool) string {
		switch {
		case p == nil:
			return "-"
		case *p:
			return "충족"
		}
		return "미충족"
	}
	for _, s := range ss {
		name := s.Arm
		if s.IsBaseline {
			name += " (기준)"
		}
		cpv := "-"
		if s.TotalCostPerVerified != nil {
			cpv = fmt.Sprintf("%.0f원", *s.TotalCostPerVerified)
		}
		red := ""
		if s.MeetsCostGoal != nil {
			red = fmt.Sprintf(" (%+.0f%%)", -100*s.CostReduction)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %.0f%% (%.0f–%.0f%%) | %s%s | %.1f%% | %s | %s |\n", name, s.Trials, s.Verified,
			100*s.Rate, 100*s.CI.Lo, 100*s.CI.Hi, cpv, red, 100*s.WasteShare, yn(s.NonInferior), yn(s.MeetsCostGoal))
	}
	for _, s := range ss {
		if s.Comparison != nil {
			fmt.Fprintf(&b, "\n%s: 과제 %d개, 짝 블록 %d개. %s\n", s.Arm, s.Comparison.Tasks, s.Comparison.Blocks, s.Comparison.Reason)
			if ci := s.Comparison.CostReduction; ci != nil {
				fmt.Fprintf(&b, "감시 비용 포함 비용 감소 구간 (%.1f%%): %.1f%%–%.1f%%. 탐색적 재표집이며 공개 과제 결과는 출시 입증이 아님.\n", 100*s.Comparison.Confidence, 100*ci.Lo, 100*ci.Hi)
			}
		}
	}
	for _, s := range ss {
		if s.SampleTooSmall {
			fmt.Fprintf(&b, "\n시행이 %d회 미만인 방식이 있어 참고용이며 비열등 판정은 보류한다.\n", MinTrials)
			break
		}
	}
	return b.String()
}
