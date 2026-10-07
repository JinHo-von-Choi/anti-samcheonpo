package bench

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

// ResolveTranscript requires both the emitted session ID and the trial's
// working directory. A fresh unrelated transcript must never supply costs.
func ResolveTranscript(agent, id, repo, claudeDir, codexDir string, start time.Time) (*event.Session, error) {
	if id == "" || strings.ContainsAny(id, "/\\*?[") {
		return nil, fmt.Errorf("missing or invalid session ID")
	}
	if agent != "claude" && agent != "codex" {
		return nil, fmt.Errorf("unsupported transcript agent: %s", agent)
	}
	var found *event.Session
	for _, file := range sources.Find(agent, start, claudeDir, codexDir) {
		if !strings.Contains(filepath.Base(file.Path), id) {
			continue
		}
		s, err := sources.Parse(file)
		if err != nil {
			return nil, fmt.Errorf("transcript parse: %w", err)
		}
		if s.ID != id {
			continue
		}
		if !sameDir(s.ProjectPath, repo) {
			return nil, fmt.Errorf("transcript workspace mismatch")
		}
		if found != nil {
			return nil, fmt.Errorf("ambiguous session transcripts")
		}
		found = s
	}
	if found == nil {
		return nil, fmt.Errorf("fresh matching transcript not found")
	}
	if found.FormatFailed || found.UsageParsed == 0 || found.UsageParsed != found.UsageLines {
		return nil, fmt.Errorf("incomplete transcript usage")
	}
	return found, nil
}

// sameDir compares two directories after resolving links, so a project under
// a linked temp directory (macOS /var) still matches its transcript.
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}
