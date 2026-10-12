package alignment

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"testing"
)

func graphFixture() (*contract.Contract, *event.Reliability, []*event.Event) {
	c := &contract.Contract{Goal: "로그인 기능 구현", Done: []contract.Check{{ID: "login", Check: "pytest tests/login.py"}, {ID: "logout", Manual: "로그아웃 동작"}}}
	b := &event.Reliability{TaskID: "t", Revision: 1, AuthorityHash: "authority"}
	r := *b
	r.Complete = true
	r.CheckID = "login"
	r.InputHash = "input"
	r.EnvironmentHash = "env"
	exit := 1
	es := []*event.Event{{Seq: 1, PromptOrigin: "user", Kind: event.KindPrompt, Text: "로그인 기능 구현"}, {Seq: 2, Kind: event.KindTool, Tool: "checkpoint_check", Category: event.CatVerify, ExitCode: &exit, Reliability: &r}, {Seq: 3, Kind: event.KindMessage, Text: "요청한 작업을 모두 완료했습니다."}}
	return c, b, es
}
func TestRequirementClaimNeedsRelatedCurrentFailure(t *testing.T) {
	c, b, es := graphFixture()
	g := Build(c.Goal, c, true, es, b)
	if g.Completion != "unknown" || g.Requirements[0].Status != "failure_observed" || !g.ValidIssue("partial_completion", []string{"login"}, []int64{3}, []int64{2}, nil) {
		t.Fatal(g)
	}
	if g.ValidIssue("partial_completion", []string{"logout"}, []int64{3}, []int64{2}, nil) || g.ValidIssue("partial_completion", []string{"invented"}, []int64{3}, []int64{2}, nil) {
		t.Fatal("unrelated result/requirement accepted")
	}
	zero := 0
	es[1].ExitCode = &zero
	g = Build(c.Goal, c, true, es, b)
	if g.Requirements[0].Status != "passing_evidence_candidate" || g.ValidIssue("partial_completion", []string{"login"}, []int64{3}, []int64{2}, nil) {
		t.Fatal("passing observation is contradiction")
	}
	es[1].Reliability.Revision = 2
	g = Build(c.Goal, c, true, es, b)
	if g.Requirements[0].Status != "unknown" {
		t.Fatal("other revision created progress")
	}
}
func TestDisclosedPartialCodeAndNewExternalPermissionProtected(t *testing.T) {
	c, b, es := graphFixture()
	for _, claim := range []string{"일부만 구현했고 아직 남았습니다.", "```go\n// 완료했습니다 SECRET\n```", "운영 배포를 진행할까요?"} {
		es[2].Text = claim
		g := Build(c.Goal, c, true, es, b)
		if g.ValidIssue("partial_completion", []string{"login"}, []int64{3}, []int64{2}, nil) || g.ValidIssue("repeated_permission", []string{"login"}, []int64{3}, nil, []int64{1}) {
			t.Fatal(claim)
		}
	}
	es[2].Text = "구현을 계속할까요?"
	g := Build(c.Goal, c, true, es, b)
	if !g.ValidIssue("repeated_permission", []string{"login"}, []int64{3}, nil, []int64{1}) {
		t.Fatal(g)
	}
}
func TestFileActivityDoesNotFulfillEveryRequirement(t *testing.T) {
	c, b, es := graphFixture()
	es = append(es, &event.Event{Seq: 4, Kind: event.KindTool, Category: event.CatProduce, Paths: []string{"unrelated.md"}})
	g := Build(c.Goal, c, true, es, b)
	if g.Requirements[0].Status != "inputs_changed" || g.Requirements[1].Status != "unknown" || g.Completion != "unknown" {
		t.Fatal(g)
	}
}

func TestMetaToolAndUnknownOriginCannotEstablishUserAuthorization(t *testing.T) {
	c, b, es := graphFixture()
	es[2].Text = "구현을 계속할까요?"
	for _, origin := range []string{"meta", "", "tool"} {
		es[0].PromptOrigin = origin
		g := Build(c.Goal, c, true, es, b)
		if g.ValidIssue("repeated_permission", []string{"login"}, []int64{3}, nil, []int64{1}) {
			t.Fatal("non-user input granted approval basis")
		}
	}
}

func TestAcceptedConfirmationNeedsTrustedOriginAndCurrentAuthority(t *testing.T) {
	c, b, es := graphFixture()
	es[0].Text = "맞아요"
	es[0].Summary = "explicit intent command"
	es[2].Text = "구현을 계속할까요?"
	g := Build(c.Goal, c, true, es, b)
	if !g.ValidIssue("repeated_permission", []string{"login"}, []int64{3}, nil, []int64{1}) {
		t.Fatal("accepted user confirmation lost")
	}
	b.AuthorityHash = ""
	g = Build(c.Goal, c, true, es, b)
	if g.ValidIssue("repeated_permission", []string{"login"}, []int64{3}, nil, []int64{1}) {
		t.Fatal("revoked confirmation reused")
	}
}

func TestLaterRequestAndLaterFailureCannotRewriteEarlierClaims(t *testing.T) {
	c, b, es := graphFixture()
	es[2].Text = "구현을 계속할까요?"
	es = append(es, &event.Event{Seq: 4, Kind: event.KindPrompt, PromptOrigin: "user", Text: "현황만 설명해"})
	g := Build(c.Goal, c, true, es, b)
	if g.ValidIssue("repeated_permission", []string{"login"}, []int64{3}, nil, []int64{1}) {
		t.Fatal("old authorization or claim reused after new request")
	}
	c, b, es = graphFixture()
	later := *es[1]
	later.Seq = 4
	es = append(es, &later)
	g = Build(c.Goal, c, true, es, b)
	if g.ValidIssue("partial_completion", []string{"login"}, []int64{3}, []int64{2}, nil) {
		t.Fatal("later failure used to contradict earlier claim")
	}
}
