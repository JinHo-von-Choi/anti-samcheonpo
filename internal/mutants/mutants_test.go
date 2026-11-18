package mutants

import (
	"strings"
	"testing"
)

// The output of go-mutesting on one changed line, as it prints it.
func TestParseGoOutput(t *testing.T) {
	out := "ESCAPED internal/pathnorm/pathnorm.go:34 (conditional/negated)\n" +
		"------------------------------------------------------------\n" +
		"PASS internal/pathnorm/pathnorm.go:35 (branch/if)\n" +
		"ESCAPED internal/pathnorm/pathnorm.go:34 (numbers/incrementer)\n" +
		"The mutation score is 33.33% (1 killed, 2 escaped, 0 errored, 0 not covered, 0 skipped, 3 total)\n"
	r, ok := parseGo(out)
	if !ok || r.Total != 3 || r.Missed != 2 || len(r.Survivors) != 2 || r.Survivors[0] != "internal/pathnorm/pathnorm.go:34: conditional/negated" {
		t.Fatalf("%+v %v", r, ok)
	}
	if _, ok := parseGo("no mutations found\n"); ok {
		t.Fatal("output without a summary read as a run")
	}
}

// Only the functions and methods holding a changed code line are named; the
// blank lines added before a new function belong to none.
func TestMutmutPatterns(t *testing.T) {
	src := "def clamp(x):\n    return x\n\n\ndef discount(t):\n    return t\n\n\nclass Cart:\n    size = 0\n\n    def add(self, p):\n        return p\n"
	got := mutmutPatterns("src/calc/__init__.py", src, [][2]int{{3, 6}, {12, 13}})
	want := []string{"calc.x_discount__mutmut_*", "calc.xǁCartǁadd__mutmut_*"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("patterns %q, want %q", got, want)
	}
}
