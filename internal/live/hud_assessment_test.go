package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"testing"
)

func TestHUDLateCostAndStablePolling(t *testing.T) {
	cfg := config.Default()
	s := &Session{Cfg: cfg, eng: detect.NewEngine(cfg, nil, false, "live", "/p", "s", "웹서치 후 플랜 작성"), firstPrompt: "웹서치 후 플랜 작성"}
	ev := &event.Event{Seq: 1, Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"doc"}, Text: "new evidence"}
	s.eng.Observe(ev)
	before, w := s.currentAssessment(0, 0)
	if before.WasteRatio != "미확인" || w.Kind != "research_document" || !before.Progress.Knowledge {
		t.Fatalf("%+v %+v", before, w)
	}
	ev.Usage.In = 100
	ev.Priced = true
	s.eng.AddCost(ev, 100)
	after, _ := s.currentAssessment(0, 0)
	if after.WasteRatio != "0%" || after.CostBasis != "money" {
		t.Fatal(after)
	}
	after.Reasons["mutation"] = 9
	next, _ := s.currentAssessment(0, 0)
	if next.Reasons["mutation"] != 0 || next.Coverage.ShellCalls != 0 {
		t.Fatal("poll aliases or recounts cached facts")
	}
	s.eng.Verdicts = append(s.eng.Verdicts, detect.Signal{Seq: 1, Rule: "s4.research_repeat", Estimate: true})
	next, _ = s.currentAssessment(0, 0)
	if next.Candidates != 1 || next.CostAttribution != "unattributed_findings" {
		t.Fatal(next)
	}
	s.recordDecision(detect.Signal{Rule: "s4.research_repeat", Seq: 1}, "unsupported_route")
	next, _ = s.currentAssessment(0, 0)
	if next.Reasons["unsupported_route"] != 1 || next.Findings != 1 {
		t.Fatal(next)
	}
}
func BenchmarkHUDAssessmentPolling(b *testing.B) {
	cfg := config.Default()
	s := &Session{Cfg: cfg, eng: detect.NewEngine(cfg, nil, false, "live", "/p", "s", "research")}
	for i := 0; i < 10000; i++ {
		s.eng.St.Events = append(s.eng.St.Events, &event.Event{Seq: int64(i), Kind: event.KindTool, Tool: event.ToolRead, Usage: event.Usage{In: 1}})
	}
	s.currentAssessment(0, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.currentAssessment(0, 0)
	}
}
