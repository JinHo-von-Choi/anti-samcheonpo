package classify

import (
	"regexp"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// Publish kinds: commands whose text becomes part of a shared record.
const (
	PublishNone    = ""
	PublishCommit  = "commit"
	PublishTag     = "tag"
	PublishPR      = "pr"
	PublishRelease = "release"
	PublishPush    = "push"
)

// publishFlags are the options whose value is text other people read, per
// command. Values are given without the leading dashes.
var publishFlags = map[string]map[string]bool{
	PublishCommit:  {"m": true, "message": true},
	PublishTag:     {"m": true, "message": true},
	PublishPR:      {"t": true, "title": true, "b": true, "body": true, "d": true, "description": true},
	PublishRelease: {"t": true, "title": true, "n": true, "notes": true},
}

var (
	publishCommitRe  = regexp.MustCompile(`^git(?:\s+-[cC]\s+\S+)*\s+commit\b`)
	publishTagRe     = regexp.MustCompile(`^git(?:\s+-[cC]\s+\S+)*\s+tag\b.*\s-(?:a|s|m|-message|-annotate)\b`)
	publishPushRe    = regexp.MustCompile(`^git(?:\s+-[cC]\s+\S+)*\s+push\b`)
	publishPRRe      = regexp.MustCompile(`^(?:gh\s+pr\s+(?:create|edit)|glab\s+mr\s+(?:create|update))\b`)
	publishReleaseRe = regexp.MustCompile(`^(?:gh\s+release\s+(?:create|edit)|glab\s+release\s+create)\b`)
)

// Published reports what a shell command publishes and the text it carries:
// commit, tag, pull request and release messages given inline or through a
// here-document. A push carries no text of its own; it publishes what was
// committed before. Text in a file named by -F or --body-file is not read.
func Published(cmd string) (kind, text string) {
	outer, docs := splitHeredocs(cmd)
	var parts []string
	for _, st := range fp.SplitStages(outer) {
		words := shellWords(strings.TrimSpace(st))
		k := publishKind(words)
		if k == PublishNone {
			continue
		}
		if kind == PublishNone || kind == PublishPush {
			kind = k
		}
		parts = append(parts, flagValues(words, publishFlags[k])...)
	}
	if kind == PublishNone {
		return PublishNone, ""
	}
	if kind != PublishPush {
		for _, d := range docs {
			if d.target == "" && (d.reader == "git" || d.reader == "gh" || d.reader == "glab" || d.reader == "cat") {
				// a here-document read into the message, as in
				// -m "$(cat <<'EOF' ... EOF)" or -F - <<EOF; one written to a
				// file or fed to another program is not the message
				parts = append(parts, d.body)
			}
		}
	}
	return kind, strings.TrimSpace(strings.Join(parts, "\n"))
}

// Pushes reports a command with a git push stage, alone or after a commit.
func Pushes(cmd string) bool {
	outer, _ := splitHeredocs(cmd)
	for _, st := range fp.SplitStages(outer) {
		if publishKind(shellWords(strings.TrimSpace(st))) == PublishPush {
			return true
		}
	}
	return false
}

// IsAmend reports a commit command that rewrites the previous commit.
func IsAmend(cmd string) bool {
	for _, st := range fp.SplitStages(cmd) {
		w := shellWords(strings.TrimSpace(st))
		if publishKind(w) == PublishCommit {
			for _, x := range w {
				if x == "--amend" {
					return true
				}
			}
		}
	}
	return false
}

func publishKind(words []string) string {
	line := strings.Join(words, " ")
	switch {
	case publishCommitRe.MatchString(line):
		return PublishCommit
	case publishTagRe.MatchString(line):
		return PublishTag
	case publishPushRe.MatchString(line):
		return PublishPush
	case publishPRRe.MatchString(line):
		return PublishPR
	case publishReleaseRe.MatchString(line):
		return PublishRelease
	}
	return PublishNone
}

// flagValues returns the values of the named options: -m X, -mX, --message X
// and --message=X.
func flagValues(words []string, names map[string]bool) []string {
	var out []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		if !strings.HasPrefix(w, "-") || w == "-" || w == "--" {
			continue
		}
		name, value, inline := strings.TrimLeft(w, "-"), "", false
		if j := strings.IndexByte(name, '='); j >= 0 && strings.HasPrefix(w, "--") {
			name, value, inline = name[:j], name[j+1:], true
		} else if !strings.HasPrefix(w, "--") && len(name) > 1 && names[name[:1]] {
			// -mmessage
			name, value, inline = name[:1], name[1:], true
		}
		if !names[name] {
			continue
		}
		if !inline {
			if i+1 >= len(words) {
				continue
			}
			i++
			value = words[i]
		}
		if v := strings.TrimSpace(value); v != "" && !strings.HasPrefix(v, "$(") {
			out = append(out, v)
		}
	}
	return out
}
