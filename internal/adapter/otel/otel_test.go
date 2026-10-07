package otel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/claude"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// TestRoundTrip exports a parsed Claude session as GenAI spans and imports it
// back: session, tool calls and token totals are preserved.
func TestRoundTrip(t *testing.T) {
	s, err := claude.ParseFile(filepath.Join("..", "..", "..", "testdata", "claude", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "trace.jsonl")
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ParseFile(p)
	if err != nil || len(got) != 1 {
		t.Fatalf("sessions %v %v", len(got), err)
	}
	g := got[0]
	if g.ID != s.ID || g.Agent != "claude" {
		t.Errorf("session identity %s %s", g.ID, g.Agent)
	}
	var tw, tg event.Usage
	var cw, cg int
	for _, ev := range s.Events {
		tw.Add(ev.Usage)
		if ev.Kind == event.KindTool {
			cw++
		}
	}
	for _, ev := range g.Events {
		tg.Add(ev.Usage)
		if ev.Kind == event.KindTool {
			cg++
		}
	}
	if cw != cg {
		t.Errorf("tool calls %d -> %d", cw, cg)
	}
	if tw.In != tg.In || tw.Out != tg.Out || tw.CacheRead != tg.CacheRead || tw.CacheWrite != tg.CacheWrite || tw.CacheWrite1h != tg.CacheWrite1h {
		t.Errorf("tokens %+v -> %+v", tw, tg)
	}
	var shell *event.Event
	for _, ev := range g.Events {
		if ev.Tool == event.ToolShell {
			shell = ev
		}
	}
	if shell == nil || shell.CmdNorm != "pytest -q" || shell.ExitCode == nil || *shell.ExitCode != 1 {
		t.Fatalf("shell span %+v", shell)
	}
}
