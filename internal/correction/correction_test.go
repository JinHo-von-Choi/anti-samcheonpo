package correction

import "testing"

func TestExtract(t *testing.T) {
	cases := []struct {
		msg        string
		action     string
		persistent bool
	}{
		{"앞으로 PR 만들지 마", "pr", true},
		{"이번엔 푸시는 하지 마", "push", false},
		{"문서만 바꿨으면 시험 돌리지 마", ActTestAfterDocs, false},
		{"현재 브랜치에 커밋하라고 몇 번을 말해", ActNewBranch, true},
		{"`/mnt/backup` 쓰지 마", ActAvoidPath, false},
		{"한국어로 답하라고", ActKoreanAnswer, false},
		{"릴리즈까지 하라니까", "release", false},
	}
	for _, c := range cases {
		got := Extract(c.msg)
		found := false
		for _, x := range got {
			if x.Action == c.action && x.Persistent == c.persistent {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: %+v", c.msg, got)
		}
	}
	if got := Extract("PR 만들어 줘"); len(got) != 0 {
		t.Errorf("an ask became a correction: %+v", got)
	}
}

func TestBreaksKeepsTheCurrentRequest(t *testing.T) {
	c := Correction{Kind: KindForbid, Action: "pr"}
	if !Breaks(c, Call{Cmd: "gh pr create --fill"}) {
		t.Fatal("the rule missed the call")
	}
	if Breaks(c, Call{Cmd: "gh pr create --fill", Requested: func(a string) bool { return a == "pr" }}) {
		t.Fatal("the current request did not win")
	}
	docs := Correction{Kind: KindForbid, Action: ActTestAfterDocs}
	if !Breaks(docs, Call{Verify: true, DocsOnly: true}) || Breaks(docs, Call{Verify: true}) {
		t.Fatal("documentation-only rule")
	}
}
