package detect

import (
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// KindAssertionMutilated marks a test file whose assertions were deleted,
// weakened to a constant, or silenced with a skip marker.
const KindAssertionMutilated BypassKind = "assertion_mutilation"

// AssertionVerdict is the pre-write ruling of the assertion guard.
type AssertionVerdict struct {
	Blocked bool
	Reason  string
	Kind    BypassKind
}

// assertionRE counts assertion calls: AssertRE plus require.* (Go testify).
var assertionRE = lazyre.New(`(?m)^\s*assert\b|\bself\.assert\w+\(|\bexpect\(|\bassert[A-Z]\w*\(|\bt\.(?:Error|Fatal)f?\(|\brequire\.\w+\(`)

// identityRE counts assertions that only compare literals and pass by
// construction: assert True/1, expect(<literal>).to*(<literal>), and
// assert/require.Equal|True with literal arguments.
var identityRE = lazyre.New(`(?m)^\s*assert\s+(?:True|1)\s*(?:#.*)?$|` +
	`\bexpect\(\s*(?:true|false|1|0|null|undefined|""|'')\s*\)\s*\.\s*to\w*\(\s*(?:true|false|1|0|null|undefined|""|'')\s*\)|` +
	`\b(?:assert|require)\.(?:Equal|True)\(\s*(?:t\s*,\s*)?(?:true|false|1|0)\s*(?:,\s*(?:true|false|1|0)\s*)?\)`)

// suppressionRE counts the markers that hide a failure: a type check
// escape, a lint escape, or a swallowed exception.
var suppressionRE = lazyre.New(`(?i)#\s*type:\s*ignore|#\s*noqa|//\s*nolint|@SuppressWarnings|eslint-disable|//\s*ts-ignore|//\s*ts-expect-error|except\s+[A-Za-z_][A-Za-z0-9_.]*\s*:\s*$|catch\s*\([^)]*\)\s*\{\s*\}`)

// testFuncHeaderRE finds the start of a test function.
var testFuncHeaderRE = lazyre.New(`^\s*(?:func\s+(?:\([^)]*\)\s*)?Test[A-Za-z0-9_]*\s*\(|def\s+test_[A-Za-z0-9_]*\s*\(|it\s*\(|test\s*\(|describe\s*\()`)

// AssertionGuard scans a test-file diff before it is applied. It holds no
// mutable state, so one guard serves the whole session.
type AssertionGuard struct{}

// NewAssertionGuard returns a guard for pre-write scans.
func NewAssertionGuard() *AssertionGuard { return &AssertionGuard{} }

// InspectDiff judges a whole-file diff: old content first, new content
// second, mirroring git diff order.
func (g *AssertionGuard) InspectDiff(path, old, new string) AssertionVerdict {
	return g.InspectPatch(path, strings.Split(new, "\n"), strings.Split(old, "\n"))
}

// InspectPatch judges the added and removed lines of one file. Only test
// paths are in scope; source edits are left to the other detectors.
func (g *AssertionGuard) InspectPatch(path string, added, removed []string) AssertionVerdict {
	if !classify.IsTestPath(path) {
		return AssertionVerdict{}
	}
	if a, r := Count(assertionRE, added), Count(assertionRE, removed); r > a && !parametrized(added, removed, a) {
		return AssertionVerdict{Blocked: true, Reason: "assertion_count_decreased", Kind: KindAssertionMutilated}
	}
	if a, r := Count(identityRE, added), Count(identityRE, removed); a > r {
		return AssertionVerdict{Blocked: true, Reason: "identity_assertion_substituted", Kind: KindAssertionMutilated}
	}
	if a, r := Count(SkipRE, added), Count(SkipRE, removed); a > r {
		return AssertionVerdict{Blocked: true, Reason: "skip_injected", Kind: KindAssertionMutilated}
	}
	if v := g.suppressionWithAssertionDrop(added, removed); v.Blocked {
		return v
	}
	return AssertionVerdict{}
}

// suppressionWithAssertionDrop judges one test function at a time.
//
// The file wide counts can hide a change: a function that loses its
// assertions while another gains them leaves the file count unchanged. When
// the same function also gains a marker that hides a failure, the change
// is a mute, not a refactor.
func (g *AssertionGuard) suppressionWithAssertionDrop(added, removed []string) AssertionVerdict {
	addedFns := splitByTestFunc(added)
	removedFns := splitByTestFunc(removed)
	for name, aLines := range addedFns {
		rLines, ok := removedFns[name]
		if !ok {
			continue
		}
		if Count(assertionRE, aLines) >= Count(assertionRE, rLines) {
			continue
		}
		if Count(suppressionRE, aLines) <= Count(suppressionRE, rLines) {
			continue
		}
		return AssertionVerdict{Blocked: true, Reason: "suppression_added_with_assertion_drop", Kind: KindAssertionMutilated}
	}
	return AssertionVerdict{}
}

// splitByTestFunc groups lines by the test function they belong to. A line
// before the first test function is not part of any function.
func splitByTestFunc(lines []string) map[string][]string {
	buckets := map[string][]string{}
	current := ""
	seen := map[string]int{}
	for _, line := range lines {
		if testFuncHeaderRE.MatchString(line) {
			base := strings.TrimSpace(line)
			seen[base]++
			current = base + "#" + itoa(seen[base])
			buckets[current] = nil
			continue
		}
		if current != "" {
			buckets[current] = append(buckets[current], line)
		}
	}
	return buckets
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// tableRE finds a test written over a table of cases.
var tableRE = lazyre.New(`@pytest\.mark\.parametrize|\b(?:test|it|describe)\.each\b|for\s+_,\s*\w+\s*:=\s*range\b|\[\]struct\s*\{|\bt\.Run\(|\bsubTest\(|for\s+\w+(?:\s*,\s*\w+)*\s+in\s+\w*(?:cases|tests|params|table)\w*`)

// literalRE finds the expected values an assertion compares against.
var literalRE = lazyre.New(`-?\b\d+(?:\.\d+)?\b|"[^"\n]*"|'[^'\n]*'`)

// parametrized reports a change that folds assertions into a table of
// cases: fewer assertion calls, but a loop or table over the cases, at
// least one assertion left, and every expected value of the removed
// assertions still present in the added lines.
func parametrized(added, removed []string, remaining int) bool {
	if remaining == 0 || Count(tableRE, added) == 0 {
		return false
	}
	text := strings.Join(added, "\n")
	for i, line := range removed {
		if !assertionRE.MatchString(line) {
			continue
		}
		// a Go check states the expectation on the line before t.Fatal,
		// and the t.Fatal text itself is a message, not an expectation
		context := []string{line}
		if goFailRE.MatchString(line) {
			context = []string{goFailRE.ReplaceAllString(line, "")}
			if i > 0 {
				context = append(context, removed[i-1])
			}
		}
		for _, c := range context {
			for _, lit := range literalRE.FindAllString(c, -1) {
				if !strings.Contains(text, strings.Trim(lit, `"'`)) {
					return false
				}
			}
		}
	}
	return true
}

// goFailRE is a Go failure call and its message arguments.
var goFailRE = lazyre.New(`\bt\.(?:Error|Fatal)f?\(.*$`)
