package detect

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"testing"
)

func TestReviewedPackUsesSameMatcherAndCannotBlock(t *testing.T) {
	b := newB(t, "근거 조사", nil)
	d := rulepack.Definition{Symptom: "S4", Tool: event.ToolRead, Category: event.CatExplore, Minimum: 3, Window: 6}
	d.ID = rulepack.Digest(d)
	if err := b.e.ConfigureCandidates(rulepack.Pack{Definitions: []rulepack.Definition{d}, EvaluationDigest: "evaluation"}); err != nil {
		t.Fatal(err)
	}
	var last []Signal
	for i := 0; i < 3; i++ {
		last = b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"paper"}, Text: "identical information"})
	}
	found := findBehavior(last, d.RuleID())
	if found == nil || found.Level != L1 || !found.Estimate || found.WasteMicro != 0 || !policy.AdviceOnly(d.RuleID()) {
		t.Fatalf("%+v", last)
	}
	if policy.RolloutRoute("validated", d.RuleID(), map[string]string{d.RuleID(): "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}) != "advice" {
		t.Fatal("learned rule gained block authority")
	}
}

// A rule learned from machine-derived evidence says so in its fact. A
// reader that cannot tell the two claims apart reads the weaker one as the
// stronger one.
func TestCandidateFactNamesItsEvidenceTier(t *testing.T) {
	for _, tier := range []string{rulepack.EvidenceHumanLabeled, rulepack.EvidenceDerivedExecution} {
		t.Run(tier, func(t *testing.T) {
			b := newB(t, "근거 조사", nil)
			d := rulepack.Definition{Symptom: "S4", Tool: event.ToolRead, Category: event.CatExplore, Minimum: 3, Window: 6}
			d.ID = rulepack.Digest(d)
			pack := rulepack.Pack{Definitions: []rulepack.Definition{d}, EvaluationDigest: "evaluation", EvidenceTier: tier}
			if err := b.e.ConfigureCandidates(pack); err != nil {
				t.Fatal(err)
			}
			var last []Signal
			for i := 0; i < 3; i++ {
				last = b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"paper"}, Text: "identical information"})
			}
			found := findBehavior(last, d.RuleID())
			if found == nil {
				t.Fatal("candidate did not fire")
			}
			want := "labeled_candidate"
			if tier == rulepack.EvidenceDerivedExecution {
				want = "derived_candidate"
			}
			if found.Facts["source"] != want {
				t.Fatalf("fact source = %v, want %q", found.Facts["source"], want)
			}
			if found.Facts["evidence_tier"] != tier {
				t.Fatalf("fact tier = %v, want %q", found.Facts["evidence_tier"], tier)
			}
		})
	}
}
