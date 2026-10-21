package live

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testout"
)

// cursorEvidence is derived from the hook envelope, never from output prose.
type cursorEvidence struct {
	Shape       string
	Exit        *int
	State       string
	Duration    float64
	Rank        int
	EndStatus   string
	LoopCount   int
	Failure     string
	Interrupted bool
}

func cursorIn(name string, raw json.RawMessage) ([]string, []HookInput, error) {
	var c struct {
		ConversationID string          `json:"conversation_id"`
		WorkspaceRoots []string        `json:"workspace_roots"`
		Cwd            string          `json:"cwd"`
		Transcript     string          `json:"transcript_path"`
		ID             string          `json:"tool_use_id"`
		Tool           string          `json:"tool_name"`
		Input          json.RawMessage `json:"tool_input"`
		Output         json.RawMessage `json:"tool_output"`
		Command        string          `json:"command"`
		ShellOutput    string          `json:"output"`
		Exit           *int            `json:"exit_code"`
		Path           string          `json:"file_path"`
		Edits          json.RawMessage `json:"edits"`
		Prompt         string          `json:"prompt"`
		Duration       float64         `json:"duration"`
		Error          string          `json:"error_message"`
		Failure        string          `json:"failure_type"`
		Interrupted    bool            `json:"is_interrupt"`
		Status         string          `json:"status"`
		FinalStatus    string          `json:"final_status"`
		Reason         string          `json:"reason"`
		LoopCount      int             `json:"loop_count"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, nil, err
	}
	e := &cursorEvidence{State: "unknown", Duration: c.Duration, Rank: 1, Failure: c.Failure, Interrupted: c.Interrupted}
	in := HookInput{SessionID: c.ConversationID, TranscriptPath: c.Transcript, Cwd: c.Cwd, ToolUseID: c.ID, ToolName: c.Tool, ToolInput: c.Input, Cursor: e}
	// Workspace identity is stable even when a shell runs in a subdirectory.
	if len(c.WorkspaceRoots) > 0 {
		in.Cwd = c.WorkspaceRoots[0]
	}
	ev := ""
	switch name {
	case "sessionStart":
		ev = "SessionStart"
	case "sessionEnd":
		ev = "SessionEnd"
		e.EndStatus = c.FinalStatus
		if e.EndStatus == "" {
			e.EndStatus = c.Reason
		}
	case "beforeSubmitPrompt":
		ev = "UserPromptSubmit"
		in.Prompt = c.Prompt
	case "preCompact":
		ev = "PreCompact"
	case "stop":
		ev = "Stop"
		e.EndStatus = c.Status
		e.LoopCount = c.LoopCount
		in.StopHookActive = c.LoopCount > 0
	case "preToolUse", "postToolUse", "postToolUseFailure":
		e.Rank = 2
		if c.Tool == "StrReplace" {
			in.ToolName = "Edit"
		}
		if c.Tool == "Shell" {
			in.ToolName = "Bash"
		}
		ev = map[string]string{"preToolUse": "PreToolUse", "postToolUse": "PostToolUse", "postToolUseFailure": "PostToolUseFailure"}[name]
		if name == "postToolUse" {
			var body map[string]json.RawMessage
			payload := c.Output
			var encoded string
			if json.Unmarshal(payload, &encoded) == nil {
				payload = json.RawMessage(encoded)
			}
			if json.Unmarshal(payload, &body) != nil || body == nil {
				e.Shape = "Cursor: tool_output이 JSON 객체가 아님"
			} else {
				if value, ok := body["exitCode"]; in.ToolName == "Bash" && ok && string(value) != "null" {
					var code int
					if json.Unmarshal(value, &code) != nil || code < 0 {
						e.Shape = "Cursor: exitCode가 음이 아닌 정수가 아님"
					} else {
						e.Exit = &code
						e.State = "exited"
						e.Rank = 3
					}
				}
				if _, ok := body["stdout"]; !ok {
					if value, ok := body["output"]; ok {
						body["stdout"] = value
					}
				}
				in.ToolResponse, _ = json.Marshal(body)
				if in.ToolName != "Bash" {
					e.State = "completed"
				}
				var isError bool
				_ = json.Unmarshal(body["isError"], &isError)
				if isError || (e.Exit != nil && *e.Exit != 0) {
					ev = "PostToolUseFailure"
					if e.Exit == nil {
						e.State = "failed"
						e.Rank = 3
					}
					in.Error = "Cursor tool failed"
				}
			}
		} else if name == "postToolUseFailure" {
			in.Error = c.Error
			e.Rank = 3
			switch {
			case c.Interrupted:
				e.State = "interrupted"
			case c.Failure == "permission_denied":
				e.State = "blocked"
			case c.Failure == "timeout":
				e.State = "timeout"
			default:
				e.State = "failed"
			}
		}
	case "beforeShellExecution", "afterShellExecution":
		in.ToolName = "Bash"
		in.ToolInput, _ = json.Marshal(map[string]string{"command": c.Command, "cwd": c.Cwd})
		ev = "PreToolUse"
		if name == "afterShellExecution" {
			ev = "PostToolUse"
			e.Exit = c.Exit
			in.ToolResponse, _ = json.Marshal(map[string]string{"stdout": c.ShellOutput})
			if c.Exit != nil {
				e.State = "exited"
				e.Rank = 3
				if *c.Exit != 0 {
					ev = "PostToolUseFailure"
					in.Error = fmt.Sprintf("Exit code %d", *c.Exit)
				}
			}
		}
	case "beforeReadFile":
		ev = "PreToolUse"
		in.ToolName = "Read"
		in.ToolInput, _ = json.Marshal(map[string]string{"file_path": c.Path})
	case "afterFileEdit":
		// An after-only notification cannot provide a pre-edit snapshot.
		ev = "PostToolUse"
		in.ToolName = "MultiEdit"
		in.ToolInput, _ = json.Marshal(map[string]any{"file_path": c.Path, "edits": c.Edits})
		in.ToolResponse = json.RawMessage(`{}`)
	default:
		return nil, nil, nil
	}
	if in.ToolName == "Read" || in.ToolName == "Write" || in.ToolName == "Edit" || in.ToolName == "Delete" {
		var input map[string]json.RawMessage
		if json.Unmarshal(in.ToolInput, &input) == nil && input != nil && len(input["file_path"]) == 0 {
			if path, ok := input["path"]; ok {
				input["file_path"] = path
				in.ToolInput, _ = json.Marshal(input)
			}
		}
	}
	if in.ToolName == "Bash" {
		var input map[string]json.RawMessage
		if json.Unmarshal(in.ToolInput, &input) == nil && input != nil {
			for _, key := range []string{"cwd", "working_directory"} {
				if len(input["workdir"]) == 0 && len(input[key]) > 0 {
					input["workdir"] = input[key]
				}
			}
			in.ToolInput, _ = json.Marshal(input)
		}
	}
	if c.ConversationID == "" {
		e.Shape = "Cursor: conversation_id 없음"
	}
	if strings.Contains(ev, "ToolUse") && c.ID == "" {
		e.Shape = "Cursor: tool_use_id 없음"
	}
	return []string{ev}, []HookInput{in}, nil
}

func cursorOut(name string, m, hs map[string]any) json.RawMessage {
	r := map[string]any{}
	switch name {
	case "preToolUse", "beforeShellExecution", "beforeReadFile", "beforeMCPExecution", "subagentStart":
		r["permission"] = "allow"
		if hs != nil && hs["permissionDecision"] == "deny" {
			r["permission"] = "deny"
			r["agent_message"] = hs["permissionDecisionReason"]
			r["user_message"] = hs["permissionDecisionReason"]
		}
	case "postToolUse", "postToolUseFailure":
		if hs != nil {
			if context, ok := hs["additionalContext"]; ok {
				r["additional_context"] = context
			}
		}
	case "stop":
		if m["decision"] == "block" {
			r["followup_message"] = m["reason"]
		}
	case "beforeSubmitPrompt":
		r["continue"] = true
	}
	return marshalOrNil(r)
}

func applyCursorResult(ev *event.Event, in HookInput, failure bool) {
	e := in.Cursor
	if e == nil {
		return
	}
	var body struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}
	if json.Unmarshal(in.ToolResponse, &body) == nil && (body.Stdout != "" || body.Stderr != "") {
		text := body.Stdout
		if body.Stderr != "" {
			text += "\n" + body.Stderr
		}
		if len(text) > 4000 {
			text = text[len(text)-4000:]
		}
		ev.Text = text
		ev.ObservationFP = fp.Hash(text)
		if ev.Tool == event.ToolShell {
			ev.ErrFPs = fp.ErrorFPs(text)
			ev.FailedTests = testout.FailedTests(text)
		}
	}
	ev.ResultObserved = true
	ev.IsError = failure
	if ev.Tool == event.ToolShell {
		ev.ExitCode = e.Exit
		if e.State == "blocked" {
			ev.ExitCode = intPtr(-1)
		}
		if e.Exit != nil {
			ev.IsError = *e.Exit != 0
		}
	}
	ev.Execution = &event.Execution{State: e.State, ExitSource: "cursor_hook", CallID: in.ToolUseID, ObservedAt: time.Now(), FailureType: e.Failure, Interrupted: e.Interrupted}
	if e.Duration >= 0 && e.Duration < float64(1<<63) {
		ev.DurationMS = int64(e.Duration)
	}
	ev.ResultFP = fp.ResultFP(ev.ExitCode, ev.ErrFPs, ev.FailedTests)
	if ev.Reliability != nil && ev.Tool == event.ToolShell {
		ev.Reliability.SourceMissing = e.State == "unknown"
	}
}

func cursorEndStatus(status string) string {
	switch status {
	case "completed", "error", "aborted", "cancelled", "timeout":
		return status
	}
	return "unknown"
}
