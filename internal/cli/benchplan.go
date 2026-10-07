package cli

import (
	"encoding/json"
	"fmt"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/bench"
	"github.com/spf13/cobra"
)

func benchPlanCmd() *cobra.Command {
	var sd, gap, alpha, power float64
	var comparisons int
	c := &cobra.Command{Use: "plan-sample", Short: "별도 파일럿 가정으로 필요한 독립 과제 수 추정 (모델 호출 없음)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		n, err := bench.RequiredTaskPairs(sd, gap, alpha, power, comparisons)
		if err != nil {
			return err
		}
		result := struct {
			Tasks       int     `json:"independent_task_pairs"`
			SD          float64 `json:"pilot_sd"`
			Gap         float64 `json:"detectable_gap"`
			Alpha       float64 `json:"alpha"`
			Power       float64 `json:"power"`
			Comparisons int     `json:"comparisons"`
			Limitation  string  `json:"limitation"`
		}{n, sd, gap, alpha, power, comparisons, "정규 근사 사전 설계. 과제 반복 횟수가 아니며 비용 비율 검정력 보장이나 실제 효과 증거가 아님. 별도 파일럿·대표 과제·비용 상한 필요."}
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
			return fmt.Errorf("계획 출력: %w", err)
		}
		return nil
	}}
	c.Flags().Float64Var(&sd, "pilot-sd", 0, "별도 파일럿의 과제 단위 대응 차이 표준편차")
	c.Flags().Float64Var(&gap, "gap", .05, "검출할 차이 또는 비열등 한계까지의 여유")
	c.Flags().Float64Var(&alpha, "alpha", .05, "가족 단위 유의 수준")
	c.Flags().Float64Var(&power, "power", .8, "목표 검정력")
	c.Flags().IntVar(&comparisons, "comparisons", 1, "사전 선언한 비교 개수")
	return c
}
