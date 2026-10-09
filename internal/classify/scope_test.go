package classify

import (
	"slices"
	"testing"
)

func TestVerifyScope(t *testing.T) {
	for cmd, want := range map[string]string{
		"pytest":                                        ScopeFull,
		"pytest -q -n 4 --tb=short":                     ScopeFull,
		"python -m pytest tests/":                       ScopeFull,
		"uv run --python 3.13 pytest":                   ScopeFull,
		"pytest tests/test_a.py":                        ScopeTargeted,
		"pytest -k login":                               ScopeTargeted,
		"pytest tests/test_a.py::test_x":                ScopeTargeted,
		"go test ./...":                                 ScopeFull,
		"go test -count=1 ./...":                        ScopeFull,
		"go test ./internal/x":                          ScopeTargeted,
		"go test -run TestX ./...":                      ScopeTargeted,
		"npm test":                                      ScopeFull,
		"npm test -- src/a.test.ts":                     ScopeTargeted,
		"npx vitest run":                                ScopeFull,
		"npx playwright test tests/save.spec.ts":        ScopeTargeted,
		"cargo test":                                    ScopeFull,
		"cargo test -p core":                            ScopeTargeted,
		"./gradlew test":                                ScopeFull,
		"./gradlew test --tests '*Login*'":              ScopeTargeted,
		"mvn test -Dtest=LoginTest":                     ScopeTargeted,
		"tox -e py312":                                  ScopeFull,
		"go build ./...":                                ScopeUnknown,
		"ruff check .":                                  ScopeUnknown,
		"pytest -q > /tmp/out.txt 2>&1; tail -5 /tmp/o": ScopeFull,
	} {
		if got := VerifyScope(cmd); got != want {
			t.Errorf("VerifyScope(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestCommandScopeOfGateScripts(t *testing.T) {
	for cmd, want := range map[string]string{
		"python scripts/gates.py":                    ScopeFull,
		"python scripts/gates.py --gate G0-14":       ScopeTargeted,
		"python3 -I /tmp/run-final-gates.py 313":     ScopeFull,
		"sh scripts/ci.sh":                           ScopeFull,
		"python tools/check_docs.py":                 ScopeUnknown, // judged by how long it runs
		".venv/bin/python -m pytest tests/test_a.py": ScopeTargeted,
	} {
		if got := CommandScope(cmd); got != want {
			t.Errorf("CommandScope(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestVirtualenvAndScriptChecksAreVerification(t *testing.T) {
	for _, cmd := range []string{
		".venv-new/bin/python -m pytest -q tests/test_x.py > /tmp/o 2>&1; tail -5 /tmp/o",
		"/usr/bin/python3.13 -m pytest",
		"node_modules/.bin/jest",
		".venv/bin/python scripts/gates.py --gate G0-1",
		"python3 -I /tmp/panopticon-final-gates-run.py 313",
		"bash ./scripts/check-conformance.sh",
	} {
		if c, _ := Shell(cmd, Options{}); c != ShellVerify {
			t.Errorf("Shell(%q) = %s, want verify", cmd, c)
		}
	}
}

func TestHeredocBodiesAreJudgedByWhatTheyRun(t *testing.T) {
	edit := "python3 - <<'PY'\nfrom pathlib import Path\np=Path('tests/test_storage/test_x.py');s=p.read_text().replace('a','b');p.write_text(s)\nPY"
	if c, mut := Shell(edit, Options{}); c != ShellProduce || !mut {
		t.Fatalf("a Python script that writes a file is a change, not a test run: %s %v", c, mut)
	}
	if w := HeredocWrites(edit); !slices.Equal(w, []string{"tests/test_storage/test_x.py"}) {
		t.Fatalf("the written file is the call's path: %v", w)
	}
	run := "python3 - <<'PY'\nimport subprocess,os\nraise SystemExit(subprocess.call(['.venv-new/bin/python','-m','pytest','tests/test_a.py','-q'],env=os.environ))\nPY"
	if c, _ := Shell(run, Options{}); c != ShellVerify {
		t.Fatalf("a subprocess call of pytest is a test run: %s", c)
	}
	if got := TestCommands(run); !slices.Equal(got, []string{"python -m pytest tests/test_a.py -q"}) {
		t.Fatalf("the embedded test command: %v", got)
	}
	both := edit + "\n" + run
	if c, mut := Shell(both, Options{}); c != ShellVerify || !mut {
		t.Fatalf("editing then testing in one command is a mutating test run: %s %v", c, mut)
	}
	if c, mut := Shell("go build ./... && cp bin/x /tmp/x", Options{}); c != ShellProduce || !mut {
		t.Fatalf("a build and a copy stays a change: %s", c)
	}
	doc := "cat >> docs/NOTES.md <<'EOF'\nrun pytest before release\nEOF"
	if c, _ := Shell(doc, Options{}); c != ShellProduce {
		t.Fatalf("body text of a document is not a command: %s", c)
	}
}

func TestPythonScriptWritesFollowCopiesAndEdits(t *testing.T) {
	body := "from pathlib import Path\np=Path('/tmp/a.py')\np.write_text('''import subprocess\nsubprocess.run(['pytest'])\n''')\n" +
		"s=Path('/tmp/a.py').read_text().replace(\"'pytest'\", \"'pytest','-q'\")\nPath('/tmp/b.py').write_text(s)\n" +
		"q=Path('/tmp/b.py')\nq.write_text(q.read_text().replace('-q', '-x'))\n"
	w := PythonScriptWrites(body)
	if len(w) != 3 {
		t.Fatalf("three writes: %+v", w)
	}
	if w[0].Path != "/tmp/a.py" || !w[0].Literal || w[0].Content != "import subprocess\nsubprocess.run(['pytest'])\n" {
		t.Fatalf("literal content: %+v", w[0])
	}
	if w[1].Path != "/tmp/b.py" || w[1].From != "/tmp/a.py" || len(w[1].Replaces) != 1 || w[1].Replaces[0][1] != "'pytest','-q'" {
		t.Fatalf("a copy with an edit: %+v", w[1])
	}
	if w[2].Path != "/tmp/b.py" || w[2].From != "/tmp/b.py" || w[2].Replaces[0] != [2]string{"-q", "-x"} {
		t.Fatalf("an edit in place: %+v", w[2])
	}
	if got := ScriptChecks("/tmp/a.py", w[0].Content); !slices.Equal(got, []string{"pytest"}) {
		t.Fatalf("the script's tests: %v", got)
	}
}

func TestExecutedScripts(t *testing.T) {
	for cmd, want := range map[string][]string{
		"python3 -I /tmp/run.py 313":   {"/tmp/run.py"},
		"FOO=1 bash scripts/ci.sh":     {"scripts/ci.sh"},
		"./run.sh && echo ok":          {"./run.sh"},
		"cat /tmp/run.py":              nil,
		"python -m pytest":             nil,
		"tail -5 /tmp/x.log; true":     nil,
		"sudo python3 /opt/x/check.py": {"/opt/x/check.py"},
	} {
		if got := ExecutedScripts(cmd); !slices.Equal(got, want) {
			t.Errorf("ExecutedScripts(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestLongRun(t *testing.T) {
	for cmd, want := range map[string]LongRunInfo{
		"k6 run --duration 2h load.js":     {Seconds: 7200, Named: true, Target: "k6 run load.js"},
		"locust -f l.py -t 30m --headless": {Seconds: 1800, Named: true, Target: "locust -f l.py --headless"},
		"wrk -d 30s http://x":              {Seconds: 30, Named: true, Target: "wrk http://x"},
		"python perf_replay.py":            {Named: true, Target: "python perf_replay.py"},
		"timeout 900 python soak.py":       {Seconds: 900, Named: true, Target: "python soak.py"},
		"timeout 600 npm run test:int":     {},
		"pytest -t 5":                      {},
		"go test -bench . ./...":           {},
	} {
		got := LongRun(cmd)
		if got.Seconds != want.Seconds || got.Named != want.Named || got.Target != want.Target {
			t.Errorf("LongRun(%q) = %+v, want %+v", cmd, got, want)
		}
	}
}

func TestRelease(t *testing.T) {
	for cmd, want := range map[string]string{
		"git tag -a v0.5.3 -m 'x'":              ReleaseTag,
		"git tag":                               ReleaseNone,
		"git tag -l 'v*'":                       ReleaseNone,
		"git push origin main v0.5.3":           ReleasePushTag,
		"git push --tags":                       ReleasePushTag,
		"git push origin main":                  ReleaseNone,
		"gh release create v1.0.0 --notes x":    ReleaseCreate,
		"npm publish":                           ReleasePublish,
		"goreleaser release --snapshot --clean": ReleaseNone,
		"git add -A; git commit -m x\ngit tag -a v1 -m y\ngit push origin main v1.2.0":                                                                        ReleasePushTag,
		"python3 - <<'PY'\nimport subprocess\nsubprocess.run(['git','push','origin','v0.4.0'])\nPY":                                                           ReleasePushTag,
		"python3 - <<'PY'\nimport subprocess\ntag='v0.5.1'\nsubprocess.run(['git','tag','-a',tag,'-m','x'])\nsubprocess.run(['git','push','origin',tag])\nPY": ReleasePushTag,
	} {
		if got := Release(cmd); got != want {
			t.Errorf("Release(%q) = %q, want %q", cmd, got, want)
		}
	}
	if c, mut := Shell("git tag -a v1.0.0 -m x", Options{}); c != ShellProduce || !mut {
		t.Errorf("creating a tag changes the repository: %s", c)
	}
	if c, _ := Shell("git tag --sort=-version:refname", Options{}); c != ShellExplore {
		t.Errorf("listing tags reads: %s", c)
	}
}

func TestCIResult(t *testing.T) {
	type in struct {
		cmd  string
		exit int
		out  string
	}
	for c, want := range map[in]string{
		{"gh run watch 1 --exit-status", 1, ""}:                          CIFailed,
		{"gh run watch 1 --exit-status", 0, ""}:                          CIPassed,
		{"gh pr checks", 8, ""}:                                          CIUnknown,
		{"gh pr checks", 1, ""}:                                          CIFailed,
		{"gh run view 1 --log-failed", 0, "build\tstep\tFAILED tests/x"}: CIFailed,
		{"gh run list --limit 3 --json conclusion,status", 0, `[{"conclusion":"","status":"queued"},{"conclusion":"success"}]`}:                    CIUnknown,
		{"gh run view 5 --json status,conclusion,jobs", 0, `{"conclusion":"failure","jobs":[]}`}:                                                   CIFailed,
		{"gh run list", 0, "completed\tsuccess\tfix\tgates\tmain\tpush\t1\t5m\t1h"}:                                                                CIPassed,
		{"gh run list", 0, "completed\tfailure\tfix\tgates\tmain\tpush\t1\t5m\t1h"}:                                                                CIFailed,
		{"python3 - <<'PY'\nimport subprocess,sys\nr=subprocess.run(['gh','run','watch',rid,'--exit-status'])\nsys.exit(r.returncode)\nPY", 1, ""}: CIFailed,
		{"git push", 0, ""}: CIUnknown,
	} {
		if got := CIResult(c.cmd, c.exit, c.out); got != want {
			t.Errorf("CIResult(%q, %d) = %q, want %q", c.cmd, c.exit, got, want)
		}
	}
}

func TestMaskedExit(t *testing.T) {
	for cmd, want := range map[string]bool{
		"pytest > /tmp/o 2>&1; tail -5 /tmp/o": true,
		"pytest | tail -20":                    true,
		"make test || true":                    true,
		"pytest && tail -5 /tmp/o":             false,
		"pytest":                               false,
		"pytest; echo done":                    true,
		"cat x | wc -l":                        false,
	} {
		if got := MaskedExit(cmd); got != want {
			t.Errorf("MaskedExit(%q) = %v, want %v", cmd, got, want)
		}
	}
}
