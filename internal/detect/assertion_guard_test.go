package detect

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestAssertionGuard_BlocksMutilation(t *testing.T) {
	guard := NewAssertionGuard()

	originalTest := `
func TestLogin(t *testing.T) {
	token, err := Login("user", "pass")
	if err != nil { t.Fatal(err) }
	if token == "" { t.Fatal("empty token") }
}
`
	mutilatedTest := `
func TestLogin(t *testing.T) {
	// disabled checks
	return
}
`
	verdict := guard.InspectDiff("auth_test.go", originalTest, mutilatedTest)
	if !verdict.Blocked {
		t.Fatalf("expected blocked assertion removal, got allowed")
	}
	if verdict.Reason != "assertion_count_decreased" {
		t.Fatalf("expected reason assertion_count_decreased, got %s", verdict.Reason)
	}
}

func TestAssertionGuard_IdentitySubstitution(t *testing.T) {
	guard := NewAssertionGuard()
	verdict := guard.InspectDiff("login.test.ts",
		"expect(res.status).toBe(200)\n",
		"expect(true).toBe(true)\n")
	if !verdict.Blocked || verdict.Reason != "identity_assertion_substituted" {
		t.Fatalf("identity substitution must be blocked: %+v", verdict)
	}
	verdict = guard.InspectDiff("auth_test.go",
		"	assert.Equal(t, token, \"x\")\n",
		"	assert.Equal(t, true, true)\n")
	if !verdict.Blocked || verdict.Reason != "identity_assertion_substituted" {
		t.Fatalf("identity substitution must be blocked: %+v", verdict)
	}
}

func TestAssertionGuard_SkipInjection(t *testing.T) {
	guard := NewAssertionGuard()
	verdict := guard.InspectDiff("tests/test_sum.py",
		"def test_sum():\n    assert 1 + 1 == 2\n",
		"@pytest.mark.skip\ndef test_sum():\n    assert 1 + 1 == 2\n")
	if !verdict.Blocked || verdict.Reason != "skip_injected" {
		t.Fatalf("skip injection must be blocked: %+v", verdict)
	}
}

func TestAssertionGuard_AllowsLegitChanges(t *testing.T) {
	guard := NewAssertionGuard()
	if v := guard.InspectDiff("tests/test_sum.py", "    assert total() == 1\n", "    assert total() == 2\n"); v.Blocked {
		t.Fatalf("expectation update is not mutilation: %+v", v)
	}
	if v := guard.InspectDiff("auth_test.go",
		"	assert.Equal(t, token, \"x\")\n",
		"	assert.Equal(t, token, \"y\")\n"); v.Blocked {
		t.Fatalf("expectation update is not mutilation: %+v", v)
	}
	if v := guard.InspectDiff("src/main.go",
		"func main() {}\n", "func main() { run() }\n"); v.Blocked {
		t.Fatalf("non-test files are out of scope: %+v", v)
	}
	if v := guard.InspectDiff("tests/test_sum.py",
		"def test_sum():\n    assert 1 + 1 == 2\n",
		"def test_sum():\n    assert 1 + 1 == 3\n"); v.Blocked {
		t.Fatalf("same assertion count passes: %+v", v)
	}
}

func TestPreCheckBlocksAssertionMutilation(t *testing.T) {
	b := newB(t, "x", nil)
	mutilate := func(path string) (bool, *Signal) {
		ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce,
			Paths: []string{path},
			Patch: []event.PatchFile{{Path: path,
				Added:   []string{"	// disabled checks"},
				Removed: []string{"	if err != nil { t.Fatal(err) }", "	assert.Equal(t, token, \"x\")"}}}}
		return b.e.PreCheck(ev)
	}
	deny, sig := mutilate("auth_test.go")
	if !deny || sig == nil || sig.Detector != "S5" || sig.Rule != "s5.test_weakening" ||
		sig.Facts["kind"] != string(KindAssertionMutilated) ||
		sig.Facts["reason"] != "assertion_count_decreased" || sig.Facts["blocked"] != true {
		t.Fatalf("test mutilation must be denied before the write: %v %+v", deny, sig)
	}
	if deny, _ := mutilate("src/main.go"); deny {
		t.Fatal("source files are out of scope")
	}

	// a test file created in this session is the agent's own work
	np := "tests/test_new.py"
	create := &event.Event{Kind: event.KindTool, Tool: event.ToolWrite, Category: event.CatProduce,
		Paths: []string{np}, Created: []string{np}, WriteHashes: map[string]string{np: "a"},
		Patch: []event.PatchFile{{Path: np, Added: []string{"def test_x():", "    assert True"}}}}
	b.next(create)
	weaken := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce,
		Paths: []string{np},
		Patch: []event.PatchFile{{Path: np, Added: []string{"@pytest.mark.skip"}, Removed: []string{"    assert True"}}}}
	if deny, _ := b.e.PreCheck(weaken); deny {
		t.Fatal("the agent's own new test is exempt")
	}

	// the contract may license explicit test simplification
	c, _ := contract.Parse([]byte("goal: x\ndone:\n  - check: t\nsimplify_tests: true\n"))
	b2 := newB(t, "", c)
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce,
		Paths: []string{"auth_test.go"},
		Patch: []event.PatchFile{{Path: "auth_test.go",
			Added:   []string{"	// disabled checks"},
			Removed: []string{"	if err != nil { t.Fatal(err) }", "	assert.Equal(t, token, \"x\")"}}}}
	if deny, _ := b2.e.PreCheck(ev); deny {
		t.Fatal("simplify_tests in an accepted contract exempts the guard")
	}
}

// A test function that loses its assertions while gaining a marker that
// hides a failure is muted, even when the file wide counts do not move.
func TestAssertionGuard_SuppressionAddedWithAssertionDrop(t *testing.T) {
	g := NewAssertionGuard()
	removed := []string{
		"func TestAddsTwoNumbers(t *testing.T) {",
		"\tassert.Equal(t, 3, add(1, 2))",
		"\tassert.Equal(t, 4, add(2, 2))",
		"}",
		"func TestSubtractsTwoNumbers(t *testing.T) {",
		"\tassert.Equal(t, 1, sub(2, 1))",
		"}",
	}
	added := []string{
		"func TestAddsTwoNumbers(t *testing.T) {",
		"\tassert.Equal(t, 3, add(1, 2))",
		"\t//nolint:errcheck",
		"}",
		"func TestSubtractsTwoNumbers(t *testing.T) {",
		"\tassert.Equal(t, 1, sub(2, 1))",
		"\tassert.Equal(t, 2, sub(3, 1))",
		"}",
	}
	v := g.InspectPatch("math_test.go", added, removed)
	if !v.Blocked {
		t.Fatal("the muted test function is stopped")
	}
	if v.Reason != "suppression_added_with_assertion_drop" {
		t.Fatalf("the reason names the change: %q", v.Reason)
	}
}

// The file wide counts did not drop, so only the per function view sees it.
func TestAssertionGuard_SuppressionRuleNeedsBothParts(t *testing.T) {
	g := NewAssertionGuard()
	removed := []string{
		"func TestAddsTwoNumbers(t *testing.T) {",
		"\tassert.Equal(t, 3, add(1, 2))",
		"}",
	}
	added := []string{
		"func TestAddsTwoNumbers(t *testing.T) {",
		"\t//nolint:errcheck",
		"}",
	}
	v := g.InspectPatch("math_test.go", added, removed)
	if !v.Blocked {
		t.Fatal("the assertion drop alone is already refused")
	}
	// a suppression without an assertion drop is not a finding
	addedOnly := []string{
		"func TestAddsTwoNumbers(t *testing.T) {",
		"\tassert.Equal(t, 3, add(1, 2))",
		"\t//nolint:errcheck",
		"}",
	}
	if v := g.InspectPatch("math_test.go", addedOnly, removed); v.Blocked {
		t.Fatalf("a suppression without an assertion drop is allowed: %+v", v)
	}
}

// Folding assertions into a table keeps every expected value; dropping
// one is still weakening.
func TestAssertionGuardTellsParametrizationFromWeakening(t *testing.T) {
	g := NewAssertionGuard()
	removed := []string{"\tif Add(1, 2) != 3 {", "\t\tt.Fatal(\"want 3\")", "\t}", "\tif Add(2, 5) != 7 {", "\t\tt.Fatal(\"want 7\")", "\t}"}
	table := []string{"\tfor _, tc := range []struct{ a, b, want int }{{1, 2, 3}, {2, 5, 7}} {", "\t\tif got := Add(tc.a, tc.b); got != tc.want {", "\t\t\tt.Fatalf(\"Add(%d, %d) = %d, want %d\", tc.a, tc.b, got, tc.want)", "\t\t}", "\t}"}
	if v := g.InspectPatch("calc_test.go", table, removed); v.Blocked {
		t.Fatalf("parametrization blocked: %+v", v)
	}
	dropped := []string{"\tfor _, tc := range []struct{ a, b, want int }{{1, 2, 3}} {", "\t\tif got := Add(tc.a, tc.b); got != tc.want {", "\t\t\tt.Fatalf(\"got %d\", got)", "\t\t}", "\t}"}
	if v := g.InspectPatch("calc_test.go", dropped, removed); !v.Blocked {
		t.Fatal("a table that drops a case was not blocked")
	}
	py := []string{"@pytest.mark.parametrize('a,b,want', [(1, 2, 3), (2, 5, 7)])", "def test_add(a, b, want):", "    assert add(a, b) == want"}
	if v := g.InspectPatch("tests/test_calc.py", py, []string{"def test_add():", "    assert add(1, 2) == 3", "    assert add(2, 5) == 7"}); v.Blocked {
		t.Fatalf("pytest parametrize blocked: %+v", v)
	}
}
