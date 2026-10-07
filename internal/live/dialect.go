package live

import (
	"encoding/json"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// translateIn maps another agent's hook payload to the Claude dialect the
// handlers use. It returns the Claude event name, or "" to ignore the event.
// Cursor's file edit hook expands to a pre and a post event.
func translateIn(agent, event string, raw json.RawMessage) ([]string, []HookInput, error) {
	switch agent {
	case "copilot":
		// PascalCase mode: snake_case fields, tool_result instead of tool_response
		var in HookInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, nil, err
		}
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		if r, ok := m["tool_result"]; ok && len(in.ToolResponse) == 0 {
			in.ToolResponse = r
		}
		if in.ToolUseID == "" && in.ToolName != "" {
			in.ToolUseID = "cp-" + fp.Hash(in.SessionID, in.ToolName, string(in.ToolInput))
		}
		ev := event
		if event == "ErrorOccurred" {
			return nil, nil, nil
		}
		return []string{ev}, []HookInput{in}, nil
	case "cursor":
		var c struct {
			ConversationID string   `json:"conversation_id"`
			GenerationID   string   `json:"generation_id"`
			WorkspaceRoots []string `json:"workspace_roots"`
			Transcript     string   `json:"transcript_path"`
			Command        string   `json:"command"`
			Cwd            string   `json:"cwd"`
			Output         string   `json:"output"`
			ExitCode       *int     `json:"exit_code"`
			FilePath       string   `json:"file_path"`
			Edits          []struct {
				Old string `json:"old_string"`
				New string `json:"new_string"`
			} `json:"edits"`
			Prompt   string          `json:"prompt"`
			ToolName string          `json:"tool_name"`
			ToolIn   json.RawMessage `json:"tool_input"`
			Status   string          `json:"status"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, nil, err
		}
		base := HookInput{SessionID: c.ConversationID, TranscriptPath: c.Transcript, Cwd: c.Cwd}
		if base.Cwd == "" && len(c.WorkspaceRoots) > 0 {
			base.Cwd = c.WorkspaceRoots[0]
		}
		id := "cu-" + fp.Hash(c.ConversationID, c.GenerationID, c.Command, c.FilePath)
		shellIn, _ := json.Marshal(map[string]string{"command": c.Command})
		switch event {
		case "sessionStart":
			return []string{"SessionStart"}, []HookInput{base}, nil
		case "sessionEnd":
			return []string{"SessionEnd"}, []HookInput{base}, nil
		case "beforeSubmitPrompt":
			b := base
			b.Prompt = c.Prompt
			return []string{"UserPromptSubmit"}, []HookInput{b}, nil
		case "beforeShellExecution":
			b := base
			b.ToolName, b.ToolUseID, b.ToolInput = "Bash", id, shellIn
			return []string{"PreToolUse"}, []HookInput{b}, nil
		case "afterShellExecution":
			b := base
			b.ToolName, b.ToolUseID, b.ToolInput = "Bash", id, shellIn
			b.ToolResponse, _ = json.Marshal(map[string]string{"stdout": c.Output})
			if c.ExitCode != nil && *c.ExitCode != 0 {
				b.Error = "Exit code " + itoa(*c.ExitCode) + "\n" + c.Output
				return []string{"PostToolUseFailure"}, []HookInput{b}, nil
			}
			return []string{"PostToolUse"}, []HookInput{b}, nil
		case "beforeReadFile":
			b := base
			b.ToolName, b.ToolUseID = "Read", id
			b.ToolInput, _ = json.Marshal(map[string]string{"file_path": c.FilePath})
			return []string{"PreToolUse"}, []HookInput{b}, nil
		case "afterFileEdit":
			var edits []map[string]any
			for _, e := range c.Edits {
				edits = append(edits, map[string]any{"old_string": e.Old, "new_string": e.New})
			}
			b := base
			b.ToolName, b.ToolUseID = "MultiEdit", id
			b.ToolInput, _ = json.Marshal(map[string]any{"file_path": c.FilePath, "edits": edits})
			b.ToolResponse = json.RawMessage(`{}`)
			return []string{"PreToolUse", "PostToolUse"}, []HookInput{b, b}, nil
		case "stop":
			return []string{"Stop"}, []HookInput{base}, nil
		case "preCompact":
			return []string{"PreCompact"}, []HookInput{base}, nil
		}
		return nil, nil, nil
	}
	// Claude family (claude, codex, opencode forwarder, agy)
	var in HookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	if agent == "agy" {
		in.ToolName = agyTool(in.ToolName)
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
		r := map[string]any{}
		if hs != nil {
			if d, ok := hs["permissionDecision"]; ok {
				r["permissionDecision"] = d
				r["permissionDecisionReason"] = hs["permissionDecisionReason"]
			}
			if c, ok := hs["additionalContext"]; ok && claudeEvent == "PostToolUse" {
				r["additionalContext"] = c
			}
		}
		if d, ok := m["decision"]; ok && claudeEvent == "Stop" {
			r["decision"], r["reason"] = d, m["reason"]
		}
		return marshalOrNil(r)
	case "cursor":
		r := map[string]any{}
		switch event {
		case "beforeShellExecution", "beforeReadFile", "beforeMCPExecution":
			r["permission"] = "allow"
			if hs != nil && hs["permissionDecision"] == "deny" {
				r["permission"] = "deny"
				r["agent_message"] = hs["permissionDecisionReason"]
				r["user_message"] = hs["permissionDecisionReason"]
			}
		case "stop":
			if m["decision"] == "block" {
				r["followup_message"] = m["reason"]
			}
		case "beforeSubmitPrompt":
			r["continue"] = true
		}
		return marshalOrNil(r)
	case "agy":
		// fail-closed: every pre-tool answer states its decision
		if claudeEvent == "PreToolUse" && (hs == nil || hs["permissionDecision"] == nil) {
			if m == nil {
				m = map[string]any{}
			}
			m["hookSpecificOutput"] = map[string]any{"hookEventName": "PreToolUse", "permissionDecision": "allow"}
		}
		return marshalOrNil(m)
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
