package detect

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func preWrite(b *builder, path string) (bool, *Signal) {
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{path},
		Patch: []event.PatchFile{{Path: path, Added: []string{"x"}}}, WSBefore: b.ws}
	return b.e.PreCheck(ev)
}

// A cause outside the code that repeats with the workspace unchanged freezes
// source edits: no edit repairs it, so the refusal names the external action
// instead. The dependency manifest stays writable, and the freeze lifts when
// the frozen command passes.
func TestExternalCauseFreezesSourceWrites(t *testing.T) {
	b := newB(t, "src/app.cjs 고쳐 줘", nil)
	out := "Error: Cannot find module 'yaml'\nRequire stack:"
	shellFail(b, "node test_app.cjs", out)
	v := fired(shellFail(b, "node test_app.cjs", out), "s2.environment")
	if v == nil || v.Facts["remedy"] != "npm install yaml" {
		t.Fatalf("the repeat names the external action: %+v", v)
	}
	deny, sig := preWrite(b, "src/app.cjs")
	if !deny || sig.Rule != "s2.environment" || sig.Facts["frozen"] != true || sig.Facts["frozen_path"] != "src/app.cjs" || sig.Facts["remedy"] != "npm install yaml" || sig.Facts["cmd"] == "" {
		t.Fatalf("a source edit under a standing external cause is refused with the remedy: %v %+v", deny, sig)
	}
	if deny, _ := preWrite(b, "package.json"); deny {
		t.Fatal("declaring the dependency is allowed while frozen")
	}
	if deny, _ := preWrite(b, ".samcheonpo/contract.yml"); deny {
		t.Fatal("the harness state directory is not source")
	}
	b.verify("node test_app.cjs", 0, nil)
	if deny, _ := preWrite(b, "src/app.cjs"); deny {
		t.Fatal("a pass of the frozen command lifts the freeze")
	}
}

// An environment-changing command that succeeds lifts the freeze even before
// the frozen command is rerun; a code-level failure never freezes.
func TestFreezeLiftsOnEnvironmentChangeAndNotForCodeFailures(t *testing.T) {
	b := newB(t, "x", nil)
	out := "ModuleNotFoundError: No module named 'requests'"
	shellFail(b, "python3 -m pytest", out)
	shellFail(b, "python3 -m pytest", out)
	if !b.e.Frozen() {
		t.Fatal("a repeated missing package freezes")
	}
	exit := 0
	b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: "pip install requests", CmdNorm: "pip install requests", Category: event.CatProduce, Mutating: true, ExitCode: &exit})
	if b.e.Frozen() {
		t.Fatal("a successful install lifts the freeze")
	}
	b2 := newB(t, "x", nil)
	for i := 0; i < 3; i++ {
		shellFail(b2, "python3 -m pytest", "AssertionError: assert 1 == 2")
	}
	if b2.e.Frozen() {
		t.Fatal("a code failure is not an external cause")
	}
	b3 := newB(t, "x", nil)
	for i := 0; i < 4; i++ {
		shellFail(b3, "pip install x", "socket.gaierror: Temporary failure in name resolution")
	}
	if b3.e.Frozen() {
		t.Fatal("a transient network failure does not freeze")
	}
	b.e.St.freeze = &freezeMark{class: "x"}
	b.e.ReleaseFreeze()
	if b.e.Frozen() {
		t.Fatal("release clears the freeze")
	}
}
