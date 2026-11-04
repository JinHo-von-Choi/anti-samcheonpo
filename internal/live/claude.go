package live

import (
	"encoding/json"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/claude"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// Claude's child tool hooks retain the root session_id and add agent_id.
// Namespace that identity so child state and verification never mix with the
// parent's. The native envelope proves root ownership, not arbitrary nesting.
func claudeIn(name string, raw json.RawMessage) ([]string, []HookInput, error) {
	var in HookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	if in.AgentID == "" || in.SessionID == "" {
		return []string{name}, []HookInput{in}, nil
	}
	// The common transcript_path belongs to the root. Never charge it again
	// to the child. Claude supplies the child transcript at SubagentStop.
	in.TranscriptPath = in.AgentTranscriptPath
	in.ParentSessionID = in.SessionID
	in.SessionID = nativeChildSessionID(in.SessionID, in.AgentID)
	switch name {
	case "SubagentStart":
		name = "SessionStart"
	case "SubagentStop":
		name = "Stop"
		in.ChildStop = true
	}
	return []string{name}, []HookInput{in}, nil
}

// attachChildTranscript runs with s.mu held. Unlike a resumed root, a child
// first exposes its own transcript at completion, so read from the beginning.
func (s *Session) attachChildTranscript(path string) {
	if s.Agent != "claude" || s.nativeParent == "" || s.Transcript != "" || path == "" {
		return
	}
	s.Transcript = path
	s.parser.Session.SourcePath = path
	s.usage = claude.NewUsageTracker(path)
	if s.db != nil {
		s.recordStorageError(s.db.UpsertLive(s.ID, s.Agent, s.Root, path, s.parser.Session.Model))
	}
}

func nativeChildSessionID(parent, agentID string) string {
	return "child-" + fp.Hash(parent+"\x00"+agentID)
}
