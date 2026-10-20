package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/spf13/cobra"
)

// detectionTimingCmd reports how early the monitor's first judgment
// arrived.
//
// "Did it catch it" is a yes-or-no question, and a monitor that only
// answers it at the end of the trajectory has already spent the money it
// was supposed to save. This reports where the first judgment fell in the
// observed trajectory, so the lateness is visible as a number.
//
// The numbers are about detection latency, not detection accuracy. A
// monitor that raises nothing has no position to report.
func detectionTimingCmd() *cobra.Command {
	var since, agent, format string
	c := &cobra.Command{
		Use:   "detection-timing",
		Short: "최초 판정 시점을 궤적 안에서의 위치로 보여 준다",
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := ParseSince(since, time.Now())
			if err != nil {
				return err
			}
			db, err := ledger.OpenReadOnly(dbPath())
			if err != nil {
				return err
			}
			defer db.Close()
			timings, err := db.DetectionTimings(t, agent)
			if err != nil {
				return err
			}
			summary := ledger.SummarizeDetectionTiming(timings)
			w := cmd.OutOrStdout()
			if format == "json" {
				b, err := json.MarshalIndent(struct {
					Summary  ledger.DetectionTimingSummary `json:"summary"`
					Sessions []ledger.DetectionTiming      `json:"sessions"`
				}{Summary: summary, Sessions: timings}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(w, string(b))
				return nil
			}
			if summary.Sessions == 0 {
				fmt.Fprintln(w, "선택한 기간·에이전트의 기록이 없습니다. 미수집은 품질 0점이 아닙니다.")
				return nil
			}
			fmt.Fprintf(w, "세션 %d건 · 판정 있음 %d건 · 판정 없음 %d건\n", summary.Sessions, summary.WithVerdict, summary.WithoutVerdict)
			if summary.WithVerdict == 0 {
				fmt.Fprintln(w, "판정이 없어 도출 시점을 잴 수 없다. 포획 정확도가 0이라는 뜻은 아니다.")
				return nil
			}
			fmt.Fprintf(w, "최초 판정 위치 중앙값 %.1f%% · 범위 %.1f%%~%.1f%%\n",
				100*summary.MedianPosition, 100*summary.EarliestPosition, 100*summary.LatestPosition)
			fmt.Fprintf(w, "마지막 1/3에서 도출 %d건 (%.1f%%)\n", summary.LastThirdCount, 100*summary.LastThirdShare)
			if summary.MedianLeadMS > 0 {
				fmt.Fprintf(w, "선행 시간 중앙값 %s\n", (time.Duration(summary.MedianLeadMS) * time.Millisecond).Round(time.Second))
			}
			fmt.Fprintln(w, "선행 시간은 관측 종료까지의 값이다. 채점기의 확정을 대신하지 않는다.")
			return nil
		},
	}
	c.Flags().StringVar(&since, "since", "30d", "기간")
	c.Flags().StringVar(&agent, "agent", "all", "에이전트 또는 all")
	c.Flags().StringVar(&format, "format", "text", "text | json")
	return c
}
