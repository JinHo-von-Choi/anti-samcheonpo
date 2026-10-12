package rulepack

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"testing"
)

func definition() Definition {
	d := Definition{Symptom: "S2", Tool: event.ToolShell, Category: event.CatVerify, Minimum: 3, Window: 6}
	d.ID = Digest(d)
	return d
}
func TestMatcherKeepsSelectorsMutationsPassesAndRevisionsAsNewEvidence(t *testing.T) {
	for _, kind := range []string{"selection", "environment", "mutation", "pass", "scope", "flaky"} {
		t.Run(kind, func(t *testing.T) {
			m, _ := New([]Definition{definition()})
			for i := 0; i < 5; i++ {
				exit := 1
				e := &event.Event{Seq: int64(i), Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, ExitCode: &exit, BehaviorTarget: "failure", BehaviorResult: "same", BehaviorSelection: "selection"}
				scope := "task"
				switch kind {
				case "selection":
					e.BehaviorSelection = string(rune('a' + i))
				case "environment":
					e.BehaviorContext = string(rune('a' + i))
				case "mutation":
					if i%2 == 1 {
						e.Category = event.CatProduce
					}
				case "pass":
					if i%2 == 1 {
						zero := 0
						e.ExitCode = &zero
					}
				case "scope":
					scope = string(rune('a' + i))
				case "flaky":
					e.Reliability = &event.Reliability{Flaky: true}
				}
				if len(m.Observe(e, scope)) != 0 {
					t.Fatal(kind)
				}
			}
		})
	}
}
