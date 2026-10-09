// Package classify assigns first-pass categories to events and classifies
// shell commands.
package classify

import (
	"path"
	"regexp"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rules"
)

// Shell classes.
const (
	ShellVerify  = "verify"
	ShellExplore = "explore"
	ShellProduce = "produce"
	ShellUnknown = "unknown"
)

var verifyRes = []*lazyre.RE{
	lazyre.New(`^(?:(?:python3?|py)\s+-m\s+)?pytest\b`),
	lazyre.New(`^(?:python3?|py)\s+(?:-\S+\s+)*-m\s+(?:unittest|doctest)\b`),
	lazyre.New(`^node\s+(?:-\S+\s+)*--test\b`),
	lazyre.New(`^(?:uv|poetry|pipenv)\s+run\s+(?:(?:python3?|py)\s+-m\s+)?(?:pytest|mypy|ruff|tox)\b`),
	lazyre.New(`^go\s+(?:test|build|vet)\b`),
	lazyre.New(`^(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?(?:test|build|lint|typecheck|type-check|check|e2e|ci)\b`),
	lazyre.New(`^(?:npx\s+)?(?:vitest|jest|tsc|eslint|playwright\s+test|mocha)\b`),
	lazyre.New(`^(?:ruff|mypy|pyright|flake8|pylint|black\s+--check|tox|nox)\b`),
	lazyre.New(`^cargo\s+(?:test|build|check|clippy|nextest)\b`),
	lazyre.New(`^(?:\./)?mvnw?\b`),
	lazyre.New(`^(?:\./)?gradlew?\b.*\b(?:test|build|check|compileJava|compileKotlin|bootJar|assemble)\b`),
	lazyre.New(`^dotnet\s+(?:test|build)\b`),
	lazyre.New(`^make\s+(?:test|check|build|lint)\b`),
	lazyre.New(`^curl\b.*\b(?:localhost|127\.0\.0\.1|0\.0\.0\.0)\b`),
	lazyre.New(`^(?:bash|sh)\s+\S*test\S*\.sh\b`),
	lazyre.New(`^(?:python3?|py|node)\s+\S*test\S*\b`),
	lazyre.New(`^(?:shellcheck|golangci-lint|staticcheck|swift\s+(?:test|build)|xcodebuild|flutter\s+test|dart\s+test|phpunit|rspec|bundle\s+exec\s+rspec)\b`),
}

// verifyScriptRe matches a project script named as a test, gate, check,
// verification, CI or lint run (scripts/gates.py, ./check-all.sh, ci.sh).
var verifyScriptRe = lazyre.New(`^(?:(?:python3?|py|node|bash|sh|zsh|deno\s+run)\s+(?:-\S+\s+)*)?(?:\./|/)?(?:[\w.-]+/)*(?:[\w-]*[_-])?(?:tests?|gates?|checks?|verify|verification|ci|lint|smoke)(?:[_-][\w-]*)?\.(?:py|sh|js|mjs|ts|bash)\b`)

// interpreterVersionRe matches a versioned interpreter name (python3.13).
var interpreterVersionRe = lazyre.New(`^(python3?|pypy3?|node)\d*(?:\.\d+)*$`)

// bareProgram rewrites the program word of a command to its plain name, so a
// tool run from a virtual environment or a vendored bin directory
// (.venv/bin/python, node_modules/.bin/jest, /usr/bin/python3.13) is classified
// like the same tool on PATH.
func bareProgram(norm string) string {
	word, rest := norm, ""
	if i := strings.IndexAny(norm, " \t"); i >= 0 {
		word, rest = norm[:i], norm[i:]
	}
	base := pathnorm.CommandBase(word)
	if m := interpreterVersionRe.FindStringSubmatch(base); m != nil {
		base = m[1]
	}
	if base == word {
		return norm
	}
	return base + rest
}

var exploreWords = map[string]bool{
	"cat": true, "ls": true, "find": true, "grep": true, "rg": true, "head": true, "tail": true,
	"wc": true, "tree": true, "sed": true, "awk": true, "less": true, "more": true, "file": true,
	"stat": true, "du": true, "df": true, "pwd": true, "echo": true, "which": true, "whereis": true,
	"jq": true, "diff": true, "realpath": true, "readlink": true, "ps": true, "pgrep": true,
	"env": true, "printenv": true, "date": true, "sort": true, "uniq": true, "cut": true, "tr": true,
	"basename": true, "dirname": true, "test": true, "[": true, "true": true, "sleep": true,
	"journalctl": true, "systemctl": true, "docker": true, "ss": true, "netstat": true, "lsof": true,
	"fd": true, "bat": true, "xxd": true, "od": true, "md5sum": true, "sha256sum": true, "id": true,
	"whoami": true, "uname": true, "type": true, "command": true, "nl": true, "column": true,
}

var gitExplore = map[string]bool{"log": true, "show": true, "diff": true, "status": true, "blame": true,
	"branch": true, "remote": true, "rev-parse": true, "ls-files": true, "describe": true, "tag": true, "grep": true, "stash": false}
var gitProduce = map[string]bool{"checkout": true, "restore": true, "apply": true, "commit": true, "add": true,
	"mv": true, "rm": true, "reset": true, "revert": true, "merge": true, "rebase": true, "cherry-pick": true, "stash": true, "switch": true, "pull": true, "push": true, "init": true, "clone": true}
var produceWords = map[string]bool{"mv": true, "cp": true, "rm": true, "mkdir": true, "touch": true,
	"chmod": true, "chown": true, "ln": true, "rmdir": true, "patch": true, "install": true, "unzip": true, "tar": true, "truncate": true, "dd": true}
var pkgInstallRe = lazyre.New(`^(?:npm|pnpm|yarn|bun)\s+(?:i|install|add|remove|uninstall)\b|^pip3?\s+install\b|^uv\s+(?:add|pip\s+install|sync)\b|^go\s+(?:get|mod\s+tidy)\b|^cargo\s+add\b|^poetry\s+add\b|^apt(?:-get)?\s+install\b|^brew\s+install\b`)

// serviceControlRe matches commands that start, stop or restart a service.
// They change what a check can reach without touching project files.
var serviceControlRe = lazyre.New(`^(?:docker(?:-compose|\s+compose)?\s+(?:up|start|restart|stop|down|run)\b|docker\s+container\s+(?:start|restart|run)\b|podman(?:-compose|\s+compose)?\s+(?:up|start|restart|run)\b|systemctl\s+(?:--user\s+)?(?:start|restart|reload|stop)\b|service\s+\S+\s+(?:start|restart|stop)\b|brew\s+services\s+(?:start|restart|stop)\b|pg_ctl\s+(?:start|restart)\b|redis-server\b|mysqld\b|mongod\b)`)
var redirectWriteRe = lazyre.New(`(?:^|[^0-9&>])>{1,2}\s*([^\s&|;>]+)`)
var sedInplaceRe = lazyre.New(`^(?:sed|perl)\s+(?:-[a-zA-Z]*i|-i)`)

// Options holds user-configured command lists.
type Options struct {
	VerifyCommands []string // prefixes or regexes treated as verification
}

// Shell classifies a raw shell command. It returns the class and whether the
// command mutates files.
// Here-document bodies are not command lines: a body read by an interpreter
// is judged by the commands it runs (subprocess calls, exec calls, script
// lines) and the files it writes; a body redirected into a file is content.
// A command that both changes files and runs tests (a test runner, a gate
// script, or checks run from a here-document) is a verification that
// mutates, so the test run is seen.
func Shell(cmd string, opt Options) (class string, mutating bool) {
	return shellDepth(cmd, opt, 0)
}

func shellDepth(cmd string, opt Options, depth int) (class string, mutating bool) {
	outer, docs := splitHeredocs(cmd)
	stages := fp.SplitStages(outer)
	hasProduce, hasVerify, hasExplore, hasUnknown, hasTest := false, false, false, false, false
	note := func(c string, mut bool) {
		if mut {
			mutating = true
		}
		switch c {
		case ShellProduce:
			hasProduce = true
		case ShellVerify:
			hasVerify = true
		case ShellExplore:
			hasExplore = true
		default:
			hasUnknown = true
		}
	}
	for _, st := range stages {
		c, mut := stage(st, opt)
		note(c, mut)
		hasTest = hasTest || (c == ShellVerify && isTestRun(st))
	}
	for _, d := range docs {
		if depth < 2 {
			for _, c := range embeddedCommands(d) {
				cls, mut := shellDepth(c, opt, depth+1)
				note(cls, mut)
				hasTest = hasTest || cls == ShellVerify
			}
		}
		if interpreterKind(d.reader) == "python" && len(pythonWrites(d.body)) > 0 {
			hasProduce, mutating = true, true
		}
	}
	switch {
	case hasTest && hasProduce:
		// a test run in the same command as an edit is still a test run
		return ShellVerify, true
	case hasProduce:
		return ShellProduce, true
	case hasVerify:
		return ShellVerify, mutating
	case hasExplore && !hasUnknown:
		return ShellExplore, mutating
	case hasExplore:
		return ShellUnknown, mutating
	default:
		return ShellUnknown, mutating
	}
}

func stage(st string, opt Options) (string, bool) {
	norm, _ := fp.NormalizeCmd(st)
	norm = strings.TrimSpace(norm)
	for _, v := range opt.VerifyCommands {
		if v == "" {
			continue
		}
		if strings.HasPrefix(norm, v) {
			return ShellVerify, false
		}
		if re, err := regexp.Compile(v); err == nil && strings.ContainsAny(v, `^$\\(`) && re.MatchString(norm) {
			return ShellVerify, false
		}
	}
	if serviceControlRe.MatchString(norm) {
		// not a file change, but not read-only either
		return ShellUnknown, true
	}
	writes := redirectWriteRe.FindAllStringSubmatch(norm, -1)
	mut := false
	for _, w := range writes {
		if w[1] != "/dev/null" && !strings.HasPrefix(w[1], "/dev/") && !strings.EqualFold(w[1], "nul") {
			mut = true
		}
	}
	bare := bareProgram(norm)
	for _, re := range verifyRes {
		if re.MatchString(bare) {
			return ShellVerify, mut
		}
	}
	if verifyScriptRe.MatchString(bare) {
		return ShellVerify, mut
	}
	if rules.Current().VerifyCommands.Match(norm) {
		return ShellVerify, mut
	}
	if pkgInstallRe.MatchString(norm) || sedInplaceRe.MatchString(norm) {
		return ShellProduce, true
	}
	w := firstWord(norm)
	if w == "sudo" {
		i := strings.Index(norm, "sudo")
		rest := strings.TrimSpace(norm[i+len("sudo"):])
		if rest == "" || rest == norm {
			return ShellUnknown, false
		}
		return stage(rest, opt)
	}
	if w == "git" {
		sub := secondWord(norm)
		if sub == "tag" {
			// creating or deleting a tag changes the repository; listing does not
			if _, rest := gitSub(strings.Fields(norm)[1:]); gitTagMutates(rest) {
				return ShellProduce, true
			}
			return ShellExplore, false
		}
		if gitProduce[sub] {
			return ShellProduce, true
		}
		if gitExplore[sub] {
			return ShellExplore, false
		}
		return ShellUnknown, false
	}
	if w == "tee" {
		return ShellProduce, true
	}
	if produceWords[w] {
		return ShellProduce, true
	}
	if mut {
		return ShellProduce, true
	}
	if exploreWords[w] {
		return ShellExplore, false
	}
	return ShellUnknown, false
}

func firstWord(s string) string {
	s = strings.TrimLeft(s, "( ")
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return pathnorm.CommandBase(s[:i])
	}
	return pathnorm.CommandBase(s)
}

func secondWord(s string) string {
	f := strings.Fields(s)
	for i := 1; i < len(f); i++ {
		if strings.HasPrefix(f[i], "-") {
			if f[i] == "-C" || f[i] == "-c" {
				i++
			}
			continue
		}
		return f[i]
	}
	return ""
}

// Initial assigns the first-pass category for an event.
func Initial(ev *event.Event, opt Options) {
	switch ev.Kind {
	case event.KindMessage, event.KindPrompt:
		ev.Category = event.CatPlan
		return
	case event.KindCompact, event.KindStop, event.KindSessionStart, event.KindSessionEnd:
		ev.Category = event.CatPlan
		return
	}
	switch ev.Tool {
	case event.ToolShell:
		cls, mut := Shell(ev.Cmd, opt)
		ev.ShellClass = cls
		ev.Mutating = mut
		if len(ev.Paths) == 0 && mut {
			// files a here-document script writes are the call's paths
			ev.Paths = HeredocWrites(ev.Cmd)
		}
		switch cls {
		case ShellVerify:
			ev.Category = event.CatVerify
		case ShellProduce:
			ev.Category = event.CatProduce
		case ShellExplore:
			ev.Category = event.CatExplore
		default:
			ev.Category = event.CatExplore
			ev.Unknown = true
		}
	case event.ToolWrite, event.ToolEdit:
		ev.Category = event.CatProduce
	case event.ToolRead, event.ToolSearch, event.ToolWeb:
		ev.Category = event.CatExplore
	case event.ToolTodo:
		ev.Category = event.CatPlan
	case event.ToolTask:
		ev.Category = event.CatPlan
	default:
		ev.Category = event.CatExplore
	}
}

// Port of iron-laws core/paths.py TEST_DIR_NAMES and TEST_FILE_PATTERNS.
var testDirNames = map[string]bool{"test": true, "tests": true, "__tests__": true, "spec": true, "specs": true}
var testFilePatterns = []string{"test_*.py", "*_test.py", "*_test.go", "*_test.rs", "*Test.cs", "*Test.java", "*Tests.java", "*Tests.cs", "*.test.*", "*.spec.*"}

// IsTestPath reports whether p is a test file (port of iron-laws is_test_path).
func IsTestPath(p string) bool {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(p, "/")
	for _, d := range parts[:len(parts)-1] {
		if testDirNames[d] {
			return true
		}
	}
	name := parts[len(parts)-1]
	for _, pat := range testFilePatterns {
		if ok, _ := path.Match(pat, name); ok {
			return true
		}
	}
	return false
}

// TestInfraNames are files that change how tests run (port of iron-laws TEST_INFRA_NAMES).
var TestInfraNames = map[string]bool{"conftest.py": true, "pytest.ini": true, "tox.ini": true, "setup.cfg": true,
	"sitecustomize.py": true, "usercustomize.py": true, "noxfile.py": true, "jest.config.js": true, "jest.config.ts": true,
	"vitest.config.ts": true, "vitest.config.js": true}

var manifestNames = map[string]bool{
	"package.json": true, "pyproject.toml": true, "requirements.txt": true, "go.mod": true,
	"Cargo.toml": true, "pom.xml": true, "build.gradle": true, "build.gradle.kts": true,
	"Gemfile": true, "composer.json": true, "setup.py": true, "setup.cfg": true, "Pipfile": true,
}

var configNames = map[string]bool{
	"tsconfig.json": true, ".eslintrc": true, ".eslintrc.js": true, ".eslintrc.json": true, ".eslintrc.cjs": true,
	"eslint.config.js": true, "eslint.config.mjs": true, ".prettierrc": true, "ruff.toml": true, ".ruff.toml": true,
	"mypy.ini": true, ".flake8": true, "tox.ini": true, "pytest.ini": true, "jest.config.js": true, "jest.config.ts": true,
	"vitest.config.ts": true, "vitest.config.js": true, ".golangci.yml": true, ".golangci.yaml": true, "pyrightconfig.json": true,
	"babel.config.js": true, ".babelrc": true, "checkstyle.xml": true, "detekt.yml": true, "clippy.toml": true,
}

// IsManifest reports dependency manifest files.
func IsManifest(p string) bool { return manifestNames[path.Base(p)] }

// IsToolConfig reports lint/type/build/test configuration files.
func IsToolConfig(p string) bool { return configNames[path.Base(p)] }

// isTestRun reports whether a verification stage runs tests or a project
// gate rather than a build or a linter alone.
func isTestRun(st string) bool {
	n, _ := fp.NormalizeCmd(st)
	return VerifyScope(n) != ScopeUnknown || verifyScriptRe.MatchString(bareProgram(n))
}
