package detect

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func qualityEngine(t *testing.T) *builder {
	return newB(t, "버튼 동작을 고쳐 줘", nil)
}

func changeCode(b *builder, p string, lines int) {
	b.e.trackQuality(&event.Event{Seq: b.seq + 1000, Category: event.CatProduce, Tool: event.ToolEdit, Paths: []string{p}, AddedLines: map[string]int{p: lines}})
}

func TestQualityLeavesAnAnswerWithoutChangesAlone(t *testing.T) {
	if v := qualityEngine(t).e.QualityNext("설치판 교체는 구현하지 않았습니다.", true, 30); v != nil {
		t.Fatalf("a status answer that changed nothing is not held: %+v", v)
	}
}

func TestQualityHoldsEachReasonOnceInOrder(t *testing.T) {
	b := qualityEngine(t)
	changeCode(b, "web/page.tsx", 40)
	text := "버튼을 고쳤습니다.\n\n다국어는 구현하지 않았습니다."
	var got []string
	for i := 0; i < 6; i++ {
		v := b.e.QualityNext(text, true, 30)
		if v == nil {
			break
		}
		got = append(got, v.Rule)
	}
	want := []string{RuleScopeDropped, RuleBuried, RuleUnobserved, RuleCritique}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
}

func TestQualityCountsRealObservationAndCritique(t *testing.T) {
	b := qualityEngine(t)
	changeCode(b, "web/page.tsx", 40)
	zero := 0
	e := &event.Event{Seq: b.seq + 2000, Tool: event.ToolShell, Cmd: "samcheonpo ui-check web/page.html && samcheonpo critique", CmdNorm: "samcheonpo ui-check web/page.html && samcheonpo critique", ExitCode: &zero}
	b.e.trackQuality(e)
	if v := b.e.QualityNext("버튼을 고쳤습니다.", true, 30); v != nil {
		t.Fatalf("observed and reviewed work ends: %+v", v)
	}
}
