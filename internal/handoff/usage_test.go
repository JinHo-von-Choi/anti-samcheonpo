package handoff

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"testing"
)

func TestUsageDeduplicatesInclusiveParentAndExactReplay(t *testing.T) {
	links := []intent.SessionLink{
		{TaskID: "t", Agent: "claude", SessionID: "root", IncludesChildren: true},
		{TaskID: "t", Agent: "codex", SessionID: "child", ParentAgent: "claude", ParentSessionID: "root"},
	}
	rootTokens, childTokens, micro := int64(100), int64(30), int64(700)
	samples := []Usage{{Session: SessionRef{Agent: "claude", ID: "root"}, Tokens: &rootTokens, APIEquivalentMicro: &micro}, {Session: SessionRef{Agent: "codex", ID: "child"}, Tokens: &childTokens, APIEquivalentMicro: &micro}}
	samples = append(samples, samples[0])
	result := AggregateUsage("t", links, samples)
	if result.Tokens == nil || *result.Tokens != 100 || result.APIEquivalentMicro == nil || *result.APIEquivalentMicro != 700 || len(result.CoveredByParent) != 1 {
		t.Fatalf("%+v", result)
	}
	links[0].IncludesChildren = false
	result = AggregateUsage("t", links, samples)
	if result.Tokens == nil || *result.Tokens != 130 || *result.APIEquivalentMicro != 1400 {
		t.Fatalf("%+v", result)
	}
	samples[1].APIEquivalentMicro = nil
	result = AggregateUsage("t", links, samples)
	if result.Tokens == nil || result.APIEquivalentMicro != nil {
		t.Fatalf("missing price invented: %+v", result)
	}
}

func TestUsageDoesNotSumUnknownParentsCyclesOrConflictingSamples(t *testing.T) {
	base := []intent.SessionLink{{TaskID: "t", Agent: "a", SessionID: "root"}, {TaskID: "t", Agent: "b", SessionID: "child", ParentAgent: "a", ParentSessionID: "root"}}
	n := int64(1)
	samples := []Usage{{Session: SessionRef{Agent: "a", ID: "root"}, Tokens: &n, APIEquivalentMicro: &n}, {Session: SessionRef{Agent: "b", ID: "child"}, Tokens: &n, APIEquivalentMicro: &n}}
	for _, change := range []func([]intent.SessionLink){
		func(l []intent.SessionLink) { l[1].ParentAgent = ""; l[1].ParentSessionID = "" },
		func(l []intent.SessionLink) { l[1].ParentSessionID = "missing" },
		func(l []intent.SessionLink) { l[1].ParentAgent = "b"; l[1].ParentSessionID = "child" },
		func(l []intent.SessionLink) { l[1].TaskID = "other" },
	} {
		links := append([]intent.SessionLink(nil), base...)
		change(links)
		result := AggregateUsage("t", links, samples)
		if result.Tokens != nil || result.APIEquivalentMicro != nil || len(result.Unknown) == 0 {
			t.Fatalf("invalid graph summed: %+v", result)
		}
	}
	other := int64(2)
	samples = append(samples, Usage{Session: samples[0].Session, Tokens: &other, APIEquivalentMicro: &n})
	if result := AggregateUsage("t", base, samples); result.Tokens != nil {
		t.Fatal("conflicting duplicate counted")
	}
}
