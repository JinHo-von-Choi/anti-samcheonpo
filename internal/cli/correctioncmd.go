package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/correction"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
)

// correctionsCmd lists the correction rules taken from the user's messages
// and lets the user approve or withdraw one. Only an approved rule is
// enforced.
func correctionsCmd() *cobra.Command {
	var project string
	var all bool
	c := &cobra.Command{Use: "corrections", Short: lang.T("사용자 교정에서 만든 규칙 후보 보기 (승인한 규칙만 실행)"), Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			if project == "" && !all {
				if project, err = os.Getwd(); err != nil {
					return err
				}
			}
			states := []string{correction.StateCandidate, correction.StateActive}
			if all {
				states = nil
			}
			rows, err := db.Corrections(project, states...)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), lang.T("규칙 후보 없음"))
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, lang.T("ID\t상태\t규칙\t지적 횟수\t세션\t위반"))
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\n", r.ID, r.State, correction.Statement(r.Correction, lang.Current()), len(r.Evidence), len(r.Sessions), r.Violations)
			}
			return w.Flush()
		}}
	c.Flags().StringVar(&project, "project", "", lang.T("프로젝트 경로 (기본: 현재 폴더)"))
	c.Flags().BoolVar(&all, "all", false, lang.T("모든 프로젝트, 모든 상태"))
	set := func(use, short, state string) *cobra.Command {
		return &cobra.Command{Use: use + " <id>", Short: short, Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			r, err := db.Correction(args[0])
			if err != nil {
				return errors.New(lang.T("그런 규칙이 없다: ") + args[0])
			}
			if err := db.SetCorrectionState(r.ID, state, time.Now()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", r.ID, correction.Statement(r.Correction, lang.Current()), state)
			if state == correction.StateActive {
				fmt.Fprintf(cmd.OutOrStdout(), lang.T("에이전트에게도 알리려면 %s에 다음 줄을 더한다:\n+ - %s\n"), filepath.Join(r.Project, "CLAUDE.md"), correction.Statement(r.Correction, lang.Current()))
			}
			return nil
		}}
	}
	c.AddCommand(set("approve", lang.T("규칙을 승인해 실행한다"), correction.StateActive), set("withdraw", lang.T("규칙을 거둔다"), correction.StateWithdrawn))
	return c
}
