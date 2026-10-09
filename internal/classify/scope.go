package classify

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

// Test scopes of a verification command.
const (
	ScopeUnknown  = ""
	ScopeFull     = "full"
	ScopeTargeted = "targeted"
)

// runnerPrefixes are launchers that run the command after them unchanged; the
// scope is that of the wrapped command.
var runnerPrefixRe = lazyre.New(`^(?:(?:uv|poetry|pipenv|pdm|hatch)\s+run\s+(?:--?\S+(?:\s+(?:\d[\w.]*|py\S*))?\s+)*|npx\s+(?:--?\S+\s+)*|(?:python3?|py)(?:\.\d+)?\s+-m\s+)`)

// VerifyScope reports whether a verification command runs a whole test suite
// or a selected part of it. It knows the common test runners only; anything
// else, and build, lint and type checks, is ScopeUnknown and never judged.
func VerifyScope(norm string) string {
	for _, st := range fp.SplitStages(norm) {
		if s := stageScope(st); s != ScopeUnknown {
			return s
		}
	}
	return ScopeUnknown
}

func stageScope(st string) string {
	n, _ := fp.NormalizeCmd(st)
	n = bareProgram(n)
	for i := 0; i < 3; i++ {
		m := runnerPrefixRe.FindString(n)
		if m == "" {
			break
		}
		n = strings.TrimSpace(n[len(m):])
	}
	w := dropRedirects(strings.Fields(n))
	if len(w) == 0 {
		return ScopeUnknown
	}
	prog := pathnorm.CommandBase(w[0])
	args := w[1:]
	switch prog {
	case "pytest", "py.test":
		return pytestScope(args)
	case "unittest":
		return positionalScope(args, map[string]bool{"discover": true}, nil)
	case "go":
		if len(args) > 0 && args[0] == "test" {
			return goTestScope(args[1:])
		}
	case "npm", "pnpm", "yarn", "bun":
		a := args
		if len(a) > 0 && a[0] == "run" {
			a = a[1:]
		}
		if len(a) > 0 && (a[0] == "test" || a[0] == "t") {
			return afterDashDash(a[1:], prog == "bun")
		}
	case "jest", "vitest", "mocha":
		a := args
		if prog == "vitest" && len(a) > 0 && (a[0] == "run" || a[0] == "watch") {
			a = a[1:]
		}
		return jsRunnerScope(a)
	case "playwright":
		if len(args) > 0 && args[0] == "test" {
			return jsRunnerScope(args[1:])
		}
	case "cargo":
		if len(args) > 0 && (args[0] == "test" || args[0] == "nextest") {
			a := args[1:]
			if args[0] == "nextest" && len(a) > 0 && a[0] == "run" {
				a = a[1:]
			}
			return cargoScope(a)
		}
	case "gradle", "gradlew":
		return gradleScope(args)
	case "mvn", "mvnw":
		return mavenScope(args)
	case "make":
		if len(args) == 1 && (args[0] == "test" || args[0] == "check") {
			return ScopeFull
		}
	case "dotnet":
		if len(args) > 0 && args[0] == "test" {
			for _, a := range args[1:] {
				if strings.HasPrefix(a, "--filter") || !strings.HasPrefix(a, "-") {
					return ScopeTargeted
				}
			}
			return ScopeFull
		}
	case "tox", "nox":
		for i, a := range args {
			if a == "--" && i+1 < len(args) {
				return ScopeTargeted
			}
		}
		return ScopeFull
	case "rspec", "phpunit":
		return positionalScope(args, nil, nil)
	}
	return ScopeUnknown
}

// pytestValueFlags take the next word as their value.
var pytestValueFlags = map[string]bool{"-n": true, "-p": true, "-c": true, "-o": true, "-W": true, "--maxfail": true, "--tb": true, "--rootdir": true,
	"--durations": true, "--junitxml": true, "--junit-xml": true, "--cov": true, "--cov-report": true, "--basetemp": true, "--log-level": true,
	"--dist": true, "--confcutdir": true, "--import-mode": true, "-r": true, "--capture": true}

func pytestScope(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-k" || a == "-m" || strings.HasPrefix(a, "-k=") || strings.HasPrefix(a, "-m=") || a == "--lf" || a == "--last-failed" || a == "--sw" || a == "--stepwise" || a == "--deselect" || strings.HasPrefix(a, "--deselect="):
			return ScopeTargeted
		case pytestValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		case selectsPart(a):
			return ScopeTargeted
		}
	}
	return ScopeFull
}

// selectsPart reports whether a path argument names part of a test tree
// rather than the whole of it ("tests", "tests/", ".").
func selectsPart(a string) bool {
	if strings.Contains(a, "::") {
		return true
	}
	t := strings.TrimSuffix(strings.TrimPrefix(a, "./"), "/")
	switch t {
	case "", ".", "test", "tests", "spec", "specs", "__tests__", "src":
		return false
	}
	return true
}

var goValueFlags = map[string]bool{"-count": true, "-timeout": true, "-p": true, "-parallel": true, "-tags": true, "-coverprofile": true,
	"-bench": true, "-benchtime": true, "-cpu": true, "-o": true, "-exec": true, "-ldflags": true, "-gcflags": true, "-covermode": true,
	"-coverpkg": true, "-shuffle": true, "-mod": true, "-vet": true, "-outputdir": true, "-cpuprofile": true, "-memprofile": true}

func goTestScope(args []string) string {
	full := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := a
		if j := strings.IndexByte(a, '='); j > 0 {
			name = a[:j]
		}
		switch {
		case name == "-run" || name == "-skip" || name == "--run":
			return ScopeTargeted
		case strings.HasPrefix(a, "-"):
			if goValueFlags[a] {
				i++
			}
		case a == "./..." || a == "..." || a == "all":
			full = true
		default:
			return ScopeTargeted
		}
	}
	if full {
		return ScopeFull
	}
	// no package argument: the package in the current directory
	return ScopeTargeted
}

// afterDashDash judges a package-manager test script: arguments after "--"
// (or any argument for bun, which takes them directly) select a part.
func afterDashDash(args []string, direct bool) string {
	for i, a := range args {
		if a == "--" {
			return jsRunnerScope(args[i+1:])
		}
		if direct {
			return jsRunnerScope(args)
		}
	}
	return ScopeFull
}

var jsValueFlags = map[string]bool{"--config": true, "-c": true, "--reporter": true, "--project": true, "--workers": true, "-j": true,
	"--maxWorkers": true, "--retries": true, "--timeout": true, "--shard": true, "--root": true, "--dir": true, "--environment": true}

func jsRunnerScope(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-t" || a == "--testNamePattern" || a == "-g" || a == "--grep" || strings.HasPrefix(a, "--testNamePattern=") || strings.HasPrefix(a, "--grep=") || a == "--onlyChanged" || a == "-o" || a == "--changed" || a == "--related" || a == "--findRelatedTests" || a == "--last-failed":
			return ScopeTargeted
		case jsValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		case selectsPart(a):
			return ScopeTargeted
		}
	}
	return ScopeFull
}

var cargoValueFlags = map[string]bool{"--features": true, "-F": true, "-j": true, "--jobs": true, "--target": true, "--manifest-path": true,
	"--profile": true, "--target-dir": true, "--color": true}

func cargoScope(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			if i+1 < len(args) {
				return ScopeTargeted
			}
			return ScopeFull
		case a == "-p" || a == "--package" || a == "--test" || a == "--lib" || a == "--bin" || a == "--doc" || a == "--example" || strings.HasPrefix(a, "--package=") || strings.HasPrefix(a, "--test="):
			return ScopeTargeted
		case cargoValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return ScopeTargeted
		}
	}
	return ScopeFull
}

func gradleScope(args []string) string {
	test := false
	for _, a := range args {
		switch {
		case a == "--tests" || strings.HasPrefix(a, "--tests="):
			return ScopeTargeted
		case a == "test" || a == "check" || a == "build":
			test = true
		case strings.HasPrefix(a, ":") && (strings.HasSuffix(a, ":test") || strings.HasSuffix(a, ":check")):
			return ScopeTargeted
		}
	}
	if test {
		return ScopeFull
	}
	return ScopeUnknown
}

func mavenScope(args []string) string {
	test := false
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "-Dtest=") || a == "-pl" || strings.HasPrefix(a, "-pl=") || a == "--projects":
			return ScopeTargeted
		case a == "test" || a == "verify" || a == "install" || a == "package":
			test = true
		}
	}
	if test {
		return ScopeFull
	}
	return ScopeUnknown
}

// positionalScope treats any positional argument other than the given
// subcommands as a selection.
func positionalScope(args []string, sub map[string]bool, valueFlags map[string]bool) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case valueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		case sub[a]:
		default:
			return ScopeTargeted
		}
	}
	return ScopeFull
}

// LongRunInfo describes a command that may run for a long time.
type LongRunInfo struct {
	// Seconds is the run time the command states (0: none stated).
	Seconds int
	// Named marks a command whose name or arguments say it is a load, soak,
	// stress or benchmark run.
	Named bool
	// Target identifies what runs, without the run-time arguments, so a short
	// probe and a long run of the same thing share it.
	Target string
}

// Explicit reports whether the run time is stated by the command itself.
func (l LongRunInfo) Explicit() bool { return l.Seconds > 0 }

var (
	timeoutPrefixRe = lazyre.New(`^\s*(?:g?timeout)\s+(?:-\S+\s+)*(\d+(?:\.\d+)?[smhd]?)\s+`)
	durationArgRe   = regexp.MustCompile(`(?:^|\s)(--duration|-duration|--run-time|--runtime|--time-limit|-d|-t|--time)(?:=|\s+)(\d+(?:\.\d+)?[smhd]?)\b`)
	longRunNameRe   = lazyre.New(`(?i)(?:^|[\s/_.-])(?:soak|stress|load[-_]?tests?|loadtest|benchmarks?|locust|vegeta)(?:[\s/_.-]|$)|(?:^|[\s/])perf_\w|^(?:k6\s+run|wrk|ab\s+-[nc]|hey)\b`)
)

// durationShortFlags limits the short flags -d/-t to programs where they mean a
// run time, so `-t` of an unrelated tool is not read as one.
var durationShortFlags = map[string]bool{"wrk": true, "k6": true, "locust": true, "ab": true, "hey": true, "vegeta": true}

// LongRun reads the stated run time of a raw shell command. The coreutils
// timeout prefix is read before normalization removes it.
func LongRun(raw string) LongRunInfo {
	var info LongRunInfo
	for _, st := range fp.SplitStages(raw) {
		secs := 0
		if m := timeoutPrefixRe.FindStringSubmatch(st); m != nil {
			secs = parseDuration(m[1])
		}
		norm, _ := fp.NormalizeCmd(st)
		if norm == "" {
			continue
		}
		prog := firstWord(norm)
		target := norm
		for _, m := range durationArgRe.FindAllStringSubmatchIndex(norm, -1) {
			flag := norm[m[2]:m[3]]
			if (flag == "-d" || flag == "-t") && !durationShortFlags[prog] {
				continue
			}
			if d := parseDuration(norm[m[4]:m[5]]); d > secs {
				secs = d
			}
			target = strings.Replace(target, strings.TrimSpace(norm[m[0]:m[1]]), "", 1)
		}
		named := longRunNameRe.MatchString(norm)
		if !named && secs > 0 && len(durationArgRe.FindAllStringIndex(norm, -1)) == 0 {
			// a timeout prefix caps how long any command may run; it states a
			// run time only for a load or benchmark run
			secs = 0
		}
		if secs == 0 && !named {
			continue
		}
		if secs > info.Seconds {
			info.Seconds = secs
		}
		info.Named = info.Named || named
		info.Target = strings.Join(strings.Fields(target), " ")
	}
	return info
}

// parseDuration reads "90", "90s", "5m", "2h", "1d" as seconds.
func parseDuration(s string) int {
	unit := time.Second
	switch {
	case strings.HasSuffix(s, "s"):
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "m"):
		unit, s = time.Minute, s[:len(s)-1]
	case strings.HasSuffix(s, "h"):
		unit, s = time.Hour, s[:len(s)-1]
	case strings.HasSuffix(s, "d"):
		unit, s = 24*time.Hour, s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return int(f * float64(unit) / float64(time.Second))
}

// Release kinds.
const (
	ReleaseNone    = ""
	ReleaseTag     = "tag"      // a local tag is created
	ReleasePushTag = "push_tag" // tags are pushed to a remote
	ReleaseCreate  = "release"  // a hosted release is created
	ReleasePublish = "publish"  // a package is published to a registry
)

var (
	pushTagRefRe = lazyre.New(`^(?:refs/tags/|v?\d+\.\d+)`)
	publishRe    = lazyre.New(`^(?:(?:npm|pnpm|bun)\s+publish\b|yarn\s+(?:npm\s+)?publish\b|cargo\s+publish\b|twine\s+upload\b|(?:poetry|uv|pdm|hatch|flit)\s+publish\b|gem\s+push\b|dotnet\s+nuget\s+push\b)`)
)

// gitTagReadFlags make `git tag` read tags instead of creating one.
var gitTagReadFlags = map[string]bool{"-l": true, "--list": true, "-v": true, "--verify": true, "-n": true, "--contains": true, "--no-contains": true,
	"--points-at": true, "--merged": true, "--no-merged": true, "--sort": true, "--format": true, "--column": true}

// Release reports whether a shell command ships a version: creates a tag,
// pushes tags, creates a hosted release or publishes a package.
// Pass the raw command when known: releases run from a here-document
// (subprocess.run(['git','push','origin',tag])) are read from its body; a
// push of a tag held in a variable counts when the same body creates a tag or
// spells a version.
func Release(cmd string) string {
	kind := ReleaseNone
	note := func(k string) {
		if releaseRank[k] > releaseRank[kind] {
			kind = k
		}
	}
	outer, docs := splitHeredocs(cmd)
	for _, st := range fp.SplitStages(outer) {
		note(stageRelease(st))
	}
	for _, d := range docs {
		pushVar, tagged := false, versionLiteralRe.MatchString(d.body)
		for _, c := range embeddedCommands(d) {
			for _, st := range fp.SplitStages(c) {
				k := stageRelease(st)
				note(k)
				n, _ := fp.NormalizeCmd(st)
				switch {
				case k == ReleaseTag:
					tagged = true
				case k == ReleaseNone && strings.HasPrefix(n, "git push") && strings.Contains(n, "<"):
					pushVar = true
				}
			}
		}
		if pushVar && tagged {
			note(ReleasePushTag)
		}
	}
	return kind
}

var versionLiteralRe = lazyre.New(`['"]v?\d+\.\d+\.\d+['"]`)

var releaseRank = map[string]int{ReleaseNone: 0, ReleaseTag: 1, ReleasePushTag: 2, ReleaseCreate: 3, ReleasePublish: 3}

// argvReleaseRe matches a release run through a program's argument list, as
// in subprocess.run(['git','push','origin','v0.4.0']).
var (
	argvPushTagRe = lazyre.New(`\[\s*['"]git['"]\s*,\s*['"]push['"][^\]]*(?:['"]--(?:follow-)?tags['"]|['"](?:refs/tags/|v?\d+\.\d+)[^'"]*['"])`)
	argvReleaseRe = lazyre.New(`\[\s*['"](?:gh|glab)['"]\s*,\s*['"]release['"]\s*,\s*['"]create['"]`)
)

func stageRelease(st string) string {
	n, _ := fp.NormalizeCmd(st)
	if publishRe.MatchString(n) {
		return ReleasePublish
	}
	switch {
	case argvReleaseRe.MatchString(n):
		return ReleaseCreate
	case argvPushTagRe.MatchString(n):
		return ReleasePushTag
	}
	w := strings.Fields(n)
	if len(w) == 0 {
		return ReleaseNone
	}
	switch pathnorm.CommandBase(w[0]) {
	case "gh":
		if len(w) >= 3 && w[1] == "release" && w[2] == "create" {
			return ReleaseCreate
		}
	case "glab":
		if len(w) >= 3 && w[1] == "release" && w[2] == "create" {
			return ReleaseCreate
		}
	case "goreleaser":
		if len(w) >= 2 && w[1] == "release" && !slices.Contains(w, "--snapshot") {
			return ReleaseCreate
		}
	case "git":
		sub, rest := gitSub(w[1:])
		switch sub {
		case "tag":
			if gitTagCreates(rest) {
				return ReleaseTag
			}
		case "push":
			for _, a := range rest {
				if a == "--tags" || a == "--follow-tags" || (!strings.HasPrefix(a, "-") && pushTagRefRe.MatchString(a)) {
					return ReleasePushTag
				}
			}
		}
	}
	return ReleaseNone
}

// gitSub returns a git subcommand and its arguments, skipping global options.
func gitSub(w []string) (string, []string) {
	for i := 0; i < len(w); i++ {
		if strings.HasPrefix(w[i], "-") {
			if w[i] == "-C" || w[i] == "-c" {
				i++
			}
			continue
		}
		return w[i], w[i+1:]
	}
	return "", nil
}

// gitTagCreates reports whether `git tag <args>` creates a tag: a name is
// given and no listing or verification flag is present.
func gitTagCreates(args []string) bool {
	name := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-d" || a == "--delete" || gitTagReadFlags[a] || strings.HasPrefix(a, "--list=") || strings.HasPrefix(a, "--sort=") || strings.HasPrefix(a, "--format=") || strings.HasPrefix(a, "--contains="):
			return false
		case a == "-m" || a == "-F" || a == "-u" || a == "--message" || a == "--file" || a == "--local-user":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			name = true
		}
	}
	return name
}

// gitTagMutates reports whether `git tag <args>` changes the tag set.
func gitTagMutates(args []string) bool {
	for _, a := range args {
		if a == "-d" || a == "--delete" {
			return true
		}
	}
	return gitTagCreates(args)
}

// CI outcomes read from a command that looks at remote CI.
const (
	CIUnknown = ""
	CIFailed  = "failed"
	CIPassed  = "passed"
)

var (
	ciFailLineRe   = lazyre.New(`(?i)^\s*(?:X|✗|×|❌)\s|\bcompleted\s+(?:failure|timed_out|startup_failure)\b|"conclusion"\s*:\s*"(?:failure|timed_out|startup_failure)"|^\S.*\tfail\t`)
	ciConclusionRe = lazyre.New(`"conclusion"\s*:\s*"([a-z_]*)"`)
	ciPassLineRe   = lazyre.New(`(?i)^\s*(?:✓|✔)\s|\bcompleted\s+success\b|"conclusion"\s*:\s*"success"|^\S.*\tpass\t`)
)

// CIResult reads the outcome of a command that looks at remote CI (gh run
// watch/view/list, gh pr checks, glab ci status/view). The exit status
// decides when the command reports it (--exit-status, gh pr checks);
// otherwise the first line that states an outcome does, which is the latest
// run in a list. Output of --log-failed exists only for a failed run. A
// command that does not look at CI, and output that states nothing, is
// CIUnknown.
func CIResult(cmd string, exitCode int, out string) string {
	outer, docs := splitHeredocs(cmd)
	// a here-document program that watches CI exits with the watch's result
	// when it hands it on (sys.exit(result.returncode))
	for _, d := range docs {
		for _, c := range embeddedCommands(d) {
			w := strings.Fields(c)
			if len(w) >= 3 && pathnorm.CommandBase(w[0]) == "gh" && ((w[1] == "run" && (w[2] == "watch" || w[2] == "view") && slices.Contains(w, "--exit-status")) || (w[1] == "pr" && w[2] == "checks")) {
				switch {
				case exitCode == 0:
					return CIPassed
				case exitCode == 8 && w[1] == "pr":
					return CIUnknown
				}
				return CIFailed
			}
		}
	}
	for _, st := range fp.SplitStages(outer) {
		n, _ := fp.NormalizeCmd(st)
		w := strings.Fields(n)
		if len(w) < 3 {
			continue
		}
		prog := pathnorm.CommandBase(w[0])
		switch {
		case prog == "gh" && w[1] == "pr" && w[2] == "checks":
			switch exitCode {
			case 0:
				return CIPassed
			case 8: // checks still pending
				return CIUnknown
			}
			return CIFailed
		case prog == "gh" && w[1] == "run" && (w[2] == "watch" || w[2] == "view") && slices.Contains(w, "--exit-status"):
			if exitCode == 0 {
				return CIPassed
			}
			return CIFailed
		case prog == "gh" && w[1] == "run" && w[2] == "view" && slices.Contains(w, "--log-failed"):
			if strings.TrimSpace(out) != "" {
				return CIFailed
			}
			return CIUnknown
		case (prog == "gh" && w[1] == "run" && (w[2] == "watch" || w[2] == "view" || w[2] == "list")) || (prog == "glab" && w[1] == "ci" && (w[2] == "status" || w[2] == "view" || w[2] == "list")):
			if exitCode != 0 {
				return CIUnknown // the command itself failed; it says nothing about CI
			}
			// --json output: the first conclusion is the run (or the latest
			// run of a list); an empty one is a run still in progress
			if m := ciConclusionRe.FindStringSubmatch(out); m != nil {
				switch m[1] {
				case "success":
					return CIPassed
				case "failure", "timed_out", "startup_failure", "cancelled", "action_required":
					return CIFailed
				}
				return CIUnknown
			}
			for _, l := range strings.Split(out, "\n") {
				switch {
				case ciFailLineRe.MatchString(l):
					return CIFailed
				case ciPassLineRe.MatchString(l):
					return CIPassed
				}
			}
			return CIUnknown
		}
	}
	return CIUnknown
}

// maskReaders are commands that, run last, replace a check's exit status with
// their own success: reading back the check's saved output.
var maskReaders = map[string]bool{"tail": true, "head": true, "cat": true, "echo": true, "printf": true, "true": true, ":": true, "less": true, "sed": true}

// MaskedExit reports whether a command's exit status belongs to a trailing
// read rather than to the check before it, as in `pytest > out.txt 2>&1;
// tail -20 out.txt`, `pytest | tail -20` or `make test || true`: the check
// may have failed with the command still exiting 0. Pass the raw command when
// known; normalization drops trailing pipes.
func MaskedExit(norm string) bool {
	seps, parts := topLevelSplit(norm)
	if len(parts) < 2 {
		return false
	}
	last := strings.Fields(parts[len(parts)-1])
	if len(last) == 0 {
		return false
	}
	prog := pathnorm.CommandBase(last[0])
	switch seps[len(seps)-1] {
	case ";", "\n", "||":
		return maskReaders[prog]
	case "|":
		// without pipefail a pipeline exits with its last command
		return maskReaders[prog] || prog == "grep" || prog == "tee"
	}
	return false
}

// topLevelSplit splits on ; && || | and newlines outside quotes and returns the
// separators between the parts.
func topLevelSplit(s string) (seps, parts []string) {
	inS, inD := false, false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inD:
			inS = !inS
		case c == '"' && !inS:
			inD = !inD
		case inS || inD:
		case c == ';' || c == '\n':
			parts, seps = append(parts, strings.TrimSpace(s[start:i])), append(seps, string(c))
			start = i + 1
		case (c == '&' || c == '|') && i+1 < len(s) && s[i+1] == c:
			parts, seps = append(parts, strings.TrimSpace(s[start:i])), append(seps, s[i:i+2])
			i++
			start = i + 1
		case c == '|':
			parts, seps = append(parts, strings.TrimSpace(s[start:i])), append(seps, "|")
			start = i + 1
		}
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	// drop empty parts with the separator before them
	var p2, s2 []string
	for i, p := range parts {
		if p == "" {
			continue
		}
		if len(p2) > 0 {
			s2 = append(s2, seps[i-1])
		}
		p2 = append(p2, p)
	}
	return s2, p2
}

// dropRedirects removes output redirections (> f, 2>> f, >f, &> f) from a
// command's words, so a log file is not read as a test path.
func dropRedirects(w []string) []string {
	out := w[:0:0]
	for i := 0; i < len(w); i++ {
		a := strings.TrimLeft(w[i], "0123456789&")
		if strings.HasPrefix(a, ">") || strings.HasPrefix(a, "<") {
			if strings.Trim(a, "><&") == "" && i+1 < len(w) {
				i++ // the target is the next word
			}
			continue
		}
		out = append(out, w[i])
	}
	return out
}
