package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"testing"
)

func TestLiveRequirementsSurviveLedgerAndDoNotBecomeCompletion(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "false")
	s.mu.Lock()
	ev := &event.Event{Kind: event.KindMessage, Text: "아직 일부 작업이 남았습니다.", Priced: true}
	s.parser.AddEvent(ev)
	s.observe(ev)
	g := s.alignmentGraph
	s.mu.Unlock()
	if g == nil || g.Completion != "unknown" || len(g.Requirements) == 0 {
		t.Fatal(g)
	}
	evs, err := s.db.EventsOf(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if len(last.Requirements) == 0 || last.Requirements[0].ID != "check" || last.Requirements[0].LabelHash == "" {
		t.Fatal("requirement evidence not persisted")
	}
	if last.Text != "" {
		t.Fatal("agent prose persisted")
	}
}

func TestCurrentFailedRequirementFindsFalseCompletionWithoutJudge(t *testing.T) {
	s, _ := linkedSessions(t, false)
	results := s.checkpoint(false)
	if len(results) != 1 || results[0].Pass || results[0].Evidence == nil {
		t.Fatal("real failing check required")
	}
	awaitLinked(t, s)
	s.mu.Lock()
	for _, text := range []string{"아직 일부 작업이 남았습니다.", "검사는 완료됐지만 실패했습니다.", "작업을 완료하지 않았습니다.", "```go\n// 완료했습니다\n```", "요청한 작업을 모두 완료했습니다."} {
		ev := &event.Event{Kind: event.KindMessage, Text: text, Priced: true}
		s.parser.AddEvent(ev)
		s.observe(ev)
	}
	var found int
	for _, v := range s.eng.Verdicts {
		if v.Rule == "s5.partial_done" {
			found++
			if v.Level != 1 || !v.Estimate || v.WasteMicro != 0 {
				s.mu.Unlock()
				t.Fatal(v)
			}
		}
	}
	pending, judgeDisabled, blocked := len(s.pending), s.judge == nil, s.unresolved != nil
	s.mu.Unlock()
	if found != 1 || pending != 0 || !judgeDisabled || blocked {
		t.Fatalf("grounded candidate=%d pending=%d judge_disabled=%v blocked=%v", found, pending, judgeDisabled, blocked)
	}
}
