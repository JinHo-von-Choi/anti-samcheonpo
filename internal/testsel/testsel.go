// Package testsel finds the test files a check command runs and the files
// that decide how those tests are collected and run.
//
// The answer is read from the command and the project tree. A command whose
// tests cannot be told from its text (a bare jest with no pattern, a make
// target, a script) is reported as unknown rather than guessed.
package testsel

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
)

// maxWalk caps the entries one resolution visits.
const maxWalk = 20000

// Selection is what one command runs.
type Selection struct {
	// Files are the project-relative slash paths of the test files it runs.
	Files []string
	// Infra are the files that decide how those tests are collected or run:
	// a Go test file holding TestMain, conftest.py in a test folder or above
	// it, pytest.ini, a jest or vitest config, and the pytest section of
	// pyproject.toml, setup.cfg or tox.ini.
	Infra []string
	// Dirs are the folders whose new collection files would change the run.
	Dirs []string
	// Known is false when the command's tests could not be told.
	Known bool
}

// skipDirs are never walked: dependencies, build output and tool state.
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true,
	"target": true, "dist": true, "build": true, ".tox": true, ".mypy_cache": true, ".pytest_cache": true, ".samcheonpo": true, "vendor": true}

// InfraNames are file names that change how the tests next to them, or below
// them, are collected and run.
var InfraNames = map[string]bool{"conftest.py": true, "pytest.ini": true, "jest.config.js": true, "jest.config.ts": true,
	"jest.config.mjs": true, "jest.config.cjs": true, "vitest.config.ts": true, "vitest.config.js": true, "vitest.config.mts": true,
	"vitest.config.mjs": true}

// SectionFiles hold a test runner section among other settings. Only that
// section changes how tests run.
var SectionFiles = map[string]bool{"pyproject.toml": true, "setup.cfg": true, "tox.ini": true}

// Resolve reads what cmd runs in the project at root.
func Resolve(root, cmd string) Selection {
	var sel Selection
	runs := classify.TestRuns(cmd)
	if len(runs) == 0 || root == "" {
		return sel
	}
	sel.Known = true
	files, infra, dirs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, r := range runs {
		base := root
		if r.Dir != "" {
			if filepath.IsAbs(r.Dir) {
				base = filepath.Clean(r.Dir)
			} else {
				base = filepath.Join(root, r.Dir)
			}
		}
		ok := false
		switch r.Runner {
		case "go":
			ok = resolveGo(root, base, r.Args, files, infra, dirs)
		case "pytest":
			ok = resolvePytest(root, base, r.Args, files, infra, dirs)
		case "jest", "vitest", "script":
			ok = resolveJS(root, base, r.Args, files, infra, dirs)
		case "cargo":
			ok = resolveCargo(root, base, r.Args, files, infra, dirs)
		}
		if !ok {
			sel.Known = false
		}
	}
	sel.Files, sel.Infra, sel.Dirs = sorted(files), sorted(infra), sorted(dirs)
	if len(sel.Files) == 0 {
		sel.Known = false
	}
	return sel
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// rel turns an absolute path into a project-relative slash path, or "" when
// it lies outside the project.
func rel(root, abs string) string {
	r, err := filepath.Rel(root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(r)
}

// explicitFiles adds the test files a command names directly. It returns
// true when it found one.
func explicitFiles(root, base string, args []string, files map[string]bool) bool {
	found := false
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		p := a
		if i := strings.Index(p, "::"); i >= 0 {
			p = p[:i]
		}
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(base, p)
		}
		if fi, err := os.Stat(abs); err == nil && fi.Mode().IsRegular() && classify.IsTestPath(abs) {
			if r := rel(root, abs); r != "" {
				files[r] = true
				found = true
			}
		}
	}
	return found
}

// ancestors adds the collection files of one runner in dir and the folders
// above it up to root.
func ancestors(root, dir, runner string, infra map[string]bool) {
	for d := dir; ; d = filepath.Dir(d) {
		r := rel(root, d)
		if r == "" && d != root {
			return
		}
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			py := n == "conftest.py" || n == "pytest.ini" || (SectionFiles[n] && d == root)
			js := strings.HasPrefix(n, "jest.config.") || strings.HasPrefix(n, "vitest.config.")
			if runner == "pytest" && py || runner == "js" && js {
				if x := rel(root, filepath.Join(d, n)); x != "" {
					infra[x] = true
				}
			}
		}
		if d == root || d == filepath.Dir(d) {
			return
		}
	}
}

// walk visits the regular files under dir, skipping dependency and build
// folders, until maxWalk entries.
func walk(dir string, fn func(abs string)) {
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		n++
		if n > maxWalk {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if p != dir && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			fn(p)
		}
		return nil
	})
}

var testMainRe = regexp.MustCompile(`(?m)^func\s+TestMain\s*\(`)

// DefinesTestMain reports Go test source that defines TestMain.
func DefinesTestMain(src string) bool { return testMainRe.MatchString(src) }

func resolveGo(root, base string, args []string, files, infra, dirs map[string]bool) bool {
	var pkgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := a
		if j := strings.IndexByte(a, '='); j > 0 {
			name = a[:j]
		}
		switch {
		case a == "--":
			i = len(args)
		case strings.HasPrefix(a, "-"):
			if !strings.Contains(a, "=") && (classify.GoValueFlag(name) || name == "-run" || name == "-skip") {
				i++
			}
		default:
			pkgs = append(pkgs, a)
		}
	}
	if len(pkgs) == 0 {
		pkgs = []string{"."}
	}
	ok := true
	for _, p := range pkgs {
		if strings.HasSuffix(p, "_test.go") {
			if !explicitFiles(root, base, []string{p}, files) {
				ok = false
			}
			continue
		}
		recursive := strings.HasSuffix(p, "/...") || p == "..." || p == "./..."
		p = strings.TrimSuffix(strings.TrimSuffix(p, "..."), "/")
		if p == "" {
			p = "."
		}
		if !strings.HasPrefix(p, ".") && !filepath.IsAbs(p) {
			// an import path; only one inside this module can be resolved
			mod := modulePath(root)
			if mod == "" || (p != mod && !strings.HasPrefix(p, mod+"/")) {
				ok = false
				continue
			}
			p = filepath.Join(root, strings.TrimPrefix(strings.TrimPrefix(p, mod), "/"))
		} else if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		addDir := func(d string) {
			entries, err := os.ReadDir(d)
			if err != nil {
				return
			}
			any := false
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
					continue
				}
				abs := filepath.Join(d, e.Name())
				r := rel(root, abs)
				if r == "" {
					continue
				}
				files[r] = true
				any = true
				if b, err := os.ReadFile(abs); err == nil && testMainRe.Match(b) {
					infra[r] = true
				}
			}
			if any {
				if r := rel(root, d); r != "" || d == root {
					dirs[r] = true
				}
			}
		}
		if recursive {
			_ = filepath.WalkDir(p, func(x string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if x != p && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_") || d.Name() == "testdata") {
						return filepath.SkipDir
					}
					addDir(x)
				}
				return nil
			})
		} else {
			addDir(p)
		}
	}
	return ok
}

var moduleRe = regexp.MustCompile(`(?m)^module\s+(\S+)`)

func modulePath(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	if m := moduleRe.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

func isPyTest(name string) bool {
	ok1, _ := path.Match("test_*.py", name)
	ok2, _ := path.Match("*_test.py", name)
	return ok1 || ok2
}

func resolvePytest(root, base string, args []string, files, infra, dirs map[string]bool) bool {
	var targets []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			targets = append(targets, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-"):
			if classify.PytestValueFlag(a) {
				i++
			}
		default:
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		targets = []string{"."}
	}
	for _, t := range targets {
		if i := strings.Index(t, "::"); i >= 0 {
			t = t[:i]
		}
		abs := t
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(base, t)
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return false
		}
		if fi.Mode().IsRegular() {
			if r := rel(root, abs); r != "" {
				files[r] = true
				ancestors(root, filepath.Dir(abs), "pytest", infra)
				dirs[rel(root, filepath.Dir(abs))] = true
			}
			continue
		}
		walk(abs, func(p string) {
			if isPyTest(filepath.Base(p)) {
				if r := rel(root, p); r != "" {
					files[r] = true
					dirs[rel(root, filepath.Dir(p))] = true
				}
			} else if filepath.Base(p) == "conftest.py" {
				if r := rel(root, p); r != "" {
					infra[r] = true
				}
			}
		})
		ancestors(root, abs, "pytest", infra)
	}
	return true
}

func isJSTest(p string) bool {
	n := filepath.Base(p)
	if !(strings.HasSuffix(n, ".js") || strings.HasSuffix(n, ".ts") || strings.HasSuffix(n, ".jsx") || strings.HasSuffix(n, ".tsx") ||
		strings.HasSuffix(n, ".mjs") || strings.HasSuffix(n, ".cjs") || strings.HasSuffix(n, ".mts")) {
		return false
	}
	if strings.Contains(n, ".test.") || strings.Contains(n, ".spec.") {
		return true
	}
	return strings.Contains(filepath.ToSlash(p), "/__tests__/")
}

func resolveJS(root, base string, args []string, files, infra, dirs map[string]bool) bool {
	var patterns []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "-"):
			if classify.JSValueFlag(a) {
				i++
			}
		default:
			patterns = append(patterns, a)
		}
	}
	if len(patterns) == 0 {
		// the whole suite, as configured; which files that is depends on
		// the runner config, so it is not guessed
		return false
	}
	for _, p := range patterns {
		var re *regexp.Regexp
		if r, err := regexp.Compile(p); err == nil {
			re = r
		}
		walk(base, func(abs string) {
			if !isJSTest(abs) {
				return
			}
			r := rel(root, abs)
			if r == "" {
				return
			}
			if strings.Contains(r, p) || (re != nil && re.MatchString(r)) {
				files[r] = true
				dirs[rel(root, filepath.Dir(abs))] = true
			}
		})
	}
	ancestors(root, base, "js", infra)
	return true
}

var cargoNameRe = regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`)

func resolveCargo(root, base string, args []string, files, infra, dirs map[string]bool) bool {
	var pkgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			i = len(args)
		case a == "-p" || a == "--package":
			if i+1 < len(args) {
				pkgs = append(pkgs, args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--package="):
			pkgs = append(pkgs, strings.TrimPrefix(a, "--package="))
		case strings.HasPrefix(a, "-"):
			if classify.CargoValueFlag(a) {
				i++
			}
		}
	}
	var crates []string
	if len(pkgs) == 0 {
		if _, err := os.Stat(filepath.Join(base, "Cargo.toml")); err != nil {
			return false
		}
		crates = []string{base}
	} else {
		byName := map[string]string{}
		walk(root, func(p string) {
			if filepath.Base(p) != "Cargo.toml" {
				return
			}
			if b, err := os.ReadFile(p); err == nil {
				if m := cargoNameRe.FindSubmatch(b); m != nil {
					byName[string(m[1])] = filepath.Dir(p)
				}
			}
		})
		for _, n := range pkgs {
			d, ok := byName[n]
			if !ok {
				return false
			}
			crates = append(crates, d)
		}
	}
	for _, c := range crates {
		walk(c, func(p string) {
			if !strings.HasSuffix(p, ".rs") {
				return
			}
			r := rel(root, p)
			if r == "" {
				return
			}
			inTests := strings.Contains(filepath.ToSlash(p), "/tests/")
			if !inTests {
				b, err := os.ReadFile(p)
				if err != nil || !strings.Contains(string(b), "#[cfg(test)]") {
					return
				}
			}
			files[r] = true
			dirs[rel(root, filepath.Dir(p))] = true
		})
	}
	return true
}
