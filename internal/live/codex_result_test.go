package live

import (
	"encoding/json"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/codex"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexExitRequiresExplicitEvidence(t *testing.T) {
	s := &Session{}
	ev := &event.Event{Tool: event.ToolShell, CallID: "cmd"}
	s.codexExit(ev)
	if ev.ExitCode != nil {
		t.Fatal("unknown exit became success")
	}
	ev.ExitCode = intPtr(1)
	ev.IsError = true
	s.codexExit(ev)
	if *ev.ExitCode != 1 || !ev.IsError {
		t.Fatal("explicit failure overwritten")
	}
	s.cx = &codex.Tracker{Exits: map[string]int{"cmd": 0}}
	s.codexExit(ev)
	if ev.ExitCode == nil || *ev.ExitCode != 0 || ev.IsError || ev.Execution.ExitSource != "rollout" {
		t.Fatalf("late completion missing: %+v", ev)
	}
}

func TestLateCodexExitUpdatesLedgerWithoutNewObservation(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.Agent = "codex"
	s.ID = "codex-late-result"
	s.parser.Session.ID = s.ID
	s.parser.Session.Agent = "codex"
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	s.cx = codex.NewTracker(path)
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, CallID: "late", TS: time.Now(), ResultObserved: true, Execution: &event.Execution{State: "running", CallID: "late"}}
	s.parser.AddEvent(ev)
	s.byTool[ev.CallID] = ev
	s.codexPosted = map[string]bool{ev.CallID: true}
	s.enqueue(ev, time.Second)
	s.mu.Lock()
	before := len(s.eng.St.Events)
	s.mu.Unlock()
	if err := os.WriteFile(path, []byte(`{"type":"turn_context","payload":{"model":"gpt-6-astra"}}
{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"late","exit_code":7}}}
`), 0600); err != nil {
		t.Fatal(err)
	}
	s.pollCodex()
	s.mu.Lock()
	if len(s.eng.St.Events) != before || s.parser.Session.Model != "gpt-6-astra" {
		t.Fatal("late result duplicated observation or lost model")
	}
	s.mu.Unlock()
	stored, err := s.db.EventsOf(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].ExitCode == nil || *stored[0].ExitCode != 7 || stored[0].Execution.ExitSource != "rollout" {
		t.Fatalf("late result not persisted: %+v", stored)
	}
	s.onPostTool(HookInput{ToolUseID: "late", ToolResponse: json.RawMessage(`"transport complete"`)}, false)
	if ev.ExitCode == nil || *ev.ExitCode != 7 {
		t.Fatal("duplicate transport erased failure")
	}
}
