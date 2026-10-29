package detect

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// A write outside the scope the user accepted is stopped before it lands.
func TestPreCheckStopsAWriteOutsideTheAcceptedScope(t *testing.T) {
	c := &contract.Contract{Goal: "scoped work"}
	c.Scope.Allow = []string{"src/**"}
	b := newB(t, "src/a.py 고쳐 줘", c)
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce,
		Paths: []string{"other/file.py"}, WriteHashes: map[string]string{"other/file.py": "h1"}, WSBefore: b.ws}
	deny, sig := b.e.PreCheck(ev)
	if !deny || sig.Rule != "s3.out_of_scope" || sig.Facts["path"] != "other/file.py" {
		t.Fatalf("the out-of-scope write is stopped before it lands: %v %+v", deny, sig)
	}
	// a path inside the scope is not stopped
	ev = &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce,
		Paths: []string{"src/a.py"}, WriteHashes: map[string]string{"src/a.py": "h2"}, WSBefore: b.ws}
	if deny, sig := b.e.PreCheck(ev); deny {
		t.Fatalf("a write inside the scope runs: %+v", sig)
	}
}

// A scope guessed from prose is not a boundary the user set, so a write
// outside it is reported, not stopped.
func TestPreCheckLeavesAGuessedScopeAlone(t *testing.T) {
	b := newB(t, "src/a.py 고쳐 줘", nil)
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce,
		Paths: []string{"other/file.py"}, WriteHashes: map[string]string{"other/file.py": "h1"}, WSBefore: b.ws}
	if deny, sig := b.e.PreCheck(ev); deny {
		t.Fatalf("a guessed scope stops no write: %+v", sig)
	}
}

// A write that would put a file back into a state the session already wrote
// is the oscillation, and it is stopped before it lands.
func TestPreCheckStopsAnOscillatingWrite(t *testing.T) {
	b := newB(t, "math.go 고쳐 줘", nil)
	b.write("math.go", "hashA", []string{"func add(a, b int) int { return a + b }"}, nil)
	b.write("math.go", "hashB", []string{"func add(a, b int) int { return a + b + 0 }"}, nil)
	deny, sig := churnWrite(b, "math.go", "hashA")
	if !deny || sig.Rule != "s2.oscillation" || sig.Facts["path"] != "math.go" {
		t.Fatalf("the rollback is stopped before it lands: %v %+v", deny, sig)
	}
}

// A check that passed ends the sequence of attempts, so the state it
// verified is not a rollback that counts.
func TestPreCheckAllowsTheStateAfterAPassingVerification(t *testing.T) {
	b := newB(t, "math.go 고쳐 줘", nil)
	b.write("math.go", "hashA", []string{"func add(a, b int) int { return a + b }"}, nil)
	b.write("math.go", "hashB", []string{"func add(a, b int) int { return a + b + 0 }"}, nil)
	b.verify("go test ./...", 0, nil)
	if deny, sig := churnWrite(b, "math.go", "hashA"); deny {
		t.Fatalf("a pass ends the sequence: %+v", sig)
	}
}

// A check whose failure is swallowed reports success whatever it said, so
// it is stopped while it is still a command.
func TestPreCheckStopsASwallowedTestResult(t *testing.T) {
	b := newB(t, "pytest 로 확인해 줘", nil)
	norm, _ := fp.NormalizeCmd("pytest || true")
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: "pytest || true", CmdNorm: norm,
		CmdFP: fp.CmdFP(norm), Category: event.CatVerify, WSBefore: b.ws}
	deny, sig := b.e.PreCheck(ev)
	if !deny || sig.Rule != "s5.error_hiding" || sig.Facts["kind"] != "or_true" {
		t.Fatalf("the swallowed result is stopped before it runs: %v %+v", deny, sig)
	}
}
