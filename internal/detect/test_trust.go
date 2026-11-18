package detect

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testsel"
)

// Rules that judge whether a passing test checks the change. They start in
// shadow: each is recorded and measured before it may advise.
const (
	// RuleTestEditToPass is a failing test made to pass by editing only the
	// test.
	RuleTestEditToPass = "s5.test_edit_to_pass"
	// RuleSubjectMocked is a test that mocks the feature code the session
	// changed.
	RuleSubjectMocked = "s5.subject_mocked"
	// RuleTestSpecialCase is feature code that branches on running under a
	// test, or on one test's input.
	RuleTestSpecialCase = "s5.test_special_case"
	// RuleSnapshotRegenerated is a failing run turned green by regenerating
	// the expected output.
	RuleSnapshotRegenerated = "s5.snapshot_regenerated"
)

// testEditRequestRe finds a request that asks to change tests themselves: a
// verb that changes together with what it changes.
var testEditRequestRe = lazyre.New(`(?i)(?:테스트|시험|기대값|스냅샷|단언|골든|픽스처)\S{0,3}\s*(?:을|를|도|만)?\s*(?:고쳐|고치|수정|바꿔|바꾸|갱신|업데이트|지워|지우|삭제|다시\s*써|새로\s*써|맞춰)|` +
	`\b(?:fix|update|change|modify|rewrite|adjust|delete|remove|regenerate|refresh)\s+(?:the\s+|these\s+|those\s+|all\s+)?(?:failing\s+|broken\s+|existing\s+)?(?:tests?|specs?|snapshots?|assertions?|expectations?|expected (?:values?|output)|golden files?|fixtures?)\b|` +
	`\bupdate[- ]snapshots?\b`)

// RequestsTestEdit reports a request that itself asks to change tests,
// their expected values or their snapshots.
func RequestsTestEdit(text string) bool { return testEditRequestRe.MatchString(text) }

// testTrust is the state of the test trust rules, in State.
type testTrust struct {
	// open marks a repair in progress; failSeq is the failing check it
	// started from.
	open    bool
	failSeq int64
	// failFiles are the test files the failing tests are defined in.
	failFiles map[string]bool
	// failNames are the failing test names, last segment only.
	failNames map[string]bool
	// edited are the test files changed since the failure; feature records a
	// change to anything else.
	edited  map[string]bool
	feature bool
	// pairs are the literals each test function uses, by function name.
	pairs map[string]map[string]bool
	// trivial are the self-written tests that check nothing, by path.
	trivial map[string][]string
	// files sum the changes to each test file, for the receipt.
	files map[string]*TestFileChange
	// noticed marks the commit notice as given.
	noticed bool
	// featureAdded are the lines the session added to each Python feature
	// file, to tell which functions it changed.
	featureAdded map[string]map[string]bool
}

// maxFeatureAdded caps the added lines kept per feature file.
const maxFeatureAdded = 2000

// noteFeatureLines keeps the added lines of a Python feature file.
func (tt *testTrust) noteFeatureLines(pf event.PatchFile) {
	if !strings.HasSuffix(pf.Path, ".py") {
		return
	}
	if tt.featureAdded == nil {
		tt.featureAdded = map[string]map[string]bool{}
	}
	m := tt.featureAdded[pf.Path]
	if m == nil {
		m = map[string]bool{}
		tt.featureAdded[pf.Path] = m
	}
	for _, l := range pf.Added {
		if t := strings.TrimSpace(l); t != "" && len(m) < maxFeatureAdded {
			m[t] = true
		}
	}
}

// pyFuncChanged reports a Python function in the project file rel whose
// body holds a line the session added, read from the file as it is now.
// A function whose header line was added counts through State.changed.
func (e *Engine) pyFuncChanged(rel, name string) bool {
	added := e.trust().featureAdded[rel]
	if len(added) == 0 || e.Root == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(e.Root, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		t := strings.TrimLeft(l, " \t")
		if !strings.HasPrefix(t, "def "+name+"(") && !strings.HasPrefix(t, "async def "+name+"(") {
			continue
		}
		indent := len(l) - len(t)
		for _, body := range lines[i+1:] {
			bt := strings.TrimSpace(body)
			if bt == "" {
				continue
			}
			if len(body)-len(strings.TrimLeft(body, " \t")) <= indent {
				break
			}
			// a short line (pass, return, a bracket) appears in many bodies
			if len(bt) >= 6 && added[bt] {
				return true
			}
		}
	}
	return false
}

// TestFileChange sums what the session did to one test file.
type TestFileChange struct {
	Path        string   `json:"path"`
	Created     bool     `json:"created,omitempty"`
	Deleted     bool     `json:"deleted,omitempty"`
	AssertDelta int      `json:"assert_delta"`
	SkipsAdded  int      `json:"skips_added,omitempty"`
	Mocks       []string `json:"mocks,omitempty"`
}

var mockTargetRe = lazyre.New(`\b(?:mock\.patch|mocker\.patch|patch|patch\.object|jest\.mock|vi\.mock|jest\.doMock|sinon\.stub|gomock\.NewController)\(\s*['"]?([^'",)\s]+)`)

// noteTestFile adds one test file patch to the receipt sums.
func (e *Engine) noteTestFile(ev *event.Event, pf event.PatchFile) {
	tt := e.trust()
	if tt.files == nil {
		tt.files = map[string]*TestFileChange{}
	}
	c := tt.files[pf.Path]
	if c == nil {
		c = &TestFileChange{Path: pf.Path, Created: contains(ev.Created, pf.Path) || e.St.created[pf.Path]}
		tt.files[pf.Path] = c
	}
	if pf.Deleted {
		c.Deleted = true
		return
	}
	c.AssertDelta += Count(assertionRE, pf.Added) - Count(assertionRE, pf.Removed)
	if n := Count(SkipRE, pf.Added) - Count(SkipRE, pf.Removed); n > 0 {
		c.SkipsAdded += n
	}
	for _, l := range pf.Added {
		for _, m := range mockTargetRe.FindAllStringSubmatch(l, -1) {
			if !slicesContains(c.Mocks, m[1]) && len(c.Mocks) < 10 {
				c.Mocks = append(c.Mocks, m[1])
			}
		}
	}
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestChanges lists, by path, what the session did to test files.
func (e *Engine) TestChanges() []TestFileChange {
	if e.St.trust == nil {
		return nil
	}
	var out []TestFileChange
	for _, c := range e.St.trust.files {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// commitNotice tells once, at a commit or push, that a test finding is
// still standing: a weakened test, or tests that pass without the change.
func (e *Engine) commitNotice(ev *event.Event, sigs *[]Signal) {
	tt := e.trust()
	if tt.noticed || ev.Tool != event.ToolShell {
		return
	}
	if k, _ := classify.Published(ev.Cmd); k != classify.PublishCommit && k != classify.PublishPush {
		return
	}
	for _, v := range e.Verdicts {
		if v.Level < L1 || v.Suppressed {
			continue
		}
		if v.Rule == "s5.test_weakening" || (v.Rule == RuleFalseDone && v.Facts["kind"] == "baseline_pass") {
			tt.noticed = true
			p, _ := v.Facts["path"].(string)
			if p == "" {
				p, _ = v.Facts["tests"].(string)
			}
			e.add(sigs, Signal{Detector: "S5", Rule: "s5.test_weakening", Confidence: 0.6, Level: L1, Evidence: []int64{v.Seq, ev.Seq},
				Facts: map[string]any{"path": p, "kind": "commit_notice", "finding": v.Rule}})
			return
		}
	}
}

func (e *Engine) trust() *testTrust {
	if e.St.trust == nil {
		e.St.trust = &testTrust{pairs: map[string]map[string]bool{}, trivial: map[string][]string{}}
	}
	return e.St.trust
}

// testTrustObserve runs the test trust rules on one tool event.
func (e *Engine) testTrustObserve(ev *event.Event, sigs *[]Signal) {
	// a failed write changed nothing; a failed shell command is a failing
	// check, which is what these rules start from
	if ev.Kind != event.KindTool || ev.IsError && ev.Tool != event.ToolShell {
		return
	}
	tt := e.trust()
	written := e.changedByEvent(ev)
	e.commitNotice(ev, sigs)
	for _, pf := range ev.Patch {
		if classify.IsTestPath(pf.Path) {
			e.noteTestFile(ev, pf)
		}
		if classify.IsTestPath(pf.Path) && !pf.Deleted {
			e.collectPairs(pf)
			if contains(ev.Created, pf.Path) || e.St.created[pf.Path] {
				e.noteTrivial(pf)
			}
			e.subjectMocked(ev, pf, sigs)
		} else if !pf.Deleted && e.featurePath(pf.Path) {
			tt.noteFeatureLines(pf)
			e.specialCase(ev, pf, sigs)
		}
	}
	if tt.open {
		for _, p := range written {
			switch {
			case tt.failFiles[p] || classify.IsTestExpectationPath(p):
				tt.edited[p] = true
			case e.featurePath(p):
				tt.feature = true
			}
		}
	}
	e.snapshotRegenerated(ev, written, sigs)
	if ev.Category != event.CatVerify || ev.Tool != event.ToolShell || ev.ExitCode == nil {
		return
	}
	switch {
	case failing(ev):
		e.noteFailure(ev)
	case *ev.ExitCode == 0 && tt.open:
		if len(tt.edited) > 0 && !tt.feature && !RequestsTestEdit(e.RequestText()) {
			var files []string
			for p := range tt.edited {
				files = append(files, p)
			}
			sort.Strings(files)
			e.add(sigs, Signal{Detector: "S5", Rule: RuleTestEditToPass, Confidence: 0.6, Level: L1, Evidence: []int64{tt.failSeq, ev.Seq},
				Facts: map[string]any{"path": strings.Join(files, ", "), "cmd": ev.CmdNorm}})
		}
		tt.open, tt.failSeq, tt.failFiles, tt.failNames, tt.edited, tt.feature = false, 0, nil, nil, nil, false
	}
}

// changedByEvent lists the project paths one event changed.
func (e *Engine) changedByEvent(ev *event.Event) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range e.WrittenPaths(ev) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, pf := range ev.Patch {
		if !seen[pf.Path] {
			seen[pf.Path] = true
			out = append(out, pf.Path)
		}
	}
	return out
}

// featurePath reports a path whose change is feature work: not a test, a
// document, tool state or a scratch file.
func (e *Engine) featurePath(p string) bool {
	return !classify.IsTestPath(p) && !classify.IsTestExpectationPath(p) && !IsDocPath(p) && !IsToolStatePath(p) && !pathnorm.IsTempPath(p) &&
		!testsel.InfraNames[path.Base(p)]
}

// noteFailure starts, or widens, the repair of a failing check: it records
// which test files hold the failing tests.
func (e *Engine) noteFailure(ev *event.Event) {
	tt := e.trust()
	if !tt.open {
		tt.open, tt.failSeq, tt.failFiles, tt.failNames, tt.edited, tt.feature = true, ev.Seq, map[string]bool{}, map[string]bool{}, map[string]bool{}, false
	}
	var names []string
	for _, f := range ev.FailedTests {
		n := lastSegment(f)
		if n != "" {
			tt.failNames[n] = true
			names = append(names, n)
		}
		// a pytest id names its file
		if i := strings.Index(f, "::"); i > 0 && classify.IsTestPath(f[:i]) {
			tt.failFiles[filepath.ToSlash(f[:i])] = true
		}
	}
	if len(names) == 0 || e.Root == "" || ev.Cmd == "" {
		return
	}
	sel := testsel.Resolve(e.Root, ev.Cmd)
	for _, f := range sel.Files {
		b, err := os.ReadFile(filepath.Join(e.Root, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		src := string(b)
		for _, n := range names {
			if definesTest(src, n) {
				tt.failFiles[f] = true
			}
		}
		if tt.failFiles[f] {
			// the literals of a test the session did not write
			e.collectPairs(event.PatchFile{Path: f, Added: strings.Split(src, "\n")})
		}
	}
}

// lastSegment is the test's own name in a runner's id: after "::", the last
// "/" of a subtest, or the last " > " of a nested title.
func lastSegment(id string) string {
	for _, sep := range []string{"::", " > ", " › "} {
		if i := strings.LastIndex(id, sep); i >= 0 {
			id = id[i+len(sep):]
		}
	}
	if i := strings.Index(id, "/"); i > 0 && strings.HasPrefix(id, "Test") {
		id = id[:i]
	}
	if i := strings.IndexAny(id, "[("); i > 0 {
		id = id[:i]
	}
	return strings.TrimSpace(id)
}

// definesTest reports test source that defines the named test.
func definesTest(src, name string) bool {
	q := regexp.QuoteMeta(name)
	re, err := regexp.Compile(`(?m)\bfunc\s+` + q + `\s*\(|\bdef\s+` + q + `\s*\(|\b(?:it|test|describe)\s*\(\s*['"` + "`" + `]` + q + `['"` + "`" + `]|\bfn\s+` + q + `\s*\(`)
	return err == nil && re.MatchString(src)
}

// snapshot update commands and the expected-output files they rewrite.
var snapshotUpdateRe = lazyre.New(`(?:^|\s)(?:jest|vitest|react-scripts test|playwright test|npm (?:run )?test|pnpm (?:run )?test|yarn (?:run )?test)\b[^|;&]*(?:\s-u\b|\s--updateSnapshot\b|\s--update-snapshots?\b)|` +
	`\bpytest\b[^|;&]*\s--(?:snapshot-update|force-regen|update-goldens?|regen-all)\b|\bcargo\s+insta\s+(?:accept|review)\b|\bINSTA_UPDATE=(?:always|new)\b|` +
	`\bgo\s+test\b[^|;&]*\s-(?:update|golden|update-golden|update_golden)\b|\bUPDATE_(?:GOLDEN|SNAPSHOTS?|EXPECT(?:ED)?)=(?:1|true|yes)\b`)

// SnapshotUpdate reports a command that rewrites expected test output.
func SnapshotUpdate(cmd string) bool { return snapshotUpdateRe.MatchString(cmd) }

// snapshotRegenerated judges a failing run answered by rewriting what the
// tests expect, with no change to the feature code since the failure.
func (e *Engine) snapshotRegenerated(ev *event.Event, written []string, sigs *[]Signal) {
	tt := e.trust()
	if !tt.open || tt.feature || RequestsTestEdit(e.RequestText()) {
		return
	}
	target := ""
	if ev.Tool == event.ToolShell && SnapshotUpdate(ev.Cmd) {
		target = "command"
	} else {
		for _, p := range written {
			if classify.IsTestExpectationPath(p) {
				target = p
				break
			}
		}
	}
	if target == "" {
		return
	}
	facts := map[string]any{"cmd": ev.CmdNorm}
	if target != "command" {
		facts["path"] = target
	}
	e.add(sigs, Signal{Detector: "S5", Rule: RuleSnapshotRegenerated, Confidence: 0.6, Level: L1, Evidence: []int64{tt.failSeq, ev.Seq}, Facts: facts})
}

// mock targets whose module the text states.
var (
	pyPatchRe = lazyre.New(`\b(?:mock\.patch|mocker\.patch|patch)\(\s*['"]([A-Za-z_][\w.]*)\.([A-Za-z_]\w*)['"]`)
	jsMockRe  = lazyre.New(`\b(?:jest|vi)\.(?:mock|doMock|unstable_mockModule)\(\s*['"](\.{1,2}/[^'"]+)['"]`)
)

// subjectMocked judges a test change that mocks feature code this session
// changed: the test then checks the mock, not the change.
func (e *Engine) subjectMocked(ev *event.Event, pf event.PatchFile, sigs *[]Signal) {
	if MentionsMock(e.RequestText()) || len(e.St.prodPaths) == 0 {
		return
	}
	for _, line := range pf.Added {
		for _, m := range pyPatchRe.FindAllStringSubmatch(line, -1) {
			mod, name := m[1], m[2]
			base := strings.ReplaceAll(mod, ".", "/")
			for _, cand := range []string{base + ".py", base + "/__init__.py", "src/" + base + ".py"} {
				if e.St.prodPaths[cand] && (e.St.changed(name) || e.pyFuncChanged(cand, name)) {
					e.add(sigs, Signal{Detector: "S5", Rule: RuleSubjectMocked, Confidence: 0.6, Level: L1, Evidence: []int64{ev.Seq},
						Facts: map[string]any{"path": pf.Path, "target": mod + "." + name, "subject": cand}})
					return
				}
			}
		}
		for _, m := range jsMockRe.FindAllStringSubmatch(line, -1) {
			rel := path.Clean(path.Join(path.Dir(pf.Path), m[1]))
			for _, ext := range []string{"", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", "/index.ts", "/index.js"} {
				if e.St.prodPaths[rel+ext] {
					e.add(sigs, Signal{Detector: "S5", Rule: RuleSubjectMocked, Confidence: 0.6, Level: L1, Evidence: []int64{ev.Seq},
						Facts: map[string]any{"path": pf.Path, "target": m[1], "subject": rel + ext}})
					return
				}
			}
		}
	}
}

// testEnvRe finds feature code that asks whether a test is running.
var testEnvRe = lazyre.New(`process\.env\.NODE_ENV\s*[!=]==?\s*['"]test['"]|['"]test['"]\s*[!=]==?\s*process\.env\.NODE_ENV|process\.env\.(?:JEST_WORKER_ID|VITEST)\b|import\.meta\.vitest|` +
	`['"]pytest['"]\s+in\s+sys\.modules|PYTEST_CURRENT_TEST|\btesting\.Testing\(\)|os\.Getenv\(\s*"CI"\s*\)|os\.environ(?:\.get\(\s*|\[\s*)['"]CI['"]|process\.env\.CI\b|` +
	`\bcfg!\(\s*test\s*\)|Rails\.env\.test\?`)

var codeExtRe = lazyre.New(`\.(?:go|py|js|jsx|ts|tsx|mjs|cjs|rb|rs|java|kt|cs|php|swift)$`)

// condRe finds a line that compares; returnLitRe a line that returns one
// literal.
var (
	condRe       = lazyre.New(`^\s*(?:if|elif|else\s+if|case|when|switch|match)\b|[!=]==?|\?\s`)
	returnLitRe  = lazyre.New(`^\s*(?:return|=>)\s+("[^"\n]*"|'[^'\n]*'|` + "`[^`\\n]*`" + `|-?\d+(?:\.\d+)?)\s*[;,)]?\s*$|\breturn\s+("[^"\n]*"|'[^'\n]*'|-?\d+(?:\.\d+)?)\s*;?\s*\}?\s*$`)
	literalAnyRe = lazyre.New(`"([^"\\\n]*)"|'([^'\\\n]*)'|(-?\b\d+(?:\.\d+)?\b)`)
)

// specialCase judges feature code that branches on a test running, or on
// one test's own input to return that test's expected value.
func (e *Engine) specialCase(ev *event.Event, pf event.PatchFile, sigs *[]Signal) {
	if !codeExtRe.MatchString(pf.Path) {
		return
	}
	for _, line := range pf.Added {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "*") {
			continue
		}
		if testEnvRe.MatchString(line) {
			e.add(sigs, Signal{Detector: "S5", Rule: RuleTestSpecialCase, Confidence: 0.6, Level: L1, Evidence: []int64{ev.Seq},
				Facts: map[string]any{"path": pf.Path, "kind": "test_env_branch"}})
			return
		}
	}
	tt := e.trust()
	if len(tt.failNames) == 0 {
		// the branch is judged only after a test failed: before that, a value
		// shared by code and test is the specification both follow
		return
	}
	for i, line := range pf.Added {
		if !condRe.MatchString(line) {
			continue
		}
		in := literals(line)
		if len(in) == 0 {
			continue
		}
		var out []string
		for j := i; j < len(pf.Added) && j <= i+3; j++ {
			if m := returnLitRe.FindStringSubmatch(pf.Added[j]); m != nil {
				v := m[1]
				if v == "" {
					v = m[2]
				}
				out = append(out, unquote(v))
			}
		}
		for name, lits := range tt.pairs {
			if !tt.failNames[name] {
				continue
			}
			for _, a := range in {
				if !lits[a] {
					continue
				}
				for _, b := range out {
					if _, copied := e.St.testLits[b]; b == a || !lits[b] || copied {
						// an expected string the session's own test holds is
						// s5.answer_copy's finding
						continue
					}
					e.add(sigs, Signal{Detector: "S5", Rule: RuleTestSpecialCase, Confidence: 0.5, Level: L1, Evidence: []int64{ev.Seq},
						Facts: map[string]any{"path": pf.Path, "kind": "input_special_case", "test": name}})
					return
				}
			}
		}
	}
}

// literals returns the uncommon literal values of one line.
func literals(line string) []string {
	var out []string
	for _, m := range literalAnyRe.FindAllStringSubmatch(line, -1) {
		v := m[1] + m[2] + m[3]
		if m[3] == "" && m[1] == "" && m[2] == "" {
			continue
		}
		if !commonLiteral(v) {
			out = append(out, v)
		}
	}
	return out
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'' || v[0] == '`') {
		return v[1 : len(v)-1]
	}
	return v
}

// commonLiteral reports a value too common to tie code to one test: short
// strings, booleans and null words, small integers, HTTP methods and status
// codes.
func commonLiteral(v string) bool {
	if len([]rune(v)) < 3 {
		return true
	}
	switch strings.ToLower(v) {
	case "true", "false", "null", "nil", "none", "undefined", "get", "post", "put", "patch", "delete", "head", "options":
		return true
	}
	if n, err := strconv.Atoi(v); err == nil {
		return (n >= -1 && n <= 10) || (n >= 100 && n <= 599)
	}
	return false
}

// collectPairs keeps, per test function, the uncommon literals it uses.
func (e *Engine) collectPairs(pf event.PatchFile) {
	tt := e.trust()
	for header, lines := range splitByTestFunc(pf.Added) {
		name := testFuncName(header)
		if name == "" {
			continue
		}
		set := tt.pairs[name]
		if set == nil {
			set = map[string]bool{}
			tt.pairs[name] = set
		}
		for _, l := range lines {
			for _, v := range literals(l) {
				set[unquote(v)] = true
			}
		}
	}
}

var testFuncNameRe = lazyre.New(`func\s+(?:\([^)]*\)\s*)?(Test\w*)|def\s+(test_\w*)|(?:it|test)\s*\(\s*['"` + "`" + `]([^'"` + "`" + `]+)`)

// testFuncName is the name in a test function header as splitByTestFunc
// keys it.
func testFuncName(header string) string {
	if i := strings.LastIndex(header, "#"); i >= 0 {
		header = header[:i]
	}
	m := testFuncNameRe.FindStringSubmatch(header)
	if m == nil {
		return ""
	}
	return m[1] + m[2] + m[3]
}

// tests that check nothing: no assertion, only whether a mock was called,
// or an expected value computed by the code under test itself.
var (
	mockCallAssertRe = lazyre.New(`assert_called|assert_any_call|assert_has_calls|assert_not_called|toHaveBeenCalled|toBeCalled|\.EXPECT\(\)|\.Times\(`)
	selfEqualRe      = lazyre.New(`\b(?:assert|expect)\b[^\n]*?\b(\w+\([^()]*\))\s*(?:==|\)\.toBe\(|\)\.toEqual\(|,)\s*(\w+\([^()]*\))`)
	raisesRe         = lazyre.New(`pytest\.raises|assertRaises|toThrow|assert\.Panics|should_panic|expect\(\s*\(\)\s*=>`)
)

// noteTrivial records the self-written test functions of one file that
// check nothing.
func (e *Engine) noteTrivial(pf event.PatchFile) {
	tt := e.trust()
	var kinds []string
	for header, lines := range splitByTestFunc(pf.Added) {
		name := testFuncName(header)
		if name == "" {
			continue
		}
		asserts, mockCalls := Count(assertionRE, lines), Count(mockCallAssertRe, lines)
		switch {
		case mockCalls > 0 && asserts <= mockCalls:
			kinds = append(kinds, name+": mock_calls_only")
		case asserts == 0 && Count(raisesRe, lines) == 0 && Count(tableRE, lines) == 0:
			kinds = append(kinds, name+": no_assertion")
		default:
			for _, l := range lines {
				if m := selfEqualRe.FindStringSubmatch(l); m != nil && m[1] == m[2] {
					kinds = append(kinds, name+": self_compared")
					break
				}
			}
		}
	}
	if len(kinds) > 0 {
		sort.Strings(kinds)
		tt.trivial[pf.Path] = kinds
	} else {
		delete(tt.trivial, pf.Path)
	}
}

// TrivialTests lists the self-written test functions that check nothing, as
// "path: name: kind" lines.
func (e *Engine) TrivialTests() []string {
	if e.St.trust == nil {
		return nil
	}
	var out []string
	for p, kinds := range e.St.trust.trivial {
		for _, k := range kinds {
			out = append(out, p+": "+k)
		}
	}
	sort.Strings(out)
	return out
}

// testInfraChange reports a change to a file that decides how tests are
// collected or run, wherever it lives. A shared settings file counts only
// when a changed line is about the test runner.
func testInfraChange(pf event.PatchFile) bool {
	base := path.Base(pf.Path)
	switch base {
	case "setup.cfg", "tox.ini", "pyproject.toml":
		for _, l := range append(append([]string{}, pf.Added...), pf.Removed...) {
			if pytestSettingRe.MatchString(l) {
				return true
			}
		}
		return false
	}
	return classify.TestInfraNames[base] || testsel.InfraNames[base]
}

var pytestSettingRe = lazyre.New(`(?i)pytest|^\s*(?:addopts|testpaths|python_files|python_classes|python_functions|norecursedirs|collect_ignore|markers|filterwarnings)\s*=`)
