package detect

import "testing"

func TestBuriedRemainder(t *testing.T) {
	if !buriedRemainder([]string{"CLI에 영어를 붙였습니다.", "남은 제한은 다음과 같습니다."}) {
		t.Error("a finished opening with remaining work later is the pattern")
	}
	if buriedRemainder([]string{"아직 전부 끝나지 않았습니다.", "남은 작업은 다음과 같습니다."}) {
		t.Error("an opening that says it is not done is the wanted form")
	}
}

func TestFullRequestWording(t *testing.T) {
	for text, want := range map[string]bool{"전체 시험 돌려": true, "run the full tests": true, "전체 시험 돌리지 마": false, "don't run the full test suite": false, "버튼 고쳐": false} {
		if got := fullRequestRe.MatchString(text) && !fullRequestNotRe.MatchString(text); got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
}
