// Package mutants runs a mutation testing tool on the lines a change touched.
//
// The change is the difference between a base (the tree a session started
// from, or a commit) and the work tree as it is now, untracked files
// included. Only feature files are mutated: tests and documents are left
// out. Each language goes to its own tool; samcheonpo does not install any.
// A tool only mutates what changed, so a gap in the tests left by changes
// elsewhere is not found: this does not replace a full run.
package mutants

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procgroup"
)

// Result is what one tool found in one language.
type Result struct {
	Lang      string   `json:"lang"`
	Tool      string   `json:"tool"`
	Files     []string `json:"files"`
	Ran       bool     `json:"ran"`
	Total     int      `json:"total"`  // mutants the tests ran against
	Missed    int      `json:"missed"` // mutants no test caught
	Survivors []string `json:"survivors,omitempty"`
	Note      string   `json:"note,omitempty"` // why it did not run, or what limits it
}

// Report is one run over a change.
type Report struct {
	Base    string    `json:"base"`
	At      time.Time `json:"at"`
	Results []Result  `json:"results"`
}

// Change is the feature files a change touched and their new line ranges.
type Change struct {
	Root  string
	Base  string // commit or tree the change is taken from
	Diff  string // unified diff, no context
	Lines map[string][][2]int
}

// Files lists the changed feature files in order.
func (c Change) Files() []string {
	out := make([]string, 0, len(c.Lines))
	for p := range c.Lines {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func git(ctx context.Context, root string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// workTree writes the work tree, untracked files git does not ignore
// included, as a tree object without touching the index.
func workTree(ctx context.Context, root string) (string, error) {
	f, err := os.CreateTemp("", "samcheonpo-mutants-index-*")
	if err != nil {
		return "", err
	}
	idx := f.Name()
	f.Close()
	os.Remove(idx)
	defer os.Remove(idx)
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err := git(ctx, root, nil, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		if _, err := git(ctx, root, env, "read-tree", "HEAD"); err != nil {
			return "", err
		}
	}
	if _, err := git(ctx, root, env, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	return git(ctx, root, env, "write-tree")
}

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// Diff reads the change from base to the work tree.
func Diff(root, base string) (Change, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := Change{Root: root, Base: base, Lines: map[string][][2]int{}}
	tree, err := workTree(ctx, root)
	if err != nil {
		return c, err
	}
	diff, err := git(ctx, root, nil, "diff", "-U0", "--no-color", "--no-renames", "--no-ext-diff", base, tree)
	if err != nil {
		return c, err
	}
	var file string
	var keep strings.Builder
	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	var section strings.Builder
	flush := func() {
		if file != "" && Feature(file) {
			keep.WriteString(section.String())
		}
		section.Reset()
	}
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "diff --git ") {
			flush()
			file = ""
		}
		section.WriteString(l + "\n")
		switch {
		case strings.HasPrefix(l, "+++ b/"):
			file = strings.TrimPrefix(l, "+++ b/")
		case strings.HasPrefix(l, "+++ /dev/null"):
			file = ""
		case file != "" && Feature(file):
			if m := hunkRe.FindStringSubmatch(l); m != nil {
				start, _ := strconv.Atoi(m[1])
				n := 1
				if m[2] != "" {
					n, _ = strconv.Atoi(m[2])
				}
				if n > 0 {
					c.Lines[file] = append(c.Lines[file], [2]int{start, start + n - 1})
				}
			}
		}
	}
	flush()
	c.Diff = keep.String()
	return c, sc.Err()
}

// Feature reports a file a mutation tool should mutate: code that is not a
// test, a test fixture or a document.
func Feature(p string) bool {
	if classify.IsTestPath(p) || classify.IsTestExpectationPath(p) || detect.IsDocPath(p) {
		return false
	}
	return Lang(p) != ""
}

// Lang names the language of a source file, or "" for one no tool covers.
func Lang(p string) string {
	switch path.Ext(p) {
	case ".rs":
		return "rust"
	case ".go":
		return "go"
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return "js"
	case ".php":
		return "php"
	case ".java":
		return "java"
	case ".py":
		return "python"
	}
	return ""
}

// Install is how to get each language's tool, one line each.
var Install = map[string]string{
	"rust":   "cargo install --locked cargo-mutants",
	"go":     "go install github.com/jonbaldie/go-mutesting/v2/cmd/go-mutesting@latest",
	"js":     "npm install --save-dev @stryker-mutator/core",
	"php":    "composer require --dev infection/infection",
	"java":   "pom.xml에 org.pitest:pitest-maven 플러그인 추가",
	"python": "pip install mutmut",
}

// Tools are the tool names reported per language.
var Tools = map[string]string{"rust": "cargo-mutants", "go": "go-mutesting", "js": "StrykerJS", "php": "Infection", "java": "PIT", "python": "mutmut"}

// runner runs one language's tool over its files.
type runner func(ctx context.Context, c Change, files []string, out string) Result

var runners = map[string]runner{"rust": runRust, "go": runGo, "js": runJS, "php": runPHP, "java": runJava, "python": runPython}

// Run mutates the changed lines of every language the change touched.
// timeout bounds each tool.
func Run(c Change, timeout time.Duration) Report {
	rep := Report{Base: c.Base, At: time.Now()}
	byLang := map[string][]string{}
	for _, f := range c.Files() {
		byLang[Lang(f)] = append(byLang[Lang(f)], f)
	}
	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	for _, l := range langs {
		out, err := os.MkdirTemp("", "samcheonpo-mutants-")
		if err != nil {
			rep.Results = append(rep.Results, Result{Lang: l, Tool: Tools[l], Files: byLang[l], Note: err.Error()})
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		r := runners[l](ctx, c, byLang[l], out)
		if ctx.Err() == context.DeadlineExceeded {
			r.Ran, r.Note = false, fmt.Sprintf(lang.T("시간 제한 %s 초과"), timeout)
		}
		cancel()
		os.RemoveAll(out)
		r.Lang, r.Tool, r.Files = l, Tools[l], byLang[l]
		rep.Results = append(rep.Results, r)
	}
	return rep
}

// exec runs a tool in dir and returns its combined output and exit code.
func run(ctx context.Context, dir string, env []string, name string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	procgroup.Set(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	cmd.WaitDelay = 2 * time.Second
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return buf.String(), ee.ExitCode(), nil
	}
	if err != nil {
		return buf.String(), -1, err
	}
	return buf.String(), 0, nil
}

func missing(l string) Result {
	return Result{Note: lang.T("도구 없음. 설치: ") + lang.T(Install[l])}
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return s
}

// commitOf turns a base into a commit a tool can diff against: a tree gets
// a throwaway commit object.
func commitOf(ctx context.Context, root, base string) (string, error) {
	if t, err := git(ctx, root, nil, "cat-file", "-t", base); err == nil && t == "tree" {
		return git(ctx, root, []string{"GIT_AUTHOR_NAME=samcheonpo", "GIT_AUTHOR_EMAIL=samcheonpo@localhost",
			"GIT_COMMITTER_NAME=samcheonpo", "GIT_COMMITTER_EMAIL=samcheonpo@localhost"}, "commit-tree", base, "-m", "samcheonpo mutants base")
	}
	return base, nil
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// runRust gives cargo-mutants the diff; it mutates only the lines in it.
func runRust(ctx context.Context, c Change, files []string, out string) Result {
	if !Available(c.Root, "rust") {
		return missing("rust")
	}
	diff := filepath.Join(out, "change.diff")
	if err := os.WriteFile(diff, []byte(c.Diff), 0o600); err != nil {
		return Result{Note: err.Error()}
	}
	dirs := map[string]bool{}
	for _, f := range files {
		if d := crateDir(c.Root, f); d != "" {
			dirs[d] = true
		}
	}
	if len(dirs) == 0 {
		return Result{Note: lang.T("Cargo.toml을 찾지 못함")}
	}
	r := Result{Ran: true}
	for d := range dirs {
		o := filepath.Join(out, hashOf(d))
		text, code, err := run(ctx, d, nil, "cargo", "mutants", "--in-diff", diff, "--output", o, "--no-shuffle")
		if err != nil {
			return Result{Note: err.Error()}
		}
		res := filepath.Join(o, "mutants.out")
		missed := readLines(filepath.Join(res, "missed.txt"))
		caught := readLines(filepath.Join(res, "caught.txt"))
		timeout := readLines(filepath.Join(res, "timeout.txt"))
		if len(missed)+len(caught)+len(timeout) == 0 && code != 0 {
			return Result{Note: lang.T("cargo mutants 실패: ") + tail(text, 300)}
		}
		rel, _ := filepath.Rel(c.Root, d)
		for _, m := range missed {
			// a workspace member reports paths from the workspace root
			file, _, _ := strings.Cut(m, ":")
			if _, err := os.Stat(filepath.Join(c.Root, file)); err != nil {
				m = filepath.ToSlash(filepath.Join(rel, m))
			}
			r.Survivors = append(r.Survivors, m)
		}
		r.Total += len(missed) + len(caught) + len(timeout)
		r.Missed += len(missed)
	}
	return r
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:6])
}

func readLines(p string) []string {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return lines(string(b))
}

// crateDir is the folder of the Cargo.toml nearest above a file.
func crateDir(root, rel string) string {
	for d := path.Dir(rel); ; d = path.Dir(d) {
		abs := filepath.Join(root, filepath.FromSlash(d))
		if _, err := os.Stat(filepath.Join(abs, "Cargo.toml")); err == nil {
			return abs
		}
		if d == "." || d == "/" {
			return ""
		}
	}
}

// goSurvivorRe reads a mutant go-mutesting reports as not killed, and
// goScoreRe its summary line.
var goSurvivorRe = regexp.MustCompile(`^(ESCAPED|NOT[ _-]COVERED)\s+(\S+:\d+)\s+\(([^)]+)\)`)
var goScoreRe = regexp.MustCompile(`The mutation score is [0-9.]+%? \((\d+) killed, (\d+) escaped, (\d+) errored, (\d+) not covered, (\d+) skipped, (\d+) total\)`)

// runGo gives go-mutesting the changed packages and the base commit; it
// keeps the mutants on lines the diff changed.
func runGo(ctx context.Context, c Change, files []string, out string) Result {
	if !Available(c.Root, "go") {
		return missing("go")
	}
	base, err := commitOf(ctx, c.Root, c.Base)
	if err != nil {
		return Result{Note: err.Error()}
	}
	args := []string{"--git-diff-lines", "--git-diff-base=" + base, "--no-diffs"}
	for _, f := range files {
		args = append(args, "./"+f)
	}
	text, code, err := run(ctx, c.Root, nil, "go-mutesting", args...)
	if err != nil {
		return Result{Note: err.Error()}
	}
	r, ok := parseGo(text)
	if !ok {
		if code != 0 || strings.TrimSpace(text) == "" {
			return Result{Note: lang.T("go-mutesting 실패: ") + tail(text, 300)}
		}
		return Result{Ran: true, Note: lang.T("변이 대상 없음")}
	}
	return r
}

// parseGo reads go-mutesting's terminal output: one line per mutant that
// was not killed and the summary line.
func parseGo(text string) (Result, bool) {
	m := goScoreRe.FindStringSubmatch(text)
	if m == nil {
		return Result{}, false
	}
	killed, _ := strconv.Atoi(m[1])
	escaped, _ := strconv.Atoi(m[2])
	uncovered, _ := strconv.Atoi(m[4])
	r := Result{Ran: true, Total: killed + escaped + uncovered, Missed: escaped + uncovered}
	for _, l := range strings.Split(text, "\n") {
		if s := goSurvivorRe.FindStringSubmatch(strings.TrimSpace(l)); s != nil {
			r.Survivors = append(r.Survivors, s[2]+": "+s[3])
		}
	}
	return r, true
}

// strykerReport is the part of the mutation-testing-elements JSON read here.
type strykerReport struct {
	Files map[string]struct {
		Mutants []struct {
			MutatorName string `json:"mutatorName"`
			Status      string `json:"status"`
			Location    struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"location"`
		} `json:"mutants"`
	} `json:"files"`
}

// runJS gives StrykerJS the changed line ranges; its JSON report is read.
func runJS(ctx context.Context, c Change, files []string, out string) Result {
	bin := filepath.Join(c.Root, "node_modules", ".bin", "stryker")
	if _, err := os.Stat(bin); err != nil {
		return missing("js")
	}
	var ranges []string
	for _, f := range files {
		for _, r := range c.Lines[f] {
			ranges = append(ranges, fmt.Sprintf("%s:%d-%d", f, r[0], r[1]))
		}
	}
	// the json reporter writes here unless the project's config moves it
	report := filepath.Join(c.Root, "reports", "mutation", "mutation.json")
	started := time.Now().Add(-time.Second)
	text, code, err := run(ctx, c.Root, nil, bin, "run", "--mutate", strings.Join(ranges, ","), "--incremental", "--reporters", "json")
	if err != nil {
		return Result{Note: err.Error()}
	}
	b, rerr := os.ReadFile(report)
	if fi, err := os.Stat(report); rerr == nil && (err != nil || fi.ModTime().Before(started)) {
		rerr = fmt.Errorf("report not written by this run")
	}
	if rerr != nil {
		return Result{Note: fmt.Sprintf(lang.T("stryker 실패(종료 코드 %d): %s"), code, tail(text, 300))}
	}
	var rep strykerReport
	if err := json.Unmarshal(b, &rep); err != nil {
		return Result{Note: err.Error()}
	}
	r := Result{Ran: true}
	for file, f := range rep.Files {
		for _, m := range f.Mutants {
			switch m.Status {
			case "Killed", "Timeout":
				r.Total++
			case "Survived", "NoCoverage":
				r.Total++
				r.Missed++
				r.Survivors = append(r.Survivors, fmt.Sprintf("%s:%d: %s", file, m.Location.Start.Line, m.MutatorName))
			}
		}
	}
	return r
}

// runPHP has Infection mutate the lines changed since the base.
func runPHP(ctx context.Context, c Change, files []string, out string) Result {
	bin := filepath.Join(c.Root, "vendor", "bin", "infection")
	if _, err := os.Stat(bin); err != nil {
		return missing("php")
	}
	base, err := commitOf(ctx, c.Root, c.Base)
	if err != nil {
		return Result{Note: err.Error()}
	}
	report := filepath.Join(out, "infection.json")
	name, args := bin, []string{"--git-diff-lines", "--git-diff-base=" + base, "--logger-json=" + report, "--no-progress"}
	if !phpCoverageDriver(ctx, c.Root) {
		// Infection needs code coverage; phpdbg gives it without pcov or Xdebug
		if dbg, err := exec.LookPath("phpdbg"); err == nil {
			name, args = dbg, append([]string{"-qrr", bin}, args...)
		}
	}
	text, code, err := run(ctx, c.Root, nil, name, args...)
	if err != nil {
		return Result{Note: err.Error()}
	}
	b, rerr := os.ReadFile(report)
	if rerr != nil {
		// the usage text Infection prints after an error says nothing about it
		if i := strings.Index(text, "\nrun ["); i >= 0 {
			text = text[:i]
		}
		return Result{Note: fmt.Sprintf(lang.T("infection 실패(종료 코드 %d): %s"), code, tail(strings.Join(strings.Fields(text), " "), 300))}
	}
	var rep struct {
		Stats struct {
			Total     int `json:"totalMutantsCount"`
			Escaped   int `json:"escapedCount"`
			Uncovered int `json:"notCoveredCount"`
		} `json:"stats"`
		Escaped []struct {
			Mutator struct {
				Name string `json:"mutatorName"`
				File string `json:"originalFilePath"`
				Line int    `json:"originalStartLine"`
			} `json:"mutator"`
		} `json:"escaped"`
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return Result{Note: err.Error()}
	}
	r := Result{Ran: true, Total: rep.Stats.Total, Missed: rep.Stats.Escaped + rep.Stats.Uncovered}
	for _, e := range rep.Escaped {
		f, _ := filepath.Rel(c.Root, e.Mutator.File)
		r.Survivors = append(r.Survivors, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(f), e.Mutator.Line, e.Mutator.Name))
	}
	return r
}

// phpCoverageDriver reports a coverage extension (pcov or Xdebug) loaded in
// the project's php.
func phpCoverageDriver(ctx context.Context, root string) bool {
	text, _, err := run(ctx, root, nil, "php", "-m")
	if err != nil {
		return false
	}
	for _, l := range strings.Split(text, "\n") {
		if m := strings.ToLower(strings.TrimSpace(l)); m == "pcov" || m == "xdebug" {
			return true
		}
	}
	return false
}

var pitMutationRe = regexp.MustCompile(`(?s)<mutation\b[^>]*\bstatus='([A-Z_]+)'[^>]*>.*?<mutatedClass>([^<]*)</mutatedClass>.*?<lineNumber>(\d+)</lineNumber>.*?<mutator>([^<]*)</mutator>.*?</mutation>`)

// runJava runs PIT through Maven on the changed classes. PIT mutates whole
// classes, so only the mutants on changed lines are read from its report.
func runJava(ctx context.Context, c Change, files []string, out string) Result {
	if _, err := os.Stat(filepath.Join(c.Root, "pom.xml")); err != nil {
		return Result{Note: lang.T("pom.xml 없음. PIT는 Maven 프로젝트에서만 실행한다")}
	}
	if _, err := exec.LookPath("mvn"); err != nil {
		return missing("java")
	}
	var classes []string
	fileOf := map[string]string{} // class -> changed file
	for _, f := range files {
		if i := strings.Index(f, "src/main/java/"); i >= 0 {
			cls := strings.ReplaceAll(strings.TrimSuffix(f[i+len("src/main/java/"):], ".java"), "/", ".")
			classes = append(classes, cls)
			fileOf[cls] = f
		}
	}
	if len(classes) == 0 {
		return Result{Note: lang.T("src/main/java 아래 바뀐 클래스 없음")}
	}
	reports := filepath.Join(out, "pit")
	// the goal alone does not compile; test-compile builds what PIT mutates
	text, code, err := run(ctx, c.Root, nil, "mvn", "-q", "-B", "test-compile", "org.pitest:pitest-maven:mutationCoverage",
		"-DtargetClasses="+strings.Join(classes, ","), "-DoutputFormats=XML", "-DtimestampedReports=false", "-DreportsDirectory="+reports)
	if err != nil {
		return Result{Note: err.Error()}
	}
	b, rerr := os.ReadFile(filepath.Join(reports, "mutations.xml"))
	if rerr != nil {
		return Result{Note: fmt.Sprintf(lang.T("PIT 실패(종료 코드 %d): %s"), code, tail(text, 300))}
	}
	r := Result{Ran: true}
	for _, m := range pitMutationRe.FindAllStringSubmatch(string(b), -1) {
		cls, _, _ := strings.Cut(m[2], "$")
		f := fileOf[cls]
		line, _ := strconv.Atoi(m[3])
		if f == "" || !inRanges(c.Lines[f], line) {
			continue
		}
		switch m[1] {
		case "KILLED", "TIMED_OUT", "MEMORY_ERROR":
			r.Total++
		case "SURVIVED", "NO_COVERAGE":
			r.Total++
			r.Missed++
			r.Survivors = append(r.Survivors, fmt.Sprintf("%s:%d: %s", f, line, m[4][strings.LastIndex(m[4], ".")+1:]))
		}
	}
	return r
}

// inRanges reports a line inside one of the ranges.
func inRanges(ranges [][2]int, line int) bool {
	for _, r := range ranges {
		if line >= r[0] && line <= r[1] {
			return true
		}
	}
	return false
}

// runPython has mutmut mutate the changed code. mutmut 3 picks mutants by
// name, so only the functions holding a changed line run; mutmut 2 takes
// the changed files and mutates every line of them.
func runPython(ctx context.Context, c Change, files []string, out string) Result {
	if !Available(c.Root, "python") {
		return missing("python")
	}
	if v, _, _ := run(ctx, c.Root, nil, "mutmut", "--version"); mutmut3Re.MatchString(v) {
		return runMutmut3(ctx, c, files)
	}
	text, code, err := run(ctx, c.Root, nil, "mutmut", "run", "--paths-to-mutate", strings.Join(files, ","))
	if err != nil {
		return Result{Note: err.Error()}
	}
	if code != 0 && strings.Contains(text, "No such option") {
		return Result{Note: lang.T("이 mutmut 버전은 파일 지정을 받지 않는다. pyproject.toml의 [tool.mutmut] paths_to_mutate에 바뀐 파일을 적고 mutmut run을 직접 실행한다")}
	}
	res, _, err := run(ctx, c.Root, nil, "mutmut", "results")
	if err != nil {
		return Result{Note: err.Error()}
	}
	r := Result{Ran: true, Note: lang.T("mutmut은 줄 단위 범위를 지원하지 않아 바뀐 파일 전체를 변이했다")}
	r.Total, r.Missed, r.Survivors = mutmutResults(text, res, nil)
	return r
}

var mutmut3Re = regexp.MustCompile(`version (?:[3-9]|\d{2,})\.`)

// runMutmut3 runs the mutants of the functions that hold a changed line.
func runMutmut3(ctx context.Context, c Change, files []string) Result {
	var pats []string
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		pats = append(pats, mutmutPatterns(f, string(b), c.Lines[f])...)
	}
	note := lang.T("mutmut은 함수 단위로 변이하므로 바뀐 줄이 든 함수 전체를 변이했다")
	if len(pats) == 0 {
		return Result{Ran: true, Note: lang.T("바뀐 줄이 함수 안에 없어 mutmut이 변이할 것이 없다")}
	}
	text, code, err := run(ctx, c.Root, nil, "mutmut", append([]string{"run"}, pats...)...)
	if err != nil {
		return Result{Note: err.Error()}
	}
	if strings.Contains(text, "nothing matches") {
		return Result{Ran: true, Note: lang.T("바뀐 함수에 mutmut이 만든 변이가 없다")}
	}
	if code != 0 {
		return Result{Note: fmt.Sprintf(lang.T("mutmut 실패(종료 코드 %d): %s"), code, tail(text, 300))}
	}
	res, _, err := run(ctx, c.Root, nil, "mutmut", "results", "--all", "true")
	if err != nil {
		return Result{Note: err.Error()}
	}
	r := Result{Ran: true, Note: note}
	r.Total, r.Missed, r.Survivors = mutmutResults(text, res, pats)
	return r
}

var (
	pyDefRe   = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+(\w+)\s*\(`)
	pyClassRe = regexp.MustCompile(`^class\s+(\w+)`)
)

// mutmutPatterns names, as mutmut 3 globs, the mutants of the top-level
// functions and methods of one file that hold a changed line.
func mutmutPatterns(file, src string, ranges [][2]int) []string {
	mod := strings.ReplaceAll(strings.TrimSuffix(file, ".py"), "/", ".")
	mod = strings.TrimPrefix(mod, "src.")
	lines := strings.Split(src, "\n")
	// owner[i] is the mangled name of the function line i+1 belongs to
	owner := make([]string, len(lines))
	cur, class, methodIndent := "", "", -1
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		if trim == "" || strings.HasPrefix(trim, "#") {
			// a blank or comment line changes no code to mutate
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " \t"))
		m := pyDefRe.FindStringSubmatch(l)
		switch {
		case indent == 0:
			cur, class, methodIndent = "", "", -1
			if c := pyClassRe.FindStringSubmatch(l); c != nil {
				class = c[1]
			} else if m != nil {
				cur = "x_" + m[2]
			}
		case class != "" && m != nil && (methodIndent < 0 || len(m[1]) <= methodIndent):
			methodIndent, cur = len(m[1]), "xǁ"+class+"ǁ"+m[2]
		case class != "" && methodIndent >= 0 && indent <= methodIndent && m == nil:
			// a class body statement between methods
			cur = ""
		}
		owner[i] = cur
	}
	seen := map[string]bool{}
	var pats []string
	for _, r := range ranges {
		for n := r[0]; n <= r[1] && n-1 < len(owner); n++ {
			if n < 1 || owner[n-1] == "" || seen[owner[n-1]] {
				continue
			}
			seen[owner[n-1]] = true
			name := strings.ReplaceAll(mod+"."+owner[n-1], ".__init__.", ".")
			pats = append(pats, name+"__mutmut_*")
		}
	}
	return pats
}

var mutmutTotalRe = regexp.MustCompile(`(\d+)/(\d+)\s+🎉\s*(\d+)`)

// mutmutResults reads the counts and surviving mutants from mutmut's run
// progress line and its results listing. The listing keeps earlier runs
// too, so with patterns only the mutants they name are read.
func mutmutResults(runOut, results string, pats []string) (total, missed int, survivors []string) {
	if m := mutmutTotalRe.FindAllStringSubmatch(runOut, -1); len(m) > 0 {
		total, _ = strconv.Atoi(m[len(m)-1][2])
	}
	named := func(name string) bool {
		if pats == nil {
			return true
		}
		for _, p := range pats {
			if ok, _ := path.Match(p, name); ok {
				return true
			}
		}
		return false
	}
	counted := 0
	for _, l := range strings.Split(results, "\n") {
		name, status, ok := strings.Cut(strings.TrimSpace(l), ": ")
		if !ok || !named(name) {
			continue
		}
		if status != "not checked" && status != "skipped" {
			counted++
		}
		if status == "survived" || status == "no tests" {
			survivors = append(survivors, name)
		}
	}
	if pats != nil {
		// the progress line counts every mutant of the project
		total = counted
	}
	missed = len(survivors)
	if total < missed {
		total = missed
	}
	return total, missed, survivors
}

// Path is where a session's last report is kept.
func Path(session string) string {
	h := sha256.Sum256([]byte(session))
	return filepath.Join(config.Home(), "mutants", hex.EncodeToString(h[:8])+".json")
}

// Save keeps a session's report for its receipt.
func Save(session string, rep Report) error {
	if err := procgroup.MkdirAllPrivate(filepath.Dir(Path(session))); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(session), b, 0o600)
}

// Load reads a session's last report.
func Load(session string) (Report, bool) {
	var rep Report
	b, err := os.ReadFile(Path(session))
	if err != nil || json.Unmarshal(b, &rep) != nil {
		return rep, false
	}
	return rep, true
}

// Available reports whether a language's tool can run in the project.
func Available(root, l string) bool {
	look := func(name string) bool { _, err := exec.LookPath(name); return err == nil }
	stat := func(rel string) bool { _, err := os.Stat(filepath.Join(root, rel)); return err == nil }
	switch l {
	case "rust":
		return look("cargo-mutants")
	case "go":
		return look("go-mutesting")
	case "js":
		return stat(filepath.Join("node_modules", ".bin", "stryker"))
	case "php":
		return stat(filepath.Join("vendor", "bin", "infection"))
	case "java":
		return look("mvn") && stat("pom.xml")
	case "python":
		return look("mutmut")
	}
	return false
}

// ProjectLangs lists the languages a project's top-level build files name.
func ProjectLangs(root string) []string {
	markers := []struct{ file, lang string }{{"Cargo.toml", "rust"}, {"go.mod", "go"}, {"package.json", "js"}, {"composer.json", "php"},
		{"pom.xml", "java"}, {"pyproject.toml", "python"}, {"setup.py", "python"}}
	seen := map[string]bool{}
	var out []string
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(root, m.file)); err == nil && !seen[m.lang] {
			seen[m.lang] = true
			out = append(out, m.lang)
		}
	}
	return out
}
