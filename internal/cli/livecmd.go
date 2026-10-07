package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/install"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/live"
)

func liveCommands() []*cobra.Command {
	return []*cobra.Command{daemonCmd(), hookCmd(), statuslineCmd(), slashCmd(), installCmd(), uninstallCmd(), benchHookCmd(), doctorCmd()}
}

func daemonCmd() *cobra.Command {
	var idle time.Duration
	c := &cobra.Command{
		Use:   "daemon",
		Short: "실시간 판정 데몬 (훅이 자동으로 띄운다)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return live.Run(dbPath(), idle)
		},
	}
	c.Flags().DurationVar(&idle, "idle", 30*time.Minute, "요청이 없으면 종료할 시간")
	return c
}

// hookCmd is reached only through cobra when main's fast path was bypassed.
func hookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "hook <event>",
		Short:  "에이전트 훅 클라이언트",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			hookclient.Main(args[0], cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

func statuslineCmd() *cobra.Command {
	var wrap string
	c := &cobra.Command{
		Use:   "statusline",
		Short: "Claude Code 상태줄 출력",
		RunE: func(cmd *cobra.Command, args []string) error {
			in, _ := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
			var p struct {
				SessionID string `json:"session_id"`
			}
			_ = json.Unmarshal(in, &p)
			ours := ""
			if p.SessionID != "" {
				if t, _, ok := hookclient.Query("Statusline", map[string]string{"session_id": p.SessionID}, 80*time.Millisecond); ok {
					ours = t
				}
			}
			theirs := ""
			if wrap != "" {
				c := exec.Command("/bin/sh", "-c", wrap)
				c.Stdin = bytes.NewReader(in)
				if out, err := c.Output(); err == nil {
					theirs = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
				}
			}
			switch {
			case ours != "" && theirs != "":
				fmt.Fprintln(cmd.OutOrStdout(), theirs+" | "+ours)
			case ours != "":
				fmt.Fprintln(cmd.OutOrStdout(), ours)
			default:
				fmt.Fprintln(cmd.OutOrStdout(), theirs)
			}
			return nil
		},
	}
	c.Flags().StringVar(&wrap, "wrap", "", "기존 상태줄 명령 (함께 출력)")
	return c
}

func slashCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cmd <accept|edit|skip|keep|steer|summary|check|status|card|rollback> [arg]",
		Short: "플러그인 슬래시 명령 처리",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, _ := os.Getwd()
			arg := ""
			if len(args) == 2 {
				arg = args[1]
			}
			text, errText, ok := hookclient.Query("Command", live.CommandInput{Name: args[0], Root: wd, Session: os.Getenv("CLAUDE_SESSION_ID"), Arg: arg}, 170*time.Second)
			if !ok {
				return fmt.Errorf("삼천포 데몬에 연결하지 못했다. 에이전트 세션이 시작되면 자동으로 뜬다")
			}
			if errText != "" {
				return fmt.Errorf("%s", errText)
			}
			fmt.Fprintln(cmd.OutOrStdout(), text)
			return nil
		},
	}
}

func installCmd() *cobra.Command {
	var agent, settings, hooksFile string
	var noCLI bool
	c := &cobra.Command{
		Use:   "install",
		Short: "에이전트에 삼천포를 연결한다",
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			exe, _ = filepath.EvalSymlinks(exe)
			switch agent {
			case "claude":
			case "codex":
				if hooksFile == "" {
					hooksFile = filepath.Join(codexHome(), "hooks.json")
				}
				w := cmd.OutOrStdout()
				return install.InstallHooks(install.Codex, exe, hooksFile, func(s string) { fmt.Fprintln(w, s) })
			case "opencode":
				return installOpencode(cmd, exe)
			case "copilot":
				wd, _ := os.Getwd()
				return install.CopilotFile(exe, wd, func(s string) { fmt.Fprintln(cmd.OutOrStdout(), s) })
			case "cursor":
				if hooksFile == "" {
					h, _ := os.UserHomeDir()
					hooksFile = filepath.Join(h, ".cursor", "hooks.json")
				}
				return install.InstallCursor(exe, hooksFile, func(s string) { fmt.Fprintln(cmd.OutOrStdout(), s) })
			case "agy":
				if hooksFile == "" {
					wd, _ := os.Getwd()
					hooksFile = filepath.Join(wd, ".agents", "hooks.json")
				}
				return install.InstallHooks(install.Agy, exe, hooksFile, func(s string) { fmt.Fprintln(cmd.OutOrStdout(), s+" (관찰 전용)") })
			default:
				return fmt.Errorf("지원하는 에이전트: claude, codex, opencode, copilot, cursor, agy")
			}
			if settings == "" {
				settings = install.DefaultSettings()
			}
			w := cmd.OutOrStdout()
			return install.Install(install.Options{Binary: exe, SettingsPath: settings, UsePluginCLI: !noCLI, Out: func(s string) { fmt.Fprintln(w, s) }})
		},
	}
	c.Flags().StringVar(&agent, "agent", "claude", "대상 에이전트 (claude, codex, opencode)")
	c.Flags().StringVar(&settings, "settings", "", "Claude Code settings.json 경로")
	c.Flags().StringVar(&hooksFile, "hooks-file", "", "Codex hooks.json 경로 (기본 ~/.codex/hooks.json, 프로젝트는 .codex/hooks.json)")
	c.Flags().BoolVar(&noCLI, "no-plugin-cli", false, "claude plugin 명령으로 등록하지 않는다")
	return c
}

func uninstallCmd() *cobra.Command {
	var agent string
	c := &cobra.Command{
		Use:   "uninstall",
		Short: "설치한 연결을 되돌린다",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			say := func(s string) { fmt.Fprintln(w, s) }
			switch agent {
			case "claude":
				return install.Uninstall(say)
			case "codex":
				return install.UninstallHooks("codex", say)
			case "opencode":
				return install.UninstallOpencode(say)
			case "copilot":
				wd, _ := os.Getwd()
				return install.RemoveCopilotFile(wd, say)
			case "cursor":
				return install.UninstallCursor(say)
			case "agy":
				return install.UninstallHooks("agy", say)
			}
			return fmt.Errorf("지원하는 에이전트: claude, codex, opencode, copilot, cursor, agy")
		},
	}
	c.Flags().StringVar(&agent, "agent", "claude", "대상 에이전트")
	return c
}

func codexHome() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codex")
}

// benchHookCmd measures PreToolUse round trips through the real client.
func benchHookCmd() *cobra.Command {
	var n int
	c := &cobra.Command{
		Use:   "bench-hook",
		Short: "PreToolUse 훅 왕복 시간 측정",
		RunE: func(cmd *cobra.Command, args []string) error {
			if n < 1 || n > 10000 {
				return fmt.Errorf("n은 1~10000이어야 한다")
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			hookExe := hookclient.Executable(exe)
			fmt.Fprintf(cmd.OutOrStdout(), "훅 실행 파일: %s\n", hookExe)
			root, err := os.MkdirTemp("", "samcheonpo-bench-")
			if err != nil {
				return err
			}
			home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
			if err := os.MkdirAll(project, 0700); err != nil {
				return err
			}
			previous, hadHome := os.LookupEnv("SAMCHEONPO_HOME")
			if err := os.Setenv("SAMCHEONPO_HOME", home); err != nil {
				return err
			}
			defer func() {
				if hadHome {
					_ = os.Setenv("SAMCHEONPO_HOME", previous)
				} else {
					_ = os.Unsetenv("SAMCHEONPO_HOME")
				}
			}()
			defer func() {
				_, _, _ = hookclient.Query("Shutdown", map[string]string{}, time.Second)
				for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
					if _, err := os.Stat(live.SocketPath()); os.IsNotExist(err) {
						_ = os.RemoveAll(root)
						return
					}
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "시험 데몬 종료 미확인: 진단 디렉터리 보존 %s\n", root)
			}()
			const session = "bench-hook-isolated"
			run := func(event string, payload any, cold bool) (time.Duration, hookclient.Outcome, error) {
				p, err := json.Marshal(payload)
				if err != nil {
					return 0, hookclient.Outcome{}, err
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, hookExe, "hook", event)
				child.Dir = project
				child.Stdin = bytes.NewReader(p)
				spawn := "1"
				if cold {
					spawn = "0"
				}
				child.Env = append(os.Environ(), "SAMCHEONPO_NO_SPAWN="+spawn, "SAMCHEONPO_HOOK_DIAGNOSTICS=1")
				var diagnostics bytes.Buffer
				child.Stderr = &diagnostics
				start := time.Now()
				err = child.Run()
				elapsed := time.Since(start)
				var outcome hookclient.Outcome
				if err != nil {
					return elapsed, outcome, err
				}
				if err = json.Unmarshal(diagnostics.Bytes(), &outcome); err != nil {
					return elapsed, outcome, fmt.Errorf("훅 진단 응답 누락/형식 오류: %w", err)
				}
				return elapsed, outcome, nil
			}
			cold, first, err := run("UserPromptSubmit", map[string]any{"session_id": session, "cwd": project, "prompt": "isolated first event"}, true)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "콜드 스타트: %v, 상태 %s, 타임아웃 %t\n", cold, first.Status, first.TimedOut)
			// Warm measurements must not include the one-time version subprocess.
			for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
				text, _, ok := hookclient.Query("AgentStatus", map[string]string{"agent": "claude"}, time.Second)
				var status struct{ Ready bool }
				if ok && json.Unmarshal([]byte(text), &status) == nil && status.Ready {
					break
				}
			}
			var ds []time.Duration
			var handling, overhead []time.Duration
			timeouts, failures, responses := 0, 0, 0
			for i := 0; i < n; i++ {
				elapsed, outcome, err := run("PreToolUse", map[string]any{"session_id": session, "cwd": project, "tool_name": "Read", "tool_input": map[string]string{"file_path": "README.md"}, "tool_use_id": fmt.Sprintf("t%d", i)}, false)
				if err != nil {
					return err
				}
				if outcome.TimedOut {
					timeouts++
				}
				if outcome.Status != "ok" {
					failures++
				}
				if outcome.ResponseReceived {
					responses++
				}
				ds = append(ds, elapsed)
				if outcome.HandlingNS > 0 {
					inside := time.Duration(outcome.HandlingNS)
					handling = append(handling, inside)
					overhead = append(overhead, max(0, elapsed-inside))
				}
			}
			text, errText, ok := hookclient.Query("ObservationStatus", map[string]string{"session_id": session}, time.Second)
			var observed struct {
				Prompts       int    `json:"prompts"`
				ToolCalls     int    `json:"tool_calls"`
				QueueRejected uint64 `json:"queue_rejected"`
			}
			if !ok || errText != "" || json.Unmarshal([]byte(text), &observed) != nil {
				return fmt.Errorf("서버 수신 여부를 확인하지 못했다")
			}
			sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
			pct := func(q float64) time.Duration { return ds[int(q*float64(len(ds)-1))] }
			fmt.Fprintf(cmd.OutOrStdout(), "PreToolUse 훅 %d회 (프로세스 기동 포함): p50 %v, p95 %v, p99 %v, 최대 %v\n", n, pct(0.5), pct(0.95), pct(0.99), ds[len(ds)-1])
			if len(handling) == n {
				sort.Slice(handling, func(i, j int) bool { return handling[i] < handling[j] })
				sort.Slice(overhead, func(i, j int) bool { return overhead[i] < overhead[j] })
				fmt.Fprintf(cmd.OutOrStdout(), "구간 진단: 입력/소켓/출력 처리 p95 %v, 프로세스 시작·종료/부모 대기 p95 %v (각각의 분포; 합산 불가)\n", handling[int(.95*float64(n-1))], overhead[int(.95*float64(n-1))])
			}
			fmt.Fprintf(cmd.OutOrStdout(), "응답 %d/%d, 타임아웃 %d, 실패 %d, 첫 요청 수신 %t, 도구 수신 %d/%d, 큐 누락 %d\n", responses, n, timeouts, failures, observed.Prompts == 1, observed.ToolCalls, n, observed.QueueRejected)
			if first.Status != "ok" || !first.ResponseReceived || observed.Prompts != 1 || observed.ToolCalls != n || observed.QueueRejected > 0 || failures > 0 || responses != n {
				return fmt.Errorf("훅 전송/관측 검증 실패: 빠른 실패는 지연 통과로 인정하지 않는다")
			}
			if pct(0.95) > 10*time.Millisecond {
				return fmt.Errorf("p95 %v가 10ms를 넘는다", pct(0.95))
			}
			return nil
		},
	}
	c.Flags().IntVar(&n, "n", 200, "반복 횟수")
	return c
}

// InterventionArm is the per-arm intervention statistic. Outcomes stay in
// their observed states: an advice whose rule did not recur is not "resolved",
// and an advice whose delivery was never confirmed is not counted as either.
type InterventionArm struct {
	Arm string `json:"arm"`
	N   int    `json:"n"`
	// Delivered advices whose follow-up window closed.
	Observed       int           `json:"observed"`
	NoRecurrence   int           `json:"no_recurrence_observed"`
	NoRecurrenceCI eval.Interval `json:"no_recurrence_ci"`
	Recurrence     int           `json:"recurrence_observed"`
	// Unconfirmed delivery, censored by a goal change, still open, or legacy.
	Unconfirmed        int           `json:"delivery_unconfirmed"`
	Censored           int           `json:"censored"`
	Pending            int           `json:"pending"`
	Legacy             int           `json:"legacy_unknown"`
	Progress           int           `json:"progress_after"`
	ProgressCI         eval.Interval `json:"progress_ci"`
	CostPerProgressKRW int64         `json:"cost_per_progress_krw"`
	Interrupted        int           `json:"user_interrupted"`
	Harm               int           `json:"harm"`
	HarmCI             eval.Interval `json:"harm_ci"`
}

// InterventionsReport groups intervention outcomes by experiment arm.
type InterventionsReport struct {
	Arms []InterventionArm `json:"arms"`
}

// Text renders the report.
func (r InterventionsReport) Text() string {
	var b strings.Builder
	b.WriteString("개입 실험 (L1 귀띔; 재발 미관측은 해결 확인이 아니다)\n")
	fmt.Fprintf(&b, "%-13s %5s %18s %6s %8s %6s %6s %16s %14s %8s %14s\n", "군", "건수", "재발 미관측/관찰", "재발", "전달미확인", "중단됨", "진행중", "진척 회복률", "진척당 비용", "중단", "중단 피해율")
	for _, a := range r.Arms {
		pr := func(k, n int, ci eval.Interval) string {
			if n == 0 {
				return "-"
			}
			return fmt.Sprintf("%d/%d (%.0f-%.0f%%)", k, n, ci.Lo*100, ci.Hi*100)
		}
		fmt.Fprintf(&b, "%-13s %5d %18s %6d %8d %6d %6d %16s %13s원 %8d %14s\n", a.Arm, a.N, pr(a.NoRecurrence, a.Observed, a.NoRecurrenceCI), a.Recurrence, a.Unconfirmed, a.Censored+a.Legacy, a.Pending, pr(a.Progress, a.N, a.ProgressCI), commaInt(a.CostPerProgressKRW), a.Interrupted, pr(a.Harm, a.N, a.HarmCI))
	}
	return b.String()
}

// InterventionReport computes per-arm statistics from the ledger.
func InterventionReport(db *ledger.DB) (InterventionsReport, error) {
	rows, err := db.Query(`SELECT v.id, v.session_id, v.seq, COALESCE(v.arm,''), COALESCE(i.outcome,''),
		(SELECT COUNT(*) FROM feedback f WHERE f.verdict_id=v.id AND f.reason='normal')
		FROM verdict v LEFT JOIN intervention i ON i.verdict_id=v.id
		WHERE v.is_primary=1 AND v.level=1 AND v.suppressed=0`)
	if err != nil {
		return InterventionsReport{}, err
	}
	type iv struct {
		id, sid, arm, outcome string
		seq                   int64
		normal                int
	}
	var ivs []iv
	for rows.Next() {
		var x iv
		if err := rows.Scan(&x.id, &x.sid, &x.seq, &x.arm, &x.outcome, &x.normal); err != nil {
			rows.Close()
			return InterventionsReport{}, err
		}
		if x.arm == "" {
			continue // audit-mode verdicts have no arm
		}
		ivs = append(ivs, x)
	}
	rows.Close()
	arms := map[string]*InterventionArm{}
	costs := map[string]int64{}
	for _, x := range ivs {
		a := arms[x.arm]
		if a == nil {
			a = &InterventionArm{Arm: x.arm}
			arms[x.arm] = a
		}
		a.N++
		switch x.outcome {
		case "no_recurrence_observed":
			a.Observed++
			a.NoRecurrence++
		case "recurrence_observed":
			a.Observed++
			a.Recurrence++
		case "delivery_unconfirmed", "unsupported_delivery":
			a.Unconfirmed++
		case "":
			a.Pending++
		case "resolved":
			a.Legacy++ // written by an older version with a different meaning
		default:
			a.Censored++
		}
		if x.normal > 0 {
			a.Interrupted++
		}
		var before, after int
		var cost int64
		_ = db.QueryRow(`SELECT COUNT(*) FROM event WHERE session_id=? AND seq>=? AND seq<? AND bucket='progress'`, x.sid, x.seq-20, x.seq).Scan(&before)
		_ = db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(cost_micro_krw),0) FROM event WHERE session_id=? AND seq>? AND seq<=? AND bucket='progress'`, x.sid, x.seq, x.seq+20).Scan(&after, new(int64))
		_ = db.QueryRow(`SELECT COALESCE(SUM(cost_micro_krw),0) FROM event WHERE session_id=? AND seq>? AND seq<=?`, x.sid, x.seq, x.seq+20).Scan(&cost)
		if after > 0 {
			a.Progress++
			costs[x.arm] += cost
		}
		if x.normal > 0 || (before > 0 && after == 0) {
			a.Harm++
		}
	}
	var out InterventionsReport
	for _, name := range []string{"none", "fact", "prescription"} {
		a := arms[name]
		if a == nil {
			a = &InterventionArm{Arm: name}
		}
		a.NoRecurrenceCI = eval.Wilson(a.NoRecurrence, a.Observed)
		a.ProgressCI = eval.Wilson(a.Progress, a.N)
		a.HarmCI = eval.Wilson(a.Harm, a.N)
		if a.Progress > 0 {
			a.CostPerProgressKRW = costs[name] / int64(a.Progress) / 1_000_000
		}
		out.Arms = append(out.Arms, *a)
	}
	return out, nil
}

func installOpencode(cmd *cobra.Command, exe string) error {
	w := cmd.OutOrStdout()
	return install.InstallOpencode(exe, install.OpencodePluginPath(), func(s string) { fmt.Fprintln(w, s) })
}
