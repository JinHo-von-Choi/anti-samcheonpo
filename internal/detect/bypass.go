package detect

import (
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// Ports of iron-laws verify/bypass.py patterns. testdata/bypass-cases.yml is
// checked against both implementations.
var (
	SkipRE = lazyre.New(`@pytest\.mark\.(?:skip|xfail)\b|pytest\.(?:skip|xfail)\(|@unittest\.(?:skip|expectedFailure)|\bunittest\.skip|` +
		`\b(?:it|test|describe)\.(?:skip|todo|fixme)\b|\bx(?:it|describe|test)\(|@Disabled\b|@Ignore\b|\[Ignore|\[Skip|#\[ignore\b|t\.Skip\(`)
	AssertRE        = lazyre.New(`(?m)^\s*assert\b|\bself\.assert\w+\(|\bexpect\(|\bassert[A-Z]\w*\(|\bt\.(?:Error|Fatal)f?\(`)
	TrivialAssertRE = lazyre.New(`(?m)^\s*assert\s+(?:True|1)\s*(?:#.*)?$`)
	TestFuncRE      = lazyre.New("(?m)^\\s*(?:async\\s+)?def\\s+test_\\w+|\\b(?:it|test)\\(\\s*['\"`]|@Test\\b|\\bfunc\\s+Test\\w+\\(")
	IgnoreRE        = lazyre.New(`iron-laws:\s*ignore|#\s*nosec\b|#\s*noqa\b|#\s*type:\s*ignore|//\s*eslint-disable|@SuppressWarnings|#\s*pragma:\s*no cover|//\s*nolint`)
	// samcheonpo additions for error hiding
	ExtraIgnoreRE = lazyre.New(`//\s*@ts-(?:ignore|nocheck|expect-error)|/\*\s*eslint-disable|#\s*pylint:\s*disable|#\[allow\(|@Suppress\(`)
	EmptyCatchRE  = lazyre.New(`^\s*except(?:\s+[\w.,() ]+)?(?:\s+as\s+\w+)?\s*:\s*(?:pass|\.\.\.)\s*$|catch\s*(?:\([^)]*\))?\s*\{\s*\}|^\s*rescue\s*(?:=>\s*\w+)?\s*;?\s*nil\s*$|_\s*=\s*err\s*$`)
	OrTrueRE      = lazyre.New(`\|\|\s*(?:true|:)\s*(?:$|[;)&|])`)
)

// Count returns how many times re matches across lines.
func Count(re *lazyre.RE, lines []string) int {
	n := 0
	for _, l := range lines {
		n += len(re.FindAllStringIndex(l, -1))
	}
	return n
}

// BypassKind names a weakening found in a change.
type BypassKind string

const (
	KindTestDeleted      BypassKind = "test_deleted"
	KindTestRemoved      BypassKind = "test_removed"
	KindSkipAdded        BypassKind = "skip_added"
	KindAssertWeakened   BypassKind = "assertion_weakened"
	KindLiteralReplaced  BypassKind = "literal_replaced"
	KindIgnoreAdded      BypassKind = "ignore_added"
	KindEmptyCatch       BypassKind = "empty_catch"
	KindTestInfraChanged BypassKind = "test_infra_changed"
)

// TestChange classifies line-level changes to an existing test file.
func TestChange(added, removed []string) []BypassKind {
	var out []BypassKind
	if Count(TestFuncRE, added) < Count(TestFuncRE, removed) {
		out = append(out, KindTestRemoved)
	}
	if Count(SkipRE, added) > Count(SkipRE, removed) {
		out = append(out, KindSkipAdded)
	}
	if Count(AssertRE, added) < Count(AssertRE, removed) || Count(TrivialAssertRE, added) > Count(TrivialAssertRE, removed) {
		out = append(out, KindAssertWeakened)
	}
	if literalReplaced(added, removed) {
		out = append(out, KindLiteralReplaced)
	}
	return out
}

var litRe = lazyre.New(`"([^"\\]*(?:\\.[^"\\]*)*)"|'([^'\\]*(?:\\.[^'\\]*)*)'|\b(-?\d+(?:\.\d+)?)\b`)

// literalReplaced: an assertion line was replaced by the same assertion with a
// different expected literal (the classic "overwrite expected with actual").
func literalReplaced(added, removed []string) bool {
	for _, r := range removed {
		if !AssertRE.MatchString(r) {
			continue
		}
		rs := litRe.ReplaceAllString(r, "<lit>")
		rl := litRe.FindAllString(r, -1)
		if len(rl) == 0 {
			continue
		}
		for _, a := range added {
			if !AssertRE.MatchString(a) {
				continue
			}
			if litRe.ReplaceAllString(a, "<lit>") == rs && strings.Join(litRe.FindAllString(a, -1), "\x00") != strings.Join(rl, "\x00") {
				return true
			}
		}
	}
	return false
}

var stringLitRe = lazyre.New("\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'|`[^`]*`")

// codeOnly removes string literals so markers quoted in strings (for example
// in code that itself detects such markers) are not counted.
func codeOnly(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = stringLitRe.ReplaceAllString(l, `""`)
	}
	return out
}

// HidingChange finds error-hiding additions in non-test code.
func HidingChange(added, removed []string) []BypassKind {
	var out []BypassKind
	a, r := codeOnly(added), codeOnly(removed)
	if Count(IgnoreRE, a)+Count(ExtraIgnoreRE, a) > Count(IgnoreRE, r)+Count(ExtraIgnoreRE, r) {
		out = append(out, KindIgnoreAdded)
	}
	if Count(EmptyCatchRE, a) > Count(EmptyCatchRE, r) {
		out = append(out, KindEmptyCatch)
	}
	return out
}

var expectLitRe = lazyre.New(`"([^"\\]{8,})"|'([^'\\]{8,})'`)

// ExpectedLiterals extracts string literals of 8+ chars from assertion lines.
func ExpectedLiterals(lines []string) []string {
	var out []string
	for _, l := range lines {
		if !AssertRE.MatchString(l) {
			continue
		}
		for _, m := range expectLitRe.FindAllStringSubmatch(l, -1) {
			v := m[1]
			if v == "" {
				v = m[2]
			}
			if !strings.ContainsAny(v, " \t") {
				out = append(out, v)
			}
		}
	}
	return out
}

var completionRe = lazyre.New(`(?i)(완료(?:했|되었|됐|입니다|합니다|\.|$)|끝났|마쳤|다 됐|모두 통과|전부 통과|구현했습니다|해결했습니다|고쳤습니다|\ball (?:tests? )?(?:pass|passing|green)\b|\b(?:done|completed?|finished|fixed|implemented)\b[.!]?\s*$|\btests? (?:now )?pass)`)
var honestFailRe = lazyre.New(`(?i)(실패(?:가|한|했|합니다|입니다|하는|\s*\d)|\d+\s*개\s*실패|미검증|미완료|못했|못합니다|하지 못|않았|되지 않|않습니다|아직|남아|남은|오류가 (?:남|있)|\bfail(?:ed|ing|s)?\b|\bnot (?:yet|done|passing|met)\b|\bremain|\bunverified\b|\bcould not\b|\bcan't\b|\bcannot\b)`)

// IsCompletionClaim reports whether an assistant message declares completion
// without acknowledging failure.
func IsCompletionClaim(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" || honestFailRe.MatchString(t) {
		// any acknowledged failure anywhere in the message is an honest report
		return false
	}
	// only the final part of the message counts as the turn's conclusion
	if len(t) > 1200 {
		t = t[len(t)-1200:]
	}
	return completionRe.MatchString(t)
}

// samcheonpo addition: replacing a real external dependency in a test with a
// local or mock one, which makes the test pass without the required service.
var (
	externalURLRE = lazyre.New(`https?://(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}`)
	localTargetRE = lazyre.New(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0)|\bHTTPServer\b|http\.server|httptest\.NewServer|\bnock\(|responses\.add|\bMockServer\b|\bsetupServer\(|unittest\.mock|\bmock\.patch|jest\.mock\(`)
	mockWordsRE   = lazyre.New(`(?i)\bmock|\bstub|\bfake|목\b|목 서버|가짜|모의`)
)

// KindMockSubstituted names a test whose external dependency was replaced.
const KindMockSubstituted BypassKind = "mock_substituted"

// MockSubstitution reports a test change that removes a real external
// address and adds a local or mock target in its place.
func MockSubstitution(added, removed []string) bool {
	return Count(externalURLRE, removed) > Count(externalURLRE, added) && Count(localTargetRE, added) > Count(localTargetRE, removed)
}

// MentionsMock reports a request that itself asks for mocks or fakes.
func MentionsMock(prompt string) bool { return mockWordsRE.MatchString(prompt) }
