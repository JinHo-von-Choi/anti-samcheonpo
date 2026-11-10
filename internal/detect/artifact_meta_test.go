package detect

import (
	"strings"
	"testing"
	"time"
)

func TestMetaSentencesJudgeTheSentenceNotTheWord(t *testing.T) {
	flagged := map[string]string{
		"검토 의견을 반영해 문서를 다시 썼다.":              MetaRequestRef,
		"이번 릴리즈에 파일명 충돌 검사를 추가했다.":           MetaSelfRef,
		"설치 절차를 정리한 표다.":                     MetaSelfRef,
		"회귀 검사·SDK 30개 모두 통과했다.":             MetaVerifyLog,
		"실제 SDK 그래프로 검증했으며 모델 효과를 입증하지 않았다.": MetaWorkReport,
		"README 문구를 현행화함":                    MetaWorkReport,
	}
	for s, kind := range flagged {
		h := MetaSentences(s)
		if len(h) != 1 || h[0].Kind != kind {
			t.Errorf("%q: want %s, got %+v", s, kind, h)
		}
	}
	for _, s := range []string{
		"이 명령은 중복 항목을 정리한다.",
		"설치가 끝났는지 확인했다면 다음 단계로 넘어간다.",
		"fix: preserve verification failures and completion notices",
		"실패한 검증 결과를 보존한다.",
		"검사 결과를 JSON으로 출력한다.",
		"`--since` 옵션을 추가했다.",
		"로그인에 실패하면 다시 시도 버튼을 표시한다.",
		"해석 기준은 타로 마스터 두 명의 자문을 반영했다.",
		"운영 반영은 사용자가 지시할 때만 한다.",
		`MetaWorkReport는 "~를 정리했다"처럼 글쓴이가 한 일을 말하는 문장이다.`,
		"Stephen Lindsay는 1993년 출처 감시 이론을 정리했다.",
		"test files the agent itself created in this session.",
		"feat: 행 내용을 읽고 지시대로 AI가 값을 채우는 AI 속성을 더한다",
		"| Transport | HTTPS verified by rustls.",
		"faking a real service to make tests pass",
		"fixed input per request in an empty directory",
		"계약 파일 자체가 이번 변경에 들어 있으면 기준 변경 검토로 넘긴다.",
		"정리에 실패해도 검증 결과를 통과로 바꾸지 않는다.",
	} {
		if h := MetaSentences(s); len(h) != 0 {
			t.Errorf("%q describes the thing, not the work: %+v", s, h)
		}
	}
}

func TestSharedTextReadsDocumentsAndCommentsOnly(t *testing.T) {
	if got := SharedText("README.md", []string{"- 검토 의견을 반영했다"}); got == "" {
		t.Error("a README line is shared text")
	}
	if got := SharedText("internal/x.go", []string{`	// 리뷰 의견을 반영해 정리했다`, `	x := "검토를 반영했다"`}); strings.Contains(got, "x :=") || !strings.Contains(got, "리뷰 의견") {
		t.Errorf("only the comment of a source line is read: %q", got)
	}
	for _, p := range []string{"docs/plans/2026-11-04-plan.md", "docs/190-playtest-fix-plan.md", "docs/192-온보딩플랜.md", "CLAUDE.md", ".claude/notes.md", "/tmp/scratch.md", "memory/x.md"} {
		if got := SharedText(p, []string{"검토 의견을 반영했다"}); got != "" {
			t.Errorf("%s is a note for the agent: %q", p, got)
		}
	}
}

func TestArtifactMetaFollowsSentencesFromWriteToPush(t *testing.T) {
	b := newB(t, "README 사용법 고쳐 줘", nil)
	v := fired(b.write("README.md", "h1", []string{"설치 절차를 정리한 표다."}, nil), RuleArtifactMeta)
	if v == nil || v.Facts["kind"] != "document" || v.Facts["count"] != 1 {
		t.Fatalf("a meta sentence written into the README: %+v", v)
	}
	if v := fired(b.write("docs/plans/next.md", "h2", []string{"검토 의견을 반영했다."}, nil), RuleArtifactMeta); v != nil {
		t.Fatalf("a plan note is not shared text: %+v", v)
	}
	s := b.pre(`git commit -m "docs: 설치 순서"`)
	if s == nil || s.Rule != RuleArtifactMeta || s.Facts["via"] != "commit" || !strings.Contains(s.Facts["candidates"].(string), "README.md: ") {
		t.Fatalf("committing while the README still carries the sentence: %+v", s)
	}
	b.write("README.md", "h3", []string{"| 단계 | 명령 |"}, []string{"설치 절차를 정리한 표다."})
	if s := b.pre(`git commit -m "docs: 설치 순서"`); s != nil && s.Rule == RuleArtifactMeta {
		t.Fatalf("the sentence was removed: %+v", s)
	}

	b.write("README.md", "h4", []string{"설치 절차를 정리한 표다."}, []string{"| 단계 | 명령 |"})
	b.run(`git commit -m "docs: 설치 순서"`, 0, "", time.Second)
	if s := b.pre(`git commit -m "feat: 다른 기능"`); s != nil && s.Rule == RuleArtifactMeta {
		t.Fatalf("a sentence already judged at a commit waits for the push, not every later commit: %+v", s)
	}
	b.run("git push origin main", 0, "", time.Second)

	b.run("git commit -m \"$(cat <<'EOF'\nfix: 종료 알림 보존\n\n리뷰 의견을 반영해 정리했다.\nEOF\n)\"", 0, "", time.Second)
	s = b.pre("git push origin main")
	if s == nil || s.Rule != RuleArtifactMeta || s.Facts["via"] != "push" || s.Facts["target"] != "publish" {
		t.Fatalf("pushing a commit whose message carries the sentence: %+v", s)
	}
	b.run(`git commit --amend -m "fix: 종료 알림 보존"`, 0, "", time.Second)
	if s := b.pre("git push origin main"); s != nil && s.Rule == RuleArtifactMeta {
		t.Fatalf("the amended message is clean: %+v", s)
	}
	if s := b.pre("python3 - <<'EOF'\ns = s.replace('a', '검토 결과를 반영했다')\nEOF\ngit commit -m \"fix: 문구\""); s != nil && s.Rule == RuleArtifactMeta {
		t.Fatalf("a script fed to python is not the commit message: %+v", s)
	}
	b.run(`git commit -m "fix: 알림" -m "리뷰 의견을 반영해 정리했다." && git push origin main`, 0, "", time.Second)
	if s := b.pre("git push origin main"); s != nil && s.Rule == RuleArtifactMeta {
		t.Fatalf("a commit pushed in the same command is already published: %+v", s)
	}
	if s := b.pre(`gh pr create --title "fix: 알림" --body "요청하신 대로 알림을 고쳤습니다."`); s == nil || s.Facts["via"] != "pr" {
		t.Fatalf("a PR body that points at the request: %+v", s)
	}
}

func TestAnswerPaddingShapesTheNextAnswer(t *testing.T) {
	b := newB(t, "x", nil)
	v := fired(b.msg("먼저 파일을 확인하겠습니다. 세 파일을 고쳤고 `go test`가 통과했습니다. 도움이 되었길 바랍니다."), RuleAnswerPadding)
	if v == nil || v.Facts["kinds"] != "preview,closing" || v.Level != L1 {
		t.Fatalf("an opener and a pleasantry: %+v", v)
	}
	if v := fired(b.msg("지침에 따라 메타 발언 없이 결과만 적습니다. 세 파일을 고쳤습니다."), RuleAnswerPadding); v == nil || v.Facts["kind"] != PadCompliance {
		t.Fatalf("a confirmation of following the rules: %+v", v)
	}
	for _, m := range []string{"말씀하신 두 곳을 고쳐 같은 주소에 다시 올렸습니다.", "낙찰 물건이 어디로 갔는지 실제 정산 규칙대로 적습니다."} {
		if v := fired(b.msg(m), RuleAnswerPadding); v != nil {
			t.Fatalf("%q reports the result: %+v", m, v)
		}
	}
	if v := fired(b.msg("세 파일을 고쳤습니다. 먼저 확인할 일은 설정 파일 경로입니다.\n```\n먼저 빌드하겠습니다.\n```"), RuleAnswerPadding); v != nil {
		t.Fatalf("a result-first answer: %+v", v)
	}
}

func TestDocumentFillerIsAdviceOnly(t *testing.T) {
	b := newB(t, "삼천포 설치 방법 문서 써 줘", nil)
	v := fired(b.write("docs/install.md", "h1", []string{"이 문서는 삼천포 설치 방법을 설명합니다.", "install.sh를 실행하면 데몬이 등록된다."}, nil), RuleArtifactMeta)
	if v == nil || v.Facts["kind"] != "filler" || v.Facts["meta_kinds"] != FillerRestate {
		t.Fatalf("an opening that says the request back: %+v", v)
	}
	if s := b.pre(`git commit -m "docs: 설치 순서"`); s != nil && s.Rule == RuleArtifactMeta {
		t.Fatalf("a deletion candidate does not hold the commit: %+v", s)
	}
	repeated := []string{"데몬은 소켓 경로를 환경 변수에서 읽어 연결을 시작한다.", "실패하면 종료한다.", "데몬은 소켓 경로를 환경 변수에서 읽어서 연결을 시작한다."}
	if v := fired(b.write("docs/daemon.md", "h2", repeated, nil), RuleArtifactMeta); v == nil || v.Facts["meta_kinds"] != FillerRepeat || v.Facts["count"] != 1 {
		t.Fatalf("the same explanation twice in one write: %+v", v)
	}
	for _, line := range []string{"삼천포 설치 방법은 install.sh를 실행하는 것이다.", "이 문서는 데몬 소켓 경로 규칙을 다룬다."} {
		if v := fired(b.write("docs/other.md", line, []string{line}, nil), RuleArtifactMeta); v != nil {
			t.Fatalf("%q carries the requested content or its own subject: %+v", line, v)
		}
	}
}

func TestAnswerLongerThanTheRequestedLength(t *testing.T) {
	b := newB(t, "세 줄로 요약해 줘", nil)
	if v := fired(b.msg("첫째 결과.\n둘째 결과.\n셋째 결과.\n넷째 결과."), RuleAnswerPadding); v == nil || v.Facts["kind"] != PadOverAmount {
		t.Fatalf("four lines for a three-line request: %+v", v)
	}
	if v := fired(b.msg("첫째 결과.\n둘째 결과.\n셋째 결과."), RuleAnswerPadding); v != nil {
		t.Fatalf("three lines: %+v", v)
	}
}

func TestStatedClickTargetsAreCountedAcrossWrites(t *testing.T) {
	b := newB(t, "설정 화면에 버튼 2개 넣어 줘", nil)
	if v := fired(b.write("web/Settings.tsx", "h1", []string{`<button onClick={save}>저장</button>`, `<button>취소</button>`}, nil), RuleUnrequestedUI); v != nil {
		t.Fatalf("the two requested buttons: %+v", v)
	}
	v := fired(b.write("web/Settings.tsx", "h2", []string{`<button>공유</button>`}, nil), RuleUnrequestedUI)
	if v == nil || v.Facts["element"] != "버튼" || v.Facts["requested"] != 2 || v.Facts["count"] != 3 {
		t.Fatalf("a third button in a later write: %+v", v)
	}
	r := newB(t, "버튼 추가 없이 문구만 고쳐 줘", nil)
	if v := fired(r.write("web/Login.tsx", "h1", []string{`<button>도움말</button>`}, nil), RuleUnrequestedUI); v == nil || v.Facts["requested"] != 0 {
		t.Fatalf("a button after the request ruled them out: %+v", v)
	}
}

func TestUnrequestedUIActionsCountTheRequest(t *testing.T) {
	buttons := []string{`<button onClick={save}>저장</button>`, `<button>공유</button>`, `<a href="/help">도움말</a>`}
	b := newB(t, "로그인 오류 문구를 고쳐 줘", nil)
	v := fired(b.write("web/src/Login.tsx", "h1", buttons, nil), RuleUnrequestedUI)
	if v == nil || v.Facts["count"] != 4 {
		t.Fatalf("three elements with four click targets nobody asked for: %+v", v)
	}
	if v := fired(b.write("web/src/Login.tsx", "h2", buttons, nil), RuleUnrequestedUI); v != nil {
		t.Fatalf("the same file is reported once per request: %+v", v)
	}
	if v := fired(b.write("web/src/Form.tsx", "h3", buttons[1:2], nil), RuleUnrequestedUI); v != nil {
		t.Fatalf("one new button in an edit: %+v", v)
	}
	for _, req := range []string{"로그인 화면에 공유 버튼이랑 도움말 링크 넣어 줘", "플랜 구현해"} {
		r := newB(t, req, nil)
		if v := fired(r.write("web/src/Login.tsx", "h1", buttons, nil), RuleUnrequestedUI); v != nil {
			t.Fatalf("%q names the elements or a plan that does: %+v", req, v)
		}
	}
}

func TestUIIssuesNameWhatIsWrongWithATarget(t *testing.T) {
	b := newB(t, "로그인 화면에 도움말 링크 넣어 줘", nil)
	if v := fired(b.write("web/Login.tsx", "h1", []string{`<a href="#">도움말</a>`}, nil), RuleUnrequestedUI); v == nil || v.Facts["issue"] != "dead_target" {
		t.Fatalf("a link that goes nowhere: %+v", v)
	}
	r := newB(t, "랜딩 화면에 버튼 넣어 줘", nil)
	cta := []string{`<a href="/s">시작하기</a>`, `<a href="/s">시작하기</a>`, `<a href="/s">시작하기</a>`}
	if v := fired(r.write("web/Home.tsx", "h1", cta, nil), RuleUnrequestedUI); v == nil || v.Facts["issue"] != "repeated_cta" || v.Facts["label"] != "시작하기" {
		t.Fatalf("one action offered three times: %+v", v)
	}
	ok := newB(t, "목차 링크 넣어 줘", nil)
	if v := fired(ok.write("web/Doc.tsx", "h1", []string{`<a href="#install">설치</a>`, `<a href="#usage">사용</a>`, `<button>저장</button>`}, nil), RuleUnrequestedUI); v != nil {
		t.Fatalf("anchors in the page and different actions: %+v", v)
	}
	same := newB(t, "목록 화면에 버튼 넣어 줘", nil)
	sections := []string{`<a href="/a">모두 보기</a>`, `<a href="/b">모두 보기</a>`, `<a href="/c">모두 보기</a>`, `<button onClick="c">닫기</button>`, `<button onClick="c">닫기</button>`, `<button onClick="c">닫기</button>`}
	if v := fired(same.write("web/List.tsx", "h1", sections, nil), RuleUnrequestedUI); v != nil && v.Facts["issue"] == "repeated_cta" {
		t.Fatalf("one label for different places, and a dialog's own control: %+v", v)
	}
}

func TestRestraintLeavesNormalWritingAlone(t *testing.T) {
	b := newB(t, "재시도 동작을 자세히 설명하는 문서 써 줘", nil)
	lines := []string{
		"재시도는 첫 실패 뒤 2초를 기다리고 대기 시간을 두 배씩 늘린다.",
		"이 명령은 중복 항목을 정리한다.",
		"이 값은 데이터 보존을 보장하지 않는다. 백업을 먼저 받는다.",
	}
	if v := fired(b.write("docs/retry.md", "h1", lines, nil), RuleArtifactMeta); v != nil {
		t.Fatalf("a detailed document that says what things do: %+v", v)
	}
	if s := b.pre(`git commit -m "fix: 실패한 검증 결과를 보존한다"`); s != nil && s.Rule == RuleArtifactMeta {
		t.Fatalf("a commit that states the changed behavior: %+v", s)
	}
	form := newB(t, "입력 오류 문구 넣어 줘", nil)
	if v := fired(form.write("web/Form.tsx", "h1", []string{`<p role="alert">이메일 형식이 올바르지 않습니다</p>`, `<button type="submit">저장</button>`}, nil), RuleUnrequestedUI); v != nil {
		t.Fatalf("an error message and its submit button: %+v", v)
	}
	if v := fired(b.write("docs/eval.md", "h2", []string{"실제 SDK 그래프로 검증했으며 모델 효과를 입증한 것은 아니다."}, nil), RuleArtifactMeta); v == nil {
		t.Fatal("a sentence about what the work did not prove")
	}
}

func TestAnswerWorkLogAndClosingSummary(t *testing.T) {
	b := newB(t, "x", nil)
	v := fired(b.msg("먼저 설정 파일을 읽었습니다. 이어서 로그를 확인했습니다. 다음으로 시험을 실행했습니다. 원인은 경로 오류입니다."), RuleAnswerPadding)
	if v == nil || v.Facts["kind"] != PadWorkLog {
		t.Fatalf("steps one after another: %+v", v)
	}
	if v := fired(b.msg("세 파일을 고쳤습니다. 시험이 통과했습니다. 남은 일은 없습니다. 요약하면 경로 오류를 고쳤습니다."), RuleAnswerPadding); v == nil || v.Facts["kind"] != PadSummary {
		t.Fatalf("the body said again: %+v", v)
	}
}

func TestMetaCandidatesCarryARewriteOrADeletion(t *testing.T) {
	hits := MetaSentences("요청에 따라 재시도 횟수를 늘렸다.\n이번 작업에서 파일을 정리했다.")
	got := MetaCandidates(hits, nil)
	if !strings.Contains(got, "→ 고친 문장: 재시도 횟수를 늘렸다") && !strings.Contains(got, "→ 고친 문장: 재시도 횟수를 늘") {
		t.Fatalf("%s", got)
	}
	if !strings.Contains(got, "→ 삭제") {
		t.Fatalf("a sentence that is only a report is deleted: %s", got)
	}
}

func TestConditionListIsADeletionCandidate(t *testing.T) {
	cases := map[string]bool{
		"실행 환경, 데이터 규모, 설정 방식과 사용하는 버전에 따라 결과가 달라질 수 있다.":                          true,
		"Results may vary depending on your hardware, network, and configuration.": true,
		"메모리가 8GB 미만인 환경이나 설정, 버전에 따라 빌드가 실패할 수 있다.":                               false,
		"재시도 횟수를 넘으면 실패로 끝난다.":                                                     false,
	}
	for s, want := range cases {
		if got := conditionList(s); got != want {
			t.Errorf("%q: %v", s, got)
		}
	}
}

func TestFillerCardsNeedTextOnlyBlocks(t *testing.T) {
	text := `<div class="card"><h3>시작하기</h3><p>이 화면에서 작업을 시작해 보세요.</p></div><div class="tip">언제든 다시 돌아올 수 있습니다.</div>`
	if n := fillerCardCount(text); n != 2 {
		t.Fatalf("%d", n)
	}
	actionable := `<div class="card"><a href="/new">새 프로젝트 만들기</a></div><div class="card">{project.name}의 사용량입니다</div>`
	if n := fillerCardCount(actionable); n != 0 {
		t.Fatalf("a card with an action or data is not filler: %d", n)
	}
}

func TestEnglishWorkReportsAreMetaAndBehaviorIsNot(t *testing.T) {
	for text, want := range map[string]bool{
		"We updated the docs and verified it.":     true,
		"Incorporated the review feedback.":        true,
		"In this session the parser was reworked.": true,
		"The parser retries twice on failure.":     false,
		"Fix crash when the input is empty":        false,
	} {
		if got := len(MetaSentences(text)) > 0; got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
}
