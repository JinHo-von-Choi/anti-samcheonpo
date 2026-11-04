package cli

import (
	"encoding/json"
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
	var byAgent, byArm, asJSON bool
	var session, verdict string
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
			if session != "" {
				traces, err := db.InterventionTraces(session, verdict)
				if err != nil {
					return err
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(traces)
			}
			if asJSON || verdict != "" {
				return fmt.Errorf("--json/--verdict에는 --session이 필요합니다")
			}
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
				if byArm {
					key = "unattributed/" + key
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
					version := a.Experiment
					if version == "" {
						version = "legacy"
					}
					key = version + "/" + arm + "/" + key
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
				fmt.Fprintln(w, "군은 설정된 목록에서 세션별 동일 확률로 배정된다. wording/2의 none·fact는 사실만 전달한다. legacy는 이전 의미를 보존한다. unattributed 차단은 군별 효과에 합산하지 않는다.")
			}
			fmt.Fprintln(w, "재발 없음은 관찰 창에서 같은 판정이 다시 나오지 않았다는 뜻이며 처방의 인과 효과가 아니다.")
			fmt.Fprintln(w, "차단 집계에는 실행 전 거부와 종료 보류가 포함된다. 정확한 단계는 --session 상세 기록으로 확인한다. 출력 확인은 호스트 수신이나 비용 절감의 증거가 아니다.")
			return nil
		},
	}
	c.Flags().StringVar(&session, "session", "", "개입 추적 세션 ID")
	c.Flags().StringVar(&verdict, "verdict", "", "판정 ID 필터")
	c.Flags().BoolVar(&asJSON, "json", false, "구조화된 개입 추적")
	c.Flags().StringVar(&since, "since", "30d", "기간")
	c.Flags().StringVar(&agent, "agent", "all", "에이전트 또는 all")
	c.Flags().BoolVar(&byAgent, "by-agent", false, "에이전트·규칙별 집계")
	c.Flags().BoolVar(&byArm, "by-arm", false, "실험 군별 집계")
	return c
}
