package live

import (
	"encoding/json"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/mcode"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/bgrun"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// mcodeEvidence is what a MiniMax Code hook payload states about a call.
// Every field comes from the hook envelope (testdata/hooks/mcode), never
// from the model's prose.
type mcodeEvidence struct {
	Shape  string // why the payload does not match what the handlers read
	Result mcode.Result
	// TaskState is the stated status of the background task a task tool
	// result names.
	TaskState string
}

// mcodeIn maps a MiniMax Code hook payload to the Claude dialect. Results of
// the task tools become BackgroundResult: they complete an earlier call.
func mcodeIn(name string, raw json.RawMessage) ([]string, []HookInput, error) {
	var in HookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	e := &mcodeEvidence{Result: mcode.Result{State: "unknown"}}
	in.Mcode = e
	if in.SessionID == "" {
		e.Shape = "mcode 훅에 세션 ID가 없다"
	}
	switch name {
	case "SessionStart", "SessionEnd", "UserPromptSubmit", "Stop", "PreCompact":
		return []string{name}, []HookInput{in}, nil
	case "PreToolUse", "PostToolUse":
	default:
		// SubagentStart/Stop arrive with the child's session id and no parent;
		// the child's own tool hooks open its session. PermissionRequest and
		// PostCompact have no counterpart the handlers read.
		return nil, nil, nil
	}
	if mcode.TaskTool(in.ToolName) {
		if name == "PreToolUse" {
			return nil, nil, nil
		}
		var r mcode.Response
		if json.Unmarshal(in.ToolResponse, &r) != nil {
			return nil, nil, nil
		}
		var names []string
		var outs []HookInput
		for _, t := range mcode.ReadTasks(&r) {
			c := in
			c.Mcode = &mcodeEvidence{Result: t.Result, TaskState: t.Status}
			names = append(names, "BackgroundResult")
			outs = append(outs, c)
		}
		return names, outs, nil
	}
	in.ToolName = mcode.Tool(in.ToolName)
	in.ToolInput = mcode.Input(in.ToolInput)
	if name == "PreToolUse" {
		return []string{name}, []HookInput{in}, nil
	}
	var r mcode.Response
	if len(in.ToolResponse) > 0 && json.Unmarshal(in.ToolResponse, &r) != nil {
		e.Shape = "mcode 도구 결과 형식을 읽을 수 없다"
	}
	e.Result = mcode.ReadResult(in.ToolName, &r)
	out, _ := json.Marshal(map[string]any{"stdout": e.Result.Text})
	in.ToolResponse = out
	if e.Result.State == "failed" || e.Result.State == "timeout" {
		// a failed shell call still arrives as PostToolUse; the failure is
		// stated in the result
		return []string{"PostToolUseFailure"}, []HookInput{in}, nil
	}
	return []string{"PostToolUse"}, []HookInput{in}, nil
}

// mcodeOut maps a Claude dialect decision to what MiniMax Code honours: a
// pre-tool or stop decision "block" with a reason, and additionalContext
// next to a tool result.
func mcodeOut(name string, m, hs map[string]any) json.RawMessage {
	r := map[string]any{}
	block := func(reason string) {
		r["decision"] = "block"
		if reason != "" {
			r["reason"] = reason
		}
	}
	switch name {
	case "PreToolUse":
		if d, _ := hs["permissionDecision"].(string); d == "deny" {
			reason, _ := hs["permissionDecisionReason"].(string)
			block(reason)
		}
		if d, _ := m["decision"].(string); d == "block" {
			reason, _ := m["reason"].(string)
			block(reason)
		}
	case "PostToolUse":
		ctx, _ := hs["additionalContext"].(string)
		if ctx == "" {
			// a finding the dialect ends the turn for reaches mcode as
			// guidance on the result: a post-tool hook cannot end its turn
			for _, k := range []string{"reason", "stopReason"} {
				if s, _ := m[k].(string); s != "" {
					ctx = s
					break
				}
			}
		}
		if ctx != "" {
			r["hookSpecificOutput"] = map[string]any{"hookEventName": "PostToolUse", "additionalContext": ctx}
		}
	case "Stop":
		if d, _ := m["decision"].(string); d == "block" {
			reason, _ := m["reason"].(string)
			block(reason)
		}
	}
	return marshalOrNil(r)
}

// applyMcodeResult records the result a MiniMax Code hook stated. The parser
// would read a shell result without an error as exit 0; the stated
// execution decides instead, and a call moved to the background has no
// result yet.
func applyMcodeResult(ev *event.Event, in HookInput, failure bool) {
	e := in.Mcode
	if e == nil {
		return
	}
	res := e.Result
	ev.ResultObserved = true
	ev.IsError = failure
	if ev.Tool == event.ToolShell {
		bgrun.SetShellResult(ev, res.Exit, res.Text)
		if res.State == "background" {
			// the call returned at launch; its status is not a result
			ev.Background = true
			ev.ExitCode = nil
			ev.IsError = false
		}
	}
	ev.Execution = &event.Execution{State: res.State, ExitSource: "mcode_hook", CallID: in.ToolUseID, ProcessID: res.TaskID, ObservedAt: time.Now(), FailureType: res.Failure}
	ev.ResultFP = fp.ResultFP(ev.ExitCode, ev.ErrFPs, ev.FailedTests)
	if ev.Reliability != nil {
		ev.Reliability.SourceMissing = res.State == "unknown"
	}
}
