package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/evasion"
	"github.com/spf13/cobra"
)

// evasionCmd measures what the detectors miss.
//
// The false-positive corpus asks whether a rule fires when it should not.
// This one asks the mirror question: when an agent knows the rule and tries
// to slip past it, does the rule still catch it? A rule that misses is a
// blind spot, and the report names every one of them.
func evasionCmd() *cobra.Command {
	var corpus, format, out string
	c := &cobra.Command{
		Use:   "evasion",
		Short: "탐지 규칙이 놓치는 회피를 재현해 측정한다",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			scenarios, err := evasion.Load(corpus)
			if err != nil {
				return err
			}
			if len(scenarios) == 0 {
				return fmt.Errorf("회피 코퍼스가 비어 있다")
			}
			report, err := evasion.Measure(scenarios)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if format == "json" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(w, string(b))
			} else {
				fmt.Fprintln(w, evasion.Summary(report))
			}
			if out != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return err
				}
				if err := os.WriteFile(out, append(b, '\n'), 0o600); err != nil {
					return err
				}
			}
			if report.Missed > 0 {
				return fmt.Errorf("규칙이 회피를 %d건 놓쳤다", report.Missed)
			}
			return nil
		},
	}
	c.Flags().StringVar(&corpus, "corpus", "", "회피 코퍼스 파일 (기본은 내장 코퍼스)")
	c.Flags().StringVar(&format, "format", "text", "text | json")
	c.Flags().StringVar(&out, "out", "", "보고서 JSON 파일 경로")
	return c
}
