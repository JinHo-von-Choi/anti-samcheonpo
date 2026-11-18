package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/live"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/mutants"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
	"github.com/spf13/cobra"
)

func mutantsCmd() *cobra.Command {
	var session, base string
	var timeout time.Duration
	var asJSON bool
	c := &cobra.Command{
		Use:   "mutants",
		Short: lang.T("바뀐 줄만 변이해 시험이 변경을 잡는지 확인한다"),
		Long: lang.T("세션이 시작할 때(--session) 또는 지정한 커밋(--base, 기본 HEAD) 이후 바뀐 기능 코드의 줄을 언어별 변이 시험 도구에 넘긴다. " +
			"Rust는 cargo-mutants, Go는 go-mutesting, JS·TS는 StrykerJS, PHP는 Infection, Java는 PIT, Python은 mutmut을 쓴다. 삼천포는 도구를 설치하지 않는다. " +
			"도구가 없으면 설치 방법만 알리고 끝낸다. --session을 주면 결과를 그 세션의 영수증에 붙인다. 결과는 증거로 보여 줄 뿐 차단 근거로 쓰지 않는다."),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			out, err := exec.Command("git", "-C", wd, "rev-parse", "--show-toplevel").Output()
			if err != nil {
				return fmt.Errorf("%s", lang.T("git 저장소 안에서 실행해야 한다"))
			}
			root := strings.TrimSpace(string(out))
			from := base
			if from == "" && session != "" {
				if t, ok := live.SessionStartTree(session); ok {
					from = t
				} else {
					fmt.Fprintln(cmd.ErrOrStderr(), lang.T("이 세션의 시작 상태가 기록되지 않아 HEAD 이후 변경을 쓴다"))
				}
			}
			if from == "" {
				from = "HEAD"
			}
			ch, err := mutants.Diff(root, from)
			if err != nil {
				return err
			}
			if len(ch.Lines) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), lang.T("변이할 기능 코드 변경이 없다"))
				return nil
			}
			rep := mutants.Run(ch, timeout)
			lines := receipt.MutantSentences(live.MutantLines(rep))
			if session != "" {
				if err := mutants.Save(session, rep); err != nil {
					return err
				}
				if err := appendToReceipt(session, lines); err != nil {
					return err
				}
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			for _, l := range lines {
				fmt.Fprintln(cmd.OutOrStdout(), l)
			}
			return nil
		},
	}
	c.Flags().StringVar(&session, "session", "", lang.T("이 세션이 시작한 뒤의 변경을 쓰고 결과를 영수증에 붙인다"))
	c.Flags().StringVar(&base, "base", "", lang.T("이 커밋 이후의 변경을 쓴다 (기본 HEAD)"))
	c.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, lang.T("언어별 도구 하나의 시간 제한"))
	c.Flags().BoolVar(&asJSON, "json", false, lang.T("JSON으로 출력"))
	return c
}

// appendToReceipt adds the mutation lines to a receipt already written for
// the session. A session not finalized yet takes them in when it is.
func appendToReceipt(session string, lines []string) error {
	p := filepath.Join(config.Home(), "receipts", session+".md")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	head := lang.T("### 변이 시험\n\n")
	text := string(b)
	// a later run replaces the earlier one, whichever language wrote it
	for _, h := range []string{"### 변이 시험\n\n", "### Mutation testing\n\n"} {
		if i := strings.Index(text, h); i >= 0 {
			rest := text[i+len(h):]
			end := strings.Index(rest, "\n### ")
			if end < 0 {
				text = text[:i]
			} else {
				text = text[:i] + rest[end+1:]
			}
		}
	}
	var add strings.Builder
	add.WriteString("\n" + head)
	for _, l := range lines {
		add.WriteString("- " + l + "\n")
	}
	return os.WriteFile(p, []byte(strings.TrimRight(text, "\n")+"\n"+add.String()), 0o600)
}
