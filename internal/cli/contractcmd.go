package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

func contractCmd() *cobra.Command {
	c := &cobra.Command{Use: "contract", Short: "작업 계약 확인과 수락 (세션 밖에서)"}
	c.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "현재 프로젝트의 계약과 상태",
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, _ := os.Getwd()
			k, raw, err := contract.Load(wd)
			if err != nil {
				return err
			}
			if k == nil {
				fmt.Fprint(cmd.OutOrStdout(), intent.GoalCard(nil, contract.CurrentState(wd, nil, raw), "", nil).Text())
				fmt.Fprintln(cmd.OutOrStdout(), "계약 없음. 완료 조건 후보:")
				for _, c := range contract.Candidates(wd) {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s (%s, %s)\n", c.Command, c.Kind, c.Source)
				}
				return nil
			}
			st := contract.CurrentState(wd, k, raw)
			fmt.Fprint(cmd.OutOrStdout(), intent.GoalCard(k, st, "", nil).Text())
			return nil
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "accept",
		Short: "현재 계약을 수락한다",
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, _ := os.Getwd()
			k, raw, err := contract.Load(wd)
			if err != nil {
				return err
			}
			if k == nil {
				return fmt.Errorf("%s가 없다", contract.Path(wd))
			}
			if _, err := contract.Accept(wd, k, raw, time.Now()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "수락했다: %s (기계 검증 조건 %d개)\n", k.Goal, len(k.MachineChecks()))
			return nil
		},
	})
	return c
}
