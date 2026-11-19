package reproach

import (
	"slices"
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		msg   string
		want  bool
		theme string
	}{
		{"문서만 바꿨는데 무슨 또 테스트야 몇 번을 말해", true, "test_loop"},
		{"릴리즈하라고 했잖아", true, "deliverable_missing"},
		{"시발 요점만 말해", true, ""},
		{"왜 니 좆대로 영어로 말해", true, "language"},
		{"왜 버러지같이 구냐고", true, ""},
		{"게을러빠진놈", true, ""},
		{"라이선스는 대충 정하고 구현이나 똑바로 해", true, ""},
		{"화면을 쳐다보면 버튼이 안 보여", false, ""},
		{"몇 번 실행했어?", false, ""},
		{"이 함수 뭐하는 거야?", false, ""},
		{"<pasted_content>하라고 했잖아</pasted_content> 이 로그 봐줘", false, ""},
		{"> 몇 번을 말해\n이 인용문의 출처 찾아줘", false, ""},
		{"/samcheonpo:keep wrong", false, ""},
		{strings.Repeat("가", MaxRunes) + " 시발", false, ""},
	}
	for _, c := range cases {
		r := Detect(c.msg)
		if r.Scolding != c.want || c.theme != "" && !slices.Contains(r.Themes, c.theme) {
			t.Errorf("%.40q: %+v", c.msg, r)
		}
		if r.Scolding && strings.Contains(r.Hash, "하라고") {
			t.Error("the hash keeps the text")
		}
	}
}

func TestSplitMatchesThemesToRules(t *testing.T) {
	matched, missed := Split([]string{"test_loop", "deliverable_missing"}, []string{"s1.identical_rerun", "s3.out_of_scope"})
	if !slices.Equal(matched, []string{"s1.identical_rerun"}) || !slices.Equal(missed, []string{"deliverable_missing"}) {
		t.Fatalf("%v %v", matched, missed)
	}
	if m, x := Split(nil, []string{"s1.identical_rerun"}); m != nil || x != nil {
		t.Fatal("a scolding without a theme confirmed a rule")
	}
}
