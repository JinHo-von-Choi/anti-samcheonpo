package live

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
)

// Each case starts from a committed module, makes the session's change,
// and reruns the passing command on the change undone.
func TestBaselineOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		start    map[string]string
		change   map[string]string
		refactor bool
		want     string
	}{
		{
			name:   "a test that reproduces the fixed bug",
			start:  map[string]string{"calc.go": "package calc\n\nfunc Add(a, b int) int { return a - b }\n"},
			change: map[string]string{"calc.go": "package calc\n\nfunc Add(a, b int) int { return a + b }\n", "calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(Add(1, 2))\n\t}\n}\n"},
			want:   BaselineVerified,
		},
		{
			name:   "a test of behaviour the change did not touch",
			start:  map[string]string{"calc.go": "package calc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Sub(a, b int) int { return a + b }\n"},
			change: map[string]string{"calc.go": "package calc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Sub(a, b int) int { return a - b }\n", "calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(Add(1, 2))\n\t}\n}\n"},
			want:   BaselinePass,
		},
		{
			name:   "a test that only calls a new function",
			start:  map[string]string{"calc.go": "package calc\n\nfunc Add(a, b int) int { return a + b }\n"},
			change: map[string]string{"mul.go": "package calc\n\nfunc Mul(a, b int) int { return a * b }\n", "mul_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestMul(t *testing.T) { _ = Mul(2, 3) }\n"},
			want:   BaselineWeak,
		},
		{
			name:     "a refactor keeps behaviour",
			start:    map[string]string{"calc.go": "package calc\n\nfunc Add(a, b int) int { return a + b }\n"},
			change:   map[string]string{"calc.go": "package calc\n\nfunc Add(a, b int) int {\n\tsum := a + b\n\treturn sum\n}\n", "calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(Add(1, 2))\n\t}\n}\n"},
			refactor: true,
			want:     BaselinePreserved,
		},
		{
			name:   "another test of the package fails without the change",
			start:  map[string]string{"calc.go": "package calc\n\nfunc Add(a, b int) int { return a - b }\n", "add_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestAddOld(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(Add(2, 2))\n\t}\n}\n"},
			change: map[string]string{"calc.go": "package calc\n\nfunc Add(a, b int) int { return a + b }\n", "zero_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestZero(t *testing.T) {\n\tif Add(0, 0) != 0 {\n\t\tt.Fatal(Add(0, 0))\n\t}\n}\n"},
			want:   BaselineUndecided,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := reliabilitySession(t)
			root := s.Root
			git(t, root, "init", "-q")
			put := func(files map[string]string) {
				for name, body := range files {
					if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			put(map[string]string{"go.mod": "module example.com/calc\n\ngo 1.21\n"})
			put(c.start)
			git(t, root, "add", "-A")
			git(t, root, "commit", "-qm", "start")
			start, err := snapshotTree(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			put(c.change)
			r := s.compareBaseline(baselineJob{seq: 1, cmd: "go test ./...", start: start, refactor: c.refactor})
			if r == nil || r.Outcome != c.want {
				t.Fatalf("outcome %+v, want %s", r, c.want)
			}
			if out, _ := runGit(context.Background(), root, nil, "worktree", "list", "--porcelain"); strings.Count(out, "worktree ") != 1 {
				t.Fatalf("the comparison left a work tree behind:\n%s", out)
			}
			if out, _ := runGit(context.Background(), root, nil, "status", "--porcelain"); out == "" {
				t.Fatal("the user's uncommitted change disappeared")
			}
		})
	}
}

// A done check at the end of a command that also wrote files is still the
// check; one whose exit the command does not give, or run in another folder,
// is not.
func TestContractCheckAtTheEndOfACommand(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.c = &contract.Contract{Goal: "g", Done: []contract.Check{{Check: "env/bin/python -m pytest tests/", Pure: true}}}
	s.acc.State = contract.StateAccepted
	cases := map[string]bool{
		"env/bin/python -m pytest tests/": true,
		"cat > money.py <<'EOF'\ndef cents(a):\n    return round(a * 100)\nEOF\nenv/bin/python -m pytest tests/": true,
		"cd " + s.Root + " && env/bin/python -m pytest tests/ 2>&1":                                              true,
		"env/bin/python -m pytest tests/ 2>&1 | tail -5; echo done":                                              false,
		"false || env/bin/python -m pytest tests/":                                                               false,
		"cd sub && env/bin/python -m pytest tests/":                                                              false,
		"env/bin/python -m pytest tests/ &":                                                                      false,
	}
	for cmd, want := range cases {
		check, ok := s.contractCheckCommand(cmd)
		if ok != want {
			t.Errorf("%q: matched %v, want %v", cmd, ok, want)
		}
		if ok && check != "env/bin/python -m pytest tests/" {
			t.Errorf("%q: reruns %q, not the contract's check", cmd, check)
		}
	}
}
