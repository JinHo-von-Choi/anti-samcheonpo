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
	var since, agent string
	var byAgent, byArm bool
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
			blocks, err := db.BlocksSince(t)
			if err != nil {
				return err
			}
			type row struct{ proposed, emitted, delivered, acked, effect, recur, censored, blocked int }
			by := map[string]*row{}
			reasons := map[string]int{}
			for _, b := range blocks {
				if agent != "all" && b.Agent != agent {
					continue
				}
				key := b.Rule
				if byAgent {
					key = b.Agent + "/" + b.Rule
				}
				r := by[key]
				if r == nil {
					r = &row{}
					by[key] = r
				}
				r.blocked += b.Count
			}
			for _, a := range as {
				if agent != "all" && a.Agent != agent {
					continue
				}
				key := a.Rule
				if byAgent {
					key = a.Agent + "/" + a.Rule
				}
				if byArm {
					arm := a.Arm
					if arm == "" {
						arm = "-"
					}
					key = arm + "/" + key
				}
				if a.Stage == recovery.Censored {
					reasons[a.Agent+"/"+a.Observation]++
				}
				r := by[key]
				if r == nil {
					r = &row{}
					by[key] = r
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
				fmt.Fprintln(w, "기록된 처방도 차단도 없다.")
				return nil
			}
			fmt.Fprintf(w, "%-24s %5s %5s %5s %5s %6s %8s %8s %6s %6s\n", "규칙", "제안", "발신", "전달", "인지", "차단", "재발없음", "재발", "미관측", "진행")
			for _, rule := range rules {
				r := by[rule]
				open := r.proposed - r.effect - r.recur - r.censored
				fmt.Fprintf(w, "%-24s %5d %5d %5d %5d %6d %8d %8d %6d %6d\n", rule, r.proposed, r.emitted, r.delivered, r.acked, r.blocked, r.effect, r.recur, r.censored, open)
			}
			var reasonKeys []string
			for k := range reasons {
				reasonKeys = append(reasonKeys, k)
			}
			sort.Strings(reasonKeys)
			for _, k := range reasonKeys {
				fmt.Fprintf(w, "미관측 사유 %s: %d\n", k, reasons[k])
			}
			if byArm {
				fmt.Fprintln(w, "군은 세션마다 none·fact·prescription 중 하나로 같은 확률(1/3)로 배정된다. none은 처방을 보내지 않고 같은 방식으로 결과만 관측한다. -는 실험 밖 판정이다.")
			}
			fmt.Fprintln(w, "재발 없음은 관찰 창에서 같은 판정이 다시 나오지 않았다는 뜻이며 처방의 인과 효과가 아니다.")
			fmt.Fprintln(w, "차단은 처방이 아니라 실행 전에 막은 횟수다. 막은 도구 호출은 종료 코드 -1로 기록되며, 막았다고 비용이 줄었다는 뜻은 아니다.")
			return nil
		},
	}
	c.Flags().StringVar(&since, "since", "30d", "기간")
	c.Flags().StringVar(&agent, "agent", "all", "에이전트 또는 all")
	c.Flags().BoolVar(&byAgent, "by-agent", false, "에이전트·규칙별 집계")
	c.Flags().BoolVar(&byArm, "by-arm", false, "실험 군별 집계")
	return c
}
