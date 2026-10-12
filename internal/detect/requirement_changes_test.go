package detect

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"testing"
)

func TestRequestBoundSubstitutionUsesActualNativeDiffAndProtectsAllowedMocks(t *testing.T) {
	for _, tc := range []struct {
		name, goal, path string
		added, removed   []string
		want             bool
	}{
		{"real_to_constant", "실제 API 연동을 구현하고 mock으로 대체하지 마세요", "src/login.py", []string{`return {"ok": True}`}, []string{`return requests.post(endpoint).json()`}, true},
		{"allowed", "실제 API 연동도 검토하되 mock 사용해도 됩니다", "src/login.py", []string{`return {"ok": True}`}, []string{`return requests.post(endpoint).json()`}, false},
		{"test_mock", "실제 API 연동을 구현해", "tests/test_login.py", []string{`return MockResponse()`}, []string{`return requests.post(endpoint)`}, false},
		{"new_network", "실제 API 연동을 구현해", "src/login.py", []string{`return client.send(endpoint)`}, []string{`return requests.post(endpoint)`}, false},
		{"no_removed_evidence", "실제 API 연동을 구현해", "src/login.py", []string{`return {"ok": True}`}, nil, false},
		{"quoted_markers", "실제 API 연동을 구현해", "src/login.py", []string{`description = "return MockResponse()"`}, []string{`description = "requests.post(endpoint)"`}, false},
		{"weaken", "테스트를 약화하지 말고 오류를 수정해", "tests/test_login.py", []string{`assert True`}, []string{`assert login() == "ok"`}, true},
		{"legitimate_expected_update", "테스트를 유지하고 새 스펙의 기대값을 반영해", "tests/test_login.py", []string{`assert login() == "new"`}, []string{`assert login() == "old"`}, false},
		{"feature_removed", "기능을 유지하고 `login` 함수를 삭제하지 마세요", "src/login.py", []string{`def logout(): pass`}, []string{`def login(): pass`}, true},
		{"feature_forwarder", "기능을 유지하고 `login` 함수를 삭제하지 마세요", "src/login.py", []string{`login = provider.login`}, []string{`def login(): pass`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEngine(config.Default(), nil, false, "audit", t.TempDir(), "s", tc.goal)
			ev := &event.Event{ResultObserved: true, Seq: 1, Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{tc.path}, Patch: []event.PatchFile{{Path: tc.path, Added: tc.added, Removed: tc.removed}}}
			vs := e.Observe(ev)
			found := false
			for _, v := range vs {
				if v.Rule == "s3.requirement_substitution" {
					found = true
					if v.Level != L1 || !v.Estimate || v.WasteMicro != 0 {
						t.Fatal(v)
					}
				}
			}
			if found != tc.want {
				t.Fatalf("found %v want %v: %+v", found, tc.want, vs)
			}
			if tc.want && (len(ev.RequirementChanges) != 1 || ev.RequirementChanges[0].PathHash == tc.path) {
				t.Fatal("missing private structured evidence")
			}
		})
	}
}

func TestUnfinishedPatchCannotBecomeRequirementViolation(t *testing.T) {
	e := NewEngine(config.Default(), nil, false, "audit", t.TempDir(), "s", "실제 API 연동을 구현해")
	ev := &event.Event{Seq: 1, Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{"src/api.py"}, Patch: []event.PatchFile{{Path: "src/api.py", Removed: []string{`return requests.get(url)`}, Added: []string{`return {"ok": True}`}}}}
	if v := findBehavior(e.Observe(ev), "s3.requirement_substitution"); v != nil {
		t.Fatal("unpaired attempt treated as applied change")
	}
	if len(ev.Decisions) == 0 {
		t.Fatal("missing result not explained")
	}
}
