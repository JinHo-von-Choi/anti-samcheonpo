package intent

import "testing"

func TestConversationDoesNotReviseWork(t *testing.T) {
	for _, text := range []string{"context", "뭐해", "진행 상황?", "어디까지 됐어?", "응", "고마워"} {
		for _, has := range []bool{false, true} {
			if got := ClassifyChange(User, text, has); got.Kind != Ignored {
				t.Errorf("%q has=%v: %+v", text, has, got)
			}
		}
	}
	for _, text := range []string{"상태 화면 추가해", "좋아, 운영에 배포해", "진행 상황을 파일에 기록해", "플랜 구현해"} {
		if got := ClassifyChange(User, text, true); got.Kind != Followup {
			t.Errorf("lost request %q: %+v", text, got)
		}
	}
}
