package analyze

import (
	"reflect"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// repairSequence builds the events that the repair reclassification changes:
// a write, a failed check, and two more writes to the same file.
//
// The second write after the failure is a repair, and the reclassification
// writes that back onto the event. Classifying the sequence twice therefore
// classifies the last write from a category the first pass had already
// changed.
func repairSequence() []*event.Event {
	fail := 1
	return []*event.Event{
		{Seq: 1, TS: time.Unix(1, 0), Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce,
			Mutating: true, Paths: []string{"a.go"}, CostMicroKRW: 1000, Priced: true},
		{Seq: 2, TS: time.Unix(2, 0), Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify,
			ExitCode: &fail, CostMicroKRW: 1000, Priced: true},
		{Seq: 3, TS: time.Unix(3, 0), Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce,
			Mutating: true, Paths: []string{"a.go"}, CostMicroKRW: 1000, Priced: true},
		{Seq: 4, TS: time.Unix(4, 0), Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce,
			Mutating: true, Paths: []string{"a.go"}, CostMicroKRW: 1000, Priced: true},
	}
}

func engineWith(t *testing.T, evs []*event.Event) *detect.Engine {
	t.Helper()
	eng := detect.NewEngine(config.Default(), nil, false, "live", t.TempDir(), "s", "")
	for _, ev := range evs {
		eng.Observe(ev)
	}
	return eng
}

// A snapshot must not change what the final analysis says.
//
// The reclassification writes the bucket and the category back onto the
// events it reads. A session that was snapshotted between turns would then
// be classified a second time from a category the first pass had already
// changed, and its final totals would differ from those of a session that
// was never snapshotted.
func TestSnapshotDoesNotChangeFinalAnalysis(t *testing.T) {
	plain := &event.Session{ID: "plain", Agent: "claude", Mode: "live"}
	want := Finalize(plain, engineWith(t, repairSequence()), "cfg", "v1")

	snapshotted := &event.Session{ID: "snapshotted", Agent: "claude", Mode: "live"}
	eng := engineWith(t, repairSequence())
	Snapshot(snapshotted, eng, "cfg", "v1")
	got := Finalize(snapshotted, eng, "cfg", "v1")

	if !reflect.DeepEqual(want.Totals.BucketMicro, got.Totals.BucketMicro) {
		t.Fatalf("snapshot changed the final totals: %v != %v", want.Totals.BucketMicro, got.Totals.BucketMicro)
	}
	if !reflect.DeepEqual(want.Totals.BucketTokens, got.Totals.BucketTokens) {
		t.Fatalf("snapshot changed the final token totals: %v != %v", want.Totals.BucketTokens, got.Totals.BucketTokens)
	}
	for i, ev := range want.Session.Events {
		if got.Session.Events[i].Bucket != ev.Bucket {
			t.Fatalf("event %d bucket %q != %q", ev.Seq, got.Session.Events[i].Bucket, ev.Bucket)
		}
		if got.Session.Events[i].Category != ev.Category {
			t.Fatalf("event %d category %q != %q", ev.Seq, got.Session.Events[i].Category, ev.Category)
		}
	}
}

// The snapshot itself classifies the events, so its own totals are the ones
// the final analysis would produce.
func TestSnapshotTotalsMatchFinalTotals(t *testing.T) {
	snapshotted := &event.Session{ID: "snapshotted", Agent: "claude", Mode: "live"}
	eng := engineWith(t, repairSequence())
	snap := Snapshot(snapshotted, eng, "cfg", "v1")

	plain := &event.Session{ID: "plain", Agent: "claude", Mode: "live"}
	want := Finalize(plain, engineWith(t, repairSequence()), "cfg", "v1")

	if !reflect.DeepEqual(snap.Totals.BucketMicro, want.Totals.BucketMicro) {
		t.Fatalf("snapshot totals %v != final totals %v", snap.Totals.BucketMicro, want.Totals.BucketMicro)
	}
	if err := CheckInvariant(snap.Session, snap.Totals); err != nil {
		t.Fatal(err)
	}
}
