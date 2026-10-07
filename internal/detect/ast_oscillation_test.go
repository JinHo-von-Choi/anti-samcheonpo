package detect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

func TestSemanticOscillation_DetectsA_B_APingPong(t *testing.T) {
	detector := NewSemanticOscillationDetector()

	codeA1 := "func add(a, b int) int { return a + b }"
	codeB := "func add(x, y int) int { return x + y + 0 }"
	codeA2 := "func add(foo, bar int) int { /* comment */ return foo + bar }"

	detector.RecordFailure("math.go", codeA1)
	detector.RecordFailure("math.go", codeB)

	isOscillating, prevIndex := detector.CheckOscillation("math.go", codeA2)
	if !isOscillating {
		t.Fatalf("expected oscillation detected for semantic equivalent codeA2, got false")
	}
	if prevIndex != 0 {
		t.Fatalf("expected previous match index 0, got %d", prevIndex)
	}
}

func TestSemanticOscillation_IgnoresUnchangedRerun(t *testing.T) {
	detector := NewSemanticOscillationDetector()

	code := "func add(a, b int) int { return a + b }"
	detector.RecordFailure("math.go", code)

	if ok, _ := detector.CheckOscillation("math.go", code); ok {
		t.Fatalf("expected no oscillation for byte-identical rerun")
	}
}

func TestSemanticOscillation_SeparatePathsAndBufferLimit(t *testing.T) {
	detector := NewSemanticOscillationDetector()

	other := "func other() {}"
	detector.RecordFailure("a.go", other)
	if ok, _ := detector.CheckOscillation("b.go", other); ok {
		t.Fatalf("expected no match across different paths")
	}

	base := "func add(a, b int) int { return a + b }"
	for i := 0; i < 6; i++ {
		detector.RecordFailure("a.go", base)
	}
	// six identical records over a five-slot buffer keep the newest five
	for i := 0; i < 6; i++ {
		alt := "func add(a, b int) int { return a + b + " + string(rune('0'+i)) + " }"
		if ok, idx := detector.CheckOscillation("a.go", alt); ok || idx != -1 {
			t.Fatalf("expected no match inside an identical-record buffer, got ok=%v idx=%d", ok, idx)
		}
	}
}

func TestSemanticOscillation_DifferentSemanticsNotBlocked(t *testing.T) {
	detector := NewSemanticOscillationDetector()

	detector.RecordFailure("math.go", "func add(a, b int) int { return a + b }")

	if ok, _ := detector.CheckOscillation("math.go", "func sub(a, b int) int { return a - b }"); ok {
		t.Fatalf("expected no oscillation for a genuinely different body")
	}
}

func TestPreCheckBlocksSemanticPingPongOnVerifyRerun(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	cfg.Detectors.Overrides = map[string]int{}
	e := NewEngine(cfg, nil, false, "audit", tmp, "s", "")
	path := "math.go"
	writeFile := func(t *testing.T, code string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(tmp, path), []byte(code), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	produce := func() {
		ev := &event.Event{Kind: event.KindTool, Tool: event.ToolWrite, Category: event.CatProduce,
			Paths: []string{path}, WriteHashes: map[string]string{path: "h"},
			AddedLines: map[string]int{path: 1}}
		ev.Seq = int64(len(e.St.Events))
		e.Observe(ev)
	}
	failVerify := func() {
		exit := 1
		norm := "go test ./..."
		ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: norm, CmdNorm: norm,
			CmdFP: fp.CmdFP(norm), Category: event.CatVerify, ExitCode: &exit, FailedTests: []string{"TestAdd"}}
		ev.ResultFP = fp.ResultFP(&exit, nil, ev.FailedTests)
		ev.Seq = int64(len(e.St.Events))
		e.Observe(ev)
	}

	writeFile(t, "func add(a, b int) int { return a + b }")
	produce()
	failVerify()

	writeFile(t, "func add(x, y int) int { return x + y + 0 }")
	produce()
	failVerify()

	writeFile(t, "func add(foo, bar int) int { /* comment */ return foo + bar }")
	produce()

	exit := 1
	norm := "go test ./..."
	rerun := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: norm, CmdNorm: norm,
		CmdFP: fp.CmdFP(norm), Category: event.CatVerify, ExitCode: &exit}
	deny, sig := e.PreCheck(rerun)
	if !deny {
		t.Fatalf("expected semantic ping-pong deny on verify rerun")
	}
	if sig == nil || sig.Rule != "s2.semantic_oscillation" {
		t.Fatalf("expected s2.semantic_oscillation, got %+v", sig)
	}

	// a genuinely new body after the ping-pong history must still run
	writeFile(t, "func mul(a, b int) int { return a * b }")
	if deny, _ := e.PreCheck(rerun); deny {
		t.Fatalf("expected a semantically new body to pass PreCheck")
	}
}
