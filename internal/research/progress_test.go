package research

import (
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func read(seq int64, url, content string) *event.Event {
	id, host := Reference(url)
	return &event.Event{Seq: seq, Kind: event.KindTool, Category: event.CatExplore, ResultObserved: true, ObservationFP: content, ResearchSources: []event.ResearchSource{{ReferenceHash: id, HostHash: host}}}
}

func TestOnlyObservedSourcesSupportClaims(t *testing.T) {
	tr := New("웹서치 조사 후 플랜 작성", "task")
	tr.Observe(read(3, "https://EXAMPLE.com/paper#fragment", "result"))
	tr.Observe(&event.Event{Seq: 4, Kind: event.KindMessage, Text: "원인은 배제했다 [근거](https://example.com/paper). https://unknown.com/claim"})
	s := tr.Snapshot()
	if s.Sources != 1 || len(s.Links) != 1 || s.Links[0].Evidence != 3 || s.Links[0].Claim != 4 || s.Links[0].Kind != "hypothesis_exclusion_claim" || s.Completion != "unknown" {
		t.Fatalf("unexpected progress: %+v", s)
	}
	if s.Links[0].Meaning != "observed_source_link_not_verified_claim" {
		t.Fatal("citation must not imply a verified claim")
	}
	s.Links[0].Evidence = 999
	if tr.Snapshot().Links[0].Evidence != 3 {
		t.Fatal("snapshot aliases tracker")
	}
}

func TestMissingFailedAndRepeatedResults(t *testing.T) {
	tr := New("웹서치 조사", "task")
	for i := int64(1); i <= 3; i++ {
		e := read(i, "https://example.com/paper", "same")
		if i == 1 {
			e.ResultObserved = false
		}
		if i == 2 {
			e.IsError = true
		}
		tr.Observe(e)
	}
	tr.Observe(read(4, "https://other.com/copy", "same"))
	s := tr.Snapshot()
	if s.Sources != 2 || s.ContentResults != 1 || s.DuplicateResults != 1 || s.Hostnames != 2 || s.Completion != "unknown" {
		t.Fatalf("%+v", s)
	}
}

func TestReferenceBoundsAndNormalization(t *testing.T) {
	a, _ := Reference("https://USER:password@EXAMPLE.com/a?b=2&a=1#x")
	b, _ := Reference("https://example.com/a?a=1&b=2")
	if a != b {
		t.Fatal("fragment, credentials and query order should not change identity")
	}
	if id, _ := Reference(strings.Repeat("a", 2049)); id != "" {
		t.Fatal("oversized reference accepted")
	}
	refs := References("[long](" + strings.Repeat("a", 2049) + ")")
	if len(refs) != 0 {
		t.Fatal("oversized markdown reference accepted")
	}
}

func TestSourceLinksSurviveRedactedReplayAndCompaction(t *testing.T) {
	goal := "웹서치 조사 후 플랜 작성"
	tr := New(goal, "task")
	source := read(1, "https://example.com/paper", "result")
	claim := &event.Event{Seq: 2, Kind: event.KindMessage, Text: "근거 https://example.com/paper"}
	tr.Observe(source)
	tr.Observe(claim)
	claim.Text = ""
	restarted := New(goal, "task")
	restarted.Observe(source)
	restarted.Observe(&event.Event{Seq: 3, Kind: event.KindCompact})
	restarted.Observe(claim)
	if len(restarted.Snapshot().Links) != 1 || restarted.Snapshot().Sources != 1 {
		t.Fatal("redacted replay lost observed citation")
	}
}
