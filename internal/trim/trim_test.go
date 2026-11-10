package trim

import (
	"slices"
	"testing"
)

func TestNewWordsAllowsJoiningButNotNewContent(t *testing.T) {
	before := "검사를 돌린다. 그리고 결과를 본다. 실패하면 로그를 연다. 설치는 npm install로 한다."
	if got := NewWords(before, "검사를 돌리고 결과를 본다. 설치는 npm install로 한다."); len(got) != 0 {
		t.Errorf("joining sentences and deleting one adds nothing: %v", got)
	}
	got := NewWords(before, "검증을 돌리고 결과를 본다. 설치는 pnpm install로 한다.")
	if !slices.Contains(got, "검증을") || !slices.Contains(got, "pnpm") {
		t.Errorf("a changed word is new content: %v", got)
	}
	if got := NewWords("책을 읽는다.", "채를 읽는다."); len(got) != 1 {
		t.Errorf("one-syllable words must match exactly: %v", got)
	}
}
