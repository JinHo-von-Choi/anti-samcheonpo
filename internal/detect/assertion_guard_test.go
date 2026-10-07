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
