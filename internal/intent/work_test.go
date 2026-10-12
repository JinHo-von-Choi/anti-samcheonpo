package intent

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"testing"
)

func TestMixedResearchDocumentIsNotImplementation(t *testing.T) {
	goal := "원인 검토하고 웹서치를 통해 비슷한 도구 찾아서 기능 개선 플랜 작성해"
	w := InferWork(goal)
	if w.Kind != "research_document" || w.Phase != "research" || !ResearchRequest(goal) {
		t.Fatalf("%+v", w)
	}
	w = DescribeWork(goal, []*event.Event{{Kind: event.KindTool, Category: event.CatProduce}})
	if w.Phase != "synthesis" {
		t.Fatal(w)
	}
	if ResearchRequest("검토한 플랜대로 구현 시작해") {
		t.Fatal("explicit implementation must win")
	}
}
