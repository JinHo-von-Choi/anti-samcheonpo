package cli

import (
	"encoding/json"
	"fmt"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/install"
	"github.com/spf13/cobra"
)

// pluginsCmd shows and verifies the integrity record of the plugin bundles.
//
// The manifest is the monitor's own record of what it installed. Checking
// it answers one question: are the files on disk still the files that
// were installed? It does not say who published them, and it does not
// claim to.
func pluginsCmd() *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "plugins",
		Short: "설치한 플러그인 묶음의 무결성 기록을 확인한다",
		RunE: func(cmd *cobra.Command, _ []string) error {
			registry, err := install.LoadRegistry()
			if err != nil {
				return err
			}
			problems := install.VerifyRegistry(registry)
			w := cmd.OutOrStdout()
			if format == "json" {
				b, err := json.MarshalIndent(struct {
					Bundles  []install.ManifestSummary `json:"bundles"`
					Problems []install.ManifestProblem `json:"problems"`
				}{Bundles: install.SummarizeManifest(registry), Problems: problems}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(w, string(b))
			} else {
				if len(registry.Bundles) == 0 {
					fmt.Fprintln(w, "기록된 플러그인 묶음이 없다. 설치 기록이 없거나 설치하지 않은 상태다.")
					return nil
				}
				for _, b := range install.SummarizeManifest(registry) {
					state := "일치"
					if b.Problems > 0 {
						state = "불일치 " + fmt.Sprintf("%d건", b.Problems)
					}
					fmt.Fprintf(w, "%s · 파일 %d개 · %s\n  %s\n  요약 %s\n", b.Plugin, b.Files, state, b.Root, b.Digest)
				}
				for _, p := range problems {
					fmt.Fprintf(w, "  %s · %s\n", p.Plugin, install.ProblemText(p))
				}
			}
			if len(problems) > 0 {
				return fmt.Errorf("플러그인 무결성 확인 실패 %d건", len(problems))
			}
			return nil
		},
	}
	c.Flags().StringVar(&format, "format", "text", "text | json")
	return c
}
