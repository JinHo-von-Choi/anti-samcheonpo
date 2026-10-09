// Package fp computes the deterministic fingerprints every detector relies on:
// command, error, result and workspace fingerprints.
package fp

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

// Hash returns the first 16 bytes of SHA-256 of the joined parts, hex encoded.
func Hash(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

var (
	envPrefixRe = lazyre.New(`^(?:[A-Za-z_][A-Za-z0-9_]*=(?:'[^']*'|"[^"]*"|\S*)\s+)+`)
	cdPrefixRe  = lazyre.New(`^cd\s+('[^']*'|"[^"]*"|\S+)\s*(?:&&|;)\s*`)
	redirRe     = lazyre.New(`\s*(?:2>&1|1>&2|&>\s*/dev/null|[12]?>\s*/dev/null)`)
	spaceRe     = lazyre.New(`\s+`)
	timeoutRe   = lazyre.New(`^(?:timeout\s+(?:-[a-zA-Z]+\s+\S+\s+)*\d+[smhd]?\s+|time\s+|npx\s+(?:--yes|-y)\s+)`)
	assignRe    = lazyre.New(`[A-Za-z_][A-Za-z0-9_]*=(?:'[^']*'|"[^"]*"|\S*)`)
	varRefRe    = lazyre.New(`\$[A-Za-z_{(]`)
)

// opaque marks shell text whose effect cannot be read from the command
// alone: substitutions, variable references, command lists and builtins
// that change the shell itself.
var opaque = []string{"`", "&&", "||", ";", "<(", ">(", "\n"}
var opaqueLead = map[string]bool{"eval": true, "source": true, ".": true, "export": true, "cd": true, "pushd": true, "popd": true, "alias": true, "set": true, "unset": true}

// ExecFP is the identity used to decide that two shell runs are the same
// execution. Unlike CmdFP (display and statistics), it keeps the effective
// directory, the leading environment assignments (last value wins), the
// timeout limit and whitespace inside quotes, all of which NormalizeCmd drops
// or folds. dir is the shell's directory relative to the project ("" for the
// root). certain is false when the command uses constructs whose effect the
// text does not show; such runs must never be blocked as repeats.
func ExecFP(cmd, dir string) (id string, certain bool) {
	s := protectQuoted(strings.TrimSpace(cmd))
	env := map[string]string{}
	var wrappers []string
	for i := 0; i < 4; i++ {
		before := s
		if m := envPrefixRe.FindString(s); m != "" {
			for _, a := range assignRe.FindAllString(m, -1) {
				k, v, _ := strings.Cut(a, "=")
				env[k] = v
			}
			s = s[len(m):]
		}
		if m := cdPrefixRe.FindStringSubmatch(s); m != nil {
			d := strings.Trim(restoreQuoted(m[1]), `'"`)
			if strings.HasPrefix(d, "/") || strings.HasPrefix(d, "~") || strings.Contains(d, "$") || pathnorm.IsWindowsAbs(d) {
				return "", false
			}
			dir = path.Join(dir, d)
			s = s[len(m[0]):]
		}
		if m := timeoutRe.FindString(s); m != "" {
			// a time limit changes what the run can show; npx --yes does not
			if w := strings.Fields(m); len(w) > 0 && w[0] == "timeout" {
				wrappers = append(wrappers, strings.Join(w, " "))
			}
			s = strings.TrimSpace(s[len(m):])
		}
		if s == before {
			break
		}
	}
	norm, _ := NormalizeCmd(s)
	norm = restoreQuoted(norm)
	if norm == "" {
		return "", false
	}
	certain = !varRefRe.MatchString(norm) && !opaqueLead[firstWord(norm)]
	for _, o := range opaque {
		if strings.Contains(norm, o) {
			certain = false
		}
	}
	if dir = path.Clean(dir); dir == ".." || strings.HasPrefix(dir, "../") {
		certain = false
	}
	if dir == "." {
		dir = ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+env[k])
	}
	return Hash("exec/2", dir, strings.Join(parts, "\x00"), strings.Join(wrappers, "\x00"), norm), certain
}

// Whitespace inside quotes is meaningful ("a  b" is not "a b"). It is
// replaced by private markers while the command is normalized, then put back.
const quotedSpace, quotedTab = "\x01", "\x02"

func protectQuoted(s string) string {
	var b strings.Builder
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
			b.WriteRune(r)
		case quote != 0 && r == ' ':
			b.WriteString(quotedSpace)
		case quote != 0 && r == '\t':
			b.WriteString(quotedTab)
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func restoreQuoted(s string) string {
	return strings.NewReplacer(quotedSpace, " ", quotedTab, "\t").Replace(s)
}

// cosmetic final pipe stages that only trim or filter output.
var cosmeticStages = map[string]bool{"grep": true, "tail": true, "head": true, "less": true, "cat": true, "tee": true, "more": true, "sort": false}

// NormalizeCmd applies the normalization and returns the normalized
// command and the directory from a leading `cd <dir> &&` prefix (if any).
func NormalizeCmd(cmd string) (norm string, dir string) {
	s := strings.TrimSpace(cmd)
	for i := 0; i < 4; i++ {
		before := s
		s = envPrefixRe.ReplaceAllString(s, "")
		if m := cdPrefixRe.FindStringSubmatch(s); m != nil {
			dir = strings.Trim(m[1], `'"`)
			s = s[len(m[0]):]
		}
		s = strings.TrimSpace(timeoutRe.ReplaceAllString(s, ""))
		if s == before {
			break
		}
	}
	s = redirRe.ReplaceAllString(s, "")
	// strip trailing cosmetic pipe stages
	for {
		idx := lastTopLevelPipe(s)
		if idx < 0 {
			break
		}
		stage := strings.TrimSpace(s[idx+1:])
		name := firstWord(stage)
		if !cosmeticStages[name] || (name == "tee" && strings.Contains(stage, "-a")) {
			break
		}
		s = strings.TrimSpace(s[:idx])
	}
	s = spaceRe.ReplaceAllString(strings.TrimSpace(s), " ")
	return canonicalProgram(s), dir
}

// canonicalProgram rewrites a program spelled the Windows way (a backslash
// path, a drive letter, an .exe/.cmd/.bat suffix) to its bare name so the same
// run has one fingerprint on every system. Other spellings are left alone,
// which keeps existing fingerprints unchanged.
func canonicalProgram(s string) string {
	word, rest := s, ""
	if s != "" && (s[0] == '"' || s[0] == '\'') {
		if end := strings.IndexByte(s[1:], s[0]); end >= 0 {
			word, rest = s[1:1+end], s[end+2:]
		}
	} else if i := strings.IndexAny(s, " \t"); i >= 0 {
		word, rest = s[:i], s[i:]
	}
	if word == "" || !(strings.Contains(word, `\`) || pathnorm.HasExecutableSuffix(word) || pathnorm.IsWindowsAbs(word)) {
		return s
	}
	return pathnorm.CommandBase(word) + rest
}

// CmdFP returns the fingerprint of a normalized command.
func CmdFP(norm string) string {
	if norm == "" {
		return ""
	}
	return Hash("cmd", norm)
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// lastTopLevelPipe finds the last `|` (not `||`) outside quotes.
func lastTopLevelPipe(s string) int {
	inS, inD := false, false
	last := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inD:
			inS = !inS
		case c == '"' && !inS:
			inD = !inD
		case c == '|' && !inS && !inD:
			if i+1 < len(s) && s[i+1] == '|' {
				i++
				continue
			}
			if i > 0 && s[i-1] == '|' {
				continue
			}
			last = i
		}
	}
	return last
}

// SplitStages splits a shell command on top-level `&&`, `||`, `;` and `|`.
func SplitStages(s string) []string {
	var out []string
	inS, inD := false, false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inD:
			inS = !inS
		case c == '"' && !inS:
			inD = !inD
		case !inS && !inD && (c == ';' || c == '|' || c == '&' || c == '\n'):
			if c == '&' && !(i+1 < len(s) && s[i+1] == '&') {
				continue
			}
			out = append(out, strings.TrimSpace(s[start:i]))
			if (c == '&' || c == '|') && i+1 < len(s) && s[i+1] == c {
				i++
			}
			start = i + 1
		}
	}
	out = append(out, strings.TrimSpace(s[start:]))
	res := out[:0]
	for _, p := range out {
		if p != "" {
			res = append(res, p)
		}
	}
	return res
}

// ResultFP hashes exit code, sorted error fingerprints and sorted failed tests.
func ResultFP(exit *int, errFPs, failed []string) string {
	ec := "nil"
	if exit != nil {
		ec = strconv.Itoa(*exit)
	}
	e := append([]string(nil), errFPs...)
	f := append([]string(nil), failed...)
	sort.Strings(e)
	sort.Strings(f)
	return Hash("result", ec, strings.Join(e, ","), strings.Join(f, ","))
}

// MapFP hashes a path->hash map deterministically (virtual workspace state).
func MapFP(m map[string]string, extra []string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte('\n')
	}
	for _, x := range extra {
		b.WriteString(x)
		b.WriteByte('\n')
	}
	return Hash("ws", b.String())
}
