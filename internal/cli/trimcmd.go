package cli

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"os"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/trim"
)

// trimCmd checks a delete-only pass over a document: the result may drop
// sentences and join them, but every content word has to come from the
// original.
func trimCmd() *cobra.Command {
	var apply bool
	c := &cobra.Command{
		Use:   lang.T("trim <원본> <결과>"),
		Short: lang.T("덜어낸 결과에 원본에 없던 낱말이 생겼는지 확인한다"),
		Long:  lang.T("원본 파일과 덜어낸 결과 파일을 비교한다. 문장을 이어 붙이며 바뀐 조사·어미·접속사는 새 낱말로 보지 않는다. 새 낱말이 있으면 목록을 출력하고 종료 코드 1로 끝난다."),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			before, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			after, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			added := trim.NewWords(string(before), string(after))
			if len(added) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), lang.T("새 낱말 없음"))
				if apply {
					info, err := os.Stat(args[0])
					if err != nil {
						return err
					}
					if err := os.WriteFile(args[0], after, info.Mode().Perm()); err != nil {
						return err
					}
					fmt.Fprintln(cmd.OutOrStdout(), lang.T("결과를 원본에 적용했다"))
				}
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), lang.T("원본에 없던 낱말 %d개:\n"), len(added))
			for _, w := range added {
				fmt.Fprintln(cmd.OutOrStdout(), "- "+w)
			}
			cmd.SilenceUsage = true
			if apply {
				return fmt.Errorf("%s", lang.T("덜어내기 결과에 새 낱말이 있어 원본을 그대로 뒀다"))
			}
			return fmt.Errorf("%s", lang.T("덜어내기 결과에 새 낱말이 있다"))
		},
	}
	c.Flags().BoolVar(&apply, "apply", false, lang.T("새 낱말이 없을 때만 결과를 원본에 덮어쓴다"))
	return c
}
