package cli

import (
	"encoding/json"
	"os"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/spf13/cobra"
)

func corpusCmd() *cobra.Command {
	var manifest, labels, set, out string
	c := &cobra.Command{Use: "corpus", Short: "격리된 코퍼스 재생으로 사실 추출·규칙 적용률·탐지 성능 평가", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := eval.EvaluateCorpus(manifest, labels, set)
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(report, "", "  ")
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
	c.Flags().StringVar(&manifest, "manifest", "", "출처 해시·과제 그룹·train/eval 분할 manifest")
	c.Flags().StringVar(&labels, "labels", "", "독립 라벨 파일(없으면 탐지·적용률만 보고)")
	c.Flags().StringVar(&set, "set", "eval", "train 또는 eval")
	c.Flags().StringVar(&out, "out", "", "JSON 보고서 파일")
	return c
}
