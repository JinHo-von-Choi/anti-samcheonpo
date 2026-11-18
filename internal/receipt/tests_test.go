package receipt

import (
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
)

func TestTestSection(t *testing.T) {
	if got := TestSection(TestInput{}); len(got) != 1 || got[0] != "시험 변경 없음" {
		t.Fatalf("no changes: %q", got)
	}
	got := TestSection(TestInput{
		Files: []detect.TestFileChange{{Path: "tests/test_a.py", Created: true, AssertDelta: 2}, {Path: "tests/test_b.py", AssertDelta: -1, SkipsAdded: 1, Mocks: []string{"app.pricing.total"}}},
		Baselines: []BaselineLine{
			{Outcome: "verified", Tests: []string{"tests/test_a.py"}, Failed: []string{"tests/test_a.py::test_a"}},
			{Outcome: "baseline_pass", Tests: []string{"tests/test_b.py"}},
			{Outcome: "not_compared", Tests: []string{"tests/test_c.py"}, Reason: "time_mismatch"},
		},
		Trivial:  []string{"tests/test_a.py: test_x: no_assertion"},
		Verdicts: []detect.Signal{{Rule: "s5.test_weakening", Level: detect.L1, Facts: map[string]any{"kind": "skip_added", "path": "tests/test_b.py"}}},
	})
	text := strings.Join(got, "\n")
	for _, want := range []string{
		"바뀐 시험 파일 2개(새 파일 1개, 지운 파일 0개), 단언 2개 늘고 1개 줄었다, 새 skip 1개, mock 대상: app.pricing.total.",
		"시험 `tests/test_a.py`는 변경 전 코드에서는 실패하고 지금은 통과한다.",
		"시험 `tests/test_b.py`는 변경 전 코드에서도 통과한다.",
		"시험 `tests/test_c.py`의 변경 전 대조를 하지 못했다: 통과한 뒤 파일이 바뀌어",
		"시험 약화: skip 추가",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "확인하는 것이 없는 새 시험") {
		t.Error("a self-written test note shown next to a baseline comparison")
	}
	if strings.Contains(text, "시험 약화 판정 없음") {
		t.Error("no-finding line shown with a finding")
	}
}
