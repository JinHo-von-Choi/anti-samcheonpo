package live

import (
	"context"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/judge"
	"slices"
	"testing"
)

type issueTestJudge struct {
	in    judge.Input
	calls int
}

func (j *issueTestJudge) Judge(_ context.Context, in judge.Input) (judge.Verdict, error) {
	j.in = in
	j.calls++
	return judge.Verdict{Label: judge.Drift, Issues: []judge.Issue{{Kind: "partial_completion", Evidence: []int64{17}}, {Kind: "repeated_permission", Evidence: []int64{999}}}, Usage: event.Usage{In: 10, Out: 1, Model: "claude-haiku-4-5"}}, nil
}
func TestDraftSemanticJudgeIsBudgetedGroundedAndCannotBlock(t *testing.T) {
	s, _ := budgetSession(t)
	s.c = nil
	s.acc = contract.Acceptance{State: contract.StateDraft}
	s.firstPrompt = "요청한 두 기능 구현"
	j := &issueTestJudge{}
	s.judge = j
	s.eng.Observe(&event.Event{Seq: 17, Kind: event.KindMessage, Text: "한 기능 구현을 완료했습니다.", Summary: "message"})
	s.runJudge()
	if j.calls != 1 || !slices.Contains(j.in.EvidenceIDs, int64(17)) || slices.Contains(j.in.EvidenceIDs, int64(999)) {
		t.Fatalf("%+v", j)
	}
	n := 0
	for _, v := range s.eng.Verdicts {
		if v.Rule == "s5.partial_done" {
			n++
			if !v.Estimate || v.Level != 1 || v.Detector != "S5" {
				t.Fatal(v)
			}
		}
		if v.Rule == "s3.repeated_permission" {
			t.Fatal("invented model evidence accepted")
		}
	}
	if n != 0 || s.unresolved != nil {
		t.Fatalf("candidates %d blocked %v", n, s.unresolved)
	}
}
