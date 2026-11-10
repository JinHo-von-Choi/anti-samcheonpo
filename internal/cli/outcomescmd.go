package cli

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"io"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/outcome"
)

// outcomesCmd reports what agents did after each rule's verdicts, measured
// the same way for every experiment arm.
func outcomesCmd() *cobra.Command {
	var since string
	var window int
	var byArm bool
	c := &cobra.Command{
		Use:   "outcomes",
		Short: lang.T("경고 뒤 에이전트 행동(검증·재실행·재발·시험 수정·되돌림)을 규칙별로 센다"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if window < 1 {
				return fmt.Errorf("%s", lang.T("--window는 1 이상이어야 한다"))
			}
			t, err := ParseSince(since, time.Now())
			if err != nil {
				return err
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			rows, err := db.InterventionsSince(t)
			if err != nil {
				return err
			}
			type agg struct{ n, complete, verified, repeated, recurred, testEdited, reverted int }
			by := map[string]*agg{}
			// per experiment arm, each session's share of warnings followed by
			// each behaviour: the session is the unit the arm was assigned to
			bySession := map[string][][4]float64{}
			// rows are ordered by session, so each session is loaded once
			for i := 0; i < len(rows); {
				j := i
				for j < len(rows) && rows[j].Session == rows[i].Session {
					j++
				}
				evs, err := db.EventsOf(rows[i].Session)
				if err != nil {
					return err
				}
				verdicts, err := db.VerdictsOf(rows[i].Session)
				if err != nil {
					return err
				}
				var share [4]float64
				arm, inArm := "", 0
				for _, r := range rows[i:j] {
					key := r.Rule
					if byArm {
						arm := r.Arm
						if arm == "" {
							arm = "-"
						}
						key = arm + "/" + key
					}
					a := by[key]
					if a == nil {
						a = &agg{}
						by[key] = a
					}
					o := outcome.Measure(r.Seq, r.Rule, r.Evidence, evs, verdicts, window)
					if r.Arm != "" {
						arm = r.Arm
						inArm++
						for k, on := range []bool{o.Verified, o.Repeated, o.Recurred, o.Reverted} {
							if on {
								share[k]++
							}
						}
					}
					a.n++
					for _, x := range []struct {
						on  bool
						cnt *int
					}{{o.Complete, &a.complete}, {o.Verified, &a.verified}, {o.Repeated, &a.repeated}, {o.Recurred, &a.recurred}, {o.TestEdited, &a.testEdited}, {o.Reverted, &a.reverted}} {
						if x.on {
							*x.cnt++
						}
					}
				}
				if inArm > 0 {
					for k := range share {
						share[k] /= float64(inArm)
					}
					bySession[arm] = append(bySession[arm], share)
				}
				i = j
			}
			w := cmd.OutOrStdout()
			if len(by) == 0 {
				fmt.Fprintln(w, lang.T("기록된 경고가 없다."))
				return nil
			}
			keys := make([]string, 0, len(by))
			for k := range by {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			pct := func(x, n int) string { return fmt.Sprintf("%.0f%%", 100*float64(x)/float64(n)) }
			fmt.Fprintf(w, "%-36s %6s %7s %6s %7s %6s %8s %7s\n", lang.T("규칙"), lang.T("건수"), lang.T("창완료"), lang.T("검증"), lang.T("재실행"), lang.T("재발"), lang.T("시험수정"), lang.T("되돌림"))
			for _, k := range keys {
				a := by[k]
				fmt.Fprintf(w, "%-36s %6d %7s %6s %7s %6s %8s %7s\n", k, a.n, pct(a.complete, a.n), pct(a.verified, a.n), pct(a.repeated, a.n), pct(a.recurred, a.n), pct(a.testEdited, a.n), pct(a.reverted, a.n))
			}
			fmt.Fprintf(w, lang.T("각 비율은 경고 뒤 도구 호출 %d번 안에서 그 행동이 한 번 이상 나온 경고의 비율이다. 창완료는 세션이 %d번을 다 채운 비율이다.\n"), window, window)
			if err := printObservations(w, db, t); err != nil {
				return err
			}
			if byArm {
				compareArms(w, bySession)
			}
			fmt.Fprintln(w, lang.T("경고를 보낸 군과 보내지 않은 군(none)을 --by-arm으로 비교할 때만 차이를 경고의 효과로 읽을 수 있다. 실험 밖(-) 수치는 경고 뒤 행동일 뿐이다."))
			return nil
		},
	}
	c.Flags().StringVar(&since, "since", "30d", lang.T("이 기간 안에 시작한 세션 (예: 7d, 30d)"))
	c.Flags().IntVar(&window, "window", 20, lang.T("경고 뒤에 볼 도구 호출 수"))
	c.Flags().BoolVar(&byArm, "by-arm", false, lang.T("실험 군별 집계"))
	return c
}

// printObservations says how each advice's follow-up closed: the same
// warning again, the wished-for behavior, or why nothing could be observed.
func printObservations(w io.Writer, db *ledger.DB, since time.Time) error {
	attempts, err := db.RecoveryAttemptsSince(since)
	if err != nil {
		return err
	}
	recurred, desired, open := 0, 0, 0
	unknown := map[string]int{}
	for _, a := range attempts {
		switch a.Observation {
		case "recurrence_observed":
			recurred++
		case "alternative_observed":
			desired++
		case "":
			open++
		case "no_recurrence_observed":
			unknown[lang.T("재발 없음(바라는 행동은 정의되지 않음)")]++
		default:
			unknown[a.Observation]++
		}
	}
	if len(attempts) == 0 {
		return nil
	}
	fmt.Fprintf(w, lang.T("\n개입 뒤 관측 (처방 %d건): 재발 %d, 바라는 행동 %d, 관측 진행 중 %d\n"), len(attempts), recurred, desired, open)
	reasons := make([]string, 0, len(unknown))
	for reason := range unknown {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Fprintf(w, lang.T("  관측 불가 %-40s %d\n"), reason, unknown[reason])
	}
	fmt.Fprintln(w, lang.T("이 값은 개입 뒤에 무엇이 관측됐는지만 나타낸다. 개입이 원인이라는 뜻은 아니다."))
	return nil
}

// compareArms prints, per behaviour, each arm's mean share over sessions and
// its difference from the control arm with a 95% interval that stays valid
// however often it is read.
func compareArms(w io.Writer, bySession map[string][][4]float64) {
	control := bySession["none"]
	if len(control) == 0 {
		fmt.Fprintln(w, lang.T("\n대조군(none) 세션이 아직 없어 군 비교를 하지 않는다."))
		return
	}
	var arms []string
	for arm := range bySession {
		if arm != "none" {
			arms = append(arms, arm)
		}
	}
	sort.Strings(arms)
	column := func(rows [][4]float64, k int) []float64 {
		out := make([]float64, len(rows))
		for i, r := range rows {
			out[i] = r[k]
		}
		return out
	}
	fmt.Fprintf(w, lang.T("\n군 비교 (세션 단위, 대조군 none %d세션)\n"), len(control))
	for _, arm := range arms {
		fmt.Fprintf(w, lang.T("%s %d세션\n"), arm, len(bySession[arm]))
		for k, name := range []string{lang.T("검증"), lang.T("재실행"), lang.T("재발"), lang.T("되돌림")} {
			mc, hc := outcome.ConfSeq(column(control, k), 0.025)
			ma, ha := outcome.ConfSeq(column(bySession[arm], k), 0.025)
			d, h := ma-mc, ha+hc
			verdict := lang.T("차이 없음 또는 판단 보류")
			if d-h > 0 {
				verdict = lang.T("높다")
			} else if d+h < 0 {
				verdict = lang.T("낮다")
			}
			fmt.Fprintf(w, lang.T("  %-6s none %5.1f%%  %s %5.1f%%  차이 %+5.1f%%p (95%% 구간 %+5.1f ~ %+5.1f) %s\n"), name, 100*mc, arm, 100*ma, 100*d, 100*(d-h), 100*(d+h), verdict)
		}
	}
	fmt.Fprintln(w, lang.T("구간은 표본이 쌓이는 동안 언제 봐도 유효한 신뢰열이다. 구간이 0을 포함하면 차이를 주장하지 않는다."))
}
