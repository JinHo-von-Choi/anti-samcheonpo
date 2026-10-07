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
	lazyre.New(`^(?:python3?\s+-m\s+)?pytest\b`),
	lazyre.New(`^(?:uv|poetry|pipenv)\s+run\s+(?:python3?\s+-m\s+)?(?:pytest|mypy|ruff|tox)\b`),
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
	lazyre.New(`^(?:python3?|node)\s+\S*test\S*\b`),
	lazyre.New(`^(?:shellcheck|golangci-lint|staticcheck|swift\s+(?:test|build)|xcodebuild|flutter\s+test|dart\s+test|phpunit|rspec|bundle\s+exec\s+rspec)\b`),
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
var redirectWriteRe = lazyre.New(`(?:^|[^0-9&>])>{1,2}\s*([^\s&|;>]+)`)
var sedInplaceRe = lazyre.New(`^(?:sed|perl)\s+(?:-[a-zA-Z]*i|-i)`)

// Options holds user-configured command lists.
type Options struct {
	VerifyCommands []string // prefixes or regexes treated as verification
}

// Shell classifies a raw shell command. It returns the class and whether the
// command mutates files.
func Shell(cmd string, opt Options) (class string, mutating bool) {
	stages := fp.SplitStages(cmd)
	hasProduce, hasVerify, hasExplore, hasUnknown := false, false, false, false
	for _, st := range stages {
		c, mut := stage(st, opt)
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
	switch {
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
	writes := redirectWriteRe.FindAllStringSubmatch(norm, -1)
	mut := false
	for _, w := range writes {
		if w[1] != "/dev/null" && !strings.HasPrefix(w[1], "/dev/") {
			mut = true
		}
	}
	for _, re := range verifyRes {
		if re.MatchString(norm) {
			return ShellVerify, mut
		}
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
		return path.Base(s[:i])
	}
	return path.Base(s)
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
