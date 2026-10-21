package codex

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestExecutionResponseMetadata(t *testing.T) {
	for _, tc := range []struct {
		raw, state string
		exit       *int
	}{
		{`"hello"`, "unknown", nil},
		{`"Process running with session ID 12345"`, "running", nil},
		{`{"exit_code":null,"session_id":12345}`, "running", nil},
		{`{"output":"Exit code: 0"}`, "unknown", nil},
		{`{"metadata":{"exit_code":1}}`, "exited", intValue(1)},
		{`"Process exited with code 0"`, "exited", intValue(0)},
		{`"Command interrupted"`, "interrupted", nil},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			ev := &event.Event{Tool: event.ToolShell, ExitCode: intValue(0)}
			ApplyResponse(ev, json.RawMessage(tc.raw))
			if ev.Execution.State != tc.state || (ev.ExitCode == nil) != (tc.exit == nil) || tc.exit != nil && *ev.ExitCode != *tc.exit {
				t.Fatalf("result %+v exit %v", ev.Execution, ev.ExitCode)
			}
		})
	}
}

func intValue(n int) *int { return &n }

func TestCapturedCodex0163CodeMode(t *testing.T) {
	s, err := ParseFile(td("captured-0.163.0.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var usage event.Usage
	var codes []int
	for _, ev := range s.Events {
		usage.Add(ev.Usage)
		if ev.Tool == event.ToolShell {
			if ev.ExitCode == nil || ev.Execution == nil || ev.Execution.State != "exited" {
				t.Fatalf("lost observed exit: %+v", ev)
			}
			codes = append(codes, *ev.ExitCode)
		}
	}
	if len(codes) != 5 || codes[3] != 7 || s.ToolUses != 5 || usage.Total() != 169674 || usage.Model != "gpt-6-astra" {
		t.Fatalf("nested execution/usage mismatch: codes=%v tools=%d usage=%+v", codes, s.ToolUses, usage)
	}
}

func TestUnifiedExecPollUpdatesOriginalCommand(t *testing.T) {
	p := &Parser{sess: &event.Session{}, byCall: map[string]*event.Event{}}
	ts := time.Unix(100, 0)
	p.responseItem(map[string]any{"name": "exec_command", "call_id": "start", "arguments": `{"cmd":"go test ./..."}`}, "function_call", ts, "test")
	p.responseItem(map[string]any{"call_id": "start", "output": "Process running with session ID 12345"}, "function_call_output", ts, "test")
	ev := p.sess.Events[0]
	if ev.ExitCode != nil || !ev.Background {
		t.Fatalf("running process marked complete: %+v", ev)
	}
	for i := 0; i < 2; i++ {
		p.responseItem(map[string]any{"name": "write_stdin", "call_id": "poll", "arguments": `{"session_id":12345,"chars":""}`}, "function_call", ts, "test")
		out := "Process running with session ID 12345"
		if i == 1 {
			out = "Process exited with code 1\nFAIL"
		}
		p.responseItem(map[string]any{"call_id": "poll", "output": out}, "function_call_output", ts, "test")
	}
	if len(p.sess.Events) != 1 || p.sess.ToolUses != 1 || p.sess.ToolUsesPaired != 1 || ev.ExitCode == nil || *ev.ExitCode != 1 || ev.Background || ev.Execution.CallID != "start" {
		t.Fatalf("poll duplicated command or lost failure: events=%d uses=%d paired=%d event=%+v", len(p.sess.Events), p.sess.ToolUses, p.sess.ToolUsesPaired, ev)
	}
}
