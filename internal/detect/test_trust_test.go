package detect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func firedRule(sigs []Signal, rule string) *Signal {
	for i := range sigs {
		if sigs[i].Rule == rule {
			return &sigs[i]
		}
	}
	return nil
}

func TestRequestsTestEdit(t *testing.T) {
	for text, want := range map[string]bool{
		"깨진 테스트 고쳐 줘":                 true,
		"스냅샷 갱신해 줘":                   true,
		"please update the snapshots": true,
		"fix the failing tests":       true,
		"로그인 버그 고쳐 줘. 테스트도 돌려 봐":      false,
		"add tests for the parser":    false,
	} {
		if RequestsTestEdit(text) != want {
			t.Errorf("%q: want %v", text, want)
		}
	}
}

// A failing test turned green by changing only the test is judged; the same
// failure fixed in the code, even with a test typo fixed too, is not.
func TestTestEditToPass(t *testing.T) {
	failed := []string{"tests/test_price.py::test_total"}
	b := newB(t, "가격 계산 버그 고쳐 줘", nil)
	b.verify("pytest tests", 1, failed)
	b.write("tests/test_price.py", "a", []string{"    assert total(2) == 5"}, []string{"    assert total(2) == 4"})
	if v := firedRule(b.verify("pytest tests", 0, nil), RuleTestEditToPass); v == nil || v.Facts["path"] != "tests/test_price.py" {
		t.Fatalf("a literal changed in the test only: %+v", v)
	}

	b = newB(t, "가격 계산 버그 고쳐 줘", nil)
	b.verify("pytest tests", 1, failed)
	b.write("tests/test_price.py", "a", []string{"from app.price import total"}, []string{"from app.prices import total"})
	b.write("app/price.py", "b", []string{"    return n * 2"}, []string{"    return n * 2 + 1"})
	if v := firedRule(b.verify("pytest tests", 0, nil), RuleTestEditToPass); v != nil {
		t.Fatalf("a code fix with a test import fix: %+v", v)
	}

	b = newB(t, "실패하는 테스트 고쳐 줘", nil)
	b.verify("pytest tests", 1, failed)
	b.write("tests/test_price.py", "a", []string{"    assert total(2) == 5"}, []string{"    assert total(2) == 4"})
	if v := firedRule(b.verify("pytest tests", 0, nil), RuleTestEditToPass); v != nil {
		t.Fatalf("a request to fix the tests: %+v", v)
	}
}

// A Go test name is traced to its file through the command's package.
func TestTestEditToPassFindsGoTestFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "calc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "calc", "add_test.go"), []byte("package calc\n\nfunc TestAdd(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := newB(t, "덧셈 고쳐 줘", nil)
	b.e.Root = root
	b.verify("go test ./calc", 1, []string{"TestAdd"})
	b.write("calc/add_test.go", "a", []string{"\tif Add(1, 2) != 4 {"}, []string{"\tif Add(1, 2) != 3 {"})
	if v := firedRule(b.verify("go test ./calc", 0, nil), RuleTestEditToPass); v == nil {
		t.Fatal("the Go test file was not traced")
	}
}

// Mocking the function the session just changed is judged; mocking an
// outside call, or another function of the same file, is not.
func TestSubjectMocked(t *testing.T) {
	b := newB(t, "합계 계산 고쳐 줘", nil)
	b.write("app/pricing.py", "a", []string{"def total(items):", "    return sum(i.price for i in items)"}, nil)
	sigs := b.write("tests/test_pricing.py", "b", []string{"def test_total():", "    with mock.patch(\"app.pricing.total\", return_value=3):", "        assert checkout() == 3"}, nil)
	if v := firedRule(sigs, RuleSubjectMocked); v == nil || v.Facts["subject"] != "app/pricing.py" {
		t.Fatalf("the changed function mocked: %+v", v)
	}
	sigs = b.write("tests/test_pricing.py", "c", []string{"def test_fetch():", "    with mock.patch(\"requests.get\"):", "        assert fetch() == 1"}, nil)
	if v := firedRule(sigs, RuleSubjectMocked); v != nil {
		t.Fatalf("an outside call mocked: %+v", v)
	}
	sigs = b.write("tests/test_pricing.py", "d", []string{"def test_tax():", "    with mock.patch(\"app.pricing.tax_rate\"):", "        assert total([]) == 0"}, nil)
	if v := firedRule(sigs, RuleSubjectMocked); v != nil {
		t.Fatalf("an unchanged function mocked: %+v", v)
	}

	// only the body changed: the function header is not among the added lines
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "def tax_rate():\n    return 0.1\n\ndef total(items):\n    return sum(i.price for i in items)\n"
	if err := os.WriteFile(filepath.Join(root, "app", "pricing.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	b = newB(t, "합계 계산 고쳐 줘", nil)
	b.e.Root = root
	b.write("app/pricing.py", "a", []string{"    return sum(i.price for i in items)"}, []string{"    return 0"})
	if v := firedRule(b.write("tests/test_pricing.py", "b", []string{"    with mock.patch(\"app.pricing.total\"):"}, nil), RuleSubjectMocked); v == nil {
		t.Fatal("a function whose body changed, mocked")
	}
	if v := firedRule(b.write("tests/test_pricing.py", "c", []string{"    with mock.patch(\"app.pricing.tax_rate\"):"}, nil), RuleSubjectMocked); v != nil {
		t.Fatalf("an unchanged function next to it mocked: %+v", v)
	}

	b = newB(t, "장바구니 고쳐 줘", nil)
	b.write("web/src/cart.ts", "a", []string{"export function add() {}"}, nil)
	sigs = b.write("web/src/cart.test.ts", "b", []string{"jest.mock('./cart')", "test('adds', () => { expect(1).toBe(1) })"}, nil)
	if v := firedRule(sigs, RuleSubjectMocked); v == nil || v.Facts["subject"] != "web/src/cart.ts" {
		t.Fatalf("the changed module mocked: %+v", v)
	}
}

func TestTestSpecialCase(t *testing.T) {
	b := newB(t, "배포 오류 고쳐 줘", nil)
	if v := firedRule(b.write("deploy/run.go", "a", []string{"\tif os.Getenv(\"CI\") != \"\" {", "\t\treturn nil", "\t}"}, nil), RuleTestSpecialCase); v == nil || v.Facts["kind"] != "test_env_branch" {
		t.Fatalf("a branch on CI: %+v", v)
	}
	if v := firedRule(b.write("deploy/env.go", "b", []string{"\tenv := os.Getenv(\"APP_ENV\")"}, nil), RuleTestSpecialCase); v != nil {
		t.Fatalf("reading an app setting: %+v", v)
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	test := "def test_feb30():\n    assert validate(\"2024-02-30\") == \"invalid-date\"\n"
	if err := os.WriteFile(filepath.Join(root, "tests", "test_date.py"), []byte(test), 0o644); err != nil {
		t.Fatal(err)
	}
	b = newB(t, "날짜 검증 고쳐 줘", nil)
	b.e.Root = root
	b.verify("pytest tests", 1, []string{"tests/test_date.py::test_feb30"})
	sigs := b.write("app/date.py", "a", []string{"def validate(s):", "    if s == \"2024-02-30\":", "        return \"invalid-date\"", "    return parse(s)"}, nil)
	if v := firedRule(sigs, RuleTestSpecialCase); v == nil || v.Facts["kind"] != "input_special_case" || v.Facts["test"] != "test_feb30" {
		t.Fatalf("the test's input special-cased: %+v", v)
	}
	sigs = b.write("app/http.py", "b", []string{"    if method == \"GET\":", "        return 200"}, nil)
	if v := firedRule(sigs, RuleTestSpecialCase); v != nil {
		t.Fatalf("a common value: %+v", v)
	}
}

// A failing run turned green by regenerating expected output is judged; a
// regeneration after a feature change is not.
func TestSnapshotRegenerated(t *testing.T) {
	b := newB(t, "화면 버그 고쳐 줘", nil)
	b.verify("npx jest", 1, []string{"Button renders"})
	if v := firedRule(b.verify("npx jest -u", 0, nil), RuleSnapshotRegenerated); v == nil {
		t.Fatal("jest -u after a failure")
	}
	b = newB(t, "화면 버그 고쳐 줘", nil)
	b.verify("npx jest", 1, []string{"Button renders"})
	b.write("src/Button.tsx", "a", []string{"export const Button = () => <b/>"}, nil)
	if v := firedRule(b.verify("npx jest -u", 0, nil), RuleSnapshotRegenerated); v != nil {
		t.Fatalf("a regeneration after a feature change: %+v", v)
	}
	b = newB(t, "출력 고쳐 줘", nil)
	b.verify("go test ./render", 1, []string{"TestRender"})
	if v := firedRule(b.write("render/testdata/page.golden", "a", []string{"<p>new</p>"}, []string{"<p>old</p>"}), RuleSnapshotRegenerated); v == nil || v.Facts["path"] != "render/testdata/page.golden" {
		t.Fatalf("a golden file edited after a failure: %+v", v)
	}
	for cmd, want := range map[string]bool{"jest -u": true, "vitest run -u": true, "pytest --snapshot-update": true, "cargo insta accept": true,
		"go test ./x -update": true, "UPDATE_GOLDEN=1 go test ./x": true, "jest --watch": false, "go test ./x": false} {
		if SnapshotUpdate(cmd) != want {
			t.Errorf("SnapshotUpdate(%q) != %v", cmd, want)
		}
	}
}

// New tests that check nothing are recorded.
func TestTrivialTests(t *testing.T) {
	b := newB(t, "기능 추가해 줘", nil)
	b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolWrite, Category: event.CatProduce, Paths: []string{"tests/test_new.py"}, Created: []string{"tests/test_new.py"},
		Patch: []event.PatchFile{{Path: "tests/test_new.py", Added: []string{
			"def test_runs():", "    run()",
			"def test_calls():", "    run()", "    m.assert_called_once()",
			"def test_same():", "    assert f(1) == f(1)",
			"def test_raises():", "    with pytest.raises(ValueError):", "        run(-1)",
			"def test_real():", "    assert f(1) == 2",
		}}}})
	got := b.e.TrivialTests()
	want := []string{"tests/test_new.py: test_calls: mock_calls_only", "tests/test_new.py: test_runs: no_assertion", "tests/test_new.py: test_same: self_compared"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q", got)
		}
	}
}

// A shared settings file counts as test setup only for its test runner
// lines; a root conftest.py always counts.
func TestTestInfraAtRoot(t *testing.T) {
	b := newB(t, "버그 고쳐 줘", nil)
	if v := firedRule(b.write("conftest.py", "a", []string{"collect_ignore = ['tests/test_slow.py']"}, nil), "s5.test_weakening"); v == nil || v.Facts["kind"] != string(KindTestInfraChanged) {
		t.Fatalf("root conftest: %+v", v)
	}
	if v := firedRule(b.write("pytest.ini", "b", []string{"addopts = -k \"not slow\""}, nil), "s5.test_weakening"); v == nil {
		t.Fatal("root pytest.ini")
	}
	if v := firedRule(b.write("pyproject.toml", "c", []string{"requests = \"2.32\""}, []string{"requests = \"2.31\""}), "s5.test_weakening"); v != nil {
		t.Fatalf("a dependency bump: %+v", v)
	}
	if v := firedRule(b.write("pyproject.toml", "d", []string{"addopts = \"-q --deselect tests/test_a.py\""}, nil), "s5.test_weakening"); v == nil {
		t.Fatal("pytest options in pyproject.toml")
	}
}

// A commit with a test finding standing is told once and never blocked.
func TestCommitNotice(t *testing.T) {
	b := newB(t, "버그 고쳐 줘", nil)
	b.write("tests/test_a.py", "a", []string{"def test_a():"}, nil)
	b.write("tests/test_a.py", "b", []string{"@pytest.mark.skip"}, nil)
	commit := func() []Signal {
		return b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: "git commit -m fix", CmdNorm: "git commit -m fix", Category: event.CatExplore})
	}
	v := firedRule(commit(), "s5.test_weakening")
	if v == nil || v.Facts["kind"] != "commit_notice" {
		t.Fatalf("commit with a weakened test: %+v", v)
	}
	if v := firedRule(commit(), "s5.test_weakening"); v != nil && v.Facts["kind"] == "commit_notice" {
		t.Fatalf("told twice: %+v", v)
	}
}
