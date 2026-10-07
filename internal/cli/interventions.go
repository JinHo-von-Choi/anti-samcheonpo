package cli

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

// interventionsCmd reports how far each rule's recovery prescriptions got:
// proposed, emitted, delivered, acknowledged, effect observed, or censored.
// Reaching a stage is not evidence that the prescription helped.
func interventionsCmd() *cobra.Command {
	var since string
	c := &cobra.Command{
		Use:   "interventions",
		Short: "규칙별 처방이 제안·전달·인지·효과 관측 중 어디까지 갔는지 센다",
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := ParseSince(since, time.Now())
			if err != nil {
				return err
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			as, err := db.RecoveryAttemptsSince(t)
			if err != nil {
				return err
			}
			type row struct{ proposed, emitted, delivered, acked, effect, recur, censored int }
			by := map[string]*row{}
			for _, a := range as {
				r := by[a.Rule]
				if r == nil {
					r = &row{}
					by[a.Rule] = r
				}
				r.proposed++
				if a.EmittedAt != nil {
					r.emitted++
				}
				if a.DeliveredAt != nil {
					r.delivered++
				}
				if a.AcknowledgedAt != nil {
					r.acked++
				}
				switch a.Stage {
				case recovery.EffectObserved:
					if a.Observation == "recurrence_observed" {
						r.recur++
					} else {
						r.effect++
					}
				case recovery.Censored:
					r.censored++
				}
			}
			rules := make([]string, 0, len(by))
			for rule := range by {
				rules = append(rules, rule)
			}
			sort.Strings(rules)
			w := cmd.OutOrStdout()
			if len(rules) == 0 {
				fmt.Fprintln(w, "기록된 처방이 없다.")
				return nil
			}
			fmt.Fprintf(w, "%-24s %5s %5s %5s %5s %8s %8s %6s\n", "규칙", "제안", "발신", "전달", "인지", "재발없음", "재발", "미관측")
			for _, rule := range rules {
				r := by[rule]
				fmt.Fprintf(w, "%-24s %5d %5d %5d %5d %8d %8d %6d\n", rule, r.proposed, r.emitted, r.delivered, r.acked, r.effect, r.recur, r.censored)
			}
			fmt.Fprintln(w, "재발 없음은 관찰 창에서 같은 판정이 다시 나오지 않았다는 뜻이며 처방의 인과 효과가 아니다.")
			return nil
		},
	}
	c.Flags().StringVar(&since, "since", "30d", "기간")
	return c
}
