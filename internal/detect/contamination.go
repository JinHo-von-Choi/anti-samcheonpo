package detect

import (
	"path/filepath"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// RuleGraderAccess is a tool call that reached a path the operator
// declared protected.
//
// The benchmark puts the grader, the answer and the rubric on that list.
// A trial that reads them is not being measured, it is being told the
// answer, so the call is stopped before it runs.
const RuleGraderAccess = "s10.grader_access"

// protectedHit returns the protected path an event touches.
//
// The check is on the paths the event names and on the command it runs.
// A shell call can reach a protected path without naming a file at all,
// so the command text is checked too.
func (e *Engine) protectedHit(ev *event.Event) string {
	for _, p := range e.Cfg.ProtectedPaths {
		if p == "" {
			continue
		}
		abs := e.resolve(p)
		for _, ep := range ev.Paths {
			if ep == "" {
				continue
			}
			candidate := e.resolve(ep)
			if under(abs, candidate) || under(candidate, abs) {
				return ep
			}
		}
		if ev.CmdNorm != "" && strings.Contains(ev.CmdNorm, abs) {
			return p
		}
		// a relative path in the command ("cat grader/answer.txt") is
		// resolved against the project. Only a word inside the protected
		// path counts: "ls ." names the project, not the grader.
		for _, w := range commandWords(ev.Cmd) {
			if under(abs, e.resolve(w)) {
				return w
			}
		}
	}
	return ""
}

// commandWords returns the words of a shell command that look like paths.
func commandWords(cmd string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(cmd, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || strings.ContainsRune(";|&<>()'\"`=", r)
	}) {
		if strings.HasPrefix(w, "-") || !strings.ContainsAny(w, "/.") {
			continue
		}
		out = append(out, w)
	}
	return out
}

// resolve makes a path absolute against the session's project. Event paths
// are stored relative to the project, and one daemon serves many projects,
// so the daemon's own working directory is never the base.
func (e *Engine) resolve(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if e.Root != "" {
		return filepath.Join(e.Root, p)
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// under reports whether path is inside root.
func under(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if root == path {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
