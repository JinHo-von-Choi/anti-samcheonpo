package cli

import (
	"fmt"
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
		Short: "경고 뒤 에이전트 행동(검증·재실행·재발·시험 수정·되돌림)을 규칙별로 센다",
		RunE: func(cmd *cobra.Command, args []string) error {
			if window < 1 {
				return fmt.Errorf("--window는 1 이상이어야 한다")
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
				i = j
			}
			w := cmd.OutOrStdout()
			if len(by) == 0 {
				fmt.Fprintln(w, "기록된 경고가 없다.")
				return nil
			}
			keys := make([]string, 0, len(by))
			for k := range by {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			pct := func(x, n int) string { return fmt.Sprintf("%.0f%%", 100*float64(x)/float64(n)) }
			fmt.Fprintf(w, "%-36s %6s %7s %6s %7s %6s %8s %7s\n", "규칙", "건수", "창완료", "검증", "재실행", "재발", "시험수정", "되돌림")
			for _, k := range keys {
				a := by[k]
				fmt.Fprintf(w, "%-36s %6d %7s %6s %7s %6s %8s %7s\n", k, a.n, pct(a.complete, a.n), pct(a.verified, a.n), pct(a.repeated, a.n), pct(a.recurred, a.n), pct(a.testEdited, a.n), pct(a.reverted, a.n))
			}
			fmt.Fprintf(w, "각 비율은 경고 뒤 도구 호출 %d번 안에서 그 행동이 한 번 이상 나온 경고의 비율이다. 창완료는 세션이 %d번을 다 채운 비율이다.\n", window, window)
			fmt.Fprintln(w, "경고를 보낸 군과 보내지 않은 군(none)을 --by-arm으로 비교할 때만 차이를 경고의 효과로 읽을 수 있다. 실험 밖(-) 수치는 경고 뒤 행동일 뿐이다.")
			return nil
		},
	}
	c.Flags().StringVar(&since, "since", "30d", "이 기간 안에 시작한 세션 (예: 7d, 30d)")
	c.Flags().IntVar(&window, "window", 20, "경고 뒤에 볼 도구 호출 수")
	c.Flags().BoolVar(&byArm, "by-arm", false, "실험 군별 집계")
	return c
}
