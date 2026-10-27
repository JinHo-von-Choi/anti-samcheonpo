package outcome

import (
	"math"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func call(seq int64, tool string, f func(*event.Event)) *event.Event {
	zero := 0
	ev := &event.Event{Seq: seq, Kind: event.KindTool, Tool: tool, ExitCode: &zero}
	if f != nil {
		f(ev)
	}
	return ev
}

func TestMeasureReadsTheWindowAfterTheVerdict(t *testing.T) {
	evs := []*event.Event{
		call(1, event.ToolShell, func(e *event.Event) { e.CmdFP = "pytest"; e.Category = event.CatVerify }),
		call(2, event.ToolShell, func(e *event.Event) { e.CmdFP = "pytest"; e.Category = event.CatVerify }),
		{Seq: 3, Kind: event.KindMessage},
		call(4, event.ToolShell, func(e *event.Event) { e.CmdFP = "pytest"; e.Category = event.CatVerify }),
		call(5, event.ToolEdit, func(e *event.Event) { e.Paths = []string{"tests/test_x.py"} }),
		call(6, event.ToolShell, func(e *event.Event) { e.Summary = "shell: git restore src/x.py" }),
		call(7, event.ToolRead, nil),
	}
	verdicts := []detect.Signal{{Rule: "s1.identical_rerun", Seq: 2, Level: detect.L1}, {Rule: "s1.identical_rerun", Seq: 4, Level: detect.L1}}
	o := Measure(2, "s1.identical_rerun", []int64{1, 2}, evs, verdicts, 2)
	want := Outcome{Calls: 2, Complete: true, Verified: true, Repeated: true, Recurred: true, TestEdited: true}
	if o != want {
		t.Fatalf("got %+v want %+v", o, want)
	}
	// the revert at seq 6 lies outside a window of two calls; a longer
	// window reaches it, and the session ends before the window fills
	if o := Measure(2, "s1.identical_rerun", []int64{1, 2}, evs, verdicts, 10); !o.Reverted || o.Complete || o.Calls != 4 {
		t.Fatalf("%+v", o)
	}
}

func TestMeasureIgnoresFailedChecksAndOtherRules(t *testing.T) {
	one := 1
	evs := []*event.Event{
		call(2, event.ToolShell, func(e *event.Event) { e.Category = event.CatVerify; e.ExitCode = &one }),
		call(3, event.ToolShell, func(e *event.Event) { e.Summary = "shell: git checkout main" }),
	}
	verdicts := []detect.Signal{{Rule: "s4.read_only_streak", Seq: 3, Level: detect.L1}, {Rule: "s1.identical_rerun", Seq: 3, Level: detect.L0}}
	if o := Measure(1, "s1.identical_rerun", nil, evs, verdicts, 5); o.Verified || o.Reverted || o.Recurred || o.Repeated {
		t.Fatalf("%+v", o)
	}
}

func TestConfSeqNarrowsWithSamplesAndCoversTheMean(t *testing.T) {
	xs := make([]float64, 0, 4000)
	for i := 0; i < 4000; i++ {
		xs = append(xs, float64(i%10)/10) // mean 0.45
	}
	m1, h1 := ConfSeq(xs[:100], 0.05)
	m2, h2 := ConfSeq(xs, 0.05)
	if h2 >= h1 || h2 <= 0 {
		t.Fatalf("width %v then %v", h1, h2)
	}
	if m1-h1 > 0.45 || m1+h1 < 0.45 || m2-h2 > 0.45 || m2+h2 < 0.45 {
		t.Fatalf("mean not covered: %v±%v %v±%v", m1, h1, m2, h2)
	}
	if _, h := ConfSeq(nil, 0.05); !math.IsInf(h, 1) {
		t.Fatal("no sample, no interval")
	}
}
