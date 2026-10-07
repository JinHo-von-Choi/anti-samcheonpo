package codex

import (
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func td(p string) string { return filepath.Join("..", "..", "..", "testdata", "codex", p) }

func TestItemMode(t *testing.T) {
	s, err := ParseFile(td("items.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "cx1" || s.Model != "gpt-5.5" || s.ProjectPath != "/w/cx" {
		t.Fatalf("meta %+v", s)
	}
	if s.QuotaUsedPct != 12.5 || s.QuotaWindowMin != 300 {
		t.Errorf("quota %v %v", s.QuotaUsedPct, s.QuotaWindowMin)
	}
	var total event.Usage
	var shell, patch *event.Event
	compacts := 0
	for _, ev := range s.Events {
		total.Add(ev.Usage)
		if ev.Tool == event.ToolShell {
			shell = ev
		}
		if ev.RawTool == "apply_patch" {
			patch = ev
		}
		if ev.Kind == event.KindCompact {
			compacts++
		}
	}
	// token deltas must sum to the last total (input includes cached input)
	if total.In+total.CacheRead+total.Out != 2700 || total.CacheRead != 1900 || total.Out != 100 {
		t.Errorf("token sum %+v", total)
	}
	if shell == nil || shell.CmdNorm != "pytest -q" || *shell.ExitCode != 1 || len(shell.FailedTests) != 1 {
		t.Fatalf("shell %+v", shell)
	}
	if patch == nil || len(patch.Paths) != 2 || len(patch.Created) != 1 || patch.Created[0] != "lib/new.py" || patch.AddedLines["lib/x.py"] != 1 {
		t.Fatalf("patch %+v", patch)
	}
	if compacts != 1 {
		t.Errorf("compactions %d (top-level compacted must not double count in item mode)", compacts)
	}
	// the response_item exec wrapper is ignored in item mode
	if s.ToolUses != 2 {
		t.Errorf("tool uses %d", s.ToolUses)
	}
}

func TestLegacyMode(t *testing.T) {
	s, err := ParseFile(td("legacy.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var shell, patch *event.Event
	var total event.Usage
	for _, ev := range s.Events {
		total.Add(ev.Usage)
		switch ev.RawTool {
		case "shell":
			shell = ev
		case "apply_patch":
			patch = ev
		}
	}
	if shell == nil || shell.CmdNorm != "go test ./..." || shell.ExitCode == nil || *shell.ExitCode != 1 || shell.FailedTests[0] != "TestX" {
		t.Fatalf("shell %+v", shell)
	}
	if patch == nil || patch.WriteHashes["y.go"] == "" || patch.Created[0] != "y.go" || patch.RemovedLines["x.go"] != 1 {
		t.Fatalf("patch %+v", patch)
	}
	if s.ToolUses != 2 || s.ToolUsesPaired != 2 {
		t.Errorf("call_id pairing %d/%d", s.ToolUsesPaired, s.ToolUses)
	}
	if total.In+total.CacheRead+total.Out != 540 {
		t.Errorf("token sum %+v", total)
	}
}
