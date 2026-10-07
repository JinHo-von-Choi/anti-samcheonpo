package fp

import "testing"

func TestNormalizeCmd(t *testing.T) {
	cases := []struct{ in, want, dir string }{
		{"npm test 2>&1 | tail -50", "npm test", ""},
		{"npm test", "npm test", ""},
		{"CI=1 NODE_ENV=test npm test", "npm test", ""},
		{"cd server && ./gradlew test -q | tail -n 100", "./gradlew test -q", "server"},
		{"timeout 120 pytest -x  tests/", "pytest -x tests/", ""},
		{"time go test ./... > /dev/null", "go test ./...", ""},
		{"npx --yes vitest run", "vitest run", ""},
		{"pytest | grep FAIL | head -n 5", "pytest", ""},
		{"echo a || true", "echo a || true", ""},
		{"grep -r 'a|b' src | sort", "grep -r 'a|b' src | sort", ""},
	}
	for _, c := range cases {
		got, dir := NormalizeCmd(c.in)
		if got != c.want || dir != c.dir {
			t.Errorf("NormalizeCmd(%q) = %q, %q; want %q, %q", c.in, got, dir, c.want, c.dir)
		}
	}
	if CmdFP("npm test") != CmdFP(mustNorm("npm test 2>&1 | tail -50")) {
		t.Error("equivalent commands must share a fingerprint")
	}
	if CmdFP("") != "" {
		t.Error("empty command has no fingerprint")
	}
}

func mustNorm(s string) string { n, _ := NormalizeCmd(s); return n }

func TestSplitStages(t *testing.T) {
	got := SplitStages(`cd a && go test ./... ; echo "x && y" | tee out.txt || true`)
	want := []string{"cd a", "go test ./...", `echo "x && y"`, "tee out.txt", "true"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("stage %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTemplateMasksVolatileParts(t *testing.T) {
	a := Template(`TypeError at /home/a/src/app.ts:12 value 0x7fff1234 at 2026-10-06T12:00:01Z id deadbeefcafe`)
	b := Template(`TypeError at /tmp/build/src/app.ts:99 value 0x1 at 2025-01-01T00:00:00Z id 0123456789ab`)
	if a != b {
		t.Errorf("templates differ:\n%s\n%s", a, b)
	}
}

func TestErrorFingerprints(t *testing.T) {
	py1 := "Traceback (most recent call last):\n  File \"/w/app/x.py\", line 10, in main\n    run()\n  File \"/w/app/y.py\", line 3, in run\n    1/0\nZeroDivisionError: division by zero"
	py2 := "Traceback (most recent call last):\n  File \"/other/app/x.py\", line 99, in main\n    run()\n  File \"/other/app/y.py\", line 7, in run\n    1/0\nZeroDivisionError: division by zero"
	if a, b := ErrorFPs(py1), ErrorFPs(py2); len(a) != 1 || a[0] != b[0] {
		t.Errorf("same python error must fingerprint equally: %v %v", a, b)
	}
	py3 := "Traceback (most recent call last):\n  File \"/w/app/x.py\", line 10, in other\n    run()\nZeroDivisionError: division by zero"
	if ErrorFPs(py1)[0] == ErrorFPs(py3)[0] {
		t.Error("different frames must change the fingerprint")
	}
	cases := map[string]string{
		"node":    "TypeError: Cannot read properties of undefined (reading 'x')\n    at f (/w/src/a.js:1:2)\n    at g (/w/src/b.js:3:4)",
		"go":      "panic: runtime error: index out of range [3] with length 2\n\ngoroutine 1 [running]:\nmain.f(...)\n\t/w/main.go:5",
		"gofail":  "--- FAIL: TestAdd (0.00s)\n    add_test.go:9: got 1 want 2\nFAIL",
		"rust":    "error[E0308]: mismatched types\n --> src/main.rs:2:5",
		"tsc":     "src/user.ts(3,5): error TS2322: Type 'string' is not assignable to type 'number'.",
		"java":    "java.lang.IllegalStateException: boom 42\n\tat kr.app.Svc.run(Svc.java:10)\n\tat kr.app.Main.main(Main.java:3)",
		"kotlin":  "e: file:///w/src/A.kt:12:5 Unresolved reference: foo",
		"pytest":  "E   AssertionError: assert 200 == 401",
		"generic": "Error: listen EADDRINUSE: address already in use :::3000",
	}
	for name, out := range cases {
		if len(ErrorFPs(out)) == 0 {
			t.Errorf("%s: no error fingerprint", name)
		}
	}
	if len(ErrorFPs("all good\n3 passed\n")) != 0 {
		t.Error("clean output must have no error fingerprint")
	}
}

func TestResultFPOrderInsensitive(t *testing.T) {
	one := 1
	a := ResultFP(&one, []string{"b", "a"}, []string{"t2", "t1"})
	b := ResultFP(&one, []string{"a", "b"}, []string{"t1", "t2"})
	if a != b {
		t.Error("result fingerprint must not depend on order")
	}
	zero := 0
	if ResultFP(&zero, nil, nil) == ResultFP(&one, nil, nil) {
		t.Error("exit code must change the result fingerprint")
	}
}

func TestMapFPDeterministic(t *testing.T) {
	m1 := map[string]string{"a": "1", "b": "2"}
	m2 := map[string]string{"b": "2", "a": "1"}
	if MapFP(m1, nil) != MapFP(m2, nil) {
		t.Error("map fingerprint must not depend on insertion order")
	}
	if MapFP(m1, nil) == MapFP(m1, []string{"shell_mutation:3"}) {
		t.Error("shell mutation marker must change the fingerprint")
	}
}

func TestExecFPKeepsWhatMakesRunsDifferent(t *testing.T) {
	same := func(a, da, b, db string) bool {
		x, cx := ExecFP(a, da)
		y, cy := ExecFP(b, db)
		return cx && cy && x == y
	}
	for _, c := range [][4]string{
		{"pytest -q", "", "pytest -q", ""},
		{"pytest -q 2>&1 | tail -20", "", "pytest -q", ""},
		{"A=1 A=2 pytest", "", "A=2 pytest", ""},
		{"npx --yes jest", "", "jest", ""},
		{"cd svc && pytest -q", "", "pytest -q", "svc"},
		{"B=2 A=1 pytest", "", "A=1 B=2 pytest", ""},
		{"cd ./svc && pytest", "", "cd svc && pytest", ""},
	} {
		if !same(c[0], c[1], c[2], c[3]) {
			t.Errorf("%q in %q should equal %q in %q", c[0], c[1], c[2], c[3])
		}
	}
	for _, c := range [][4]string{
		{"FEATURE_FLAG=0 npm test", "", "FEATURE_FLAG=1 npm test", ""},
		{"cd service-a && npm test", "", "cd service-b && npm test", ""},
		{"npm test", "a", "npm test", "b"},
		{"pytest tests/test_a.py", "", "pytest tests/test_b.py", ""},
		{`pytest -k "a b"`, "", "pytest -k a b", ""},
		{"timeout 60 pytest -q", "", "pytest -q", ""},
		{"timeout 60 pytest -q", "", "timeout 5 pytest -q", ""},
		{`grep "a  b" f`, "", `grep "a b" f`, ""},
		{"A=1 A=2 pytest", "", "A=1 pytest", ""},
	} {
		x, _ := ExecFP(c[0], c[1])
		y, _ := ExecFP(c[2], c[3])
		if x == y {
			t.Errorf("%q in %q must differ from %q in %q", c[0], c[1], c[2], c[3])
		}
	}
	for _, cmd := range []string{
		"pytest $ARGS", "pytest $(cat args)", "make test && make lint", "source .venv/bin/activate; pytest",
		"cd /tmp && pytest", "cd .. && pytest", "eval pytest", "pytest `cat x`", "cd $HOME && pytest",
	} {
		if _, ok := ExecFP(cmd, ""); ok {
			t.Errorf("%q must be uncertain", cmd)
		}
	}
}
