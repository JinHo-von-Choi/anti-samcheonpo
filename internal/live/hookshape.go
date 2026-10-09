package live

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/claude"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// hookShape checks that a tool hook carries what the handlers read: the
// session, the tool name and call ID, the tool input as an object with the
// command or file the tool works on, and a result after the call. It returns
// why a payload does not match, "" when it does. Unknown tool names are not
// a mismatch: agents add tools, and an unknown tool is only observed.
func hookShape(name string, in HookInput) string {
	switch name {
	case "PreToolUse", "PostToolUse", "PostToolUseFailure":
	default:
		return ""
	}
	if in.SessionID == "" {
		return name + ": session_id 없음"
	}
	if in.ToolName == "" {
		return name + ": tool_name 없음"
	}
	if in.ToolUseID == "" {
		return name + ": tool_use_id 없음"
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(in.ToolInput, &input); err != nil || input == nil {
		return fmt.Sprintf("%s(%s): tool_input이 객체가 아님", name, in.ToolName)
	}
	str := func(keys ...string) bool {
		for _, k := range keys {
			var s string
			if raw, ok := input[k]; ok && json.Unmarshal(raw, &s) == nil && s != "" {
				return true
			}
		}
		return false
	}
	switch {
	case in.ToolName == "apply_patch":
		if !str("command", "input", "patch") {
			return name + "(apply_patch): 패치 본문 없음"
		}
	case claude.NormalizeTool(in.ToolName) == event.ToolShell && in.ToolName != "BashOutput":
		if !str("command", "cmd") {
			return fmt.Sprintf("%s(%s): 셸 명령 없음", name, in.ToolName)
		}
	case in.ToolName == "Write" || in.ToolName == "Edit" || in.ToolName == "MultiEdit":
		if !str("file_path", "path") {
			return fmt.Sprintf("%s(%s): 파일 경로 없음", name, in.ToolName)
		}
	}
	if name == "PostToolUse" && len(strings.TrimSpace(string(in.ToolResponse))) == 0 && in.Error == "" {
		return fmt.Sprintf("PostToolUse(%s): tool_response 없음", in.ToolName)
	}
	return ""
}

// capsFor requires d.mu. An agent gets its profile's capabilities once its
// version probe finished, whatever the version, unless a hook payload of
// that agent stopped matching what the handlers read.
func (d *Daemon) capsFor(agent string) adapter.Caps {
	if d.shapeBad[agent] != "" {
		return adapter.Observe(agent)
	}
	caps, _ := adapter.Resolve(agent, d.versions[agent])
	return caps
}

// markShapeBad drops every session of an agent to observation after one of
// its hook payloads did not match: a changed format could otherwise be
// misread into wrong advice or a wrong block. It stays so until the daemon
// restarts; the reason is shown on the status line and by doctor.
func (d *Daemon) markShapeBad(agent, reason string) {
	d.mu.Lock()
	if d.shapeBad == nil {
		d.shapeBad = map[string]string{}
	}
	if d.shapeBad[agent] != "" {
		d.mu.Unlock()
		return
	}
	d.shapeBad[agent] = reason
	var sessions []*Session
	for _, s := range d.sessions {
		if s.Agent == agent {
			sessions = append(sessions, s)
		}
	}
	d.mu.Unlock()
	observe := adapter.Observe(agent)
	for _, s := range sessions {
		s.mu.Lock()
		s.caps = observe
		s.shapeNote = reason
		s.mu.Unlock()
	}
}
