package intent

import (
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
)

func TestInferOutputReadsAmounts(t *testing.T) {
	cases := []struct {
		goal, element string
		want          int
		found         bool
	}{
		{"저장 버튼 2개 넣어 줘", "버튼", 2, true},
		{"버튼은 저장과 취소 두 개로 해 줘", "버튼", 2, true},
		{"저장과 취소 버튼 만들어 줘", "버튼", 2, true},
		{"세 줄로 요약해 줘", "줄", 3, true},
		{"버튼 3개 넣어 줘\n버튼 추가 없이 다시 만들어 줘", "버튼", 0, true},
		{"버튼 2개 넣어 줘\n더 짧게 해 줘", "버튼", 2, true},
		{"한 줄기 빛이 든다", "줄", 0, false},
		{"세 절차를 거쳐 배포해 줘", "절", 0, false},
		{"네 메뉴얼을 확인해 줘", "메뉴", 0, false},
		{"버튼 둘러보기 화면 고쳐 줘", "버튼", 0, false},
		{"공유 버튼과 도움말 링크 넣어 줘", "링크", 0, false},
	}
	for _, c := range cases {
		got, ok := InferOutput(c.goal).Count(c.element)
		if ok != c.found || got != c.want {
			t.Errorf("%q %s: got %d %v, want %d %v", c.goal, c.element, got, ok, c.want, c.found)
		}
	}
}

func TestFollowupKeepsWhatItNames(t *testing.T) {
	if got := InferOutput("더 짧게 해 줘. 설명은 그대로 두고").Keep; len(got) != 1 || got[0] != "설명" {
		t.Fatalf("keep: %v", got)
	}
}

func TestInferOutputIgnoresPastedMaterial(t *testing.T) {
	if got := InferOutput(strings.Repeat("참고 자료 ", 300) + "버튼 2개"); got.Reader != "" || len(got.Amounts) != 0 {
		t.Fatalf("numbers inside pasted material: %+v", got)
	}
}

func TestInferOutputKeepsTheLatestReader(t *testing.T) {
	if got := InferOutput("설치 방법 문서 써 줘\n어디까지 됐어?").Reader; got != ReaderStatus {
		t.Fatalf("the latest request sets the reader: %q", got)
	}
	if got := InferOutput("타이머 버그 고쳐 줘").Reader; got != "" {
		t.Fatalf("a request with no reader: %q", got)
	}
}

func TestGoalCardShowsTheReaderAndAmounts(t *testing.T) {
	text := GoalCard(nil, contract.Acceptance{}, "설정 화면에 버튼 2개 넣어 줘", nil).Text()
	for _, want := range []string{"결과물: 화면 - 사용자가 여기서 마치는 일 (추정)", "요청한 양: 버튼 2개"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q missing from:\n%s", want, text)
		}
	}
}

func TestAnswerLinesNeedsAnAnswerLength(t *testing.T) {
	if n, ok := AnswerLines("세 줄로 답해 줘"); !ok || n != 3 {
		t.Fatalf("an answer length: %d %v", n, ok)
	}
	for _, req := range []string{"이 함수 3줄 고쳐 줘", "본문은 panic 한 줄로 두고 빌드해 줘", "두 사건은 한 줄로 이어진다.", strings.Repeat("긴 지시문 ", 60) + "세 줄로 요약해 줘"} {
		if n, ok := AnswerLines(req); ok {
			t.Fatalf("%q does not set the length of the answer: %d", req, n)
		}
	}
}
