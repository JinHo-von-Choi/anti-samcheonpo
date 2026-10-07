package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rules"
)

func rulesCmd() *cobra.Command {
	c := &cobra.Command{Use: "rules", Short: "규칙 데이터 확인"}
	c.AddCommand(&cobra.Command{
		Use:   "check [rules.yml]",
		Short: "규칙 파일의 패턴과 사례를 검사한다 (인자가 없으면 현재 적용 규칙)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var s *rules.Set
			if len(args) == 1 {
				b, err := os.ReadFile(args[0])
				if err != nil {
					return err
				}
				if s, err = rules.Parse(b); err != nil {
					return err
				}
			} else {
				var warns []string
				s, warns = rules.Load()
				for _, w := range warns {
					fmt.Fprintln(cmd.ErrOrStderr(), "무시한 사용자 규칙:", w)
				}
			}
			if bad := s.Check(); len(bad) > 0 {
				return fmt.Errorf("규칙 검사 실패:\n%s", strings.Join(bad, "\n"))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "통과: 검증 명령 %d개, 실패 시험 %d개, 강제 계속 %d개 패턴\n",
				len(s.VerifyCommands.Patterns), len(s.FailedTests.Patterns), len(s.ForcedPrompts.Patterns))
			return nil
		},
	})
	return c
}
