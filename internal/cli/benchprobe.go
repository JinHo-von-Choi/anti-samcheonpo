package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/bench"
	"github.com/spf13/cobra"
)

// benchProbeCmd judges a candidate rule by what it did to a live run.
//
// The two arms must differ in the monitor configuration and in nothing else.
// When they differ in more, the comparison cannot be read as the rule's
// effect, and the command says so instead of claiming one.
func benchProbeCmd() *cobra.Command {
	var base, arm, candidate, out string
	c := &cobra.Command{
		Use:          "probe <결과.jsonl>...",
		SilenceUsage: true,
		Short:        "후보 규칙의 생중 효과를 판정한다 (helped·hurt·no observed effect·confounded)",
		Args:         cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var rows []bench.TrialResult
			for _, p := range args {
				fh, err := os.Open(p)
				if err != nil {
					return err
				}
				sc := bufio.NewScanner(fh)
				sc.Buffer(make([]byte, 1<<20), 1<<20)
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
					rows = append(rows, r)
				}
				scanErr := sc.Err()
				fh.Close()
				if scanErr != nil {
					return fmt.Errorf("%s: %w", p, scanErr)
				}
			}
			verdict := bench.Probe(rows, base, arm, candidate)
			if err := verdict.Validate(); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, bench.ProbeSummary(verdict))
			if out != "" {
				b, err := json.MarshalIndent(verdict, "", "  ")
				if err != nil {
					return err
				}
				if err := os.WriteFile(out, append(b, '\n'), 0o600); err != nil {
					return err
				}
			}
			if verdict.Verdict == "hurt" || verdict.Veto {
				return fmt.Errorf("생중 probe가 후보를 거부했다")
			}
			return nil
		},
	}
	c.Flags().StringVar(&base, "base", "off", "기준 팔")
	c.Flags().StringVar(&arm, "arm", "on", "후보 규칙이 있는 팔")
	c.Flags().StringVar(&candidate, "candidate", "", "후보 규칙 ID")
	c.Flags().StringVar(&out, "out", "", "판정 JSON 파일 경로")
	return c
}
