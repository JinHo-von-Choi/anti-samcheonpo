package detect

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

const (
	// maxGlobWrites caps the files one glob in a shell command expands to.
	maxGlobWrites = 256
	// maxPatchRead caps the size of a patch file read to learn its targets.
	maxPatchRead = 1 << 20
)

// WrittenPaths lists the project-relative slash paths a call writes, deletes
// or replaces: the paths of a write or edit tool, and for a shell command the
// files it names as written (redirections, in-place edits, copies, moves,
// removals, checkouts, patches). A path outside the project or in a
// temporary folder is not part of the project and is left out.
func (e *Engine) WrittenPaths(ev *event.Event) []string {
	if ev.Tool != event.ToolShell {
		if ev.Category == event.CatProduce || ev.Tool == event.ToolWrite || ev.Tool == event.ToolEdit {
			return ev.Paths
		}
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(rel string) {
		if rel != "" && !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	if ev.Mutating {
		// a here-document write is already a call path
		for _, p := range ev.Paths {
			add(e.projectRel(ev, p))
		}
	}
	for _, p := range ev.ShellWrites {
		if strings.ContainsAny(p, "*?[") {
			matches, _ := filepath.Glob(e.shellAbs(ev, p))
			if len(matches) > maxGlobWrites {
				matches = matches[:maxGlobWrites]
			}
			for _, m := range matches {
				add(e.projectRel(ev, m))
			}
			continue
		}
		add(e.projectRel(ev, p))
	}
	for _, pf := range ev.ShellPatches {
		abs := e.shellAbs(ev, pf)
		if fi, err := os.Stat(abs); err != nil || !fi.Mode().IsRegular() || fi.Size() > maxPatchRead {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		for _, p := range classify.PatchTargets(string(b)) {
			add(e.projectRel(ev, p))
		}
	}
	return out
}

// shellBase is the folder a shell command runs in: the folder it changes to
// first, else the project root.
func (e *Engine) shellBase(ev *event.Event) string {
	if ev.Dir == "" {
		return e.Root
	}
	if rooted(ev.Dir) || e.Root == "" {
		return filepath.Clean(ev.Dir)
	}
	return filepath.Join(e.Root, ev.Dir)
}

// rooted reports a path a shell resolves from the top of the file system,
// not from its folder: a POSIX shell on Windows reads "/tmp/x" this way,
// though it has no drive letter.
func rooted(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)
}

func (e *Engine) shellAbs(ev *event.Event, p string) string {
	if rooted(p) {
		return filepath.Clean(p)
	}
	base := e.shellBase(ev)
	if base == "" {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// projectRel turns a path a shell command names into a project-relative
// slash path, or "" when it lies outside the project or in a temporary
// folder.
func (e *Engine) projectRel(ev *event.Event, p string) string {
	abs := e.shellAbs(ev, p)
	if e.Root == "" {
		if rooted(p) || pathnorm.IsTempPath(p) {
			return ""
		}
		return filepath.ToSlash(filepath.Clean(p))
	}
	// a project may itself live in a temporary folder; only the root decides
	rel, err := filepath.Rel(e.Root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	if rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// LineDiff returns the multiset difference of two line lists: the lines
// only in b (added) and the lines only in a (removed).
func LineDiff(a, b []string) (added, removed []string) {
	cnt := make(map[string]int, len(a))
	for _, x := range a {
		cnt[x]++
	}
	for _, x := range b {
		if cnt[x] > 0 {
			cnt[x]--
			continue
		}
		added = append(added, x)
	}
	cb := make(map[string]int, len(b))
	for _, x := range b {
		cb[x]++
	}
	for _, x := range a {
		if cb[x] > 0 {
			cb[x]--
			continue
		}
		removed = append(removed, x)
	}
	return added, removed
}

// baselineNote is the outcome of rerunning a passing check on the code with
// the session's feature changes undone.
type baselineNote struct {
	seq     int64
	outcome string
	tests   []string
}

// NoteBaseline records the baseline comparison of the passing check seq.
// A completion claim backed by that check is judged with it.
func (e *Engine) NoteBaseline(seq int64, outcome string, tests []string) {
	e.St.baseline = &baselineNote{seq: seq, outcome: outcome, tests: append([]string(nil), tests...)}
}
