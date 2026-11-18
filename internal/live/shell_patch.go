package live

import (
	"os"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// maxShellSnapshots caps the test files one shell call keeps a before-copy of.
const maxShellSnapshots = 32

// snapshotShellTests keeps, before a shell call runs, the content of the
// test files it names as written. A shell edit cannot be read before it
// runs, so the change is rebuilt from the two states after the call; called
// with s.mu held.
func (s *Session) snapshotShellTests(ev *event.Event) {
	if ev.Tool != event.ToolShell || s.Root == "" {
		return
	}
	var tests []string
	for _, p := range s.eng.WrittenPaths(ev) {
		if classify.IsTestPath(p) {
			tests = append(tests, p)
		}
	}
	if len(tests) == 0 {
		return
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return
	}
	defer root.Close()
	snap := map[string]fileState{}
	for _, p := range tests {
		if len(snap) >= maxShellSnapshots {
			break
		}
		if st, err := readState(root, p); err == nil {
			snap[p] = st
		}
	}
	if len(snap) == 0 {
		return
	}
	if s.shellBefore == nil {
		s.shellBefore = map[string]map[string]fileState{}
	}
	s.shellBefore[ev.CallID] = snap
}

// shellTestPatch turns the before-copies of a finished shell call into the
// call's patch, so the test weakening rules see a shell edit as they see a
// write; called with s.mu held.
func (s *Session) shellTestPatch(ev *event.Event) {
	snap := s.shellBefore[ev.CallID]
	delete(s.shellBefore, ev.CallID)
	if len(snap) == 0 {
		return
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return
	}
	defer root.Close()
	for p, before := range snap {
		after, err := readState(root, p)
		if err != nil {
			continue
		}
		switch {
		case before.absent && after.absent:
		case after.absent:
			ev.Deleted = appendOnce(ev.Deleted, p)
			ev.Patch = append(ev.Patch, event.PatchFile{Path: p, Deleted: true})
		case before.hash() == after.hash():
		default:
			if before.absent {
				ev.Created = appendOnce(ev.Created, p)
			}
			added, removed := detect.LineDiff(splitLines(string(before.data)), splitLines(string(after.data)))
			ev.Patch = append(ev.Patch, event.PatchFile{Path: p, Added: added, Removed: removed})
		}
	}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func appendOnce(list []string, p string) []string {
	for _, x := range list {
		if x == p {
			return list
		}
	}
	return append(list, p)
}
