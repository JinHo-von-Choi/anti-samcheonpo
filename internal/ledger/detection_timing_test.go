package ledger

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// seedTimingSession writes a session with n events and, when verdictAt is
// positive, one judgment raised on that event sequence number.
func seedTimingSession(t *testing.T, d *DB, id string, n int, verdictAt int64, base time.Time) {
	t.Helper()
	if err := d.UpsertLive(id, "claude", "/tmp/project", "/tmp/transcript.jsonl", "model-x"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		ev := &event.Event{SessionID: id, Seq: int64(i), TS: base.Add(time.Duration(i) * time.Second),
			Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatProduce, Summary: "event"}
		if err := d.InsertLiveEvent(id, ev); err != nil {
			t.Fatal(err)
		}
	}
	if verdictAt > 0 {
		v := detect.Signal{ID: id + "-v1", Detector: "S2", Rule: "s2.oscillation", Seq: verdictAt, Confidence: 0.9, Level: detect.L2}
		if err := d.InsertLiveVerdict(id, v, "user", "agent"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectionTimingPositionCountsFromFirstEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	base := time.Now().UTC().Add(-time.Hour)
	seedTimingSession(t, d, "late", 10, 8, base)
	seedTimingSession(t, d, "early", 10, 1, base)
	seedTimingSession(t, d, "silent", 10, 0, base)
	seedTimingSession(t, d, "single", 1, 1, base)

	late, err := d.DetectionTiming("late")
	if err != nil {
		t.Fatal(err)
	}
	// The span runs from the first event to the last: seq 1 to seq 10.
	// A judgment on seq 8 sits seven ninths of the way through.
	if late.Position < 0.77 || late.Position > 0.78 {
		t.Fatalf("position %.4f, want about 0.777", late.Position)
	}
	if !late.InLastThird {
		t.Fatal("a judgment at 78% of the trajectory is in the last third")
	}
	if late.FirstVerdictSeq != 8 || late.LastEventSeq != 10 {
		t.Fatalf("seq facts wrong: %+v", late)
	}
	if late.LeadMS != 2000 {
		t.Fatalf("lead %dms, want 2000", late.LeadMS)
	}

	early, err := d.DetectionTiming("early")
	if err != nil {
		t.Fatal(err)
	}
	if early.Position != 0 {
		t.Fatalf("position %v, want 0", early.Position)
	}
	if early.InLastThird {
		t.Fatal("a judgment on the first event is not in the last third")
	}

	silent, err := d.DetectionTiming("silent")
	if err != nil {
		t.Fatal(err)
	}
	if silent.Verdicts != 0 || silent.Position != 0 {
		t.Fatalf("a session with no judgment has no position: %+v", silent)
	}

	single, err := d.DetectionTiming("single")
	if err != nil {
		t.Fatal(err)
	}
	if single.Position != 0 || single.InLastThird {
		t.Fatalf("a single-event trajectory has no span: %+v", single)
	}
}

func TestSummarizeDetectionTimingCountsOnlyJudgedSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	base := time.Now().UTC().Add(-time.Hour)
	seedTimingSession(t, d, "a", 10, 8, base) // late
	seedTimingSession(t, d, "b", 10, 8, base) // late
	seedTimingSession(t, d, "c", 10, 2, base) // early
	seedTimingSession(t, d, "d", 10, 0, base) // silent

	summary, err := d.DetectionTimingSummary(base.Add(-time.Minute), "all")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Sessions != 4 {
		t.Fatalf("sessions %d, want 4", summary.Sessions)
	}
	if summary.WithVerdict != 3 || summary.WithoutVerdict != 1 {
		t.Fatalf("verdict split %d/%d, want 3/1", summary.WithVerdict, summary.WithoutVerdict)
	}
	if summary.LastThirdCount != 2 {
		t.Fatalf("last third count %d, want 2", summary.LastThirdCount)
	}
	if summary.LastThirdShare < 0.66 || summary.LastThirdShare > 0.67 {
		t.Fatalf("last third share %.4f, want 2/3", summary.LastThirdShare)
	}
	if summary.MedianPosition < 0.77 || summary.MedianPosition > 0.78 {
		t.Fatalf("median position %.4f, want about 0.777", summary.MedianPosition)
	}
}

func TestDetectionTimingsHonorsAgentFilter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	base := time.Now().UTC().Add(-time.Hour)
	seedTimingSession(t, d, "claude-1", 5, 2, base)
	if err := d.UpsertLive("codex-1", "codex", "/tmp/project", "/tmp/t.jsonl", "model-y"); err != nil {
		t.Fatal(err)
	}
	timings, err := d.DetectionTimings(base.Add(-time.Minute), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(timings) != 1 || timings[0].SessionID != "codex-1" {
		t.Fatalf("agent filter returned %+v", timings)
	}
}
