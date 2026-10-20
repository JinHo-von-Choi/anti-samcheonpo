package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/spf13/cobra"
)

// lineageCmd shows one task's chain of sessions and judgments.
//
// A judgment that one session made does not stay in that session. The same
// goal moves from one agent to another, and this is what says which
// judgment belongs to which session's work.
func lineageCmd() *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "lineage <taskID>",
		Short: "목표의 세션 간 계보와 최초 판정 시점을 보여 준다",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := ledger.OpenReadOnly(dbPath())
			if err != nil {
				return err
			}
			defer db.Close()
			lineage, err := db.Lineage(args[0])
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if format == "json" {
				b, err := json.MarshalIndent(lineage, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(w, string(b))
				return nil
			}
			if len(lineage.Revisions) == 0 && len(lineage.Sessions) == 0 {
				return fmt.Errorf("목표 %s에 기록된 세션이 없다", args[0])
			}
			fmt.Fprintf(w, "목표 %s\n", lineage.TaskID)
			for _, rev := range lineage.Revisions {
				fmt.Fprintf(w, "  수정 %d · %s", rev.Revision, rev.Goal)
				if !rev.At.IsZero() {
					fmt.Fprintf(w, " · %s", rev.At.Format(time.RFC3339))
				}
				fmt.Fprintln(w)
			}
			for _, s := range lineage.Sessions {
				from := ""
				if s.ParentSession != "" {
					from = fmt.Sprintf(" ← %s/%s", s.ParentAgent, s.ParentSession)
				}
				fmt.Fprintf(w, "  %s/%s%s · 판정 %d건", s.Agent, s.Session, from, s.Verdicts)
				if !s.FirstVerdict.IsZero() {
					fmt.Fprintf(w, " · 최초 판정 %s", s.FirstVerdict.Format(time.RFC3339))
				}
				if s.Position > 0 {
					fmt.Fprintf(w, " · 위치 %.1f%%", 100*s.Position)
				}
				if s.InLastThird {
					fmt.Fprintln(w, " · 마지막 1/3")
				}
				if s.LeadMS > 0 {
					fmt.Fprintf(w, " · 선행 %s", (time.Duration(s.LeadMS) * time.Millisecond).Round(time.Second))
				}
				fmt.Fprintln(w)
				for _, v := range s.Judgments {
					mark := ""
					if v.Blocked {
						mark = " 차단"
					}
					fmt.Fprintf(w, "    seq %d · %s (%s) L%d%s\n", v.Seq, v.Rule, v.Detector, v.Level, mark)
				}
			}
			fmt.Fprintln(w, "선행 시간은 관측 종료까지의 값이다. 채점기의 확정을 대신하지 않는다.")
			return nil
		},
	}
	c.Flags().StringVar(&format, "format", "text", "text | json")
	return c
}
