package eval

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"testing"
)

func TestReferenceProjectionProtectsUnknownCallsAndNewResults(t *testing.T) {
	var events []*event.Event
	for seq := int64(0); seq < 4; seq++ {
		events = append(events, &event.Event{Seq: seq, Kind: event.KindTool, Tool: event.ToolRead, ActionFP: "exact-call", ObservationFP: "same", ResultObserved: true})
	}
	if len(ReferencePredictions(events, 10)[0].Evidence) != 1 {
		t.Fatal("identical observed calls missed")
	}
	events[2].ObservationFP = "new"
	if len(ReferencePredictions(events, 10)[0].Evidence) != 0 {
		t.Fatal("new source content treated as identical")
	}
	events[2].ObservationFP = "same"
	events[1].ActionFP = ""
	r := ReferencePredictions(events, 10)[0]
	if len(r.Evidence) != 0 || r.Unknown != 1 {
		t.Fatal("missing original arguments invented", r)
	}
}

func TestReferenceEditCountIsExplicitHeuristicAndResetsAtRequest(t *testing.T) {
	events := []*event.Event{{Seq: 1, Kind: event.KindTool, Tool: event.ToolEdit, Paths: []string{"a", "a"}, ResultObserved: true}, {Seq: 2, Kind: event.KindTool, Tool: event.ToolEdit, Paths: []string{"a"}, ResultObserved: true}}
	if len(ReferencePredictions(events, 2)[1].Evidence) != 1 {
		t.Fatal("edit count")
	}
	events = append(events[:1], &event.Event{Seq: 2, Kind: event.KindPrompt}, events[1])
	if len(ReferencePredictions(events, 2)[1].Evidence) != 0 {
		t.Fatal("old request edit count retained")
	}
}

func TestReviewConsensusCannotTurnSameReviewerOrDisagreementIntoTruth(t *testing.T) {
	item := ReviewItem{Session: "s", Start: 1, End: 3}
	a := Label{Session: "s", Start: 1, End: 3, Labeler: "a", Symptom: "S2", Justified: "yes"}
	if state, _ := ReviewConsensus(item, []Label{a, a}); state != "unknown" {
		t.Fatal("one reviewer counted twice")
	}
	b := a
	b.Labeler = "b"
	if state, _ := ReviewConsensus(item, []Label{a, b}); state != "positive" {
		t.Fatal("matching votes lost")
	}
	b.Symptom = "none"
	b.Justified = "no"
	if state, why := ReviewConsensus(item, []Label{a, b}); state != "unknown" || why != "reviewer_disagreement" {
		t.Fatal(state, why)
	}
}

func TestFrozenConsensusRejectsUnboundOrChangedReviewSource(t *testing.T) {
	item := ReviewItem{Session: "s", Start: 1, End: 3, SourceDigest: "source-v1"}
	a := Label{Session: "s", Start: 1, End: 3, Labeler: "a", Symptom: "S2", Justified: "yes"}
	b := a
	b.Labeler = "b"
	if state, why := FrozenReviewConsensus(item, "queue-v1", []Label{a, b}); state != "unknown" || why != "review_source_not_bound" {
		t.Fatal("legacy unbound votes treated as frozen truth", state, why)
	}
	a.ReviewSourceDigest, b.ReviewSourceDigest = "source-v1", "source-v1"
	a.ReviewQueueDigest, b.ReviewQueueDigest = "queue-v1", "queue-v1"
	if state, _ := FrozenReviewConsensus(item, "queue-v1", []Label{a, b}); state != "positive" {
		t.Fatal("matching frozen votes lost")
	}
	b.ReviewSourceDigest = "source-v2"
	if state, _ := FrozenReviewConsensus(item, "queue-v1", []Label{a, b}); state != "unknown" {
		t.Fatal("different reviewed source counted as agreement")
	}
	b.ReviewSourceDigest = "source-v1"
	b.ReviewQueueDigest = "queue-v2"
	if state, _ := FrozenReviewConsensus(item, "queue-v1", []Label{a, b}); state != "unknown" {
		t.Fatal("different reviewed queue counted as agreement")
	}
}
