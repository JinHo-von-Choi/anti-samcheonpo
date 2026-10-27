package detect

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/research"
	"testing"
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
