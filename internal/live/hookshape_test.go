package live

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
)

func TestHookShapeAcceptsCapturedPayloads(t *testing.T) {
	for _, name := range []string{"PreToolUse-bash", "PostToolUse-bash", "PreToolUse-apply_patch"} {
		m := loadFixture(t, name)
		b, _ := json.Marshal(m)
		var in HookInput
		if err := json.Unmarshal(b, &in); err != nil {
			t.Fatal(err)
		}
		if reason := hookShape(in.HookEventName, in); reason != "" {
			t.Errorf("captured Codex %s rejected: %s", name, reason)
		}
	}
}

func TestHookShapeRejectsChangedPayloads(t *testing.T) {
	ok := HookInput{SessionID: "s", ToolName: "Bash", ToolUseID: "t1", ToolInput: json.RawMessage(`{"command":"pytest"}`)}
	if r := hookShape("PreToolUse", ok); r != "" {
		t.Fatalf("a well-formed call is accepted: %s", r)
	}
	for name, in := range map[string]HookInput{
		"renamed command field": {SessionID: "s", ToolName: "Bash", ToolUseID: "t1", ToolInput: json.RawMessage(`{"cmdline":"pytest"}`)},
		"input not an object":   {SessionID: "s", ToolName: "Bash", ToolUseID: "t1", ToolInput: json.RawMessage(`"pytest"`)},
		"no call id":            {SessionID: "s", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"pytest"}`)},
		"no tool name":          {SessionID: "s", ToolUseID: "t1", ToolInput: json.RawMessage(`{"command":"pytest"}`)},
		"write without path":    {SessionID: "s", ToolName: "Write", ToolUseID: "t1", ToolInput: json.RawMessage(`{"target":"a.py"}`)},
		"patch without body":    {SessionID: "s", ToolName: "apply_patch", ToolUseID: "t1", ToolInput: json.RawMessage(`{"diff":1}`)},
	} {
		if hookShape("PreToolUse", in) == "" {
			t.Errorf("%s is accepted", name)
		}
	}
	post := ok
	if hookShape("PostToolUse", post) == "" {
		t.Error("a result hook without a result is accepted")
	}
	unknown := HookInput{SessionID: "s", ToolName: "browser_open", ToolUseID: "t2", ToolInput: json.RawMessage(`{}`)}
	if r := hookShape("PreToolUse", unknown); r != "" {
		t.Fatalf("a new tool is observed, not a mismatch: %s", r)
	}
	if hookShape("UserPromptSubmit", HookInput{}) != "" {
		t.Fatal("only tool hooks are checked")
	}
}

// A version outside the captured range keeps the agent's capabilities; a
// payload that stops matching drops every session of that agent to
// observation, with the reason on the status line and in the agent status.
func TestVersionDoesNotGateCapabilitiesButPayloadShapeDoes(t *testing.T) {
	s, d := reliabilitySession(t)
	d.versions = map[string]string{}
	d.versionProbe = func(string) string { return "2.9.0" }
	next, err := d.session("newer", "claude", s.Root, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = next.Finalize() })
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		next.mu.Lock()
		caps := next.caps
		next.mu.Unlock()
		if caps == adapter.Profiles["claude"].Caps {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("an untested newer version lost its capabilities: %+v", caps)
		}
	}
	bad := HookInput{SessionID: "newer", Cwd: s.Root, ToolName: "Bash", ToolUseID: "t9", ToolInput: json.RawMessage(`{"cmdline":"pytest"}`)}
	if _, _, err := d.dispatch("claude", "PreToolUse", bad); err != nil {
		t.Fatal(err)
	}
	next.mu.Lock()
	caps, note := next.caps, next.shapeNote
	next.mu.Unlock()
	if caps != adapter.Observe("claude") || !strings.Contains(note, "셸 명령 없음") {
		t.Fatalf("a changed payload drops the agent to observation: %+v %q", caps, note)
	}
	if line := next.Statusline(); !strings.Contains(line, "훅 형식 불일치") {
		t.Fatalf("the status line says why: %s", line)
	}
	_, text, err := d.handle(Request{Event: "AgentStatus", Payload: json.RawMessage(`{"agent":"claude"}`)})
	if err != nil || !strings.Contains(text, "셸 명령 없음") {
		t.Fatalf("agent status carries the reason: %s %v", text, err)
	}
	later, err := d.session("later", "claude", s.Root, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = later.Finalize() })
	later.mu.Lock()
	defer later.mu.Unlock()
	if later.caps != adapter.Observe("claude") {
		t.Fatal("a new session of the same agent starts observation-only")
	}
}
