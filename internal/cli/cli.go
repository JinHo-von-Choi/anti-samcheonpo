// Package cli implements the samcheonpo command line.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/otel"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intervene"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procgroup"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/seal"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

// Version is set at build time.
var Version = "0.5.0"

var (
	flagDB        string
	flagClaudeDir string
	flagCodexDir  string
)

// Root builds the command tree.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "samcheonpo",
		Short:         "AI 코딩 에이전트의 헛짓을 잡아 원화 영수증으로 보여 주는 하네스",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flagDB, "db", "", "원장 경로 (기본 ~/.samcheonpo/ledger.db)")
	root.PersistentFlags().StringVar(&flagClaudeDir, "claude-dir", "", "Claude Code projects 경로")
	root.PersistentFlags().StringVar(&flagCodexDir, "codex-dir", "", "Codex sessions 경로")
	root.AddCommand(auditCmd(), gapsCmd(), interventionsCmd(), receiptCmd(), sessionsCmd(), priceCmd(), initCmd(), verifyCmd(), exportCmd(),
		labelCmd(), evalCmd(), benchCmd(), debugCmd(), contractCmd(), specCmd(), importOtelCmd(), rulesCmd(), handoffCmd(), reportCmd())
	root.AddCommand(liveCommands()...)
	return root
}

func dbPath() string {
	if flagDB != "" {
		return flagDB
	}
	return filepath.Join(config.Home(), "ledger.db")
}

func openDB() (*ledger.DB, error) { return ledger.Open(dbPath()) }

func claudeDir() string {
	if flagClaudeDir != "" {
		return flagClaudeDir
	}
	return sources.ClaudeDir()
}

func codexDir() string {
	if flagCodexDir != "" {
		return flagCodexDir
	}
	return sources.CodexDir()
}

func loadPrices() (*cost.Table, error) {
	return cost.Load(filepath.Join(config.Home(), "prices.yml"))
}

var sinceRe = lazyre.New(`^(\d+)([dhm])$`)

// ParseSince parses durations like 30d, 12h, 90m or a date.
func ParseSince(s string, now time.Time) (time.Time, error) {
	if s == "" || s == "all" {
		return time.Time{}, nil
	}
	if m := sinceRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "d":
			return now.AddDate(0, 0, -n), nil
		case "h":
			return now.Add(-time.Duration(n) * time.Hour), nil
		default:
			return now.Add(-time.Duration(n) * time.Minute), nil
		}
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since는 30d, 12h 또는 2026-10-01 형식이어야 한다")
	}
	return t, nil
}

// analyzeFile parses and analyzes one transcript in audit mode.
func analyzeFile(f sources.File, cfg config.Config, cfgHash string, prices *cost.Table) (*analyze.Result, error) {
	s, err := sources.Parse(f)
	if err != nil {
		return nil, err
	}
	return analyze.Run(s, analyze.Options{Config: cfg, ConfigHash: cfgHash, Prices: prices})
}

// Messages renders stored verdict messages.
func Messages(goal string) func(detect.Signal) (string, string) {
	return func(v detect.Signal) (string, string) {
		c := intervene.Context{Goal: goal, UsageUnknown: true}
		return intervene.User(v, c), intervene.Agent(v, c, "prescription")
	}
}

type sessionSummary struct {
	ID, Agent, Prompt string
	Started           time.Time
	Totals            analyze.Totals
	FormatOK          bool
	QuotaPct          float64
	QuotaWin          int
}

func auditCmd() *cobra.Command {
	var since, agent, project, format, out string
	var workers int
	var refresh bool
	c := &cobra.Command{
		Use:   "audit",
		Short: "과거 세션 기록으로 멍청비용 영수증을 낸다",
		RunE: func(cmd *cobra.Command, args []string) error {
			start := time.Now()
			t, err := ParseSince(since, start)
			if err != nil {
				return err
			}
			cfg, _, err := config.Load("")
			if err != nil {
				return err
			}
			cfgHash := config.Hash(cfg)
			prices, err := loadPrices()
			if err != nil {
				return err
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			files := sources.Find(agent, t, claudeDir(), codexDir())
			if project != "" {
				abs, _ := pathnorm.Absolute(project)
				project = abs
			}
			var total, largest int64
			agents := map[string]int{}
			for _, f := range files {
				total += f.Size
				largest = max(largest, f.Size)
				agents[f.Agent]++
			}
			stderr := cmd.ErrOrStderr()
			// measured: ~25MB/s per worker overall, ~40MB/s for one large file
			nw := float64(max(1, min(workers, runtime.NumCPU())))
			est := max(float64(largest)/40e6, float64(total)/(nw*25e6))
			fmt.Fprintf(stderr, "사전 점검: 세션 기록 %d개 (Claude Code %d, Codex %d), %.0fMB, 예상 %.0f초\n",
				len(files), agents["claude"], agents["codex"], float64(total)/1e6, est)
			if len(files) == 0 {
				fmt.Fprintf(stderr, "분석할 세션 기록이 없다. 확인한 경로: %s, %s\n", claudeDir(), codexDir())
				return nil
			}
			var todo []sources.File
			for _, f := range files {
				if refresh || db.NeedsAudit(f.Path, f.Size, f.MTime) {
					todo = append(todo, f)
				}
			}
			// largest first so one big transcript does not finish last alone
			sort.SliceStable(todo, func(i, j int) bool { return todo[i].Size > todo[j].Size })
			type res struct {
				f   sources.File
				r   *analyze.Result
				err error
			}
			jobs := make(chan sources.File)
			results := make(chan res)
			var wg sync.WaitGroup
			for i := 0; i < max(1, workers); i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for f := range jobs {
						r, err := analyzeFile(f, cfg, cfgHash, prices)
						results <- res{f, r, err}
					}
				}()
			}
			go func() {
				for _, f := range todo {
					jobs <- f
				}
				close(jobs)
			}()
			go func() { wg.Wait(); close(results) }()
			failed := 0
			var firstErr error
			for x := range results {
				if x.err != nil {
					failed++
					if firstErr == nil {
						firstErr = fmt.Errorf("%s: %w", x.f.Path, x.err)
					}
					continue
				}
				if err := analyze.CheckInvariant(x.r.Session, x.r.Totals); err != nil {
					return fmt.Errorf("%s: %w", x.f.Path, err)
				}
				if db.Mode(x.r.Session.ID) == "live" {
					continue // the live record (with checkpoints and interventions) is kept
				}
				if _, err := db.SaveAnalysis(x.r, x.f.Size, x.f.MTime, Messages(x.r.Session.FirstPrompt)); err != nil {
					return fmt.Errorf("원장 기록 실패 %s: %w", x.f.Path, err)
				}
			}
			if failed > 0 {
				fmt.Fprintf(stderr, "읽지 못한 기록 %d개 (첫 오류: %v)\n", failed, firstErr)
			}
			rows, err := db.Sessions(t, agent, project)
			if err != nil {
				return err
			}
			rep, sums := aggregate(db, rows)
			fmt.Fprintf(stderr, "분석 %d개 (새로 분석 %d개), %.1f초\n", len(rows), len(todo), time.Since(start).Seconds())
			title := fmt.Sprintf("삼천포 소급 영수증 · 최근 %s · 세션 %d개", since, len(rows))
			rc := receipt.Build(receipt.Input{Title: title, Audit: true, Totals: rep, Grade: analyze.GradeEstimated})
			w := cmd.OutOrStdout()
			if out != "" {
				fh, err := os.Create(out)
				if err != nil {
					return err
				}
				defer fh.Close()
				w = fh
			}
			return writeAudit(w, rc, sums, format)
		},
	}
	c.Flags().StringVar(&since, "since", "30d", "분석 기간 (30d, 12h, 2026-10-01, all)")
	c.Flags().StringVar(&agent, "agent", "all", "claude | codex | all")
	c.Flags().StringVar(&project, "project", "", "특정 프로젝트 경로만")
	c.Flags().StringVar(&format, "format", "text", "text | md | json")
	c.Flags().StringVarP(&out, "output", "o", "", "출력 파일")
	c.Flags().IntVar(&workers, "workers", runtime.NumCPU(), "동시 분석 수")
	c.Flags().BoolVar(&refresh, "refresh", false, "바뀌지 않은 기록도 다시 분석")
	return c
}

// aggregate sums stored session totals for the audit receipt.
func aggregate(db *ledger.DB, rows []ledger.SessionRow) (analyze.Totals, []sessionSummary) {
	t := analyze.Totals{BucketMicro: map[string]int64{}, BucketTokens: map[string]int64{}, SymptomMicro: map[string]int64{},
		SymptomTokens: map[string]int64{}, SymptomCount: map[string]int{}, Interventions: map[string]int{}}
	var sums []sessionSummary
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	bucket := db.BucketTotals(ids)
	for _, r := range rows {
		b := bucket[r.ID]
		t.Micro += b.Micro
		t.Tokens += b.Tokens
		t.UnpricedTokens += b.UnpricedTokens
		for k, v := range b.BucketMicro {
			t.BucketMicro[k] += v
		}
		for k, v := range b.BucketTokens {
			t.BucketTokens[k] += v
		}
		for k, v := range b.SymptomMicro {
			t.SymptomMicro[k] += v
		}
		for k, v := range b.SymptomTokens {
			t.SymptomTokens[k] += v
		}
		for k, v := range b.SymptomCount {
			t.SymptomCount[k] += v
		}
		for k, v := range b.Interventions {
			t.Interventions[k] += v
		}
		t.EstimatedMicro += b.EstimatedMicro
		t.EstimatedTokens += b.EstimatedTokens
		t.Minutes += b.Minutes
		sums = append(sums, sessionSummary{ID: r.ID, Agent: r.Agent, Prompt: r.Prompt, Started: r.Started, Totals: b, FormatOK: r.FormatOK, QuotaPct: r.QuotaPct})
	}
	return t, sums
}

func writeAudit(w io.Writer, rc receipt.Receipt, sums []sessionSummary, format string) error {
	sort.SliceStable(sums, func(i, j int) bool {
		a, b := sums[i].Totals, sums[j].Totals
		if a.SymptomTotal() != b.SymptomTotal() {
			return a.SymptomTotal() > b.SymptomTotal()
		}
		return a.SymptomTokenTotal() > b.SymptomTokenTotal()
	})
	top := sums
	if len(top) > 5 {
		top = top[:5]
	}
	formatFail := 0
	for _, s := range sums {
		if !s.FormatOK {
			formatFail++
		}
	}
	if formatFail > 0 {
		rc.Notices = append(rc.Notices, fmt.Sprintf("형식 인식 실패 %d개 세션은 판정에서 제외", formatFail))
	}
	switch format {
	case "json":
		type topRow struct {
			ID          string  `json:"id"`
			Agent       string  `json:"agent"`
			Started     string  `json:"started"`
			Prompt      string  `json:"prompt"`
			WasteWon    int64   `json:"waste_won"`
			WasteTokens int64   `json:"waste_tokens"`
			TotalWon    int64   `json:"total_won"`
			WastePct    float64 `json:"waste_pct"`
		}
		var tr []topRow
		for _, s := range top {
			tr = append(tr, topRow{s.ID, s.Agent, s.Started.Format(time.RFC3339), s.Prompt, cost.Won(s.Totals.SymptomTotal()),
				s.Totals.SymptomTokenTotal(), cost.Won(s.Totals.Micro), pctOf(s.Totals)})
		}
		b, _ := json.MarshalIndent(map[string]any{"receipt": rc, "top_sessions": tr}, "", "  ")
		_, err := fmt.Fprintln(w, string(b))
		return err
	case "md":
		fmt.Fprint(w, rc.Markdown())
		fmt.Fprint(w, "\n### 헛짓 상위 세션\n\n| 세션 | 시작 | 헛짓 | 헛짓률 | 첫 요청 |\n| --- | --- | ---: | ---: | --- |\n")
		for _, s := range top {
			fmt.Fprintf(w, "| `%s` | %s | %s | %.0f%% | %s |\n", short8(s.ID), s.Started.Local().Format("01-02 15:04"), amountOf(rc, s.Totals), pctOf(s.Totals), strings.ReplaceAll(s.Prompt, "|", "\\|"))
		}
		return nil
	default:
		fmt.Fprint(w, rc.Text())
		fmt.Fprintln(w, "\n헛짓 상위 세션")
		for _, s := range top {
			fmt.Fprintf(w, "  %s  %s  %s  헛짓 %3.0f%%  %s\n", short8(s.ID), s.Started.Local().Format("01-02 15:04"),
				padL(amountOf(rc, s.Totals), 12), pctOf(s.Totals), trunc(s.Prompt, 40))
		}
		fmt.Fprintln(w, "\n세션 하나의 근거는 samcheonpo receipt <세션> --evidence")
		return nil
	}
}

func amountOf(rc receipt.Receipt, t analyze.Totals) string {
	if rc.Unit == "tokens" {
		return receipt.HumanTokens(t.SymptomTokenTotal())
	}
	return contract.Comma(cost.Won(t.SymptomTotal())) + "원"
}

func pctOf(t analyze.Totals) float64 {
	if t.Micro > 0 && t.PriceCoverage() >= 0.9 {
		return float64(t.SymptomTotal()) / float64(t.Micro) * 100
	}
	if t.Tokens == 0 {
		return 0
	}
	return float64(t.SymptomTokenTotal()) / float64(t.Tokens) * 100
}

func padL(s string, n int) string {
	if d := n - receipt.Width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

func short8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// reanalyze re-runs a stored session from its source.
func reanalyze(db *ledger.DB, id string) (*analyze.Result, sources.File, error) {
	full, src, agent, err := db.SourceOf(id)
	if err != nil {
		return nil, sources.File{}, fmt.Errorf("세션 %s를 원장에서 찾지 못했다. 먼저 samcheonpo audit을 실행한다: %w", id, err)
	}
	if src == "" {
		return nil, sources.File{}, fmt.Errorf("세션 %s의 원본 기록 경로가 없다", full)
	}
	st, err := os.Stat(src)
	if err != nil {
		return nil, sources.File{}, err
	}
	f := sources.File{Path: src, Agent: agent, Size: st.Size(), MTime: st.ModTime().UnixNano()}
	cfg, _, err := config.Load("")
	if err != nil {
		return nil, f, err
	}
	prices, err := loadPrices()
	if err != nil {
		return nil, f, err
	}
	r, err := analyzeFile(f, cfg, config.Hash(cfg), prices)
	return r, f, err
}

func receiptCmd() *cobra.Command {
	var format string
	var billingKind string
	var evidence, share bool
	c := &cobra.Command{
		Use:   "receipt <session-id>",
		Short: "세션 하나의 영수증",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cost.ValidBillingKind(cost.BillingKind(billingKind)) {
				return fmt.Errorf("billing: unknown, api, subscription 중 하나가 필요하다")
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			r, _, err := reanalyze(db, args[0])
			if err != nil {
				return err
			}
			sl, _ := ledger.BuildSeal(r)
			s := r.Session
			observation := cost.ObserveSession(s, r.PriceVersion)
			budget, err := db.JudgeBudget(s.Agent, s.ID)
			if err != nil {
				return err
			}
			if budget.Unknown || budget.Pending {
				observation.UsageComplete = false
				observation.Missing = append(observation.Missing, "감시 판정 비용 미확인: 완료되지 않은 예약을 무료로 계산하지 않는다")
			}
			if billingKind != "unknown" {
				observation.Kind = cost.BillingKind(billingKind)
				observation.KindSource = "user_declared"
			}
			recoveries, err := db.Recoveries(s.Agent, s.ID)
			if err != nil {
				return err
			}
			rc := receipt.Build(receipt.Input{Title: receipt.SessionTitle(s.StartedAt, s.ID, s.Agent), Agent: s.Agent, Audit: s.Mode == "audit",
				Totals: r.Totals, Grade: r.Grade, Verdicts: r.Verdicts, Seal: &sl, Billing: observation, Recoveries: recoveries})
			if !r.FormatOK {
				rc.Notices = append(rc.Notices, "형식 인식 실패: 판정을 중단하고 비용만 합산했다")
			}
			if evidence && !share {
				sm := map[int64]string{}
				for _, ev := range s.Events {
					sm[ev.Seq] = ev.Summary
				}
				rc.WithEvidence(r.Verdicts, sm)
			}
			if share {
				rc = rc.Share()
			}
			w := cmd.OutOrStdout()
			switch format {
			case "json":
				fmt.Fprint(w, rc.JSON())
			case "md":
				fmt.Fprint(w, rc.Markdown())
			default:
				fmt.Fprint(w, rc.Text())
			}
			if share && format != "json" {
				fmt.Fprintf(w, "\n이 숫자는 samcheonpo verify로 다시 계산할 수 있다 (봉인 %s).\n", sl.Short())
			}
			return nil
		},
	}
	c.Flags().StringVar(&format, "format", "text", "text | md | json")
	c.Flags().BoolVar(&evidence, "evidence", false, "근거 이벤트 펼치기")
	c.Flags().BoolVar(&share, "share", false, "코드, 경로, 프롬프트 없는 공유용")
	c.Flags().StringVar(&billingKind, "billing", "unknown", "과금 유형을 명시: unknown | api | subscription (청구액 검증 아님)")
	return c
}

func sessionsCmd() *cobra.Command {
	var since, agent string
	c := &cobra.Command{
		Use:   "sessions",
		Short: "분석된 세션 목록과 헛짓률",
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := ParseSince(since, time.Now())
			if err != nil {
				return err
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			rows, err := db.Sessions(t, agent, "")
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%-8s  %-6s  %-11s  %12s  %6s  %s\n", "세션", "에이전트", "시작", "총 지출", "헛짓", "첫 요청")
			for _, r := range rows {
				tot := contract.Comma(cost.Won(r.Micro)) + "원"
				pct := 0.0
				if r.Micro > 0 {
					pct = float64(r.WasteMicro) / float64(r.Micro) * 100
				} else if r.Tokens > 0 {
					pct = float64(r.WasteTokens) / float64(r.Tokens) * 100
				}
				fmt.Fprintf(w, "%-8s  %-6s  %-11s  %12s  %5.0f%%  %s\n", short8(r.ID), r.Agent, r.Started.Local().Format("01-02 15:04"), tot, pct, trunc(r.Prompt, 40))
			}
			return nil
		},
	}
	c.Flags().StringVar(&since, "since", "7d", "기간")
	c.Flags().StringVar(&agent, "agent", "all", "claude | codex | all")
	return c
}

func priceCmd() *cobra.Command {
	c := &cobra.Command{Use: "price", Short: "단가표와 환율"}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "단가표 출력",
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := loadPrices()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "단가표 버전 %s (USD / 1M 토큰)\n", t.Version[:12])
			for _, f := range t.FX {
				fmt.Fprintf(w, "환율 %s부터 1 USD = %g원\n", f.ValidFrom.Format("2006-01-02"), f.USDKRW)
			}
			for _, p := range t.Models {
				fmt.Fprintf(w, "%-20s 입력 %7.3f 출력 %7.3f 캐시쓰기 %7.3f 캐시읽기 %7.3f  %s부터  %s\n", p.Model, p.Input, p.Output, p.CacheWrite, p.CacheRead,
					p.ValidFrom.Format("2006-01-02"), p.Source)
			}
			return nil
		},
	})
	var from string
	setfx := &cobra.Command{
		Use:   "set-fx RATE",
		Short: "기간별 환율 추가 (~/.samcheonpo/prices.yml)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rate, err := strconv.ParseFloat(args[0], 64)
			if err != nil || rate <= 0 {
				return fmt.Errorf("환율은 양수여야 한다: %s", args[0])
			}
			d, err := time.Parse("2006-01-02", from)
			if err != nil {
				return fmt.Errorf("--from은 2026-10-01 형식이어야 한다")
			}
			p := filepath.Join(config.Home(), "prices.yml")
			if err := procgroup.MkdirAllPrivate(config.Home()); err != nil {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("단가표를 읽지 못해 환율을 추가하지 않았다: %w", err)
			}
			s := string(b)
			if !strings.Contains(s, "fx:") {
				s = "fx:\n" + s
			}
			s = strings.Replace(s, "fx:\n", "fx:\n"+cost.SetFXLine(rate, d), 1)
			if err := writeAtomic(p, []byte(s)); err != nil {
				return err
			}
			if _, err := loadPrices(); err != nil {
				return fmt.Errorf("추가한 환율로 단가표를 읽지 못했다: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s부터 1 USD = %g원\n", from, rate)
			return nil
		},
	}
	setfx.Flags().StringVar(&from, "from", time.Now().Format("2006-01-02"), "적용 시작일")
	c.AddCommand(setfx)
	return c
}

func writeAtomic(p string, b []byte) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return procgroup.Rename(tmp, p)
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: ".samcheonpo.yml 생성",
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ".samcheonpo.yml"
			if _, err := os.Stat(p); err == nil {
				return fmt.Errorf("%s가 이미 있다", p)
			}
			if err := os.WriteFile(p, []byte(config.Template), 0o644); err != nil {
				return err
			}
			cands := contract.Candidates(".")
			fmt.Fprintf(cmd.OutOrStdout(), "%s를 만들었다.\n", p)
			switch added, err := ignoreStateDir(".gitignore"); {
			case err != nil:
				fmt.Fprintf(cmd.OutOrStdout(), ".gitignore를 고치지 못했다: %v. .samcheonpo/를 직접 넣어 둔다.\n", err)
			case added:
				fmt.Fprintln(cmd.OutOrStdout(), ".gitignore에 .samcheonpo/를 넣었다 (수락 기록과 통과 시점 기록이 커밋에 섞이지 않도록).")
			}
			if len(cands) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "이 저장소에서 찾은 완료 조건 후보:")
				for _, c := range cands {
					fmt.Fprintf(cmd.OutOrStdout(), "  %-28s %s (%s)\n", c.Command, c.Kind, c.Source)
				}
			}
			return nil
		},
	}
}

// Bundle is an exported replay bundle (Evidence Ledger v1).
type Bundle struct {
	Spec   string       `json:"spec"`
	Seal   seal.Seal    `json:"seal"`
	Rows   []seal.Row   `json:"rows"`
	Prices []cost.Price `json:"prices"`
	FX     []cost.FX    `json:"fx"`
}

func exportCmd() *cobra.Command {
	var out string
	var redact, asOtel bool
	c := &cobra.Command{
		Use:   "export <session-id>",
		Short: "경로와 명령을 가린 재현 묶음, 또는 OTLP/JSON 트레이스",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if asOtel {
				db, err := openDB()
				if err != nil {
					return err
				}
				defer db.Close()
				r, _, err := reanalyze(db, args[0])
				if err != nil {
					return err
				}
				b, err := otel.Encode(r.Session)
				if err != nil {
					return err
				}
				if out == "" {
					out = r.Session.ID + ".otlp.jsonl"
				}
				if err := writeAtomic(out, append(b, '\n')); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), out)
				return nil
			}
			if !redact {
				return errors.New("--redact 묶음 또는 --otel 트레이스를 고른다")
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			r, _, err := reanalyze(db, args[0])
			if err != nil {
				return err
			}
			sl, rows := ledger.BuildSeal(r)
			prices, _ := loadPrices()
			used := map[string]bool{}
			for _, ev := range r.Session.Events {
				used[ev.Usage.Model] = true
			}
			var ps []cost.Price
			for _, p := range prices.Models {
				for m := range used {
					if q, ok := prices.Lookup(m, time.Time{}); ok && q.Model == p.Model {
						ps = append(ps, p)
						break
					}
				}
			}
			sl.SourceHash = ""
			b := Bundle{Spec: seal.SpecVersion, Seal: sl, Rows: rows, Prices: ps, FX: prices.FX}
			data, _ := json.MarshalIndent(b, "", " ")
			if out == "" {
				out = r.Session.ID + ".bundle.json"
			}
			if err := writeAtomic(out, data); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s (행 %d개, 봉인 %s)\n", out, len(rows), sl.Short())
			return nil
		},
	}
	c.Flags().StringVarP(&out, "output", "o", "", "출력 파일")
	c.Flags().BoolVar(&redact, "redact", false, "경로·명령·원문 제외 재현 묶음")
	c.Flags().BoolVar(&asOtel, "otel", false, "OpenTelemetry GenAI 트레이스(OTLP/JSON)로 내보내기 (정규화 명령과 경로 포함)")
	return c
}

func verifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <session-id|bundle.json>",
		Short: "재계산해 봉인 값과 대조",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			if strings.HasSuffix(args[0], ".json") {
				return verifyBundle(w, args[0])
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			full, _, _, err := db.SourceOf(args[0])
			if err != nil {
				return fmt.Errorf("세션을 찾지 못했다: %w", err)
			}
			stored, err := db.Seal(full)
			if err != nil {
				return fmt.Errorf("봉인이 없다: %w", err)
			}
			r, _, err := reanalyze(db, full)
			if err != nil {
				return err
			}
			got, _ := ledger.BuildSeal(r)
			var diffs []string
			if got.SourceHash != stored.SourceHash {
				diffs = append(diffs, "원본 기록이 봉인 이후 바뀌었다")
			}
			if got.Evaluator != stored.Evaluator || got.PriceVersion != stored.PriceVersion || got.ConfigHash != stored.ConfigHash {
				diffs = append(diffs, fmt.Sprintf("판정기·단가표·설정 버전이 다르다 (%s/%s/%s → %s/%s/%s)", stored.Evaluator, sh(stored.PriceVersion), sh(stored.ConfigHash), got.Evaluator, sh(got.PriceVersion), sh(got.ConfigHash)))
			}
			if got.Head != stored.Head {
				diffs = append(diffs, fmt.Sprintf("근거 사슬 머리 값이 다르다 (%s → %s)", stored.Short(), got.Short()))
			}
			if got.TotalMicro != stored.TotalMicro {
				diffs = append(diffs, fmt.Sprintf("합계가 다르다 (%d → %d 마이크로원)", stored.TotalMicro, got.TotalMicro))
			}
			if len(diffs) > 0 {
				for _, d := range diffs {
					fmt.Fprintln(w, "불일치: "+d)
				}
				return errors.New("검증 실패")
			}
			fmt.Fprintf(w, "일치: 봉인 %s, 행 %d개, 합계 %s원\n", got.Short(), got.Rows, contract.Comma(cost.Won(got.TotalMicro)))
			return nil
		},
	}
}

func sh(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// canonical event row fields needed for bundle recomputation.
type bundleEvent struct {
	Seq          int64  `json:"seq"`
	TS           string `json:"ts"`
	In           int64  `json:"in"`
	Out          int64  `json:"out"`
	CacheRead    int64  `json:"cache_read"`
	CacheWrite   int64  `json:"cache_write"`
	CacheWrite1h int64  `json:"cache_write_1h"`
	Model        string `json:"model"`
	Cost         int64  `json:"cost_micro_krw"`
	Priced       bool   `json:"priced"`
	Bucket       string `json:"bucket"`
}

// verifyBundle checks chain integrity and recomputes every event cost and the
// bucket totals from the bundle's own price rows.
func verifyBundle(w io.Writer, p string) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var bd Bundle
	if err := json.Unmarshal(b, &bd); err != nil {
		return fmt.Errorf("묶음을 읽지 못했다: %w", err)
	}
	if bd.Spec != seal.SpecVersion {
		return fmt.Errorf("지원하지 않는 규격 %s", bd.Spec)
	}
	if i := seal.CheckChain(bd.Rows); i >= 0 {
		fmt.Fprintf(w, "불일치: %d번째 행에서 근거 사슬이 끊겼다\n", i)
		return errors.New("검증 실패")
	}
	if seal.Head(bd.Rows) != bd.Seal.Head {
		fmt.Fprintln(w, "불일치: 사슬 머리 값이 봉인과 다르다")
		return errors.New("검증 실패")
	}
	t := &cost.Table{Models: bd.Prices, FX: bd.FX}
	buckets := map[string]int64{}
	var total int64
	for _, r := range bd.Rows {
		if r.Kind != "event" {
			continue
		}
		var e bundleEvent
		if err := json.Unmarshal([]byte(r.Data), &e); err != nil {
			return err
		}
		ts, _ := time.Parse("2006-01-02T15:04:05.000Z", e.TS)
		u := event.Usage{In: e.In, Out: e.Out, CacheRead: e.CacheRead, CacheWrite: e.CacheWrite, CacheWrite1h: e.CacheWrite1h, Model: e.Model}
		var c int64
		if u.Total() > 0 {
			c, _ = t.MicroKRW(u, ts)
		}
		if c != e.Cost {
			fmt.Fprintf(w, "불일치: 이벤트 %d의 비용 %d != 재계산 %d\n", e.Seq, e.Cost, c)
			return errors.New("검증 실패")
		}
		buckets[e.Bucket] += c
		total += c
	}
	if total != bd.Seal.TotalMicro {
		fmt.Fprintf(w, "불일치: 합계 %d != 봉인 %d\n", total, bd.Seal.TotalMicro)
		return errors.New("검증 실패")
	}
	for k, v := range bd.Seal.BucketMicro {
		if buckets[k] != v {
			fmt.Fprintf(w, "불일치: 분류 %s 합계 %d != 봉인 %d\n", k, buckets[k], v)
			return errors.New("검증 실패")
		}
	}
	fmt.Fprintf(w, "일치: 봉인 %s, 행 %d개, 합계 %s원\n", bd.Seal.Short(), len(bd.Rows), contract.Comma(cost.Won(total)))
	return nil
}

func debugCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "debug-session <transcript.jsonl>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, _ := config.Load("")
			prices, err := loadPrices()
			if err != nil {
				return err
			}
			st, err := os.Stat(args[0])
			if err != nil {
				return err
			}
			r, err := analyzeFile(sources.File{Path: args[0], Agent: sources.DetectAgent(args[0]), Size: st.Size()}, cfg, config.Hash(cfg), prices)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			for _, ev := range r.Session.Events {
				ec := ""
				if ev.ExitCode != nil {
					ec = fmt.Sprintf(" exit=%d", *ev.ExitCode)
				}
				fmt.Fprintf(w, "%4d %-7s %-7s %-8s %-8s %8d %s%s\n", ev.Seq, ev.Kind, ev.Tool, ev.Category, ev.Bucket+"/"+ev.Symptom, ev.CostMicroKRW/1000000, trunc(ev.Summary, 90), ec)
			}
			for _, v := range r.Verdicts {
				fmt.Fprintf(w, "V %4d %-22s %s conf=%.2f prim=%t sup=%t waste=%d %s\n", v.Seq, v.Rule, v.Level, v.Confidence, v.Primary, v.Suppressed, v.WasteMicro/1000000, receipt.Describe(v))
			}
			b, _ := json.Marshal(r.Totals)
			fmt.Fprintln(w, string(b))
			return analyze.CheckInvariant(r.Session, r.Totals)
		},
	}
}

// ignoreStateDir adds .samcheonpo/ to the project's .gitignore unless an
// entry already covers it. Without a repository there is nothing to do.
func ignoreStateDir(path string) (bool, error) {
	if _, err := os.Stat(".git"); err != nil {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		switch strings.TrimSpace(line) {
		case ".samcheonpo", ".samcheonpo/", "/.samcheonpo", "/.samcheonpo/", ".samcheonpo/**":
			return false, nil
		}
	}
	text := string(b)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += ".samcheonpo/\n"
	return true, os.WriteFile(path, []byte(text), 0o644)
}
