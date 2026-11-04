package detect

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/research"
	"testing"
	"time"
)

func TestResearchRetainsEvidenceThroughMetadataFreeMessage(t *testing.T) {
	e := &Engine{FirstPrompt: "웹서치 조사 후 플랜 작성"}
	id, host := research.Reference("https://example.com/paper")
	e.observeResearchProgress(&event.Event{Seq: 1, Kind: event.KindTool, Category: event.CatExplore, ResultObserved: true, ObservationFP: "result", Reliability: &event.Reliability{TaskID: "task", Revision: 1}, ResearchSources: []event.ResearchSource{{ReferenceHash: id, HostHash: host}}})
	e.observeResearchProgress(&event.Event{Seq: 2, Kind: event.KindMessage, Text: "근거 https://example.com/paper"})
	if len(e.Research.Snapshot().Links) != 1 {
		t.Fatal("message discarded tool evidence")
	}
	e.observeResearchProgress(&event.Event{Seq: 3, Kind: event.KindTool, Reliability: &event.Reliability{TaskID: "task", Revision: 2}})
	if e.Research.Snapshot().Sources != 0 {
		t.Fatal("old task revision evidence retained")
	}
	e.FirstPrompt = "웹서치 새 질문 조사"
	e.observeResearchProgress(&event.Event{Seq: 4, Kind: event.KindMessage, Text: "https://example.com/paper"})
	if len(e.Research.Snapshot().Links) != 0 {
		t.Fatal("new question inherited old citations")
	}
}

func TestResearchFollowupUsesItsOwnEvidenceWindow(t *testing.T) {
	b := newB(t, "context", nil)
	for i := 0; i < 4; i++ {
		b.read("README.md")
	}
	b.ts = b.ts.Add(time.Hour)
	b.e.AddRequest("사용기록을 연구해서 하네스 리서치 후 플랜 작성해")
	for i := 0; i < 25; i++ {
		if fired(b.read("docs/evidence.md"), "s4.read_only_streak") != nil {
			t.Fatal("research was treated as implementation stagnation")
		}
	}
	if b.e.Research == nil || b.e.Research.Goal != b.e.LatestRequest() {
		t.Fatal("research tracker used the first conversation message")
	}
	b.e.AddRequest("플랜 구현해")
	for i := 0; i < 19; i++ {
		if fired(b.read("main.go"), "s4.read_only_streak") != nil {
			t.Fatal("previous phase reads leaked into implementation")
		}
	}
	if fired(b.read("main.go"), "s4.read_only_streak") == nil {
		t.Fatal("implementation reading loop no longer detected")
	}
}

func TestIdleTimeDoesNotBecomeExplorationTime(t *testing.T) {
	b := newB(t, "api.go 수정해", nil)
	b.read("a.go")
	b.ts = b.ts.Add(24 * time.Hour)
	for i := 0; i < 4; i++ {
		if fired(b.read("a.go"), "s4.read_only_streak") != nil {
			t.Fatal("unobserved wall time counted as active research")
		}
	}
}

func TestObservedToolDurationStillDetectsLongExploration(t *testing.T) {
	b := newB(t, "api.go 수정해", nil)
	var last []Signal
	for i := 0; i < 5; i++ {
		last = b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"a.go"}, DurationMS: 180000})
	}
	if fired(last, "s4.read_only_streak") == nil {
		t.Fatal("observed active time ignored")
	}
}

func TestFollowupsDoNotForgetEarlierRequirements(t *testing.T) {
	b := newB(t, "initial requirement", nil)
	for i := 0; i < 30; i++ {
		b.e.AddRequest("another required outcome")
	}
	if len(b.e.Requests) != 30 {
		t.Fatal("requirements dropped from task state")
	}
}

// A tool the classifier does not know is filed under exploration, but its
// repetition is not repeated research.
func TestResearchRepeatIgnoresUnknownTools(t *testing.T) {
	b := newB(t, "API 문서를 조사해서 정리해 줘", nil)
	for i := 0; i < 5; i++ {
		if v := fired(b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolOther, RawTool: "skill_manage", Category: event.CatExplore, Text: "skill saved"}), "s4.research_repeat"); v != nil {
			t.Fatalf("unknown tool counted as research: %+v", v)
		}
	}
}
