package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

// accuracyRow is one rule's measured standing: blocks it caused, how far its
// prescriptions traveled, the user's false-positive judgments, and the
// recurrence rate among observed outcomes.
type accuracyRow struct {
	rule      string
	blocked   int
	proposed  int
	delivered int
	acked     int
	effect    int
	recur     int
	censored  int
	fp        int
	extend    int
}

// accuracyRuleJSON is the machine-readable form of one accuracy row.
type accuracyRuleJSON struct {
	Rule                  string   `json:"rule"`
	Blocked               int      `json:"blocked"`
	Proposed              int      `json:"proposed"`
	Delivered             int      `json:"delivered"`
	Acked                 int      `json:"acked"`
	NoRecurrence          int      `json:"no_recurrence"`
	Recurrence            int      `json:"recurrence"`
	Censored              int      `json:"censored"`
	FalsePositiveFeedback int      `json:"false_positive_feedback"`
	ExtendFeedback        int      `json:"extend_feedback"`
	PrecisionEstimatePct  *float64 `json:"precision_estimate_pct,omitempty"`
}

// accuracyCmd reports per-rule accuracy measured from this ledger alone: how
// often a rule's verdicts turned into blocks, prescriptions, observed
// outcomes without recurrence, or user-recorded false positives. Every
// number here is reproducible from the ledger; nothing is stated without
// evidence.
func accuracyCmd() *cobra.Command {
	var since, agent, format string
	c := &cobra.Command{
		Use:   "accuracy",
		Short: "규칙별 실측 신뢰도: 차단·처방·재발·위양성 표시를 원장에서 계산한다",
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
			blocks, err := db.BlocksSince(t)
			if err != nil {
				return err
			}
			attempts, err := db.RecoveryAttemptsSince(t)
			if err != nil {
				return err
			}
			feedback, err := db.FeedbackStats()
			if err != nil {
				return err
			}
			by := map[string]*accuracyRow{}
			for _, b := range blocks {
				if agent != "all" && b.Agent != agent {
					continue
				}
				r := by[b.Rule]
				if r == nil {
					r = &accuracyRow{rule: b.Rule}
					by[b.Rule] = r
				}
				r.blocked += b.Count
			}
			for _, a := range attempts {
				if agent != "all" && a.Agent != agent {
					continue
				}
				r := by[a.Rule]
				if r == nil {
					r = &accuracyRow{rule: a.Rule}
					by[a.Rule] = r
				}
				r.proposed++
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
			for _, f := range feedback {
				r := by[f.Rule]
				if r == nil {
					r = &accuracyRow{rule: f.Rule}
					by[f.Rule] = r
				}
				r.fp = f.FalsePositive
				r.extend = f.Extend
			}
			rules := make([]string, 0, len(by))
			for rule := range by {
				rules = append(rules, rule)
			}
			sort.Strings(rules)
			w := cmd.OutOrStdout()
			if len(rules) == 0 {
				fmt.Fprintln(w, "원장에 규칙별 증거(차단·처방·피드백)가 없다.")
				return nil
			}
			tot := &accuracyRow{rule: "합계"}
			rows := make([]*accuracyRow, 0, len(rules))
			jsonRows := make([]accuracyRuleJSON, 0, len(rules))
			for _, rule := range rules {
				r := by[rule]
				rows = append(rows, r)
				jsonRows = append(jsonRows, accRowToJSON(r))
				tot.blocked += r.blocked
				tot.proposed += r.proposed
				tot.delivered += r.delivered
				tot.acked += r.acked
				tot.effect += r.effect
				tot.recur += r.recur
				tot.censored += r.censored
				tot.fp += r.fp
				tot.extend += r.extend
			}
			if format == "json" {
				return json.NewEncoder(w).Encode(map[string]any{
					"generated_at": time.Now().UTC().Format(time.RFC3339),
					"rules":        jsonRows,
				})
			}
			fmt.Fprintf(w, "%-24s %5s %5s %5s %5s %8s %6s %7s %5s %8s\n", "규칙", "차단", "제안", "전달", "인지", "재발없음", "재발", "미관측", "위양성", "정밀도%")
			for _, r := range rows {
				fmt.Fprintf(w, "%-24s %5d %5d %5d %5d %8d %6d %7d %5d %8s\n", r.rule, r.blocked, r.proposed, r.delivered, r.acked, r.effect, r.recur, r.censored, r.fp, accPrecision(r.effect, r.recur))
			}
			fmt.Fprintf(w, "%-24s %5d %5d %5d %5d %8d %6d %7d %5d %8s\n", "합계", tot.blocked, tot.proposed, tot.delivered, tot.acked, tot.effect, tot.recur, tot.censored, tot.fp, accPrecision(tot.effect, tot.recur))
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "정밀도 추정 = 재발없음/(재발없음+재발). 관측 창 안에서 같은 판정이 다시 나오지 않은 처방의 비율이며 인과 효과가 아니다.")
			fmt.Fprintln(w, "위양성은 사용자가 직접 기록한 반박(false_positive 라벨)의 수다. 분류기가 스스로 세는 성공과 겹치지 않는다.")
			fmt.Fprintln(w, "차단은 처방과 별개의 행동이고, 재발은 관측 기회가 새겨진 처방만 계산된다. 자세한 전달 단계는 samcheonpo interventions를 본다.")
			return nil
		},
	}
	c.Flags().StringVar(&since, "since", "30d", "범위: 30d, 12h, 2026-10-01, all")
	c.Flags().StringVar(&agent, "agent", "all", "에이전트 필터 (all | claude | codex | ...)")
	c.Flags().StringVar(&format, "format", "text", "text | json")
	return c
}

// accPrecision is the share of observed prescriptions that did not recur.
// It is a proxy, not a causal claim; unmeasurable means "-" (blank).
func accPrecision(effect, recur int) string {
	if n := effect + recur; n > 0 {
		return fmt.Sprintf("%.1f", 100.0*float64(effect)/float64(n))
	}
	return "-"
}

func accRowToJSON(r *accuracyRow) accuracyRuleJSON {
	out := accuracyRuleJSON{
		Rule: r.rule, Blocked: r.blocked, Proposed: r.proposed, Delivered: r.delivered, Acked: r.acked,
		NoRecurrence: r.effect, Recurrence: r.recur, Censored: r.censored,
		FalsePositiveFeedback: r.fp, ExtendFeedback: r.extend,
	}
	if n := r.effect + r.recur; n > 0 {
		p := 100.0 * float64(r.effect) / float64(n)
		out.PrecisionEstimatePct = &p
	}
	return out
}
