package live

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Antigravity CLI (agy) hooks: named hook groups in ~/.gemini/config/hooks.json,
// camelCase payloads, no session or prompt events. A turn starts with the
// first PreInvocation; tool results and the user's request are read from the
// conversation transcript, because PostToolUse carries no output or exit code.
// agy appends a shell result to the transcript only after the PostToolUse
// hooks return, so a shell PostToolUse is held and resolved at the
// conversation's next event.
type agyPayload struct {
	ConversationID string   `json:"conversationId"`
	ModelName      string   `json:"modelName"`
	WorkspacePaths []string `json:"workspacePaths"`
	TranscriptPath string   `json:"transcriptPath"`
	StepIdx        *int     `json:"stepIdx"`
	InvocationNum  *int     `json:"invocationNum"`
	ToolCall       struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	} `json:"toolCall"`
	Error             string `json:"error"`
	TerminationReason string `json:"terminationReason"`
}

func agyIn(event string, raw json.RawMessage) ([]string, []HookInput, error) {
	var a agyPayload
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, nil, err
	}
	base := HookInput{SessionID: a.ConversationID}
	if len(a.WorkspacePaths) > 0 {
		base.Cwd = a.WorkspacePaths[0]
	}
	step := -1
	if a.StepIdx != nil {
		step = *a.StepIdx
	}
	id := "agy-" + a.ConversationID + "-" + strconv.Itoa(step)
	switch event {
	case "PreInvocation":
		evs, ins := agyFlush(a.ConversationID, true)
		if a.InvocationNum != nil && *a.InvocationNum == 0 {
			b := base
			b.Prompt = agyUserRequest(a.TranscriptPath)
			return append(evs, "UserPromptSubmit"), append(ins, b), nil
		}
		// later model calls of the same turn: record the previous calls'
		// usage, then hand over pending advice
		uevs, uins := agyUsage(base, a.TranscriptPath, a.ModelName)
		return append(append(evs, uevs...), "Inject"), append(append(ins, uins...), base), nil
	case "PreToolUse":
		evs, ins := agyFlush(a.ConversationID, false)
		b := base
		b.ToolName, b.ToolUseID, b.ToolInput = agyTool(a.ToolCall.Name), id, agyInput(a.ToolCall.Name, a.ToolCall.Args)
		return append(evs, "PreToolUse"), append(ins, b), nil
	case "PostToolUse":
		b := base
		b.ToolName, b.ToolUseID, b.ToolInput = agyTool(a.ToolCall.Name), id, agyInput(a.ToolCall.Name, a.ToolCall.Args)
		if a.Error != "" {
			b.Error = a.Error
			evs, ins := agyFlush(a.ConversationID, false)
			return append(evs, "PostToolUseFailure"), append(ins, b), nil
		}
		if b.ToolName == "Bash" {
			agyHold(a.ConversationID, agyPost{in: b, transcript: a.TranscriptPath, step: step})
			return nil, nil, nil
		}
		evs, ins := agyFlush(a.ConversationID, false)
		return append(evs, "PostToolUse"), append(ins, b), nil
	case "Stop":
		evs, ins := agyFlush(a.ConversationID, true)
		uevs, uins := agyUsage(base, a.TranscriptPath, a.ModelName)
		return append(append(evs, uevs...), "Stop"), append(append(ins, uins...), base), nil
	}
	return nil, nil, nil
}

type agyPost struct {
	in         HookInput
	transcript string
	step       int
}

var agyHeld = struct {
	sync.Mutex
	m map[string][]agyPost
}{m: map[string][]agyPost{}}

func agyHold(conv string, p agyPost) {
	agyHeld.Lock()
	defer agyHeld.Unlock()
	agyHeld.m[conv] = append(agyHeld.m[conv], p)
}

// agyFlush releases held shell results whose transcript line exists, in
// order. With final set (a model call or stop, which agy sends only after
// writing the results) the rest are released with an unknown exit code.
func agyFlush(conv string, final bool) ([]string, []HookInput) {
	agyHeld.Lock()
	defer agyHeld.Unlock()
	var evs []string
	var ins []HookInput
	held := agyHeld.m[conv]
	for len(held) > 0 {
		p := held[0]
		out, code, known := agyStepResult(p.transcript, p.step)
		if !known && !final {
			break
		}
		b := p.in
		b.ToolResponse, _ = json.Marshal(map[string]string{"stdout": out})
		if known && code != 0 {
			b.Error = "Exit code " + strconv.Itoa(code) + "\n" + out
			evs = append(evs, "PostToolUseFailure")
		} else {
			evs = append(evs, "PostToolUse")
		}
		ins = append(ins, b)
		held = held[1:]
	}
	if len(held) == 0 {
		delete(agyHeld.m, conv)
	} else {
		agyHeld.m[conv] = held
	}
	return evs, ins
}

// agyInput maps Antigravity tool arguments to the Claude tool input fields.
func agyInput(name string, args map[string]any) json.RawMessage {
	s := func(k string) string { v, _ := args[k].(string); return v }
	var m map[string]any
	switch strings.ToLower(name) {
	case "run_command", "run_terminal_command":
		m = map[string]any{"command": s("CommandLine"), "workdir": s("Cwd")}
	case "write_to_file", "create_file":
		m = map[string]any{"file_path": s("TargetFile"), "content": s("CodeContent")}
	case "view_file", "read_file":
		p := s("AbsolutePath")
		if p == "" {
			p = s("TargetFile")
		}
		m = map[string]any{"file_path": p}
	case "replace_file_content", "multi_replace_file_content", "edit_file":
		// the replacement chunks are not reproduced exactly; the path is enough
		// for scope and repeat decisions, and rollback treats ownership as unknown
		m = map[string]any{"file_path": s("TargetFile")}
	default:
		b, _ := json.Marshal(args)
		return b
	}
	b, _ := json.Marshal(m)
	return b
}

var (
	agyExitRe  = regexp.MustCompile(`(?s)exited with code (-?\d+)\.?\s*(.*)$`)
	agyLabelRe = regexp.MustCompile(`(?m)^(?:Output|Stdout|Stderr):[ \t]*\n?`)
)

// agyStepResult reads a tool result from the transcript line whose
// step_index equals the hook's stepIdx.
func agyStepResult(path string, step int) (out string, code int, known bool) {
	if path == "" || step < 0 {
		return "", 0, false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l struct {
			Step    json.Number `json:"step_index"` // a string in agy 1.3
			Content string      `json:"content"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Step.String() != strconv.Itoa(step) {
			continue
		}
		if m := agyExitRe.FindStringSubmatch(l.Content); m != nil {
			code, _ = strconv.Atoi(m[1])
			out = agyLabelRe.ReplaceAllString(strings.ReplaceAll(m[2], "\r", ""), "")
			return strings.TrimSpace(out), code, true
		}
		return strings.TrimSpace(l.Content), 0, false
	}
	return "", 0, false
}

// agyUsage turns the token counts of the transcript's model responses into
// Usage events. The daemon keeps per-message totals, so reporting a
// response again adds nothing.
func agyUsage(base HookInput, path, model string) ([]string, []HookInput) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	var evs []string
	var ins []HookInput
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l struct {
			Step      json.Number `json:"step_index"`
			Type      string      `json:"type"`
			In        json.Number `json:"input_tokens"`
			Out       json.Number `json:"output_tokens"`
			CacheRead json.Number `json:"cache_read_tokens"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Type != "PLANNER_RESPONSE" {
			continue
		}
		in, _ := l.In.Int64()
		out, _ := l.Out.Int64()
		cache, _ := l.CacheRead.Int64()
		if in+out == 0 {
			continue
		}
		// Gemini counts cached prompt tokens inside the input count
		if cache <= in {
			in -= cache
		}
		u, _ := json.Marshal(map[string]int64{"input_tokens": in, "output_tokens": out, "cache_read_input_tokens": cache})
		b := base
		b.MessageID, b.Model = "agy-"+base.SessionID+"-"+l.Step.String(), model
		if json.Unmarshal([]byte(`{"usage":`+string(u)+`}`), &b) != nil {
			continue
		}
		evs, ins = append(evs, "Usage"), append(ins, b)
	}
	return evs, ins
}

var agyRequestRe = regexp.MustCompile(`(?s)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)

// agyUserRequest returns the latest user request in the transcript.
func agyUserRequest(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	last := ""
	for sc.Scan() {
		var l struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Type != "USER_INPUT" {
			continue
		}
		last = l.Content
		if m := agyRequestRe.FindStringSubmatch(l.Content); m != nil {
			last = m[1]
		}
	}
	return last
}

// agyOut maps a Claude-dialect answer to agy's: nothing printed means the
// call proceeds through agy's own permission flow ("allow" would skip it).
func agyOut(event, claudeEvent string, m map[string]any) map[string]any {
	hs, _ := m["hookSpecificOutput"].(map[string]any)
	r := map[string]any{}
	switch claudeEvent {
	case "PreToolUse":
		if hs != nil && hs["permissionDecision"] == "deny" {
			r["decision"] = "deny"
			r["reason"] = hs["permissionDecisionReason"]
		}
	case "UserPromptSubmit", "Inject":
		if hs != nil {
			if c, ok := hs["additionalContext"].(string); ok && c != "" {
				r["injectSteps"] = []map[string]string{{"ephemeralMessage": c}}
			}
		}
	case "Stop":
		if m["decision"] == "block" {
			r["decision"] = "continue"
			r["reason"] = m["reason"]
		}
	}
	return r
}
