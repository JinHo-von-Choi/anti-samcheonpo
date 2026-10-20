package cli

import (
	"encoding/json"
	"fmt"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/surface"
	"github.com/spf13/cobra"
)

// surfaceCmd shows where each part of the harness lives.
//
// The mapping answers a question a reader of another project keeps asking:
// in this vocabulary, which piece is the sensor and which is the guide. The
// answer is data, and the data is checked against the repository, so it
// cannot go stale quietly.
func surfaceCmd() *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "surface",
		Short: "하네스 성분의 위치를 보여 준다",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := surface.Load()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if format == "json" {
				b, err := json.MarshalIndent(s, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(w, string(b))
				return nil
			}
			fmt.Fprintf(w, "하네스 성분 위치 %s\n", s.Version)
			for _, c := range s.Components {
				fmt.Fprintf(w, "  %s (%s, %s)\n", c.Name, c.Role, c.ExternalTerm)
				for _, p := range c.Paths {
					fmt.Fprintf(w, "    %s\n", p)
				}
				if c.Note != "" {
					fmt.Fprintf(w, "    %s\n", c.Note)
				}
			}
			fmt.Fprintln(w, "이 표는 위치를 말할 뿐, 성분의 권한을 바꾸지 않는다. 규칙 팩만 편집 대상이다.")
			return nil
		},
	}
	c.Flags().StringVar(&format, "format", "text", "text | json")
	return c
}
