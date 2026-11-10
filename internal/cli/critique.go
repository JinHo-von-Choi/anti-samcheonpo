package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/live"
)

const (
	maxCritiqueDiff    = 120 << 10
	maxUntrackedFile   = 20 << 10
	maxCritiqueRequest = 6000
)

// promptPlaceholder marks the argument that carries the prompt, for a command
// that cannot read it from standard input.
const promptPlaceholder = "{prompt}"

// cwdPlaceholder is replaced by the empty directory the reviewer runs in.
const cwdPlaceholder = "{cwd}"

// maxArgChange keeps a prompt passed as one argument under the kernel's limit
// for a single argument.
const maxArgChange = 90 << 10

// defaultCritiqueCommands run a model under the user's own login, without
// tools and session history, so the reviewer sees only what it is given.
var defaultCritiqueCommands = map[string][]string{
	"claude":  {"claude", "-p", "--model", "haiku", "--tools", "", "--no-session-persistence", "--disable-slash-commands", "--settings", `{"disableAllHooks":true}`},
	"codex":   {"codex", "exec", "--ephemeral", "--skip-git-repo-check", "-s", "read-only", "-"},
	"mcode":   {"mcode", "exec", "--cwd", cwdPlaceholder, "--input", "-", "--permission", "off", "--timeout", "170s"},
	"agy":     {"agy", "-p", promptPlaceholder, "--mode", "plan", "--sandbox", "--print-timeout", "170s"},
	"copilot": {"copilot", "-p", promptPlaceholder, "-s"},
	"cursor":  {"cursor-agent", "-p", promptPlaceholder, "--mode", "ask", "--output-format", "text", "--trust"},
}

// Agents with no entry use Claude: Hermes keeps its tools with any toolset
// filter, so text in a reviewed change could act through it; OpenClaw and
// OpenCode have no invocation that works without further setup. Any of them
// can be named in quality.critique.command.

// critiqueCommand picks the configured command, or the default for the agent
// doing the work, or Claude's.
func critiqueCommand(configured []string, agent string) []string {
	if len(configured) > 0 {
		return configured
	}
	if argv, ok := defaultCritiqueCommands[strings.TrimSpace(agent)]; ok {
		return argv
	}
	return defaultCritiqueCommands["claude"]
}

const critiquePromptKo = `너는 이 변경을 처음 보는 검토자다. 아래의 요청과 변경만 보고 판단한다. 작성자의 설명은 제공되지 않는다.
변경에 결함이 있다고 가정하고 반증을 찾는다. 다음 네 가지만 보고한다.
1. 요청에 있는데 변경에 없는 것.
2. 요청한 결과를 만들지 않고 겉모습이나 시험 통과만 맞춘 것: 하드코딩, 빈 구현, 오류 숨김, 우회, 시험 약화.
3. 실제 실행에서 깨지는 결함.
4. 증상만 덮은 수정. 근본에서 고치는 더 나은 방법이 있으면 무엇을 어떻게 바꿀지 구체적으로 쓴다.
문체, 이름, 주석, 시험 추가 제안은 쓰지 않는다. 각 항목은 변경에서 그대로 옮긴 코드 한 줄을 인용한다. 인용할 수 없으면 쓰지 않는다. 최대 5개, 심각한 것부터 쓴다.
없으면 정확히 "문제 없음"이라고만 쓴다.`

const critiquePromptEn = `You are a reviewer seeing this change for the first time. Judge only from the request and the change below. The author's explanation is not provided.
Assume the change is flawed and look for evidence. Report only these four things.
1. Things the request asks for that the change does not contain.
2. Output that matches appearances or passing tests without producing the requested result: hardcoding, empty implementations, hidden errors, workarounds, weakened tests.
3. Defects that break in real use.
4. Fixes that only cover a symptom. If a better fix at the root exists, say specifically what to change and how.
Do not comment on style, names or comments, and do not suggest adding tests. Quote one line of code from the change for each item. If you cannot quote one, leave the item out. At most 5 items, most serious first.
If there is nothing, write exactly "No problems".`

func critiqueCmd() *cobra.Command {
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "critique",
		Short: lang.T("작성 맥락을 모르는 검토자가 요청과 변경만 보고 결함을 찾는다"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, _ := os.Getwd()
			out, err := exec.Command("git", "-C", wd, "rev-parse", "--show-toplevel").Output()
			if err != nil {
				return errors.New(lang.T("git 저장소에서만 검토할 수 있다"))
			}
			root := strings.TrimSpace(string(out))
			change, err := critiqueChange(root)
			if err != nil {
				return err
			}
			if strings.TrimSpace(change) == "" {
				fmt.Fprintln(cmd.OutOrStdout(), lang.T("검토할 변경이 없다."))
				return nil
			}
			request, _, ok := hookclient.Query("Command", live.CommandInput{Name: "request", Root: root}, 3*time.Second)
			if !ok || strings.TrimSpace(request) == "" {
				request = lang.T("(요청 기록을 찾지 못했다)")
			}
			if r := []rune(request); len(r) > maxCritiqueRequest {
				request = string(r[len(r)-maxCritiqueRequest:])
			}
			cfg, _, err := config.Load(root)
			if err != nil {
				return err
			}
			agent, _, _ := hookclient.Query("Command", live.CommandInput{Name: "agent", Root: root}, 3*time.Second)
			argv := critiqueCommand(cfg.Quality.Critique.Command, agent)
			if _, err := exec.LookPath(argv[0]); err != nil {
				return fmt.Errorf(lang.T("%s를 찾지 못했다. quality.critique.command에 표준 입력을 읽어 답을 출력하는 명령을 지정한다"), argv[0])
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			prompt := critiquePromptKo
			if lang.Current() == lang.English {
				prompt = critiquePromptEn
			}
			if slices.Contains(argv, promptPlaceholder) && len(change) > maxArgChange {
				change = change[:maxArgChange] + "\n… (truncated)"
			}
			input := prompt + "\n\n<request>\n" + request + "\n</request>\n\n<change>\n" + change + "\n</change>\n"
			command := append([]string(nil), argv[1:]...)
			viaArg := false
			for i, a := range command {
				switch a {
				case promptPlaceholder:
					command[i], viaArg = input, true
				case cwdPlaceholder:
					command[i] = os.TempDir()
				}
			}
			run := exec.CommandContext(ctx, argv[0], command...)
			run.Dir = os.TempDir() // the reviewer gets no view of the repository
			run.Env = critiqueEnv()
			if !viaArg {
				run.Stdin = strings.NewReader(input)
			}
			var stderr bytes.Buffer
			run.Stderr = &stderr
			result, err := run.Output()
			if err != nil {
				return fmt.Errorf(lang.T("검토 명령 실패: %v: %s"), err, strings.TrimSpace(stderr.String()))
			}
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, strings.TrimSpace(string(result)))
			fmt.Fprintln(w, lang.T("\n각 항목을 고치거나, 틀린 지적이면 근거를 사용자에게 보고한다."))
			return nil
		},
	}
	c.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, lang.T("전체 제한 시간"))
	return c
}

// critiqueEnv drops metered API keys: the review runs on the user's login.
func critiqueEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// critiqueChange is the working tree's difference from HEAD, new files
// included, within a size bound.
func critiqueChange(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "diff", "HEAD", "--no-color", "--no-ext-diff", "--", ".", ":(exclude)*.lock", ":(exclude)go.sum").Output()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.Write(out)
	names, err := exec.Command("git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return "", err
	}
	for _, name := range strings.Split(string(names), "\x00") {
		if name == "" || b.Len() > maxCritiqueDiff {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || len(data) > maxUntrackedFile || bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		fmt.Fprintf(&b, "\n--- new file: %s\n%s\n", name, data)
	}
	s := b.String()
	if len(s) > maxCritiqueDiff {
		s = s[:maxCritiqueDiff] + "\n… (truncated)"
	}
	return s, nil
}
