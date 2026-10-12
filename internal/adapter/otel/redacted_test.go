package otel

import (
	"encoding/json"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactedOTLPExcludesIdentifyingTextAndHasValidSpanIDs(t *testing.T) {
	s := &event.Session{ID: "private-session", Agent: "claude", ProjectPath: "/private/root", FirstPrompt: "private-prompt", Events: []*event.Event{{Seq: 1, Kind: event.KindTool, Tool: event.ToolShell, CmdNorm: "private-command", RawTool: "private-tool", CallID: "private-call", Usage: event.Usage{In: 100, Model: "model"}}}}
	b, err := EncodeRedacted(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "private-") || strings.Contains(string(b), "/private") {
		t.Fatal("private data exported")
	}
	var ex Export
	if err = json.Unmarshal(b, &ex); err != nil {
		t.Fatal(err)
	}
	for _, r := range ex.ResourceSpans {
		for _, scope := range r.ScopeSpans {
			for _, sp := range scope.Spans {
				if len(sp.TraceID) != 32 || len(sp.SpanID) != 16 {
					t.Fatalf("invalid ID %s %s", sp.TraceID, sp.SpanID)
				}
			}
		}
	}
	p := filepath.Join(t.TempDir(), "export.jsonl")
	if err = os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range sessions[0].Events {
		if ev.Tool == event.ToolShell && ev.ExitCode != nil {
			t.Fatal("missing shell exit invented as success")
		}
	}
	if s.Events[0].CmdNorm != "private-command" || s.ID != "private-session" {
		t.Fatal("export mutated source")
	}
}
