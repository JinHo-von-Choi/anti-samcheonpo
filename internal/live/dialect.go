package live

import (
	"encoding/json"
	"strings"
)

// translateIn maps another agent's hook payload to the Claude dialect the
// handlers use. It returns the Claude event name, or "" to ignore the event.
func translateIn(agent, event string, raw json.RawMessage) ([]string, []HookInput, error) {
	switch agent {
	case "copilot":
		return copilotIn(event, raw)
	case "cursor":
		return cursorIn(event, raw)
	}
	if agent == "agy" {
		return agyIn(event, raw)
	}
	// Claude family (claude, codex, opencode forwarder)
	var in HookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	return []string{event}, []HookInput{in}, nil
}

// agyTool maps Antigravity tool names to the Claude names the parser knows.
func agyTool(name string) string {
	switch strings.ToLower(name) {
	case "run_command", "run_terminal_command":
		return "Bash"
	case "view_file", "read_file":
		return "Read"
	case "write_to_file", "create_file":
		return "Write"
	case "replace_file_content", "edit_file", "multi_replace_file_content":
		return "Edit"
	case "grep_search", "find_by_name", "list_dir":
		return "Grep"
	}
	return name
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// translateOut maps a Claude-dialect decision to the agent's output format.
func translateOut(agent, event, claudeEvent string, out json.RawMessage, failClosed bool) json.RawMessage {
	var m map[string]any
	if len(out) > 0 {
		_ = json.Unmarshal(out, &m)
	}
	hs, _ := m["hookSpecificOutput"].(map[string]any)
	switch agent {
	case "copilot":
		return copilotOut(event, m, hs)
	case "cursor":
		return cursorOut(event, m, hs)
	case "agy":
		return marshalOrNil(agyOut(event, claudeEvent, m))
	}
	_ = failClosed
	return out
}

func marshalOrNil(m map[string]any) json.RawMessage {
	if len(m) == 0 {
		return nil
	}
	b, _ := json.Marshal(m)
	return b
}
