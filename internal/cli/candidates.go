package cli

import (
	"encoding/json"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/spf13/cobra"
	"os"
)

func candidatesCmd() *cobra.Command {
	var labels, split, out string
	c := &cobra.Command{Use: "candidates", Short: "학습 분할 라벨만으로 개선 검토 후보를 생성 (정책 적용 없음)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if labels == "" || split == "" {
			return fmt.Errorf("--labels와 --split 필요")
		}
		s, err := eval.LoadSplit(split)
		if err != nil {
			return err
		}
		ls, err := eval.LoadLabels(labels)
		if err != nil {
			return err
		}
		p, err := eval.BuildCandidates(ls, s)
		if err != nil {
			return err
		}
		b, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return err
		}
		b = append(b, '\n')
		if out != "" {
			return os.WriteFile(out, b, 0600)
		}
		_, err = cmd.OutOrStdout().Write(b)
		return err
	}}
	c.Flags().StringVar(&labels, "labels", "", "사람 라벨 디렉터리")
	c.Flags().StringVar(&split, "split", "", "출처 그룹이 격리된 split/2")
	c.Flags().StringVar(&out, "out", "", "비식별 후보 JSON 파일")
	return c
}
