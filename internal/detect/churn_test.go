package detect

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestChurnDetectorRemembersTheRecentProposals(t *testing.T) {
	d := NewChurnDetector()
	d.Record("a.go", "h1", 1)
	d.Record("a.go", "h2", 2)
	if _, _, ok := d.Repeat("a.go", "h1"); !ok {
		t.Fatal("the earlier proposal is found")
	}
	if seq, count, ok := d.Repeat("a.go", "h1"); !ok || seq != 1 || count != 1 {
		t.Fatalf("the first proposal is reported: seq=%d count=%d ok=%v", seq, count, ok)
	}
	if _, _, ok := d.Repeat("a.go", "h3"); ok {
		t.Fatal("an unseen content is not a repeat")
	}
	if _, _, ok := d.Repeat("b.go", "h1"); ok {
		t.Fatal("a repeat does not cross files")
	}
	d.Clear()
	if _, _, ok := d.Repeat("a.go", "h1"); ok {
		t.Fatal("a cleared detector remembers nothing")
	}
}

func TestChurnDetectorKeepsOnlyTheRecentWindow(t *testing.T) {
	d := NewChurnDetector()
	d.Record("a.go", "old", 1)
	for i := 0; i < churnWindow; i++ {
		d.Record("a.go", "h", int64(i+2))
	}
	if _, _, ok := d.Repeat("a.go", "old"); ok {
		t.Fatal("a proposal outside the window is forgotten")
	}
	if _, _, ok := d.Repeat("a.go", "h"); !ok {
		t.Fatal("a proposal inside the window is kept")
	}
}

// churnWrite is a pre-write check for a write that proposes hash for path.
func churnWrite(b *builder, path, hash string) (bool, *Signal) {
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{path},
		WriteHashes: map[string]string{path: hash}, WSBefore: b.ws}
	return b.e.PreCheck(ev)
}

// The A-B-A loop: the agent re-proposes a content it already wrote while
// the file sits at a state that already failed. The third proposal is
// stopped before it lands.
func TestChurnBlocksTheThirdProposalOfAFailedContent(t *testing.T) {
	b := newB(t, "math.go 고쳐 줘", nil)
	b.write("math.go", "hashA", []string{"func add(a, b int) int { return a + b }"}, nil)
	// the file is left at a failed state
	b.e.St.semOsc.RecordFailure("math.go", "func add(a, b int) int { return a + b }")
	b.write("math.go", "hashB", []string{"func add(a, b int) int { return a + b + 0 }"}, nil)

	deny, sig := churnWrite(b, "math.go", "hashA")
	if !deny {
		t.Fatal("the repeated content is stopped before it lands")
	}
	if sig.Rule != "s2.semantic_oscillation" || sig.Facts["path"] != "math.go" || sig.Facts["blocked"] != true {
		t.Fatalf("the refusal names the file and the rule: %+v", sig)
	}
	if sig.Facts["prev_seq"] == nil {
		t.Fatalf("the refusal names the earlier proposal: %+v", sig.Facts)
	}
}

// The same content written twice without a failure in between is not a loop.
func TestChurnAllowsTheSameContentWithoutAFailedState(t *testing.T) {
	b := newB(t, "math.go 고쳐 줘", nil)
	b.write("math.go", "hashA", []string{"func add(a, b int) int { return a + b }"}, nil)
	if deny, sig := churnWrite(b, "math.go", "hashA"); deny {
		t.Fatalf("a harmless repeat is allowed: %+v", sig)
	}
}

// A passing verification ends the sequence, so the next write is allowed.
func TestChurnAllowsTheWriteAfterAPassingVerification(t *testing.T) {
	b := newB(t, "math.go 고쳐 줘", nil)
	b.write("math.go", "hashA", []string{"func add(a, b int) int { return a + b }"}, nil)
	b.e.St.semOsc.RecordFailure("math.go", "func add(a, b int) int { return a + b }")
	b.write("math.go", "hashB", []string{"func add(a, b int) int { return a + b + 0 }"}, nil)
	b.verify("go test ./...", 0, nil)
	if deny, sig := churnWrite(b, "math.go", "hashA"); deny {
		t.Fatalf("a pass ends the sequence: %+v", sig)
	}
}

// An edit whose prior state the host did not report has no known content,
// so it cannot be compared and is not stopped.
func TestChurnSkipsEditsWithUnknownPriorState(t *testing.T) {
	b := newB(t, "math.go 고쳐 줘", nil)
	b.write("math.go", "hashA", []string{"func add(a, b int) int { return a + b }"}, nil)
	b.e.St.semOsc.RecordFailure("math.go", "func add(a, b int) int { return a + b }")
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{"math.go"},
		WriteHashes: map[string]string{"math.go": "edit:deadbeef"}, WSBefore: b.ws}
	if deny, sig := b.e.PreCheck(ev); deny {
		t.Fatalf("an unknown content is not stopped: %+v", sig)
	}
}
