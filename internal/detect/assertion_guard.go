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
	if a, r := Count(assertionRE, added), Count(assertionRE, removed); r > a {
		return AssertionVerdict{Blocked: true, Reason: "assertion_count_decreased", Kind: KindAssertionMutilated}
	}
	if a, r := Count(identityRE, added), Count(identityRE, removed); a > r {
		return AssertionVerdict{Blocked: true, Reason: "identity_assertion_substituted", Kind: KindAssertionMutilated}
	}
	if a, r := Count(SkipRE, added), Count(SkipRE, removed); a > r {
		return AssertionVerdict{Blocked: true, Reason: "skip_injected", Kind: KindAssertionMutilated}
	}
	return AssertionVerdict{}
}
