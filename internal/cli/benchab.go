package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/bench"
)

func benchABCmd() *cobra.Command {
	var tasksDir, cfgPath, out, only string
	c := &cobra.Command{
		Use:   "ab",
		Short: "실제 에이전트로 실행 방식별 비교 실행 (하네스 유무 등)",
		RunE: func(cmd *cobra.Command, args []string) error {
			tasks, err := bench.LoadLiveTasks(tasksDir)
			if err != nil {
				return err
			}
			if len(tasks) == 0 {
				return fmt.Errorf("%s에 과제가 없다", tasksDir)
			}
			cfg, err := bench.LoadABConfig(cfgPath)
			if err != nil {
				return err
			}
			schedule, err := bench.Schedule(tasks, cfg, only)
			if err != nil {
				return err
			}
			prices, err := loadPrices()
			if err != nil {
				return err
			}
			work, err := os.MkdirTemp("", "samcheonpo-ab-")
			if err != nil {
				return err
			}
			f, err := os.OpenFile(out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			defer f.Close()
			w := cmd.OutOrStdout()
			for order, scheduled := range schedule {
				t, a, trial := scheduled.Task, scheduled.Arm, scheduled.Trial
				r := bench.RunTrial(t, a, cfg.Arms[a], trial, cfg, prices, claudeDir(), work)
				r.RunID = filepath.Base(work)
				r.Order = order
				b, _ := json.Marshal(r)
				if _, err := f.Write(append(b, '\n')); err != nil {
					return err
				}
				st := "실패"
				if r.Verified {
					st = "완료"
				}
				amount := "미확인"
				if r.CostKnown {
					amount = commaInt(r.TotalKRW) + "원(API 등가)"
				}
				fmt.Fprintf(w, "%-10s %-24s #%d %s %s %6.0f초 %s\n", a, t.Name, trial, st, amount, float64(r.DurationMS)/1000, r.Error)
			}
			fmt.Fprintf(w, "작업 폴더: %s\n결과: %s\n", work, out)
			return nil
		},
	}
	c.Flags().StringVar(&tasksDir, "tasks", "bench/live", "과제 폴더")
	c.Flags().StringVar(&cfgPath, "config", "bench/ab.yml", "실행 방식 설정")
	c.Flags().StringVar(&out, "out", "ab-results.jsonl", "결과 파일 (이어 쓴다)")
	c.Flags().StringVar(&only, "arm", "", "이 실행 방식만 돌린다")
	return c
}

func benchReportCmd() *cobra.Command {
	var baseline, format string
	var margin, goal float64
	var check bool
	c := &cobra.Command{
		Use:          "report <결과.jsonl>...",
		SilenceUsage: true,
		Short:        "비교 실행 결과를 표로 만든다 (다른 도구가 제출한 결과 포함)",
		Args:         cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var rows []bench.TrialResult
			for _, p := range args {
				fh, err := os.Open(p)
				if err != nil {
					return err
				}
				sc := bufio.NewScanner(fh)
				n := 0
				for sc.Scan() {
					n++
					if len(sc.Bytes()) == 0 {
						continue
					}
					var r bench.TrialResult
					if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
						fh.Close()
						return fmt.Errorf("%s:%d: %w", p, n, err)
					}
					if r.Arm == "" {
						fh.Close()
						return fmt.Errorf("%s:%d: arm이 없다", p, n)
					}
					rows = append(rows, r)
				}
				scanErr := sc.Err()
				fh.Close()
				if scanErr != nil {
					return fmt.Errorf("%s: %w", p, scanErr)
				}
			}
			if err := bench.ValidateRows(rows); err != nil {
				return err
			}
			if math.IsNaN(margin) || math.IsNaN(goal) || margin < 0 || margin > 1 || goal < 0 || goal > 1 {
				return fmt.Errorf("판정 기준은 0~1 범위여야 한다")
			}
			ss := bench.SummarizeWithGates(rows, baseline, margin, goal)
			gateErr := func() error {
				if !check {
					return nil
				}
				comparisons := 0
				for _, s := range ss {
					if s.IsBaseline {
						continue
					}
					comparisons++
					if s.NonInferior == nil || s.MeetsCostGoal == nil {
						return fmt.Errorf("%s: 근거 부족으로 효과 판정 보류", s.Arm)
					}
					if !*s.NonInferior || !*s.MeetsCostGoal {
						return fmt.Errorf("%s: 사전 지정 성공률·총비용 기준 미충족", s.Arm)
					}
				}
				if comparisons == 0 {
					return fmt.Errorf("비교군이 없다")
				}
				return nil
			}
			if format == "json" {
				b, _ := json.MarshalIndent(ss, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return gateErr()
			}
			fmt.Fprintf(cmd.OutOrStdout(), "사전 기준: 성공률 허용 감소 %.1f%%p · 감시 포함 완료당 비용 절감 %.1f%%\n", margin*100, goal*100)
			fmt.Fprint(cmd.OutOrStdout(), strings.Replace(bench.Markdown(ss), "비용 20% 절감", "비용 기준 충족", 1))
			return gateErr()
		},
	}
	c.Flags().Float64Var(&margin, "success-margin", 0.02, "사전 지정 성공률 비열등 허용 감소(비율)")
	c.Flags().Float64Var(&goal, "cost-goal", 0.15, "사전 지정 감시 포함 완료당 비용 최소 절감(비율)")
	c.Flags().BoolVar(&check, "check", false, "근거 부족·효과 기준 미충족이면 실패 종료")
	c.Flags().StringVar(&baseline, "baseline", "off", "기준 실행 방식")
	c.Flags().StringVar(&format, "format", "md", "md | json")
	return c
}
