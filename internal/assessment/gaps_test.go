package assessment

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"testing"
	"time"
)

func TestGapClosesOnlyAtObservedCompleteToolResult(t *testing.T) {
	var tr GapTracker
	tr.Observe(&event.Event{Seq: 2, Kind: event.KindTool, TS: time.Unix(100, 0), Reliability: &event.Reliability{Complete: false}, Decisions: []event.Decision{{Rule: "observation.collection", Code: "watch_unavailable"}, {Rule: "s2.behavior_loop", Code: "environment_unknown"}}})
	tr.Observe(&event.Event{Seq: 3, Kind: event.KindMessage, TS: time.Unix(200, 0)})
	if !tr.Snapshot()[0].Open {
		t.Fatal("message falsely restored observation")
	}
	tr.Observe(&event.Event{Seq: 4, Kind: event.KindTool, Reliability: &event.Reliability{Complete: false}})
	tr.Observe(&event.Event{Seq: 5, Kind: event.KindTool, TS: time.Unix(300, 0), Reliability: &event.Reliability{Complete: true}})
	g := tr.Snapshot()[0]
	if g.Open || g.First != 2 || g.Last != 4 || g.ResumedAt == nil || !g.TimeUnknown || len(g.AffectedRules) != 1 {
		t.Fatalf("%+v", g)
	}
	g.Reasons[0] = "tampered"
	if tr.Snapshot()[0].Reasons[0] == "tampered" {
		t.Fatal("gap snapshot aliases source")
	}
}
