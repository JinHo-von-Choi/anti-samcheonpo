package detect

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func (b *builder) prompt(text string) []Signal {
	return b.next(&event.Event{Kind: event.KindPrompt, Text: text, PromptOrigin: "user"})
}

// Ending a turn on a failing check with an honest "left for later" report
// is still leaving work the agent can do.
func TestAbandonedVerificationFailingCheckLeft(t *testing.T) {
	b := newB(t, "계산기 덧셈 버그 고쳐 줘", nil)
	b.write("calc/add.go", "a", []string{"return a - b"}, nil)
	b.verify("go test ./calc", 1, []string{"TestAdd"})
	v := fired(b.msg("덧셈 함수를 수정했습니다. TestAdd가 아직 실패하는데 추후 확인이 필요합니다."), RuleAbandonedVerification)
	if v == nil || v.Level != L2 || v.Facts["kind"] != KindFailingLeft || v.Facts["deferral"] != true {
		t.Fatalf("failing check left: %+v", v)
	}
	if fired(b.e.TurnEnd(&event.Event{Seq: 50, Kind: event.KindMessage, Text: "덧셈 함수를 수정했습니다. TestAdd가 아직 실패하는데 추후 확인이 필요합니다."}, nil), "s5.false_done") != nil {
		t.Fatal("an honest report is not a false completion")
	}
}

// A change with no check after it, reported as "not verified", is caught
// whether or not the report says so.
func TestAbandonedVerificationNoCheckAfterChange(t *testing.T) {
	for _, text := range []string{"구현했습니다. 검증은 하지 못했습니다.", "변경 사항을 정리했습니다."} {
		b := newB(t, "로그인 기능 만들어 줘", nil)
		b.verify("go test ./...", 0, nil)
		b.write("auth/login.go", "a", []string{"func Login() {}"}, nil)
		v := fired(b.msg(text), RuleAbandonedVerification)
		if v == nil || v.Facts["kind"] != KindNoCheckAfterChange || v.Facts["path"] != "auth/login.go" {
			t.Fatalf("%q: %+v", text, v)
		}
	}
}

// Documenting the failure instead of fixing it does not resolve it.
func TestAbandonedVerificationDocumentingFailureIsNotFixing(t *testing.T) {
	b := newB(t, "결제 모듈 버그 고쳐 줘", nil)
	b.write("pay/charge.go", "a", []string{"x"}, nil)
	b.verify("go test ./pay", 1, []string{"TestCharge"})
	b.write("docs/known-issues.md", "b", []string{"- TestCharge 미검증"}, nil)
	if v := fired(b.msg("남은 문제는 문서에 미검증으로 적어 두었습니다."), RuleAbandonedVerification); v == nil || v.Facts["kind"] != KindFailingLeft {
		t.Fatalf("documented failure: %+v", v)
	}
}

func TestAbandonedVerificationLeavesLegitimateStopsAlone(t *testing.T) {
	// a passing check after the change
	b := newB(t, "버그 고쳐 줘", nil)
	b.write("a.go", "a", []string{"x"}, nil)
	b.verify("go test ./...", 0, nil)
	if fired(b.msg("고쳤고 시험이 통과합니다."), RuleAbandonedVerification) != nil {
		t.Error("verified change was flagged")
	}
	// a failure outside the code: the command is not installed
	b = newB(t, "버그 고쳐 줘", nil)
	b.write("a.go", "a", []string{"x"}, nil)
	exit := 127
	b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: "pytest", CmdNorm: "pytest", Category: event.CatVerify, ExitCode: &exit, Text: "bash: pytest: command not found"})
	if fired(b.msg("pytest가 설치되어 있지 않아 검증하지 못했습니다."), RuleAbandonedVerification) != nil {
		t.Error("an external cause was flagged")
	}
	// the user asked to stop
	b = newB(t, "버그 고쳐 줘", nil)
	b.write("a.go", "a", []string{"x"}, nil)
	b.prompt("여기까지만 하고 멈춰")
	if fired(b.msg("멈췄습니다. 검증은 하지 않았습니다."), RuleAbandonedVerification) != nil {
		t.Error("a user stop was flagged")
	}
	// the user asked for no checks, in either ending
	b = newB(t, "Add 함수만 추가하고 테스트는 돌리지 말고 바로 끝내", nil)
	b.write("calc.go", "a", []string{"func Add(a, b int) int { return a + b }"}, nil)
	if fired(b.msg("추가했습니다."), RuleAbandonedVerification) != nil {
		t.Error("a request not to run tests was flagged")
	}
	b = newB(t, "Add 함수만 추가해 줘. 테스트 없이 파일만 수정해", nil)
	b.write("calc.go", "a", []string{"func Add(a, b int) int { return a + b }"}, nil)
	if fired(b.msg("추가했습니다. 테스트는 돌리지 않았습니다."), RuleAbandonedVerification) != nil {
		t.Error("an explicit no-check request was flagged")
	}
	// documentation only
	b = newB(t, "README 고쳐 줘", nil)
	b.write("README.md", "a", []string{"x"}, nil)
	if fired(b.msg("README를 고쳤습니다."), RuleAbandonedVerification) != nil {
		t.Error("a docs change was flagged")
	}
	// a change from an earlier turn, and the user now asks a question
	b = newB(t, "버그 고쳐 줘", nil)
	b.write("a.go", "a", []string{"x"}, nil)
	b.prompt("이 함수는 어디서 호출돼?")
	if fired(b.msg("main.go의 run에서 호출됩니다."), RuleAbandonedVerification) != nil {
		t.Error("an answer to a later question was flagged")
	}
	// a scratch script in a temporary directory
	b = newB(t, "원장 분석해 줘", nil)
	b.write("/tmp/x/analyze.py", "a", []string{"print(1)"}, nil)
	if fired(b.msg("분석 결과입니다."), RuleAbandonedVerification) != nil {
		t.Error("a scratch script was flagged")
	}
}

// A passing check does not make a stub body a finished one.
func TestFalseDoneHollowBody(t *testing.T) {
	b := newB(t, "`ParseConfig` 구현해 줘", nil)
	b.write("config/parse.go", "a", []string{"func ParseConfig(path string) (*Config, error) {", "\t// TODO: read the file", "\treturn nil, nil", "}"}, nil)
	b.verify("go test ./config", 0, nil)
	v := fired(b.msg("ParseConfig를 구현했습니다. 시험이 모두 통과합니다."), "s5.false_done")
	if v == nil || v.Facts["kind"] != KindHollow || v.Level != L2 {
		t.Fatalf("hollow: %+v", v)
	}
	// writing the body for real clears it
	b.write("config/parse.go", "b", []string{"\tdata, err := os.ReadFile(path)"}, []string{"\t// TODO: read the file"})
	b.verify("go test ./config", 0, nil)
	if v := fired(b.msg("ParseConfig를 구현했습니다. 시험이 모두 통과합니다."), "s5.false_done"); v != nil {
		t.Fatalf("resolved stub still flagged: %+v", v)
	}
	// a requested skeleton is the output itself
	b = newB(t, "핸들러 골격만 만들어 줘", nil)
	b.write("api/handler.go", "a", []string{"// TODO: implement"}, nil)
	b.verify("go build ./...", 0, nil)
	if v := fired(b.msg("골격을 만들었습니다. 완료했습니다."), "s5.false_done"); v != nil {
		t.Fatalf("requested skeleton flagged: %+v", v)
	}
}

// Of three named functions only one was written.
func TestFalseDoneNamedTargetsWithoutEvidence(t *testing.T) {
	b := newB(t, "calc.go에 `Add`, `Sub`, `Mul` 함수 구현해 줘", nil)
	b.write("calc.go", "a", []string{"func Add(a, b int) int { return a + b }"}, nil)
	b.verify("go test ./...", 0, nil)
	v := fired(b.msg("구현했습니다. 시험이 모두 통과합니다."), "s5.false_done")
	if v == nil || v.Facts["kind"] != KindNoEvidence || v.Facts["missing"] != "sub, mul" {
		t.Fatalf("missing targets: %+v", v)
	}
	b.write("calc.go", "b", []string{"func Sub(a, b int) int { return a - b }", "func Mul(a, b int) int { return a * b }"}, nil)
	b.verify("go test ./...", 0, nil)
	if v := fired(b.msg("구현했습니다. 시험이 모두 통과합니다."), "s5.false_done"); v != nil {
		t.Fatalf("all targets written, still flagged: %+v", v)
	}
}

// The only passing check ran the test file the agent just wrote.
func TestFalseDoneNarrowCheck(t *testing.T) {
	b := newB(t, "장바구니 할인 버그 고쳐 줘", nil)
	b.write("cart/discount.py", "a", []string{"    return price * 0.9"}, nil)
	b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolWrite, Category: event.CatProduce, Paths: []string{"tests/test_my_discount.py"},
		Patch: []event.PatchFile{{Path: "tests/test_my_discount.py", Added: []string{"def test_x(): assert True"}}}})
	b.verify("pytest tests/test_my_discount.py", 0, nil)
	if v := fired(b.msg("고쳤습니다. 시험이 모두 통과합니다."), "s5.false_done"); v == nil || v.Facts["kind"] != KindNarrowCheck {
		t.Fatalf("narrow check: %+v", v)
	}
	b.verify("pytest tests/", 0, nil)
	if v := fired(b.msg("고쳤습니다. 시험이 모두 통과합니다."), "s5.false_done"); v != nil {
		t.Fatalf("a check over the suite flagged: %+v", v)
	}
}

// Twelve steps on one lint error while two named functions are untouched.
func TestFixationOnOneErrorWhileNamedTargetsWait(t *testing.T) {
	b := newB(t, "`Export`, `Import`, `Validate` 함수 만들어 줘", nil)
	b.write("data/export.go", "a", []string{"func Export() {}"}, nil)
	var got *Signal
	for i := 0; i < 6; i++ {
		b.write("data/export.go", "x", []string{"// lint"}, nil)
		if v := fired(b.verify("golangci-lint run", 1, nil, "lint:unused-param"), RuleFixation); v != nil {
			got = v
		}
	}
	if got == nil || got.Level != L1 || got.Facts["missing"] != "import, validate" {
		t.Fatalf("fixation: %+v", got)
	}
}

func TestFixationLeavesFocusedWorkAlone(t *testing.T) {
	// one bug, no other named target: concentration is the work
	b := newB(t, "`Export` 버그 고쳐 줘", nil)
	b.write("data/export.go", "a", []string{"func Export() {}"}, nil)
	for i := 0; i < 8; i++ {
		b.write("data/export.go", "x", []string{"// try"}, nil)
		if v := fired(b.verify("go test ./data", 1, nil, "e1"), RuleFixation); v != nil {
			t.Fatalf("single-target work flagged: %+v", v)
		}
	}
	// progress inside the window: new files keep appearing
	b = newB(t, "`Export`, `Import` 함수 만들어 줘", nil)
	for i := 0; i < 14; i++ {
		if v := fired(b.write("data/f"+string(rune('a'+i))+".go", "x", []string{"x"}, nil), RuleFixation); v != nil {
			t.Fatalf("progressing work flagged: %+v", v)
		}
	}
}

// Running the changed program after the change is a check of it.
func TestAbandonedVerificationRunningTheChangedFileCounts(t *testing.T) {
	b := newB(t, "a.py가 2를 출력하게 고쳐 줘", nil)
	b.write("a.py", "a", []string{"print(2)"}, nil)
	exit := 0
	b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: "python a.py", CmdNorm: "python a.py", ExitCode: &exit})
	if v := fired(b.msg("a.py를 고쳤고 실행하면 2가 나옵니다."), RuleAbandonedVerification); v != nil {
		t.Fatalf("a run of the changed file was not taken as a check: %+v", v)
	}
	b.write("a.py", "b", []string{"print(3)"}, nil)
	if v := fired(b.msg("다시 고쳤습니다."), RuleAbandonedVerification); v == nil || v.Facts["kind"] != KindNoCheckAfterChange {
		t.Fatalf("a run before the latest change still counted: %+v", v)
	}
}

// A file the request tells the agent to leave alone, and a test file to be
// satisfied, are context, not targets that can be missing.
func TestRequestIdentsSkipLeftAloneAndTestFiles(t *testing.T) {
	got := requestIdents("calc_test.go는 수정하지 말고 calc.go의 `Add`만 고쳐 줘. `Sub`는 건드리지 마.")
	if strings.Join(got, ",") != "calc.go,add" {
		t.Fatalf("idents %q", got)
	}
	if got := requestIdents("tests/test_cart.py가 통과하도록 cart.py 고쳐 줘"); strings.Join(got, ",") != "cart.py" {
		t.Fatalf("test file kept as a target: %q", got)
	}
}

// A shell read of a protected path by a relative name is stopped; a
// command that only names the project is not.
func TestProtectedPathRelativeShellRead(t *testing.T) {
	b := newB(t, "작업", nil)
	b.e.Root = t.TempDir()
	b.e.Cfg.ProtectedPaths = []string{filepath.Join(b.e.Root, "grader")}
	read := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: "cat grader/answer.txt", CmdNorm: "cat grader/answer.txt"}
	if s := b.e.ProtectedCheck(read); s == nil {
		t.Fatal("relative read of the grader was not stopped")
	}
	for _, cmd := range []string{"ls .", "rg -n Add .", "go test ./...", "cat calc.go"} {
		if s := b.e.ProtectedCheck(&event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: cmd, CmdNorm: cmd}); s != nil {
			t.Fatalf("%q was stopped", cmd)
		}
	}
}

// A shell command that writes the file names it, but writing is not
// running it.
func TestAbandonedVerificationShellWriteIsNotACheck(t *testing.T) {
	b := newB(t, "calc.go에 Add 추가해 줘", nil)
	exit := 0
	b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatProduce, Mutating: true, Paths: []string{"calc.go"},
		Cmd: "cat > calc.go <<'EOF'\npackage calc\nEOF", CmdNorm: "cat > calc.go", ExitCode: &exit})
	if v := fired(b.msg("추가했습니다."), RuleAbandonedVerification); v == nil || v.Facts["kind"] != KindNoCheckAfterChange {
		t.Fatalf("a shell write counted as a check: %+v", v)
	}
}

// A harness note before the request names no work targets.
func TestRequestIdentsSkipHarnessNotes(t *testing.T) {
	got := requestIdents("[IMPORTANT: You are running as a scheduled cron job. Do NOT use send_message.]\nreport.py 정리해 줘")
	if strings.Join(got, ",") != "report.py" {
		t.Fatalf("idents %q", got)
	}
}

// Cases from real use: a scheduled job writing its run record, harness
// prompts naming tools, and a document drafted by repeated edits.
func TestRealUseFalsePositives(t *testing.T) {
	// a run record written at the end is not a code change
	b := newB(t, "허브 순회 작업", nil)
	b.write("scripts/agent/last_cycle_x.c1330.2026-10-27.json", "a", []string{`{"done": true}`}, nil)
	if v := fired(b.msg("이번 순회를 마쳤습니다."), RuleAbandonedVerification); v != nil {
		t.Fatalf("a run record held the stop: %+v", v)
	}
	// ...nor does it make a passing check stale
	b = newB(t, "게이트 통과시키기", nil)
	b.verify("python3 gate.py th57.json draft.txt", 0, nil)
	b.write("scripts/agent/last_cycle_y.json", "a", []string{`{}`}, nil)
	if v := fired(b.msg("완료했습니다."), "s5.false_done"); v != nil {
		t.Fatalf("a record write made the check stale: %+v", v)
	}
	// a build manifest still counts as a change a check depends on
	b = newB(t, "의존성 올려 줘", nil)
	b.write("package.json", "a", []string{`"x": "2"`}, nil)
	if v := fired(b.msg("올렸습니다."), RuleAbandonedVerification); v == nil {
		t.Fatal("a manifest change was not seen")
	}
	// tool and parameter names in a harness prompt are not targets
	if got := requestIdents("send_message를 쓰지 말고 write_file로 저장하라. include_memories=false"); len(got) != 0 {
		t.Fatalf("tool names taken as targets: %q", got)
	}
	// drafting a document by repeated edits is not fixation
	b = newB(t, "`Byron` 문서와 `Shelley` 문서를 작성하라", nil)
	for i := 0; i < 14; i++ {
		if v := fired(b.write("drafts/b11_바이런.txt", "x", []string{"줄"}, nil), RuleFixation); v != nil {
			t.Fatalf("document drafting flagged: %+v", v)
		}
	}
}
