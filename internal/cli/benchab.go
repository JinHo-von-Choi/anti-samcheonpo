package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	c := &cobra.Command{
		Use:   "report <결과.jsonl>...",
		Short: "비교 실행 결과를 표로 만든다 (다른 도구가 제출한 결과 포함)",
		Args:  cobra.MinimumNArgs(1),
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
			ss := bench.Summarize(rows, baseline)
			if format == "json" {
				b, _ := json.MarshalIndent(ss, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), bench.Markdown(ss))
			return nil
		},
	}
	c.Flags().StringVar(&baseline, "baseline", "off", "기준 실행 방식")
	c.Flags().StringVar(&format, "format", "md", "md | json")
	return c
}
