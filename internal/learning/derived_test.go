package learning

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
)

// derive turns labels into labels an independent grader derived.
//
// The grader stands outside the task, so its verdict is one vote with a
// named method behind it.
func derive(labels []eval.Label) []eval.Label {
	out := make([]eval.Label, len(labels))
	for i, l := range labels {
		l.Source = eval.LabelSourceDerived
		l.Derivation = &eval.Derivation{Method: "execution_oracle", Oracle: "grader", Digest: "digest"}
		out[i] = l
	}
	return out
}

// A label a grader derived goes through the same pipeline as a label a
// person made, and it comes out as its own grade of evidence.
func TestDerivedLabelsPassThroughTheSamePipelineAtTheirOwnGrade(t *testing.T) {
	s, ls, data := learningFixture()
	derived := derive(ls)

	p, err := Propose(derived, s, data)
	if err != nil {
		t.Fatal(err)
	}
	if p.EvidenceTier != rulepack.EvidenceDerivedExecution {
		t.Fatalf("proposal tier = %q", p.EvidenceTier)
	}
	r, err := Evaluate(p, s, derived, data, p.Gate)
	if err != nil {
		t.Fatal(err)
	}
	if !r.DerivedPassed {
		t.Fatalf("derived grade did not pass: %+v", r.DerivedScores)
	}
	if r.Passed {
		t.Fatal("derived evidence passed the human grade")
	}
	if r.EvidenceTier != rulepack.EvidenceDerivedExecution {
		t.Fatalf("evaluation tier = %q", r.EvidenceTier)
	}
	if len(r.DerivedScores) != len(p.Definitions) || r.DerivedScores[0].TP != 80 || r.DerivedScores[0].TN != 80 {
		t.Fatalf("derived counts = %+v", r.DerivedScores)
	}
	pack, err := Promote(p, r)
	if err != nil {
		t.Fatal(err)
	}
	if pack.EvidenceTier != rulepack.EvidenceDerivedExecution {
		t.Fatalf("pack tier = %q", pack.EvidenceTier)
	}
}

// A person's labels keep their own grade. The derived grade never stands
// in for them.
func TestHumanLabelsKeepTheirOwnGrade(t *testing.T) {
	s, ls, data := learningFixture()
	p, err := Propose(ls, s, data)
	if err != nil {
		t.Fatal(err)
	}
	if p.EvidenceTier != rulepack.EvidenceHumanLabeled {
		t.Fatalf("proposal tier = %q", p.EvidenceTier)
	}
	r, err := Evaluate(p, s, ls, data, p.Gate)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed || r.EvidenceTier != rulepack.EvidenceHumanLabeled {
		t.Fatalf("evaluation = %+v", r)
	}
	if r.DerivedPassed {
		t.Fatal("empty derived grade reported as passed")
	}
	pack, err := Promote(p, r)
	if err != nil {
		t.Fatal(err)
	}
	if pack.EvidenceTier != rulepack.EvidenceHumanLabeled {
		t.Fatalf("pack tier = %q", pack.EvidenceTier)
	}
}

// A rule learned from a person's label and a rule derived from a grader's
// verdict are not the same claim. One proposal cannot hold both.
func TestMixedLabelSourcesCannotShareOneProposal(t *testing.T) {
	s, ls, data := learningFixture()
	ls[0].Source = eval.LabelSourceDerived
	ls[0].Derivation = &eval.Derivation{Method: "execution_oracle", Oracle: "grader"}
	ls = append(ls, eval.Label{Session: "train", Start: 0, End: 2, Symptom: "S4", Justified: "yes", Labeler: "trainer"})
	if _, err := Propose(ls, s, data); err == nil {
		t.Fatal("mixed label sources accepted in one proposal")
	}
}

// A derived label without a derivation is an anonymous claim. It is not
// evidence, and the proposal refuses it.
func TestDerivedLabelWithoutDerivationIsRefused(t *testing.T) {
	s, ls, data := learningFixture()
	ls[0].Source = eval.LabelSourceDerived
	if _, err := Propose(ls, s, data); err == nil {
		t.Fatal("derived label without a derivation accepted")
	}
}

// The two grades are counted apart. A candidate that the grader's verdicts
// confirm is not silently counted as one a person confirmed.
func TestGradesAreCountedApart(t *testing.T) {
	s, ls, data := learningFixture()
	derived := derive(ls)
	// Half of the holdout spans are labeled by a person, half by a
	// grader. The person saw the positives and the grader the negatives.
	for i := 1; i < len(ls); i++ {
		if i%2 == 0 {
			ls[i] = derived[i]
		}
	}
	p, err := Propose(ls, s, data)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Evaluate(p, s, ls, data, p.Gate)
	if err != nil {
		t.Fatal(err)
	}
	if r.Passed && r.DerivedPassed {
		t.Fatal("both grades reported as passed on one set of labels")
	}
	if r.Passed == r.DerivedPassed {
		t.Fatal("the two grades did not separate")
	}
}

// A live probe can refuse what the holdout allowed. A candidate that leaves
// the agent with nothing is not a rule to adopt, and the holdout alone may
// not see it.
func TestProbeRefusalBlocksPromotion(t *testing.T) {
	s, ls, data := learningFixture()
	p, err := Propose(ls, s, data)
	if err != nil {
		t.Fatal(err)
	}
	veto := &eval.ProbeVerdict{Version: "rule-probe/1", CandidateID: "candidate", Verdict: eval.VerdictHurt,
		Veto: true, Reason: "후보 팔에서 에이전트가 아무것도 내지 못했다", BaseTrials: 40, CandidateTrials: 40}
	r, err := EvaluateWithProbe(p, s, ls, data, p.Gate, veto)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed {
		t.Fatal("the holdout gate should still pass")
	}
	if _, err := Promote(p, r); err == nil {
		t.Fatal("a vetoed probe was promoted")
	}
	// A probe that says the candidate helped does not block the promotion.
	veto = &eval.ProbeVerdict{Version: "rule-probe/1", CandidateID: "candidate", Verdict: eval.VerdictHelped,
		Reason: "후보 팔의 채점기 통과가 늘었다", BaseTrials: 40, CandidateTrials: 40}
	r, err = EvaluateWithProbe(p, s, ls, data, p.Gate, veto)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Promote(p, r); err != nil {
		t.Fatal(err)
	}
}
