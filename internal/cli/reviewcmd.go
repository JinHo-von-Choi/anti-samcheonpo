package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
)

// reviewLabels maps a review answer to the feedback label it records.
var reviewLabels = map[string]string{
	"y": ledger.LabelTruePositive, "맞음": ledger.LabelTruePositive,
	"n": ledger.LabelFalsePositive, "오탐": ledger.LabelFalsePositive,
	"s": "unsure", "모름": "unsure",
}

// reviewCmd asks the user to judge delivered warnings one by one in the
// terminal. Each answer is stored as feedback and on the intervention, so
// accuracy can count judged warnings instead of guessing from recurrence.
func reviewCmd() *cobra.Command {
	var since string
	var limit int
	c := &cobra.Command{
		Use:   "review",
		Short: "전달된 경고를 하나씩 보여 주고 맞음·오탐·모름 판정을 기록",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 {
				return fmt.Errorf("--limit은 1 이상이어야 합니다")
			}
			from, err := ParseSince(since, time.Now())
			if err != nil {
				return err
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			rows, err := db.UnreviewedInterventions(from, limit)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(rows) == 0 {
				fmt.Fprintln(out, "판정할 경고가 없습니다.")
				return nil
			}
			in := bufio.NewScanner(cmd.InOrStdin())
			counts := map[string]int{}
			for i, r := range rows {
				sid := r.Session
				if len(sid) > 12 {
					sid = sid[:12]
				}
				fmt.Fprintf(out, "\n[%d/%d] %s  %s  %s  세션 %s\n  %s\n", i+1, len(rows), r.EmittedAt, r.Agent, r.Verdict.Rule, sid, receipt.Describe(r.Verdict))
				label := ""
				for label == "" {
					fmt.Fprint(out, "  맞음(y) / 오탐(n) / 모름(s) / 건너뛰기(Enter) / 끝내기(q): ")
					if !in.Scan() {
						return finishReview(out, counts, in.Err())
					}
					answer := strings.TrimSpace(strings.ToLower(in.Text()))
					if answer == "q" {
						return finishReview(out, counts, nil)
					}
					if answer == "" {
						label = "skip"
						break
					}
					label = reviewLabels[answer]
				}
				if label == "skip" {
					continue
				}
				if err := db.AddFeedback(r.Verdict.ID, r.Session, r.Verdict.Rule, label, "review", "", r.Project); err != nil {
					return err
				}
				counts[label]++
			}
			return finishReview(out, counts, nil)
		},
	}
	c.Flags().StringVar(&since, "since", "7d", "이 기간 안에 전달된 경고만 (예: 7d, 30d)")
	c.Flags().IntVar(&limit, "limit", 20, "한 번에 보여 줄 최대 건수")
	return c
}

func finishReview(out io.Writer, counts map[string]int, err error) error {
	fmt.Fprintf(out, "\n기록: 맞음 %d, 오탐 %d, 모름 %d\n", counts[ledger.LabelTruePositive], counts[ledger.LabelFalsePositive], counts["unsure"])
	return err
}
