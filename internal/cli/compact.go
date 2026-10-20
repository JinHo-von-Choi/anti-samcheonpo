package cli

import (
	"fmt"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/spf13/cobra"
)

// compactCmd reports the ledger's size and reclaims the free space it holds.
//
// The ledger grows with every session and nothing in it ever shrinks, so a
// monitor that runs for months ends up with a file that is mostly space
// freed by earlier deletions. Compacting rewrites the file without removing
// a row: the sealed analyses, the receipts and the evidence chains all stay.
//
// With --prune-days it also removes the event-level detail of sessions that
// were closed long ago. The seal, the receipt snapshot and the verdicts of
// those sessions stay, so their receipts still verify against their seals.
func compactCmd() *cobra.Command {
	var report bool
	var pruneDays int
	c := &cobra.Command{
		Use:   "compact",
		Short: "원장 용량을 보고하고 빈 공간을 회수한다",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := dbPath()
			before := ledger.FileSize(path)
			db, err := ledger.Open(path)
			if err != nil {
				return err
			}
			defer db.Close()
			usage, err := db.Usage()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "원장 %.0fMB · 세션 %d · 이벤트 %d · 판정 %d · 봉인 %d\n",
				float64(before)/(1024*1024), usage.Sessions, usage.Events, usage.Verdicts, usage.Sealed)
			if report {
				return nil
			}
			if pruneDays > 0 {
				cutoff := time.Now().AddDate(0, 0, -pruneDays)
				pr, err := db.Prune(cutoff)
				if err != nil {
					return fmt.Errorf("원장 보존 정리 실패: %w", err)
				}
				fmt.Fprintf(w, "%d일 지난 종료 세션 %d건의 상세 %d건·관측 %d건을 정리했다. 봉인·영수증·판정은 남았다\n",
					pruneDays, pr.Sessions, pr.Events, pr.Observations)
			}
			if err := db.Compact(); err != nil {
				return fmt.Errorf("원장 공간 회수 실패: %w", err)
			}
			after := ledger.FileSize(path)
			fmt.Fprintf(w, "빈 공간 회수 후 %.0fMB\n", float64(after)/(1024*1024))
			return nil
		},
	}
	c.Flags().BoolVar(&report, "report", false, "보고만 하고 회수하지 않는다")
	c.Flags().IntVar(&pruneDays, "prune-days", 0, fmt.Sprintf("이 날짜보다 오래 전에 종료된 세션의 상세만 정리한다 (기본 보존 기준 %d일, 0이면 정리하지 않음)", ledger.PruneDays))
	return c
}
