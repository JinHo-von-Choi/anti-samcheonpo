package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

func cursorTestSession(t *testing.T) (*Session, *Daemon) {
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
	s, err := newSession("cursor-test", "cursor", root, "", db, prices, adapter.Observe("cursor"))
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{sessions: map[string]*Session{s.ID: s}, db: db, prices: prices, versions: map[string]string{"cursor": "2026.10.19-6bbf2c4"}}
	t.Cleanup(func() {
		if _, err := s.Finalize(); err != nil {
			t.Error(err)
		}
		db.Close()
	})
	return s, d
}

func cursorSend(t *testing.T, s *Session, d *Daemon, name string, extra map[string]any) json.RawMessage {
	t.Helper()
	m := map[string]any{"conversation_id": s.ID, "workspace_roots": []string{s.Root}, "tool_use_id": "call-1", "tool_name": "Shell", "tool_input": map[string]string{"command": "printf ok"}}
	for k, v := range extra {
		m[k] = v
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := d.handle(Request{Agent: "cursor", Event: name, Payload: raw})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCursorCapturedShape(t *testing.T) {
	files, err := filepath.Glob("../../testdata/hooks/cursor/*-*.json")
	if err != nil || len(files) != 10 {
		t.Fatalf("fixtures %v %v", files, err)
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var c struct {
			Name string `json:"hook_event_name"`
			ID   string `json:"tool_use_id"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		names, ins, err := translateIn("cursor", c.Name, raw)
		if err != nil || len(names) != 1 {
			t.Fatalf("%s: %v %v", path, names, err)
		}
		if reason := hookShape(names[0], ins[0]); reason != "" {
			t.Fatalf("%s: %s", path, reason)
		}
		if c.ID != "" && ins[0].ToolUseID != c.ID {
			t.Fatalf("lost native ID: %s", path)
		}
		if c.Name == "postToolUse" && ins[0].ToolName == "Bash" && (ins[0].Cursor.Exit == nil || *ins[0].Cursor.Exit != 0) {
			t.Fatal("lost captured exitCode")
		}
	}
}

func TestCursorResultFailureUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, event, output, failure, state string
		code                                *int
		interrupted                         bool
	}{
		{"success", "postToolUse", `{"output":"ok","exitCode":0}`, "", "exited", intPtr(0), false},
		{"exit7", "postToolUse", `{"stdout":"failure","exitCode":7}`, "", "exited", intPtr(7), false},
		{"unknown", "postToolUse", `{"output":"no status"}`, "", "unknown", nil, false},
		{"timeout", "postToolUseFailure", "", "timeout", "timeout", nil, false},
		{"denied", "postToolUseFailure", "", "permission_denied", "blocked", intPtr(-1), false},
		{"interrupted", "postToolUseFailure", "", "error", "interrupted", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, d := cursorTestSession(t)
			cursorSend(t, s, d, "preToolUse", nil)
			cursorSend(t, s, d, tc.event, map[string]any{"tool_output": tc.output, "failure_type": tc.failure, "is_interrupt": tc.interrupted, "error_message": "tool failed"})
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
			if len(tools) != 1 {
				t.Fatalf("events %+v", tools)
			}
			ev := tools[0]
			if (ev.ExitCode == nil) != (tc.code == nil) || tc.code != nil && *ev.ExitCode != *tc.code {
				t.Fatalf("exit=%v want=%v", ev.ExitCode, tc.code)
			}
			if ev.Execution == nil || ev.Execution.State != tc.state {
				t.Fatalf("execution %+v", ev.Execution)
			}
		})
	}
}

func TestCursorIdentityDuplicateOrdering(t *testing.T) {
	for _, order := range []string{"ordinary", "post-first", "legacy-first", "flush-first", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			s, d := cursorTestSession(t)
			for _, id := range []string{"same-command-a", "same-command-b"} {
				pre := map[string]any{"tool_use_id": id}
				post := map[string]any{"tool_use_id": id, "tool_output": `{"output":"failure","exitCode":7}`}
				if order == "post-first" {
					cursorSend(t, s, d, "postToolUse", post)
				}
				cursorSend(t, s, d, "preToolUse", pre)
				cursorSend(t, s, d, "preToolUse", pre)
				if order == "legacy-first" {
					cursorSend(t, s, d, "beforeShellExecution", map[string]any{"tool_use_id": id, "command": "printf ok"})
					cursorSend(t, s, d, "afterShellExecution", map[string]any{"tool_use_id": id, "command": "printf ok", "output": "failure"})
				}
				if order == "flush-first" {
					s.flushTurn()
				}
				if order == "concurrent" {
					var wg sync.WaitGroup
					for i := 0; i < 3; i++ {
						wg.Add(1)
						go func() { defer wg.Done(); cursorSend(t, s, d, "postToolUse", post) }()
					}
					wg.Wait()
				} else {
					cursorSend(t, s, d, "postToolUse", post)
				}
				cursorSend(t, s, d, "postToolUse", post)
				cursorSend(t, s, d, "afterShellExecution", map[string]any{"tool_use_id": id, "command": "printf ok", "output": "failure"})
			}
			if _, err := s.Finalize(); err != nil {
				t.Fatal(err)
			}
			var count, failed int
			if err := d.db.QueryRow(`SELECT COUNT(*),SUM(CASE WHEN exit_code=7 THEN 1 ELSE 0 END) FROM event WHERE session_id=? AND tool='shell'`, s.ID).Scan(&count, &failed); err != nil {
				t.Fatal(err)
			}
			if count != 2 || failed != 2 || s.parser.Session.ToolUses != 2 || s.parser.Session.ToolUsesPaired != 2 {
				t.Fatalf("count=%d failed=%d uses=%d paired=%d", count, failed, s.parser.Session.ToolUses, s.parser.Session.ToolUsesPaired)
			}
		})
	}
}

func TestCursorMalformedShapeDoesNotBecomeSuccess(t *testing.T) {
	for _, change := range []map[string]any{
		{"tool_use_id": ""}, {"tool_input": "not an object"}, {"tool_output": "garbage"}, {"tool_output": `{"exitCode":"0"}`},
	} {
		t.Run(fmtMap(change), func(t *testing.T) {
			s, d := cursorTestSession(t)
			m := map[string]any{"tool_output": `{"exitCode":0}`}
			for k, v := range change {
				m[k] = v
			}
			cursorSend(t, s, d, "postToolUse", m)
			if d.shapeBad["cursor"] == "" || len(s.byTool) != 0 {
				t.Fatalf("malformed payload accepted: %v", s.byTool)
			}
		})
	}
}

func fmtMap(m map[string]any) string {
	for key := range m {
		return key
	}
	return "empty"
}

func TestCursorFileAfterDoesNotCaptureBaseline(t *testing.T) {
	s, d := cursorTestSession(t)
	path := filepath.Join(s.Root, "a.txt")
	if err := os.WriteFile(path, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	cursorSend(t, s, d, "afterFileEdit", map[string]any{"file_path": path, "edits": []map[string]string{{"old_string": "before", "new_string": "after"}}})
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rb.baseline) != 0 {
		t.Fatal("after-only event fabricated baseline")
	}
	if len(s.byTool) != 1 {
		t.Fatal("missing edit observation")
	}
}

func TestCursorOutputAndStopStatus(t *testing.T) {
	for _, name := range []string{"postToolUse", "postToolUseFailure"} {
		raw := translateOut("cursor", name, "PostToolUse", json.RawMessage(`{"hookSpecificOutput":{"additionalContext":"evidence"}}`), true)
		if !strings.Contains(string(raw), `"additional_context":"evidence"`) {
			t.Fatalf("%s", raw)
		}
	}
	s, d := cursorTestSession(t)
	for _, status := range []string{"aborted", "error", ""} {
		if out := cursorSend(t, s, d, "stop", map[string]any{"status": status}); len(out) > 0 {
			t.Fatalf("continued stopped session: %s", out)
		}
	}
	if out := cursorSend(t, s, d, "stop", map[string]any{"status": "completed", "loop_count": 1}); len(out) > 0 {
		t.Fatalf("continued loop: %s", out)
	}
}

func TestCursorFileBeforeAfterAndMCP(t *testing.T) {
	s, d := cursorTestSession(t)
	path := filepath.Join(s.Root, "a.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	input := map[string]string{"path": path, "old_string": "before", "new_string": "after"}
	cursorSend(t, s, d, "preToolUse", map[string]any{"tool_name": "StrReplace", "tool_input": input})
	if err := os.WriteFile(path, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	cursorSend(t, s, d, "postToolUse", map[string]any{"tool_name": "StrReplace", "tool_input": input, "tool_output": map[string]any{"success": true}})
	s.mu.Lock()
	if string(s.rb.baseline["a.txt"].data) != "before" || s.rb.untracked["a.txt"] != "" {
		t.Errorf("snapshot %+v", s.rb)
	}
	s.mu.Unlock()
	cursorSend(t, s, d, "postToolUse", map[string]any{"tool_use_id": "mcp-1", "tool_name": "MCP:lookup", "tool_input": map[string]string{"query": "fixture"}, "tool_output": `{"isError":true,"content":[]}`})
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev := s.byTool["mcp-1"]; !ev.IsError || ev.Execution.State != "failed" {
		t.Fatalf("MCP %+v", ev)
	}
}

func TestCursorMalformedEnvelope(t *testing.T) {
	_, d := cursorTestSession(t)
	_, _, err := d.handle(Request{Agent: "cursor", Event: "preToolUse", Payload: json.RawMessage(`{"tool_use_id":123}`)})
	if err == nil || d.shapeBad["cursor"] == "" {
		t.Fatal("malformed envelope not diagnosed")
	}
}

func TestCursorDeleteAndMissingResult(t *testing.T) {
	s, d := cursorTestSession(t)
	cursorSend(t, s, d, "preToolUse", map[string]any{"tool_name": "Delete", "tool_input": map[string]string{"path": "a.txt"}})
	s.mu.Lock()
	ev := s.byTool["call-1"]
	if ev.Tool != event.ToolEdit || len(ev.Deleted) != 1 || ev.Deleted[0] != "a.txt" {
		t.Errorf("delete %+v", ev)
	}
	s.mu.Unlock()
	cursorSend(t, s, d, "preToolUse", map[string]any{"tool_use_id": "missing"})
	s.flushTurn()
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev := s.byTool["missing"]; ev.ExitCode != nil || ev.Execution.State != "unknown" {
		t.Fatalf("invented blocked/success exit: %+v", ev)
	}
}

func TestCursorFailureRecoversInput(t *testing.T) {
	s, d := cursorTestSession(t)
	cursorSend(t, s, d, "preToolUse", nil)
	cursorSend(t, s, d, "postToolUseFailure", map[string]any{"tool_input": nil, "failure_type": "timeout", "error_message": "timed out"})
	if d.shapeBad["cursor"] != "" {
		t.Fatal(d.shapeBad["cursor"])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := s.byTool["call-1"]
	if ev.ExitCode != nil || !ev.IsError || ev.Execution.State != "timeout" || ev.Execution.FailureType != "timeout" {
		t.Fatalf("failure %+v", ev)
	}
}

func TestCursorSessionEndFailurePreserved(t *testing.T) {
	s, d := cursorTestSession(t)
	cursorSend(t, s, d, "preToolUse", nil)
	cursorSend(t, s, d, "sessionEnd", map[string]any{"final_status": "error"})
	d.ending.Wait()
	events, err := d.db.EventsOf(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Kind == event.KindSessionEnd {
			found = ev.Summary == "cursor session end: error"
		}
	}
	if !found {
		t.Fatal("lost error termination")
	}
}
