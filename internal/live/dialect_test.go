package live

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
)

// Payload shapes follow the published hook references (Copilot CLI PascalCase
// mode, Cursor hooks); they are not captured from running agents.

func TestCopilotTranslation(t *testing.T) {
	raw := json.RawMessage(`{"session_id":"cp1","cwd":"/w","tool_name":"Bash","tool_input":{"command":"npm test"},"tool_result":{"text_result_for_llm":"1 failed"}}`)
	evs, ins, err := translateIn("copilot", "PostToolUse", raw)
	if err != nil || len(evs) != 1 || ins[0].ToolName != "Bash" || !strings.Contains(string(ins[0].ToolResponse), "1 failed") || ins[0].ToolUseID == "" {
		t.Fatalf("%v %+v %v", evs, ins, err)
	}
	out := translateOut("copilot", "PreToolUse", "PreToolUse",
		json.RawMessage(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"r"}}`), false)
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if m["permissionDecision"] != "deny" || m["permissionDecisionReason"] != "r" {
		t.Errorf("copilot decisions are top-level: %s", out)
	}
}

func TestCursorTranslation(t *testing.T) {
	before := json.RawMessage(`{"conversation_id":"cv1","generation_id":"g1","command":"pytest","cwd":"/w","workspace_roots":["/w"]}`)
	evs, ins, _ := translateIn("cursor", "beforeShellExecution", before)
	if evs[0] != "PreToolUse" || ins[0].ToolName != "Bash" || ins[0].SessionID != "cv1" {
		t.Fatalf("%v %+v", evs, ins)
	}
	after := json.RawMessage(`{"conversation_id":"cv1","generation_id":"g1","command":"pytest","cwd":"/w","output":"1 failed","exit_code":1}`)
	evs2, ins2, _ := translateIn("cursor", "afterShellExecution", after)
	if evs2[0] != "PostToolUseFailure" || ins2[0].ToolUseID != ins[0].ToolUseID || !strings.HasPrefix(ins2[0].Error, "Exit code 1") {
		t.Fatalf("before and after pair by id: %v %+v", evs2, ins2)
	}
	edit := json.RawMessage(`{"conversation_id":"cv1","generation_id":"g2","file_path":"/w/a.py","edits":[{"old_string":"a","new_string":"b"}]}`)
	evs3, _, _ := translateIn("cursor", "afterFileEdit", edit)
	if len(evs3) != 2 || evs3[0] != "PreToolUse" || evs3[1] != "PostToolUse" {
		t.Fatalf("file edits become a pre and a post event: %v", evs3)
	}
	deny := json.RawMessage(`{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"same test again"}}`)
	var m map[string]any
	_ = json.Unmarshal(translateOut("cursor", "beforeShellExecution", "PreToolUse", deny, true), &m)
	if m["permission"] != "deny" || m["agent_message"] != "same test again" {
		t.Errorf("cursor deny %v", m)
	}
	_ = json.Unmarshal(translateOut("cursor", "beforeShellExecution", "PreToolUse", nil, true), &m)
	if m["permission"] != "allow" {
		t.Errorf("cursor permission hooks always answer: %v", m)
	}
	_ = json.Unmarshal(translateOut("cursor", "stop", "Stop", json.RawMessage(`{"decision":"block","reason":"checks fail"}`), true), &m)
	if m["followup_message"] != "checks fail" {
		t.Errorf("a stop block becomes a follow-up message: %v", m)
	}
}

func TestAgyStaysInPermissionFlow(t *testing.T) {
	// an explicit "allow" would skip agy's own permission prompt
	if out := translateOut("agy", "PreToolUse", "PreToolUse", nil, false); out != nil {
		t.Fatalf("agy pass prints nothing: %s", out)
	}
	deny := json.RawMessage(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"r"}}`)
	if got := string(translateOut("agy", "PreToolUse", "PreToolUse", deny, false)); got != `{"decision":"deny","reason":"r"}` {
		t.Fatalf("agy deny: %s", got)
	}
	stop := json.RawMessage(`{"decision":"block","reason":"r"}`)
	if got := string(translateOut("agy", "Stop", "Stop", stop, false)); got != `{"decision":"continue","reason":"r"}` {
		t.Fatalf("agy stop hold: %s", got)
	}
}

func TestAgyTranscriptResult(t *testing.T) {
	tr := filepath.Join("..", "..", "testdata", "hooks", "agy", "transcript_full.jsonl")
	if out, code, ok := agyStepResult(tr, 2); !ok || code != 0 || out != "EMPTYOUT" {
		t.Fatalf("step 2: %q %d %v", out, code, ok)
	}
	if out, code, ok := agyStepResult(tr, 6); !ok || code != 1 || out != "" {
		t.Fatalf("step 6 (false): %q %d %v", out, code, ok)
	}
	if _, _, ok := agyStepResult(tr, 99); ok {
		t.Fatal("a missing step is unknown")
	}
	if got := agyUserRequest(tr); !strings.HasPrefix(got, "Run these shell commands") {
		t.Fatalf("user request: %q", got)
	}
	// the shell result is written after PostToolUse returns: held until the next event
	post, _ := os.ReadFile(filepath.Join("..", "..", "testdata", "hooks", "agy", "PostToolUse-step6.json"))
	var m map[string]any
	_ = json.Unmarshal(post, &m)
	m["conversationId"], m["transcriptPath"] = "held-1", tr
	post, _ = json.Marshal(m)
	if evs, _, _ := translateIn("agy", "PostToolUse", post); len(evs) != 0 {
		t.Fatalf("shell result is held: %v", evs)
	}
	inv, _ := json.Marshal(map[string]any{"conversationId": "held-1", "invocationNum": 3, "transcriptPath": tr})
	evs, ins, _ := translateIn("agy", "PreInvocation", inv)
	if len(evs) < 2 || evs[0] != "PostToolUseFailure" || !strings.HasPrefix(ins[0].Error, "Exit code 1") || evs[len(evs)-1] != "Inject" {
		t.Fatalf("held result is released first with its exit code: %v", evs)
	}
}

func TestClientExplicitAllowWhenDaemonDown(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", base)
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(base, "h"))
	t.Setenv("SAMCHEONPO_NO_SPAWN", "1")
	_ = os.MkdirAll(base, 0o755)
	cases := map[[2]string]string{
		{"agy", "PreToolUse"}:              "",
		{"cursor", "beforeShellExecution"}: `"permission":"allow"`,
		{"cursor", "beforeSubmitPrompt"}:   `"continue":true`,
		{"claude", "PreToolUse"}:           "",
	}
	for k, want := range cases {
		var out bytes.Buffer
		hookclient.MainAgent(k[1], k[0], strings.NewReader(`{"session_id":"x"}`), &out)
		if want == "" && out.Len() != 0 || want != "" && !strings.Contains(out.String(), want) {
			t.Errorf("%v: %q", k, out.String())
		}
	}
}
