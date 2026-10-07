package testout

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFailedTests(t *testing.T) {
	cases := []struct {
		name, out string
		want      []string
	}{
		{"pytest", "tests/test_a.py::test_x PASSED\nFAILED tests/test_a.py::test_y - assert 1 == 2\nFAILED tests/test_b.py::test_z\n===== 2 failed, 1 passed =====",
			[]string{"tests/test_a.py::test_y", "tests/test_b.py::test_z"}},
		{"jest", "FAIL src/a.test.ts\n  ✕ adds numbers (3 ms)\n  ✓ subtracts (1 ms)\n  ● adds numbers\nTests: 1 failed, 1 passed",
			[]string{"adds numbers"}},
		{"vitest", " × parses empty input 2ms\n ✓ ok", []string{"parses empty input 2ms"}},
		{"go", "=== RUN   TestA\n--- FAIL: TestA (0.00s)\n    --- FAIL: TestA/sub (0.00s)\nFAIL", []string{"TestA", "TestA/sub"}},
		{"cargo", "test tests::adds ... ok\ntest tests::divides ... FAILED\n", []string{"tests::divides"}},
		{"gradle", "AppTest > testAdd() FAILED\n    java.lang.AssertionError", []string{"AppTest > testAdd()"}},
		{"clean", "ok  pkg 0.1s\n", []string{}},
	}
	for _, c := range cases {
		got := FailedTests(c.out)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestJUnitFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "TEST-x.xml")
	xml := `<testsuite><testcase classname="a.B" name="ok"/><testcase classname="a.B" name="bad"><failure>x</failure></testcase></testsuite>`
	if err := os.WriteFile(p, []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}
	got := FromJUnitFiles("report written to " + p)
	if !reflect.DeepEqual(got, []string{"a.B.bad"}) {
		t.Errorf("got %q", got)
	}
}

func TestParseSummary(t *testing.T) {
	s := ParseSummary("Tests: 2 failed, 8 passed, 10 total")
	if !s.Found || s.Failed != 2 || s.Passed != 8 {
		t.Errorf("jest summary %+v", s)
	}
	s = ParseSummary("...\n===== 1 failed, 4 passed in 0.2s =====")
	if !s.Found || s.Failed != 1 || s.Passed != 4 {
		t.Errorf("pytest summary %+v", s)
	}
}
