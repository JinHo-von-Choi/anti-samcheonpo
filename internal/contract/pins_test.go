package contract

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The tests a done check runs are fixed at acceptance; a later change to
// one of them, to the pytest section of a shared settings file, or a new
// collection file next to them makes the acceptance stale. Other changes
// to the shared settings file do not.
func TestAcceptFixesTheTestsTheDoneChecksRun(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "tests/test_a.py", "def test_a():\n    assert f() == 2\n")
	writeFile(t, root, "pyproject.toml", "[project]\nname = \"x\"\n\n[tool.pytest.ini_options]\naddopts = \"-q\"\n")
	writeFile(t, root, ".samcheonpo/contract.yml", "goal: x\ndone:\n  - check: \"pytest tests\"\n  - check: \"make lint\"\n")
	c, raw, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Accept(root, c, raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range a.Pins {
		paths = append(paths, p.Path)
	}
	if !slices.Equal(paths, []string{"tests/test_a.py", "pyproject.toml"}) || !slices.Equal(a.PinUnknown, []string{"c2"}) {
		t.Fatalf("pins %q unknown %q", paths, a.PinUnknown)
	}
	if st := CurrentStateWithPins(root, c, raw); st.State != StateAccepted {
		t.Fatalf("fresh acceptance is %s", st.State)
	}
	writeFile(t, root, "pyproject.toml", "[project]\nname = \"x\"\nversion = \"2\"\n\n[tool.pytest.ini_options]\naddopts = \"-q\"\n")
	if st := CurrentStateWithPins(root, c, raw); st.State != StateAccepted {
		t.Fatalf("a change outside the pytest section made it %s", st.State)
	}
	writeFile(t, root, "tests/conftest.py", "collect_ignore = ['test_a.py']\n")
	if st := CurrentStateWithPins(root, c, raw); st.State != StateStale {
		t.Fatalf("a new conftest.py next to a fixed test left it %s", st.State)
	}
	os.Remove(filepath.Join(root, "tests/conftest.py"))
	writeFile(t, root, "tests/test_a.py", "def test_a():\n    assert True\n")
	if st := CurrentStateWithPins(root, c, raw); st.State != StateStale {
		t.Fatalf("an edited fixed test left it %s", st.State)
	}
}

// pin_tests: none fixes nothing, and the field does not change the digest
// of contracts that leave it out.
func TestPinTestsNone(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "tests/test_a.py", "def test_a(): pass\n")
	c, errs := Parse([]byte("goal: x\ndone:\n  - check: \"pytest tests\"\npin_tests: none\n"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if pins, dirs, unknown := ComputePins(root, c); pins != nil || dirs != nil || unknown != nil {
		t.Fatalf("pin_tests none fixed %v %v %v", pins, dirs, unknown)
	}
	plain, _ := Parse([]byte("goal: x\ndone:\n  - check: \"pytest tests\"\n"))
	before := AuthorityDigest(plain)
	plain.PinTests = nil
	if AuthorityDigest(plain) != before {
		t.Fatal("an absent pin_tests changed the digest")
	}
	if _, errs := Parse([]byte("goal: x\ndone:\n  - check: \"true\"\npin_tests: [\"../x/**\"]\n")); len(errs) == 0 {
		t.Fatal("a glob outside the project was accepted")
	}
}
