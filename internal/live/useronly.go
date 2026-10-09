package live

import (
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

// userOnlyCommand reports whether a shell command runs a samcheonpo command
// that changes what the user decided: accepting or editing a contract,
// releasing a verdict, skipping the contract, applying a rollback. These
// reach the daemon through the user's slash commands, which the agent host
// expands outside the agent's tool calls; the same command run by the
// agent's shell tool is refused. Only words in command position count, so a
// quoted mention (grep 'samcheonpo cmd accept') is not a call. A determined
// process of the same OS user can still reach the daemon socket directly;
// this closes the ordinary path.
func userOnlyCommand(cmd string) bool {
	for _, seg := range shellSegments(cmd) {
		w := shellWords(seg)
		for len(w) > 0 && (strings.Contains(w[0], "=") && !strings.HasPrefix(w[0], "=") || w[0] == "env" || w[0] == "exec" || w[0] == "command" || w[0] == "sudo" || w[0] == "nohup" || w[0] == "time") {
			w = w[1:]
		}
		if len(w) == 0 {
			continue
		}
		if shellWrappers[pathnorm.CommandBase(w[0])] {
			// a command handed to another shell is still in command position
			rest := w[1:]
			for len(rest) > 0 && (strings.HasPrefix(rest[0], "-") || strings.EqualFold(rest[0], "/c") || strings.EqualFold(rest[0], "/k")) {
				rest = rest[1:]
			}
			if userOnlyCommand(strings.Join(rest, " ")) {
				return true
			}
			continue
		}
		if pathnorm.CommandBase(w[0]) != "samcheonpo" {
			continue
		}
		w = w[1:]
		for len(w) > 0 && strings.HasPrefix(w[0], "-") {
			flag := w[0]
			w = w[1:]
			if !strings.Contains(flag, "=") && len(w) > 0 && !strings.HasPrefix(w[0], "-") && w[0] != "cmd" && w[0] != "contract" {
				w = w[1:] // the flag's value
			}
		}
		if len(w) < 2 {
			continue
		}
		switch {
		case w[0] == "cmd" && (w[1] == "accept" || w[1] == "keep" || w[1] == "skip" || w[1] == "edit"):
			return true
		case w[0] == "cmd" && w[1] == "rollback" && len(w) > 2 && w[2] == "apply":
			return true
		case w[0] == "contract" && w[1] == "accept":
			return true
		}
	}
	return false
}

// shellWrappers are interpreters that run the command text they are given.
var shellWrappers = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "cmd": true, "powershell": true, "pwsh": true}

// shellSegments splits at ; & | and newlines outside quotes.
func shellSegments(s string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			b.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			b.WriteRune(r)
		case r == ';' || r == '&' || r == '|' || r == '\n' || r == '(' || r == ')' || r == '`':
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	return append(out, b.String())
}

// shellWords splits on blanks outside quotes and removes the quotes.
func shellWords(s string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	in := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				out = append(out, b.String())
				b.Reset()
				in = false
			}
		default:
			b.WriteRune(r)
			in = true
		}
	}
	if in {
		out = append(out, b.String())
	}
	return out
}

const userOnlyReason = "[삼천포] 이 명령(계약 수락·수정·건너뛰기, 판정 해제, 되돌리기 적용)은 사용자가 슬래시 명령으로 실행하는 명령이라 에이전트 셸에서는 실행하지 않았다. 필요하면 사용자에게 해당 슬래시 명령(예: /samcheonpo:accept, /samcheonpo:keep normal, /samcheonpo:rollback apply)을 요청할 수 있다."
