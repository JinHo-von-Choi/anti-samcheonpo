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
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		for _, ep := range ev.Paths {
			if ep == "" {
				continue
			}
			candidate, err := filepath.Abs(ep)
			if err != nil {
				candidate = ep
			}
			if under(abs, candidate) || under(candidate, abs) {
				return ep
			}
		}
		if ev.CmdNorm != "" && strings.Contains(ev.CmdNorm, abs) {
			return p
		}
	}
	return ""
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
