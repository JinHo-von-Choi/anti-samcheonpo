package judge

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/alignment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"strings"
	"testing"
)

func TestStructuredJudgeRejectsInventedEvidenceAndFreeProse(t *testing.T) {
	in := Input{EvidenceIDs: []int64{1, 2}}
	for _, text := range []string{"sure, on_track", `{"label":"finished"}`, `{"label":"unknown","issues":[{"kind":"partial_completion","evidence":[1]}]}`} {
		v := ParseVerdict(text, in)
		if v.Label != Unknown || len(v.Issues) != 0 {
			t.Fatalf("%+v", v)
		}
	}
	v := ParseVerdict(`{"label":"drift","issues":[{"kind":"partial_completion","evidence":[1]},{"kind":"requirement_substitution","evidence":[99]},{"kind":"run_command","evidence":[2]}]}`, in)
	if v.Label != Drift || len(v.Issues) != 1 || v.Issues[0].Kind != "partial_completion" {
		t.Fatal(v)
	}
}

func TestClaimsExcludeCodeAndDoNotInferCompletion(t *testing.T) {
	got := ClaimSummary("```go\n// implementation complete SECRET\n```\n요청한 기능 구현을 완료했습니다.")
	if got == "" || strings.Contains(got, "SECRET") || !strings.Contains(got, "unverified") {
		t.Fatal(got)
	}
	if ClaimSummary("일반 상태 설명") == "" {
	} else {
		t.Fatal("unrelated prose becomes claim")
	}
}

func TestStructuredIssueMustLinkClaimAndActualRequirementResult(t *testing.T) {
	exit := 1
	b := &event.Reliability{TaskID: "t", Revision: 1, AuthorityHash: "auth"}
	r := *b
	r.Complete = true
	r.InputHash = "input"
	r.EnvironmentHash = "env"
	r.CheckID = "check"
	c := &contract.Contract{Goal: "구현", Done: []contract.Check{{ID: "check", Check: "pytest"}}}
	g := alignment.Build(c.Goal, c, true, []*event.Event{{Seq: 1, Kind: event.KindTool, Category: event.CatVerify, ExitCode: &exit, Reliability: &r}, {Seq: 2, Kind: event.KindMessage, Text: "전체 완료했습니다."}}, b)
	in := Input{Graph: &g, EvidenceIDs: []int64{1, 2}}
	valid := `{"label":"drift","issues":[{"kind":"partial_completion","evidence":[1,2],"requirements":["check"],"claims":[2],"results":[1]}]}`
	if len(ParseVerdict(valid, in).Issues) != 1 {
		t.Fatal("related grounded issue rejected")
	}
	for _, bad := range []string{strings.Replace(valid, `"claims":[2]`, `"claims":[1]`, 1), strings.Replace(valid, `"check"`, `"invented"`, 1), strings.Replace(valid, `"results":[1]`, `"results":[2]`, 1), strings.Replace(valid, `"evidence":[1,2]`, `"evidence":[2]`, 1)} {
		if len(ParseVerdict(bad, in).Issues) != 0 {
			t.Fatal("unsupported semantic relation accepted")
		}
	}
}
