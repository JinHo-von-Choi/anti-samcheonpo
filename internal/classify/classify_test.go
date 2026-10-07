package classify

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestShell(t *testing.T) {
	cases := []struct {
		cmd   string
		class string
		mut   bool
	}{
		{"pytest -x tests", ShellVerify, false},
		{"python3 -m pytest", ShellVerify, false},
		{"npm run test", ShellVerify, false},
		{"pnpm lint", ShellVerify, false},
		{"go test ./...", ShellVerify, false},
		{"cargo clippy", ShellVerify, false},
		{"./gradlew test -q | tail -50", ShellVerify, false},
		{"cd server && ./gradlew compileJava", ShellVerify, false},
		{"curl -s http://localhost:8080/health", ShellVerify, false},
		{"npx tsc --noEmit", ShellVerify, false},
		{"cat a.txt | grep x", ShellExplore, false},
		{"git status", ShellExplore, false},
		{"rg -n foo src", ShellExplore, false},
		{"sed -n '1,20p' a.go", ShellExplore, false},
		{"sed -i 's/a/b/' a.go", ShellProduce, true},
		{"mkdir -p a && touch a/b", ShellProduce, true},
		{"echo hi > out.txt", ShellProduce, true},
		{"echo hi > /dev/null", ShellExplore, false},
		{"git commit -m x", ShellProduce, true},
		{"npm install lodash", ShellProduce, true},
		{"sudo apt install x", ShellProduce, true},
		{"( sudo ls )", ShellExplore, false}, // terminates on parenthesized sudo
		{"sudo", ShellUnknown, false},
		{"frobnicate --all", ShellUnknown, false},
		{"go build ./... && cp bin/x /tmp/x", ShellProduce, true},
	}
	for _, c := range cases {
		cls, mut := Shell(c.cmd, Options{})
		if cls != c.class || mut != c.mut {
			t.Errorf("Shell(%q) = %s,%t; want %s,%t", c.cmd, cls, mut, c.class, c.mut)
		}
	}
	if cls, _ := Shell("make e2e-smoke", Options{VerifyCommands: []string{"make e2e"}}); cls != ShellVerify {
		t.Error("user verify_commands prefix must classify as verify")
	}
}

func TestInitial(t *testing.T) {
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: "frobnicate"}
	Initial(ev, Options{})
	if ev.Category != event.CatExplore || !ev.Unknown {
		t.Errorf("unknown shell must be explore with unknown flag: %+v", ev)
	}
	for tool, want := range map[string]event.Category{event.ToolEdit: event.CatProduce, event.ToolRead: event.CatExplore, event.ToolTodo: event.CatPlan} {
		e := &event.Event{Kind: event.KindTool, Tool: tool}
		Initial(e, Options{})
		if e.Category != want {
			t.Errorf("%s -> %s, want %s", tool, e.Category, want)
		}
	}
}

func TestIsTestPath(t *testing.T) {
	yes := []string{"tests/test_a.py", "src/__tests__/a.js", "a/spec/x.rb", "pkg/x_test.go", "test_x.py", "app.test.ts",
		"Foo.spec.tsx", "src/test/java/a/FooTest.java", "FooTests.cs", "lib_test.rs"}
	no := []string{"src/app.ts", "testing_utils.py", "contest.go", "attest/x.go", "src/latest.py"}
	for _, p := range yes {
		if !IsTestPath(p) {
			t.Errorf("%s should be a test path", p)
		}
	}
	for _, p := range no {
		if IsTestPath(p) {
			t.Errorf("%s should not be a test path", p)
		}
	}
}

func TestManifestAndConfig(t *testing.T) {
	if !IsManifest("web/package.json") || IsManifest("web/app.json") {
		t.Error("manifest detection")
	}
	if !IsToolConfig("tsconfig.json") || IsToolConfig("src/config.ts") {
		t.Error("tool config detection")
	}
}
