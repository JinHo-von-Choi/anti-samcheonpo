package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

func copilotTestSession(t *testing.T) (*Session, *Daemon) {
	t.Helper()
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	if err := os.WriteFile(filepath.Join(root, ".samcheonpo.yml"), []byte("notify: {desktop: false}\niron_laws: {enabled: false}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := ledger.Open(filepath.Join(home, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	prices, err := cost.Load("")
	if err != nil {
		t.Fatal(err)
	}
	s, err := newSession("copilot-test", "copilot", root, "", db, prices, adapter.Observe("copilot"))
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{sessions: map[string]*Session{s.ID: s}, db: db, prices: prices, versions: map[string]string{"copilot": "1.0.101"}}
	t.Cleanup(func() {
		if _, err := s.Finalize(); err != nil {
			t.Error(err)
		}
		db.Close()
	})
	return s, d
}

// copilotSend sends one Copilot hook payload to the daemon.
func copilotSend(t *testing.T, s *Session, d *Daemon, name string, extra map[string]any) json.RawMessage {
	t.Helper()
	m := map[string]any{
		"sessionId": s.ID,
		"cwd":       s.Root,
		"toolName":  "bash",
		"toolArgs":  map[string]any{"command": "printf ok"},
	}
	for k, v := range extra {
		m[k] = v
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := d.handle(Request{Agent: "copilot", Event: name, Payload: raw})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestCopilotCapturedFixtures reads the payloads captured from the real CLI.
func TestCopilotCapturedFixtures(t *testing.T) {
	files, err := filepath.Glob("../../testdata/hooks/copilot/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("fixtures %v %v", files, err)
	}
	// later releases are captured in a folder named after the version
	later, err := filepath.Glob("../../testdata/hooks/copilot/*/*.json")
	if err != nil || len(later) == 0 {
		t.Fatalf("versioned fixtures %v %v", later, err)
	}
	files = append(files, later...)
	seen := 0
	for _, path := range files {
		if strings.HasSuffix(path, "manifest.json") {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var head struct {
			HookEventName string `json:"hook_event_name"`
			ToolName      string `json:"tool_name"`
			ToolNameC     string `json:"toolName"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			t.Fatal(err)
		}
		name := head.HookEventName
		if name == "" {
			name = copilotFixtureEvent(path)
		}
		names, ins, err := translateIn("copilot", name, raw)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(names) == 0 {
			// an event without a Claude counterpart is ignored on purpose
			continue
		}
		if names[0] == "PostToolUse" || names[0] == "PostToolUseFailure" || names[0] == "PreToolUse" {
			// the session joins a start event to its result, so the shape is
			// checked after the join
			joined := copilotJoin(ins[0], names[0])
			if reason := hookShape(names[0], joined); reason != "" {
				t.Fatalf("%s: %s", path, reason)
			}
		}
		if ins[0].ToolUseID != "" {
			t.Fatalf("%s: copilot states no call id", path)
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("no fixture was translated")
	}
}

// copilotJoin applies the session join to a translated input, the way the
// daemon does before it reads the shape.
func copilotJoin(in HookInput, event string) HookInput {
	s := &Session{}
	return s.copilotCorrelate(event, in)
}

// copilotFixtureEvent names the event a fixture file holds.
func copilotFixtureEvent(path string) string {
	base := filepath.Base(path)
	switch {
	case strings.HasPrefix(base, "postToolUse"):
		return "postToolUse"
	case strings.HasPrefix(base, "PostToolUse"):
		return "PostToolUse"
	case strings.HasPrefix(base, "preToolUse"):
		return "preToolUse"
	case strings.HasPrefix(base, "PreToolUse"):
		return "PreToolUse"
	case strings.HasPrefix(base, "sessionStart"):
		return "sessionStart"
	case strings.HasPrefix(base, "SessionStart"):
		return "SessionStart"
	case strings.HasPrefix(base, "sessionEnd"):
		return "sessionEnd"
	case strings.HasPrefix(base, "SessionEnd"):
		return "SessionEnd"
	case strings.HasPrefix(base, "agentStop"):
		return "agentStop"
	case strings.HasPrefix(base, "Stop"):
		return "Stop"
	case strings.HasPrefix(base, "userPromptSubmitted"):
		return "userPromptSubmitted"
	case strings.HasPrefix(base, "permissionRequest"):
		return "permissionRequest"
	}
	return ""
}

// TestCopilotExitCodeComesFromTheResultText covers the captured contract: the
// exit code is prose in the result text, and a result without it stays unknown.
func TestCopilotExitCodeComesFromTheResultText(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       *int
		state      string
	}{
		{"zero", "123\n<shellId: 0 completed with exit code 0>", intPtr(0), "exited"},
		{"seven", "boom\n<shellId: 1 completed with exit code 7>", intPtr(7), "failed"},
		{"unstated", "123\n", nil, "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(`{"sessionId":"cp1","toolName":"bash","toolArgs":{"command":"x"},"toolResult":{"resultType":"success","textResultForLlm":` + mustJSON(tc.text) + `}}`)
			names, ins, err := translateIn("copilot", "postToolUse", raw)
			if err != nil {
				t.Fatal(err)
			}
			e := ins[0].Copilot
			if (e.Exit == nil) != (tc.want == nil) {
				t.Fatalf("exit=%v want=%v", e.Exit, tc.want)
			}
			if tc.want != nil && *e.Exit != *tc.want {
				t.Fatalf("exit=%v want=%v", *e.Exit, *tc.want)
			}
			if e.State != tc.state {
				t.Fatalf("state=%q want=%q", e.State, tc.state)
			}
			wantEvent := "PostToolUse"
			if tc.state == "failed" {
				wantEvent = "PostToolUseFailure"
			}
			if names[0] != wantEvent {
				t.Fatalf("event=%q want=%q", names[0], wantEvent)
			}
		})
	}
}

// TestCopilotPatchBodyBecomesACommand covers the captured apply_patch shape:
// the tool input is the patch body, not an object.
func TestCopilotPatchBodyBecomesACommand(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/hooks/copilot/preToolUse-apply_patch.json")
	if err != nil {
		t.Fatal(err)
	}
	names, ins, err := translateIn("copilot", "preToolUse", raw)
	if err != nil {
		t.Fatal(err)
	}
	if names[0] != "PreToolUse" || ins[0].ToolName != "apply_patch" {
		t.Fatalf("%v %+v", names, ins[0])
	}
	var input map[string]any
	if err := json.Unmarshal(ins[0].ToolInput, &input); err != nil {
		t.Fatal(err)
	}
	body, _ := input["command"].(string)
	if !strings.Contains(body, "*** Update File:") {
		t.Fatalf("patch body lost: %v", input)
	}
	if path, _ := input["file_path"].(string); !strings.HasSuffix(path, "a.py") {
		t.Fatalf("patch path lost: %v", input)
	}
	if reason := hookShape(names[0], copilotJoin(ins[0], names[0])); reason != "" {
		t.Fatalf("shape: %s", reason)
	}
}

// TestCopilotViewPathBecomesFilePath covers the captured view shape.
func TestCopilotViewPathBecomesFilePath(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/hooks/copilot/preToolUse-view.json")
	if err != nil {
		t.Fatal(err)
	}
	_, ins, err := translateIn("copilot", "preToolUse", raw)
	if err != nil {
		t.Fatal(err)
	}
	if ins[0].ToolName != "Read" {
		t.Fatalf("tool=%q", ins[0].ToolName)
	}
	var input map[string]any
	if err := json.Unmarshal(ins[0].ToolInput, &input); err != nil {
		t.Fatal(err)
	}
	if path, _ := input["file_path"].(string); path == "" {
		t.Fatalf("path lost: %v", input)
	}
}

// TestCopilotPairsRepeatedCallsByOrder covers the captured contract: Copilot
// states no call id, so two calls with the same input stay two calls.
func TestCopilotPairsRepeatedCallsByOrder(t *testing.T) {
	s, d := copilotTestSession(t)
	command := map[string]any{"command": "printf ok"}
	for i := 0; i < 2; i++ {
		copilotSend(t, s, d, "preToolUse", map[string]any{"toolArgs": command})
	}
	for i := 0; i < 2; i++ {
		copilotSend(t, s, d, "postToolUse", map[string]any{
			"toolArgs":   command,
			"toolResult": map[string]any{"resultType": "success", "textResultForLlm": "ok\n<shellId: 0 completed with exit code 0>"},
		})
	}
	if _, err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	events, err := d.db.EventsOf(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	var tools []*event.Event
	for _, ev := range events {
		if ev.Kind == event.KindTool {
			tools = append(tools, ev)
		}
	}
	if len(tools) != 2 {
		t.Fatalf("two calls must stay two calls: %+v", tools)
	}
	s.mu.Lock()
	ids := map[string]bool{}
	for id := range s.byTool {
		ids[id] = true
	}
	s.mu.Unlock()
	if len(ids) != 2 {
		t.Fatalf("calls were merged: %v", ids)
	}
	for _, ev := range tools {
		if !ev.ResultObserved {
			t.Fatalf("result not attached: %+v", ev)
		}
		if ev.ExitCode == nil || *ev.ExitCode != 0 {
			t.Fatalf("exit=%v", ev.ExitCode)
		}
		if ev.Reliability == nil || !ev.Reliability.SourceMissing {
			t.Fatalf("the missing call id must stay visible: %+v", ev.Reliability)
		}
		found := false
		for _, d := range ev.Decisions {
			if d.Rule == "observation.collection" && d.Code == "no_call_id" {
				found = true
			}
		}
		if !found {
			t.Fatalf("the missing call id must be recorded as a gap: %+v", ev.Decisions)
		}
	}
}

// TestCopilotErrorIsKept covers the errorOccurred event: the ledger keeps the
// error so a lost session end is not read as a clean finish.
func TestCopilotErrorIsKept(t *testing.T) {
	s, d := copilotTestSession(t)
	copilotSend(t, s, d, "errorOccurred", map[string]any{
		"error":        map[string]any{"message": "model call failed", "name": "Error"},
		"errorContext": "model_call",
		"recoverable":  true,
	})
	if _, err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	events, err := d.db.EventsOf(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Kind == event.KindError && strings.Contains(ev.Summary, "model call failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("error lost: %+v", events)
	}
}

// TestCopilotIgnoresEventsWithoutACounterpart fixes the reason the events with
// no Claude counterpart are dropped.
func TestCopilotIgnoresEventsWithoutACounterpart(t *testing.T) {
	for _, name := range []string{"permissionRequest", "PermissionRequest", "notification", "Notification", "subagentStart", "subagentStop"} {
		raw := json.RawMessage(`{"sessionId":"cp1","toolName":"bash","toolArgs":{"command":"x"},"toolInput":{"command":"x"}}`)
		names, ins, err := translateIn("copilot", name, raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(names) != 0 || len(ins) != 0 {
			t.Fatalf("%s must be ignored: %v %v", name, names, ins)
		}
	}
}

// TestCopilotOutputFollowsTheContract covers the fields Copilot reads.
func TestCopilotOutputFollowsTheContract(t *testing.T) {
	deny := json.RawMessage(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"r"}}`)
	var m map[string]any
	if err := json.Unmarshal(translateOut("copilot", "preToolUse", "PreToolUse", deny, false), &m); err != nil {
		t.Fatal(err)
	}
	if m["permissionDecision"] != "deny" || m["permissionDecisionReason"] != "r" {
		t.Fatalf("deny: %v", m)
	}
	ask := json.RawMessage(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask"}}`)
	m = nil
	if out := translateOut("copilot", "preToolUse", "PreToolUse", ask, false); out != nil {
		t.Fatalf("a decision Copilot cannot use must not be printed: %s", out)
	}
	post := json.RawMessage(`{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"check the diff"}}`)
	m = nil
	if err := json.Unmarshal(translateOut("copilot", "postToolUse", "PostToolUse", post, false), &m); err != nil {
		t.Fatal(err)
	}
	if m["additionalContext"] != "check the diff" {
		t.Fatalf("context: %v", m)
	}
	stop := json.RawMessage(`{"decision":"block","reason":"checks fail"}`)
	m = nil
	if err := json.Unmarshal(translateOut("copilot", "agentStop", "Stop", stop, false), &m); err != nil {
		t.Fatal(err)
	}
	if m["decision"] != "block" || m["reason"] != "checks fail" {
		t.Fatalf("stop: %v", m)
	}
	// a finding the dialect wants to end the turn for has no turn end in
	// Copilot, so it reaches the agent as guidance on the result
	finding := json.RawMessage(`{"decision":"block","reason":"the same failure repeats"}`)
	m = nil
	if err := json.Unmarshal(translateOut("copilot", "postToolUse", "PostToolUse", finding, false), &m); err != nil {
		t.Fatal(err)
	}
	if m["additionalContext"] != "the same failure repeats" {
		t.Fatalf("a post finding must not be dropped: %v", m)
	}
}

// TestCopilotShapeMismatchIsReported covers a payload the handlers cannot
// read: the agent is reported so the doctor can show the reason.
func TestCopilotShapeMismatchIsReported(t *testing.T) {
	raw := json.RawMessage(`{"toolName":"bash","toolArgs":{"command":"x"}}`)
	names, ins, err := translateIn("copilot", "preToolUse", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || ins[0].Copilot == nil || ins[0].Copilot.Shape == "" {
		t.Fatalf("a payload without a session id must state why: %+v", ins)
	}
	if reason := hookShape(names[0], ins[0]); reason == "" {
		t.Fatal("the shape reason must reach the shape check")
	}
}

// mustJSON quotes a string as JSON.
func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
