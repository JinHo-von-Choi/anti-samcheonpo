package alignment

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"strings"
	"testing"
)

func TestRequirementStorageIsNotDisplayLimit(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("기능 %d 구현", i))
	}
	g := Build(strings.Join(lines, "\n"), nil, false, nil, nil)
	if len(g.Requirements) != 30 || !strings.Contains(g.Text(), "외 22개") {
		t.Fatal("display discarded stored requirements")
	}
	es := []*event.Event{{Seq: 1, Kind: event.KindPrompt, PromptOrigin: "user", Text: "작업 취소해"}}
	g = Build(strings.Join(lines, "\n"), nil, false, es, nil)
	for _, r := range g.Requirements {
		if r.Lifecycle != "cancelled" {
			t.Fatal("explicit cancellation not reflected")
		}
	}
}

func TestAuthorizationSurvivesLongIndependentReads(t *testing.T) {
	es := []*event.Event{{Seq: 1, Kind: event.KindPrompt, PromptOrigin: "user", Text: "로그인 기능 구현해"}}
	for i := 2; i < 100; i++ {
		es = append(es, &event.Event{Seq: int64(i), Kind: event.KindTool, BehaviorResult: "read"})
	}
	es = append(es, &event.Event{Seq: 100, Kind: event.KindMessage, Text: "구현을 계속할까요?"})
	g := Build("로그인 기능 구현해", nil, false, es, nil)
	if !g.ValidIssue("repeated_permission", []string{g.Requirements[0].ID}, []int64{100}, nil, []int64{1}) {
		t.Fatal("earlier request forgotten")
	}
}
