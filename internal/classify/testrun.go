package classify

import (
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

// TestRun is one test runner invocation found in a shell command.
type TestRun struct {
	// Runner is go, pytest, jest, vitest or cargo.
	Runner string
	// Args are the runner's own arguments, after the subcommand.
	Args []string
	// Dir is the folder the command changes to first ("" for the current one).
	Dir string
}

// TestRuns lists the test runner invocations of a shell command, stage by
// stage, with the same launcher and package-script handling as VerifyScope.
func TestRuns(cmd string) []TestRun {
	var out []TestRun
	for _, st := range fp.SplitStages(cmd) {
		n, dir := fp.NormalizeCmd(st)
		n = bareProgram(n)
		for i := 0; i < 3; i++ {
			m := runnerPrefixRe.FindString(n)
			if m == "" {
				break
			}
			n = strings.TrimSpace(n[len(m):])
		}
		w := dropRedirects(shellWords(n))
		if len(w) == 0 {
			continue
		}
		prog := pathnorm.CommandBase(w[0])
		args := w[1:]
		run := TestRun{Dir: dir}
		switch prog {
		case "pytest", "py.test":
			run.Runner, run.Args = "pytest", args
		case "go":
			if len(args) > 0 && args[0] == "test" {
				run.Runner, run.Args = "go", args[1:]
			}
		case "jest":
			run.Runner, run.Args = "jest", args
		case "vitest":
			a := args
			if len(a) > 0 && (a[0] == "run" || a[0] == "watch") {
				a = a[1:]
			}
			run.Runner, run.Args = "vitest", a
		case "npm", "pnpm", "yarn", "bun":
			a := args
			if len(a) > 0 && a[0] == "run" {
				a = a[1:]
			}
			if len(a) > 0 && (a[0] == "test" || a[0] == "t") {
				rest := a[1:]
				for i, x := range rest {
					if x == "--" {
						rest = rest[i+1:]
						break
					}
				}
				run.Runner, run.Args = "script", rest
			}
		case "cargo":
			if len(args) > 0 && (args[0] == "test" || args[0] == "nextest") {
				a := args[1:]
				if args[0] == "nextest" && len(a) > 0 && a[0] == "run" {
					a = a[1:]
				}
				run.Runner, run.Args = "cargo", a
			}
		}
		if run.Runner != "" {
			out = append(out, run)
		}
	}
	return out
}

// GoValueFlag reports a go test flag that takes the next word as its value.
func GoValueFlag(a string) bool { return goValueFlags[a] }

// PytestValueFlag reports a pytest flag that takes the next word as its value.
func PytestValueFlag(a string) bool {
	return pytestValueFlags[a] || a == "-k" || a == "-m" || a == "--deselect" || a == "--ignore" || a == "--ignore-glob"
}

// JSValueFlag reports a jest or vitest flag that takes the next word as its value.
func JSValueFlag(a string) bool {
	return jsValueFlags[a] || a == "-t" || a == "--testNamePattern" || a == "-g" || a == "--grep"
}

// CargoValueFlag reports a cargo test flag that takes the next word as its value.
func CargoValueFlag(a string) bool {
	return cargoValueFlags[a] || a == "-p" || a == "--package" || a == "--test"
}
