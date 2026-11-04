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

func TestKoreanResearchRequests(t *testing.T) {
	for _, goal := range []string{
		"사용기록을 연구해서 관련 하네스 리서치 후 기능 개선 플랜 작성해",
		"사용기록을 분석하고 관련 도구를 조사해서 기능 개선 플랜 작성해",
		"구현은 나중에 하고 지금은 연구 보고서만 작성해",
	} {
		if got := InferWork(goal); got.Kind != "research_document" {
			t.Errorf("%q: %+v", goal, got)
		}
	}
}
