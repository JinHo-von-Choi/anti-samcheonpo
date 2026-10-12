package learning

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"reflect"
	"testing"
)

func learningFixture() (eval.Split, []eval.Label, map[string][]*event.Event) {
	s := eval.Split{Version: "split/2", Purpose: "preregistered_holdout", Calibration: []string{"train"}, Groups: map[string]eval.SplitGroup{"train": {Repository: "train-repo", Family: "train-family"}}}
	data := map[string][]*event.Event{}
	var labels []eval.Label
	makeEvents := func(positive bool) []*event.Event {
		var out []*event.Event
		for i := int64(0); i < 3; i++ {
			result := "same"
			if !positive {
				result = fmt.Sprint(i)
			}
			out = append(out, &event.Event{Seq: i, Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, BehaviorTarget: "target", BehaviorResult: result})
		}
		return out
	}
	data["train"] = makeEvents(true)
	labels = append(labels, eval.Label{Session: "train", Start: 0, End: 2, Symptom: "S4", Justified: "yes", Labeler: "trainer"})
	for i := 0; i < 160; i++ {
		id := fmt.Sprintf("hold-%03d", i)
		s.Holdout = append(s.Holdout, id)
		s.Groups[id] = eval.SplitGroup{Repository: "repo-" + id, Family: "family-" + id}
		positive := i < 80
		data[id] = makeEvents(positive)
		symptom := "none"
		if positive {
			symptom = "S4"
		}
		for _, who := range []string{"independent-a", "independent-b"} {
			labels = append(labels, eval.Label{Session: id, Start: 0, End: 2, Symptom: symptom, Justified: "yes", Labeler: who})
		}
	}
	return s, labels, data
}
func TestFrozenRuleEvaluatedPromotedAndActuallyDetects(t *testing.T) {
	s, ls, data := learningFixture()
	p, err := Propose(ls, s, data)
	if err != nil || len(p.Definitions) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	r, err := Evaluate(p, s, ls, data, p.Gate)
	if err != nil || !r.Passed || r.Scores[0].TP != 80 || r.Scores[0].TN != 80 {
		t.Fatalf("%+v %v", r, err)
	}
	pack, err := Promote(p, r)
	if err != nil || pack.Effectiveness != "unvalidated" {
		t.Fatalf("%+v %v", pack, err)
	}
	m, _ := rulepack.New(pack.Definitions)
	var matches []rulepack.Match
	for _, e := range data[s.Holdout[0]] {
		matches = m.Observe(e, "runtime")
	}
	if len(matches) != 1 || len(matches[0].Evidence) != 3 {
		t.Fatal("promoted rule is not executable")
	}
}
func TestProposalDoesNotLearnHoldoutAndRejectsChangedTrainingOrGates(t *testing.T) {
	s, ls, data := learningFixture()
	p, _ := Propose(ls, s, data)
	data[s.Holdout[0]][0].BehaviorResult = "holdout-only"
	ls[1].Note = "holdout changed"
	after, _ := Propose(ls, s, data)
	if !reflect.DeepEqual(p, after) {
		t.Fatal("holdout leakage")
	}
	if _, err := Evaluate(p, s, ls, data, Gate{.8, .1}); err == nil {
		t.Fatal("posthoc gate tuning accepted")
	}
	data["train"][0].BehaviorResult = "changed"
	if _, err := Evaluate(p, s, ls, data, p.Gate); err == nil {
		t.Fatal("training changed after freeze")
	}
}
func TestSmallCorrelatedDisputedOrPublicDataCannotPromote(t *testing.T) {
	for _, mode := range []string{"small", "correlated", "disputed", "public", "unlabeled"} {
		t.Run(mode, func(t *testing.T) {
			s, ls, data := learningFixture()
			switch mode {
			case "small":
				for id := range s.Groups {
					if id != "train" && id != s.Holdout[0] && id != s.Holdout[80] {
						delete(s.Groups, id)
					}
				}
				s.Holdout = []string{s.Holdout[0], s.Holdout[80]}
			case "correlated":
				for _, id := range s.Holdout {
					g := s.Groups[id]
					g.Repository = "one-holdout-repo"
					s.Groups[id] = g
				}
			case "disputed":
				ls[2].Symptom = "none"
			case "public":
				s.Purpose = "public_regression"
			case "unlabeled":
				ls = ls[:1]
			}
			p, err := Propose(ls, s, data)
			if err != nil {
				t.Fatal(err)
			}
			r, err := Evaluate(p, s, ls, data, p.Gate)
			if err != nil {
				t.Fatal(err)
			}
			if r.Passed {
				t.Fatalf("%s falsely validated", mode)
			}
			if _, err = Promote(p, r); err == nil {
				t.Fatal("unvalidated promotion")
			}
		})
	}
}

func TestFrozenSourcesIncludeAllRuntimeMatcherInputs(t *testing.T) {
	for _, field := range []string{"selection", "unknown", "mutation", "background", "error", "failed_tests", "error_fingerprints"} {
		t.Run(field, func(t *testing.T) {
			s, labels, data := learningFixture()
			p, err := Propose(labels, s, data)
			if err != nil {
				t.Fatal(err)
			}
			e := data["train"][0]
			switch field {
			case "selection":
				e.BehaviorSelection = "different"
			case "unknown":
				e.Unknown = true
			case "mutation":
				e.Mutating = true
			case "background":
				e.Background = true
			case "error":
				e.IsError = true
			case "failed_tests":
				e.FailedTests = []string{"different"}
			case "error_fingerprints":
				e.ErrFPs = []string{"different"}
			}
			if _, err := Evaluate(p, s, labels, data, p.Gate); err == nil {
				t.Fatal("changed matcher input accepted after freeze")
			}
		})
	}
}
