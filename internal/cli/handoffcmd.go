package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
)

// launch renders the command that starts the target agent with a prompt.
var launch = map[string]string{
	"claude":   `claude %s`,
	"codex":    `codex %s`,
	"opencode": `opencode run %s`,
	"agy":      `agy -p %s`,
	"hermes":   `hermes chat -q %s`,
	"openclaw": `openclaw agent --local -m %s`,
	"cursor":   `cursor-agent -p %s`,
	"copilot":  `copilot -p %s`,
}

func shellQuoteArg(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func handoffCmd() *cobra.Command {
	var to, format string
	c := &cobra.Command{
		Use:   "handoff [session-id]",
		Short: "다른 에이전트나 새 세션으로 넘길 첫 지시문을 만든다",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "text" && format != "json" {
				return fmt.Errorf("format은 text 또는 json")
			}
			tmpl, ok := launch[to]
			if !ok {
				return fmt.Errorf("대상 에이전트: claude, codex, opencode, agy, hermes, openclaw, cursor, copilot")
			}
			wd, _ := os.Getwd()
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			id := ""
			if len(args) == 1 {
				id = args[0]
			} else {
				rows, err := db.Sessions(time.Now().AddDate(0, 0, -30), "all", wd)
				if err != nil {
					return err
				}
				if len(rows) == 0 {
					return fmt.Errorf("이 프로젝트에서 분석된 세션이 없다. 먼저 samcheonpo audit --project .")
				}
				sort.Slice(rows, func(i, j int) bool { return rows[i].Started.After(rows[j].Started) })
				id = rows[0].ID
			}
			full, _, sourceAgent, err := db.SourceOf(id)
			if err != nil {
				return err
			}
			bundle, err := buildHandoff(db, sourceAgent, full, wd)
			if err != nil {
				return err
			}
			if format == "json" {
				return bundle.Encode(cmd.OutOrStdout())
			}
			if bundle.Task != nil {
				text := bundle.Text(nil, time.Now())
				fmt.Fprintln(cmd.OutOrStdout(), text)
				fmt.Fprintf(cmd.OutOrStdout(), "\n시작 명령(자동 실행 아님):\n"+tmpl+"\n", shellQuoteArg(text))
				return nil
			}
			vs, _ := db.VerdictsOf(full)
			bt := db.BucketTotals([]string{full})[full]
			var b strings.Builder
			k, _, _ := contract.Load(wd)
			if k != nil {
				fmt.Fprintf(&b, "목표: %s\n완료 조건:", k.Goal)
				for _, d := range k.Done {
					if d.Check != "" {
						fmt.Fprintf(&b, " `%s`", d.Check)
					} else {
						fmt.Fprintf(&b, " (사람 확인) %s", d.Manual)
					}
				}
				b.WriteString("\n")
				if len(k.Scope.Allow) > 0 {
					fmt.Fprintf(&b, "고쳐도 되는 경로: %s\n", strings.Join(k.Scope.Allow, ", "))
				}
				if len(k.Forbid) > 0 {
					fmt.Fprintf(&b, "금지: %s\n", strings.Join(k.Forbid, ", "))
				}
			} else {
				var prompt string
				_ = db.QueryRow(`SELECT COALESCE(first_prompt_summary,'') FROM session WHERE id=?`, full).Scan(&prompt)
				fmt.Fprintf(&b, "이전 요청: %s\n", prompt)
			}
			if bt.Tokens > 0 && bt.UnpricedTokens == 0 {
				fmt.Fprintf(&b, "이전 기록의 API 환산액(계측분)은 %s원, 낭비 판정 구간의 환산액은 %s원이다. 실제 청구액이나 절감액은 아니다.\n", contract.Comma(cost.Won(bt.Micro)), contract.Comma(cost.Won(bt.SymptomTotal())))
			} else if bt.Tokens > 0 {
				fmt.Fprintf(&b, "이전 기록에서 %d토큰을 계측했다. 단가 누락이 있어 전체 환산액은 미확인이다.\n", bt.Tokens)
			} else {
				b.WriteString("이전 사용량·비용은 미확인이다. 계측 부재를 0원으로 취급하지 않는다.\n")
			}
			var tried []string
			seen := map[string]bool{}
			for _, v := range vs {
				if v.Primary && v.Level > 0 {
					d := receipt.Describe(v)
					if !seen[d] {
						seen[d] = true
						tried = append(tried, d)
					}
				}
			}
			if len(tried) > 0 {
				if len(tried) > 6 {
					tried = tried[len(tried)-6:]
				}
				b.WriteString("이미 막힌 지점:\n")
				for _, t := range tried {
					b.WriteString("- " + t + "\n")
				}
			}
			docs, _ := filepath.Glob(filepath.Join(contract.Dir(wd), "handoff", "*.md"))
			sort.Strings(docs)
			if len(docs) > 0 {
				fmt.Fprintf(&b, "자세한 인수인계 문서: %s\n", docs[len(docs)-1])
			}
			attempts, err := db.Recoveries(sourceAgent, full)
			if err != nil {
				return err
			}
			if len(attempts) > 0 {
				b.WriteString("\n이미 제안한 복구와 미확인 결과:\n")
				for _, a := range attempts {
					fmt.Fprintf(&b, "- %s: %s (상태 %s, 관찰 %s)\n  종료 조건: %s\n", a.Prescription.Cause, a.Prescription.Action, a.Stage, a.Observation, a.Prescription.StopCondition)
				}
			}
			b.WriteString("이전 처방을 반복하기 전에 달라진 근거를 확인한다. 권한·서비스 등 외부 조건이 미해결이면 코드 수정으로 우회하지 말고 필요한 사용자 조치를 요청한다. 전달 기록이나 재발 없음만으로 문제가 해결됐다고 가정하지 않는다.")
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, b.String())
			fmt.Fprintln(w, "\n시작 명령:")
			fmt.Fprintf(w, tmpl+"\n", shellQuoteArg(b.String()))
			return nil
		},
	}
	c.Flags().StringVar(&to, "to", "claude", "대상 에이전트")
	c.Flags().StringVar(&format, "format", "text", "출력 형식 (text|json); JSON은 로컬 원문을 포함하며 공유용 비식별 자료가 아님")
	c.AddCommand(handoffInspectCmd())
	c.AddCommand(handoffLinkCmd())
	return c
}
