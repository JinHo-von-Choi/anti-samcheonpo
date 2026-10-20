package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/bench/taskgen"
	"github.com/spf13/cobra"
)

// benchTasksCmd builds benchmark tasks whose verification is designed in.
//
// The pairs it writes are the same task twice. One copy shows the agent the
// expected values of its visible cases; the other does not. Both are graded
// by an independent grader that holds cases the agent never saw, so a
// solution that hardcodes the visible values fails it.
//
// The split it writes puts both copies of a pair on the same side, and every
// family of tasks on one side too: a family is one independent unit.
func benchTasksCmd() *cobra.Command {
	var out, splitPath string
	var count int
	var seed int64
	c := &cobra.Command{
		Use:   "tasks",
		Short: "검증 가능 과제 쌍을 기계 생성한다 (저격 가능 · 대조군)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bundles, err := taskgen.Generate(taskgen.Options{Count: count, Seed: seed, Out: out})
			if err != nil {
				return err
			}
			split := taskgen.Split(bundles)
			if err := split.Validate(); err != nil {
				return fmt.Errorf("생성된 분할이 규약을 어겼다: %w", err)
			}
			if splitPath == "" {
				splitPath = filepath.Join(out, "split.json")
			}
			b, err := json.MarshalIndent(split, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(splitPath, append(b, '\n'), 0o600); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			for _, bundle := range bundles {
				fmt.Fprintf(w, "%s · 저격 가능 %s · 대조군 %s · 계열 %s\n", bundle.Name, bundle.Sniper, bundle.Blind, bundle.Family)
			}
			fmt.Fprintf(w, "묶음 %d건 · 보정 %d · 보류 %d · 분할 %s\n", len(bundles), len(split.Calibration), len(split.Holdout), splitPath)
			fmt.Fprintln(w, "저격 가능 과제에서 채점기를 속이는 해법은 채점기에서 깨진다. 대조군과 같은 계열이므로 분할에서 한쪽에 함께 둔다.")
			return nil
		},
	}
	c.Flags().StringVar(&out, "out", "bench/generated", "생성 디렉터리")
	c.Flags().StringVar(&splitPath, "split", "", "분할 파일 경로 (기본 생성 디렉터리 안의 split.json)")
	c.Flags().IntVar(&count, "count", 6, "생성할 묶음 수")
	c.Flags().Int64Var(&seed, "seed", 1, "사례를 뽑을 씨앗")
	return c
}
