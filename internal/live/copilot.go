package live

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testout"
)

// copilotEvidence is derived from the Copilot CLI hook envelope, never from
// output prose. Every field records what the agent actually stated.
type copilotEvidence struct {
	Shape       string // why the payload does not match what the handlers read
	Exit        *int   // shell exit code when Copilot states one
	State       string // completed | exited | failed | blocked | unknown
	Rank        int    // 1 envelope, 2 call, 3 result
	Failure     string
	EndStatus   string // sessionEnd reason
	Unpaired    bool   // the result had no start event to attach to
	ErrorCtx    string // errorOccurred context
	Recoverable bool
	NoCallID    bool // Copilot states no tool call id
}

// copilotField returns the first key that is present and not null. Copilot
// sends the native camelCase payload for the runtime event names and the VS
// Code compatible snake_case payload for the PascalCase aliases, so every
// field is read through both spellings.
func copilotField(m map[string]json.RawMessage, keys ...string) (json.RawMessage, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok && len(v) > 0 && string(v) != "null" {
			return v, true
		}
	}
	return nil, false
}

// copilotTool maps a Copilot runtime tool name to the Claude tool name the
// handlers read. The PascalCase payload already states the Claude name.
// apply_patch keeps its own name because the parser reads the patch body from
// its input.
func copilotTool(name string) string {
	switch strings.ToLower(name) {
	case "bash", "powershell":
		return "Bash"
	case "view":
		return "Read"
	case "create":
		return "Write"
	case "edit", "str_replace_editor", "str_replace_based_edit_tool":
		return "Edit"
	case "grep", "rg":
		return "Grep"
	case "glob":
		return "Glob"
	case "web_fetch":
		return "WebFetch"
	case "web_search":
		return "WebSearch"
	case "ask_user":
		return "AskUserQuestion"
	case "update_todo":
		return "TodoWrite"
	case "task":
		return "Agent"
	}
	return name
}

// copilotPatchPath finds the file an apply_patch body touches.
func copilotPatchPath(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
			if strings.HasPrefix(line, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(line, prefix))
			}
		}
	}
	return ""
}

// copilotInput normalizes a Copilot tool input to the field names the parser
// reads. Copilot states the patch body as a bare string and names the target
// path "path" instead of "file_path".
func copilotInput(name string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var body string
	if json.Unmarshal(raw, &body) == nil {
		out := map[string]any{"command": body}
		if p := copilotPatchPath(body); p != "" {
			out["file_path"] = p
		}
		b, _ := json.Marshal(out)
		return b
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil || input == nil {
		return raw
	}
	if _, ok := input["file_path"]; !ok {
		if p, ok := input["path"]; ok {
			input["file_path"] = p
		}
	}
	b, _ := json.Marshal(input)
	return b
}

// copilotExitRe reads the shell exit code Copilot states in the result text.
// Copilot has no exit code field, so a missing marker leaves the exit code
// unknown rather than assumed successful.
var copilotExitRe = regexp.MustCompile(`completed with exit code (\d+)`)

// copilotExit finds the shell exit code in a result text.
func copilotExit(text string) *int {
	m := copilotExitRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	code, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}
	return &code
}

// copilotResult reads the model facing text of a tool result.
func copilotResult(raw json.RawMessage) (string, string, bool) {
	if len(raw) == 0 {
		return "", "", false
	}
	var body struct {
		ResultType  string `json:"result_type"`
		ResultTypeC string `json:"resultType"`
		Text        string `json:"text_result_for_llm"`
		TextC       string `json:"textResultForLlm"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return "", "", false
	}
	kind := body.ResultType
	if kind == "" {
		kind = body.ResultTypeC
	}
	text := body.Text
	if text == "" {
		text = body.TextC
	}
	return kind, text, true
}

// copilotIn maps a GitHub Copilot CLI hook payload to the Claude dialect.
// Copilot sends no tool call id, so a start event and its result are joined
// later by the session, which keeps the pairing order based.
func copilotIn(name string, raw json.RawMessage) ([]string, []HookInput, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil, err
	}
	e := &copilotEvidence{State: "unknown", Rank: 1, NoCallID: true}
	in := HookInput{Copilot: e}
	str := func(keys ...string) string {
		v, ok := copilotField(m, keys...)
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			return ""
		}
		return s
	}
	boolean := func(keys ...string) bool {
		v, ok := copilotField(m, keys...)
		if !ok {
			return false
		}
		var b bool
		if json.Unmarshal(v, &b) != nil {
			return false
		}
		return b
	}
	in.SessionID = str("session_id", "sessionId")
	in.Cwd = str("cwd")
	in.TranscriptPath = str("transcript_path", "transcriptPath")
	in.Prompt = str("prompt")
	in.Source = str("source")
	in.StopHookActive = boolean("stop_hook_active")
	in.Trigger = str("trigger")

	claudeEvent := ""
	switch name {
	case "sessionStart", "SessionStart":
		claudeEvent = "SessionStart"
	case "sessionEnd", "SessionEnd":
		claudeEvent = "SessionEnd"
		e.EndStatus = str("reason")
	case "userPromptSubmitted", "UserPromptSubmitted":
		claudeEvent = "UserPromptSubmit"
	case "preToolUse", "PreToolUse":
		claudeEvent = "PreToolUse"
		e.Rank = 2
	case "postToolUse", "PostToolUse":
		claudeEvent = "PostToolUse"
		e.Rank = 3
	case "postToolUseFailure", "PostToolUseFailure":
		claudeEvent = "PostToolUseFailure"
		e.Rank = 3
		e.State = "failed"
	case "agentStop", "Stop":
		claudeEvent = "Stop"
	case "errorOccurred", "ErrorOccurred":
		claudeEvent = "ErrorOccurred"
		e.ErrorCtx = str("error_context", "errorContext")
		e.Recoverable = boolean("recoverable")
	case "preCompact", "PreCompact":
		claudeEvent = "PreCompact"
	default:
		// permissionRequest, notification, subagentStart and subagentStop have
		// no Claude dialect counterpart the handlers read. Ignoring them is
		// deliberate: the preToolUse event already carries the permission
		// decision and the subagent events carry no call of their own.
		return nil, nil, nil
	}

	if tool, ok := copilotField(m, "tool_name", "toolName"); ok {
		var name string
		if json.Unmarshal(tool, &name) == nil {
			in.ToolName = copilotTool(name)
		}
	}
	if args, ok := copilotField(m, "tool_input", "toolArgs", "toolInput"); ok {
		in.ToolInput = copilotInput(in.ToolName, args)
	}
	if result, ok := copilotField(m, "tool_result", "toolResult"); ok {
		kind, text, known := copilotResult(result)
		if known {
			e.State = "completed"
			if kind == "failure" {
				e.State = "failed"
			}
			if in.ToolName == "Bash" {
				if code := copilotExit(text); code != nil {
					e.Exit = code
					e.State = "exited"
					if *code != 0 {
						e.State = "failed"
						e.Failure = "nonzero_exit"
					}
				}
			}
			out, _ := json.Marshal(map[string]any{"stdout": text})
			in.ToolResponse = out
		}
	}
	if msg := str("error"); msg != "" {
		in.Error = msg
		e.State = "failed"
		if e.Failure == "" {
			e.Failure = "tool_error"
		}
	}
	if errObj, ok := copilotField(m, "error"); ok {
		var body struct {
			Message string `json:"message"`
			Name    string `json:"name"`
		}
		if json.Unmarshal(errObj, &body) == nil && body.Message != "" {
			in.Error = body.Message
			e.State = "failed"
			if e.Failure == "" {
				e.Failure = "agent_error"
			}
		}
	}
	if claudeEvent == "PostToolUse" && e.State == "failed" {
		// A non zero shell exit still arrives as a successful result, so the
		// failure is stated in the result and not in an error field.
		claudeEvent = "PostToolUseFailure"
	}
	if in.SessionID == "" {
		e.Shape = "Copilot 훅에 세션 ID가 없다"
	}
	return []string{claudeEvent}, []HookInput{in}, nil
}

// copilotOut maps a Claude dialect decision to the Copilot hook output.
// Copilot ignores an output it does not understand, so only the fields its
// contract names are written: a permission decision without a reason when it
// denies, and a stop decision with the prompt it forces.
func copilotOut(name string, m, hs map[string]any) json.RawMessage {
	r := map[string]any{}
	switch name {
	case "preToolUse", "PreToolUse":
		if hs != nil {
			if d, ok := hs["permissionDecision"].(string); ok && (d == "allow" || d == "deny") {
				r["permissionDecision"] = d
				if reason, ok := hs["permissionDecisionReason"].(string); ok && reason != "" {
					r["permissionDecisionReason"] = reason
				}
			}
		}
	case "postToolUse", "PostToolUse", "postToolUseFailure", "PostToolUseFailure":
		if hs != nil {
			if c, ok := hs["additionalContext"].(string); ok && c != "" {
				r["additionalContext"] = c
			}
		}
		if _, ok := r["additionalContext"]; !ok {
			// a finding the dialect wants to end the turn for reaches Copilot
			// as guidance on the result, because Copilot has no turn end here
			if reason, ok := m["reason"].(string); ok && reason != "" {
				r["additionalContext"] = reason
			}
		}
	case "userPromptSubmitted", "UserPromptSubmitted":
		if hs != nil {
			if c, ok := hs["additionalContext"].(string); ok && c != "" {
				r["additionalContext"] = c
			}
		}
	case "agentStop", "Stop":
		if d, ok := m["decision"].(string); ok && (d == "block" || d == "allow") {
			r["decision"] = d
			if reason, ok := m["reason"].(string); ok && reason != "" {
				r["reason"] = reason
			}
		}
	}
	return marshalOrNil(r)
}

// applyCopilotResult records the result Copilot stated. The exit code is
// left unknown when Copilot states none, and the call id is marked missing
// because Copilot sends none.
func applyCopilotResult(ev *event.Event, in HookInput, failure bool) {
	e := in.Copilot
	if e == nil {
		return
	}
	var body struct {
		Stdout string `json:"stdout"`
	}
	if json.Unmarshal(in.ToolResponse, &body) == nil && body.Stdout != "" {
		text := body.Stdout
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
		if e.Exit != nil {
			ev.ExitCode = e.Exit
			ev.IsError = *e.Exit != 0
		}
	}
	ev.Execution = &event.Execution{State: e.State, ExitSource: "copilot_hook", CallID: in.ToolUseID, ObservedAt: time.Now(), FailureType: e.Failure}
	ev.ResultFP = fp.ResultFP(ev.ExitCode, ev.ErrFPs, ev.FailedTests)
	if ev.Reliability != nil {
		ev.Reliability.SourceMissing = e.NoCallID || e.State == "unknown"
	}
	gap := func(code string) {
		ev.Decisions = append(ev.Decisions, event.Decision{Rule: "observation.collection", Code: code, Evidence: []int64{ev.Seq}})
	}
	if e.NoCallID {
		// the agent states no call id, so the pair is joined by order
		gap("no_call_id")
	}
	if e.Unpaired {
		// the result had no start event to attach to
		gap("start_not_seen")
	}
}

// copilotEndStatus maps the session end reason Copilot states.
func copilotEndStatus(status string) string {
	switch status {
	case "complete", "error", "timeout", "cancelled", "interrupted":
		return status
	}
	return "unknown"
}

// copilotPending is a start event waiting for its result.
type copilotPending struct {
	id   string
	tool string
}

// copilotPendingLimit bounds the start events kept for a join.
const copilotPendingLimit = 64

// copilotCorrelate joins a start event to its result. Copilot states no tool
// call id, so the oldest waiting start of the same tool takes the result. A
// result without a waiting start keeps its own id and is marked unpaired, so
// two calls with the same input are never merged.
func (s *Session) copilotCorrelate(name string, in HookInput) HookInput {
	s.copilotMu.Lock()
	defer s.copilotMu.Unlock()
	switch name {
	case "PreToolUse":
		s.copilotSeq++
		id := "cp-" + strconv.Itoa(s.copilotSeq)
		s.copilotPre = append(s.copilotPre, copilotPending{id: id, tool: in.ToolName})
		if len(s.copilotPre) > copilotPendingLimit {
			s.copilotPre = s.copilotPre[len(s.copilotPre)-copilotPendingLimit:]
		}
		in.ToolUseID = id
	case "PostToolUse", "PostToolUseFailure":
		for i, p := range s.copilotPre {
			if p.tool != in.ToolName {
				continue
			}
			s.copilotPre = append(s.copilotPre[:i], s.copilotPre[i+1:]...)
			in.ToolUseID = p.id
			return in
		}
		s.copilotSeq++
		in.ToolUseID = "cp-" + strconv.Itoa(s.copilotSeq)
		if in.Copilot != nil {
			in.Copilot.Unpaired = true
		}
	}
	return in
}
