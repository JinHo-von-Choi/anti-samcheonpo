// Package sources discovers agent transcript files.
package sources

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/claude"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/codex"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/otel"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// File is one transcript file.
type File struct {
	Path  string
	Agent string
	Size  int64 // including merged subagent files
	MTime int64
}

// ClaudeDir returns the Claude Code projects directory.
func ClaudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude", "projects")
}

// CodexDir returns the Codex sessions directory.
func CodexDir() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return filepath.Join(d, "sessions")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codex", "sessions")
}

// Find lists transcript files modified since `since` for an agent filter
// (claude, codex or all).
func Find(agent string, since time.Time, claudeDir, codexDir string) []File {
	var out []File
	if agent == "" || agent == "all" || agent == "claude" {
		dirs, _ := os.ReadDir(claudeDir)
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			files, _ := filepath.Glob(filepath.Join(claudeDir, d.Name(), "*.jsonl"))
			for _, f := range files {
				st, err := os.Stat(f)
				if err != nil || st.ModTime().Before(since) || st.Size() == 0 {
					continue
				}
				size := st.Size()
				mt := st.ModTime().UnixNano()
				subs, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(f, ".jsonl"), "subagents", "*.jsonl"))
				for _, s := range subs {
					if ss, err := os.Stat(s); err == nil {
						size += ss.Size()
						if ss.ModTime().UnixNano() > mt {
							mt = ss.ModTime().UnixNano()
						}
					}
				}
				out = append(out, File{Path: f, Agent: "claude", Size: size, MTime: mt})
			}
		}
	}
	if agent == "" || agent == "all" || agent == "codex" {
		_ = filepath.WalkDir(codexDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") {
				return nil
			}
			st, err := d.Info()
			if err != nil || st.ModTime().Before(since) || st.Size() == 0 {
				return nil
			}
			out = append(out, File{Path: p, Agent: "codex", Size: st.Size(), MTime: st.ModTime().UnixNano()})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Parse parses a transcript file with the matching adapter. For OTLP/JSON
// traces the first session is returned.
func Parse(f File) (*event.Session, error) {
	switch f.Agent {
	case "codex":
		return codex.ParseFile(f.Path)
	case "otel":
		ss, err := otel.ParseFile(f.Path)
		if err != nil {
			return nil, err
		}
		if len(ss) == 0 {
			return nil, fmt.Errorf("%s에 GenAI 세션이 없다", f.Path)
		}
		return ss[0], nil
	}
	return claude.ParseFile(f.Path)
}

// DetectAgent guesses the agent from a path.
func DetectAgent(p string) string {
	if strings.Contains(p, "/.codex/") || strings.HasPrefix(filepath.Base(p), "rollout-") {
		return "codex"
	}
	return "claude"
}
