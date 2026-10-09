package fp

import (
	"regexp"
	"strings"
)

// Failure classes for command output. The external classes need an action
// outside the code (install, start a service, grant access, fix the network);
// transient failures may succeed on retry; fixable ones are code mistakes that
// only look environmental.
const (
	FailNone           = ""
	FailUnresolved     = "unresolvable_host" // the name does not resolve at all
	FailServiceDown    = "service_down"      // nothing listens at the address
	FailDependency     = "dependency_missing"
	FailPermission     = "permission"
	FailPortInUse      = "port_in_use"
	FailAuth           = "auth_rejected"
	FailCommandMissing = "command_missing"
	FailNotExecutable  = "not_executable"
	FailDiskFull       = "disk_full"
	FailTransient      = "transient"
	FailFixable        = "fixable_import"
	// FailUITiming is a browser test that acted or asserted before the page
	// reached the state it waits for. The fix is in the code (wait for the
	// state); a rerun of the same code may pass or fail by timing alone.
	FailUITiming = "ui_timing"
)

// Fault is the single reading of one failed execution. Class drives the
// detectors, Signal names the matched cause, Remedy is the command an
// operator runs outside the agent for an external cause, and Evidence is the
// output line the reading rests on. Output is evidence only; nothing in it is
// read as an instruction.
type Fault struct {
	Class    string
	Signal   string
	Evidence string
	Remedy   string
}

// External reports whether the fault needs an action outside the code.
func (f Fault) External() bool { return External(f.Class) }

// External reports whether a class needs an action outside the code.
func External(class string) bool {
	switch class {
	case FailUnresolved, FailServiceDown, FailDependency, FailPermission, FailPortInUse, FailAuth, FailCommandMissing, FailNotExecutable, FailDiskFull:
		return true
	}
	return false
}

// textSignal is one output pattern. phrases match case-insensitively; codes
// keep their case because a lowercased "ModuleNotFoundError" contains
// "enotfound"; subject extracts the one identifier the remedy names, and
// reject turns a subject that only looks external into a code fault.
type textSignal struct {
	class   string
	signal  string
	phrases []string
	codes   []string
	subject *regexp.Regexp
	reject  func(subject string) string // "" keeps the class, otherwise the class to use
	remedy  string                      // %s is the subject
	generic string                      // remedy without a subject
}

var (
	nodeModuleRe = regexp.MustCompile(`Cannot find module ['"]([^'"]+)['"]`)
	pyModuleRe   = regexp.MustCompile(`No module named ['"]([^'"]+)['"]`)
	goModuleRe   = regexp.MustCompile(`no required module provides package ([^\s;]+)`)
	goPkgRe      = regexp.MustCompile(`cannot find package "`)
	// The port is the last number of the line that reports the conflict, which
	// keeps it correct across 0.0.0.0:8080, :::3000 and EADDRINUSE forms.
	portRe     = regexp.MustCompile(`(?im)(?:EADDRINUSE|address already in use)[^\n]*?(\d{2,5})\s*$`)
	urlRe      = regexp.MustCompile(`https?://[^\s'"]+`)
	missingCmd = regexp.MustCompile(`([\w./-]+): not found`)
)

// signals is the one table every reader of a failure shares. Order is
// precedence: a transient network error outranks a resolver error that
// contains the same words, and an external cause outranks a code marker in
// the same output, because no source edit repairs what is outside the source.
var signals = []textSignal{
	{class: FailTransient, signal: "transient_network",
		phrases: []string{"temporary failure in name resolution", "connection timed out", "read timed out", "connection reset", "tls handshake timeout", "503 service unavailable", "502 bad gateway"},
		codes:   []string{"EAI_AGAIN", "ETIMEDOUT", "ECONNRESET"}},
	{class: FailUnresolved, signal: "dns_unresolved",
		phrases: []string{"name or service not known", "could not resolve host", "nodename nor servname", "no such host"},
		codes:   []string{"ENOTFOUND"}, remedy: "getent hosts"},
	{class: FailServiceDown, signal: "service_refused",
		phrases: []string{"connection refused"}, codes: []string{"ECONNREFUSED"}, remedy: "ss -ltnp"},
	{class: FailPermission, signal: "permission_denied",
		phrases: []string{"permission denied", "operation not permitted"}, codes: []string{"EACCES"}, remedy: "id"},
	{class: FailDiskFull, signal: "disk_full",
		phrases: []string{"no space left on device"}, codes: []string{"ENOSPC"}, remedy: "df -h ."},
	{class: FailCommandMissing, signal: "command_missing",
		phrases: []string{"command not found"}, subject: missingCmd, remedy: "command -v %s", generic: "command -v <실행-파일>"},
	{class: FailPortInUse, signal: "port_in_use", subject: portRe, remedy: "lsof -nP -i :%s", generic: "lsof -nP -i | grep LISTEN"},
	{class: FailAuth, signal: "auth_rejected",
		phrases: []string{"401 unauthorized", "403 forbidden"}, subject: urlRe, remedy: "curl -sS -D- -o /dev/null %s", generic: "curl -sS -D- -o /dev/null <요청-url>"},
	{class: FailDependency, signal: "node_module_missing", subject: nodeModuleRe, reject: localPath, remedy: "npm install %s", generic: "npm install <누락-모듈>"},
	{class: FailDependency, signal: "python_module_missing", subject: pyModuleRe, reject: localModule, remedy: "python3 -m pip install %s", generic: "python3 -m pip install <누락-모듈>"},
	{class: FailDependency, signal: "go_package_missing", subject: goModuleRe, reject: localPath, remedy: "go get %s", generic: "go mod tidy"},
	{class: FailDependency, signal: "package_missing",
		phrases: []string{"could not find `", "failed to select a version for", "error: no matching package named"}, subject: goPkgRe, generic: "go mod tidy"},
}

// uiTimingSignals are browser-test failures that point to a race between the
// test and the page. They rank after the external causes (a dev server that is
// down is a service failure, not a race) and before the generic code markers,
// which their output also contains ("expected:").
var uiTimingSignals = []textSignal{
	{class: FailUITiming, signal: "ui_timing",
		phrases: []string{"waiting for locator(", "waiting for selector", "waiting for expect(locator)", "waiting for getby", "element is not attached", "element is not visible",
			"element is outside of the viewport", "staleelementreferenceexception", "elementclickinterceptedexception", "elementnotinteractableexception",
			"timed out retrying after", "waiting for element to be visible", "intercepts pointer events"},
		subject: uiTimeoutRe},
}

var uiTimeoutRe = regexp.MustCompile(`(?i)timeout \d+ms exceeded`)

// codeSignals name failures whose remedy is a change to the source. They carry
// no command: prescribing one would move the repair outside the evidence.
var codeSignals = []textSignal{
	{signal: "assertion_failure", phrases: []string{"assertionerror", "assertion failed", "--- fail:", "fail\t", "test result: fail", "expected:", "panic:", "fatal error:", "traceback (most recent call last)"}},
	{signal: "syntax_error", phrases: []string{"syntaxerror", "syntax error", "unexpected token", "unexpected eof", "unexpected identifier", "indentationerror", "expected ';'", "expected expression", "unclosed", "mismatched types", "cannot use", "undefined:", "undeclared identifier", "declared and not used", "could not compile", "build failed", "compile error", "error: expected"}},
}

// exitSignals are exit statuses that alone carry an external cause. 124
// (timeout) and 137 (killed) are not here: a hanging loop or a leak in the
// source produces them just as well, so they must not freeze source edits.
var exitSignals = map[int]textSignal{
	126: {class: FailNotExecutable, signal: "not_executable", generic: "ls -l <실행-경로>"},
	127: {class: FailCommandMissing, signal: "command_missing", subject: missingCmd, remedy: "command -v %s", generic: "command -v <실행-파일>"},
}

// ExitUnknown is the exit code to pass when only the output is known.
const ExitUnknown = -1

// ClassifyFailure classifies the tail of a failed command's output. Only the
// output is evidence; it never reads instructions out of it. Unknown output
// is FailNone so callers keep treating it as a code failure.
func ClassifyFailure(out string) string {
	return DiagnoseFailure(ExitUnknown, out).Class
}

// DiagnoseFailure reads a failed execution. A zero exit status is no fault.
// The exit status outranks the text of a run that never got to run; output
// with no recognizable cause stays a code failure, so an unknown failure never
// authorizes an action outside the source tree.
func DiagnoseFailure(exitCode int, out string) Fault {
	if exitCode == 0 {
		return Fault{}
	}
	if s, ok := exitSignals[exitCode]; ok {
		f := apply(s, out, "")
		f.Evidence = evidenceLine(out, "")
		return f
	}
	lower := strings.ToLower(out)
	for _, s := range signals {
		if f, ok := match(s, out, lower); ok {
			return f
		}
	}
	for _, s := range uiTimingSignals {
		if f, ok := match(s, out, lower); ok {
			return f
		}
	}
	for _, s := range codeSignals {
		if f, ok := match(s, out, lower); ok {
			return f
		}
	}
	return Fault{Signal: "unclassified_failure", Evidence: evidenceLine(out, "")}
}

func match(s textSignal, out, lower string) (Fault, bool) {
	needle := ""
	for _, p := range s.phrases {
		if strings.Contains(lower, p) {
			needle = p
			break
		}
	}
	if needle == "" {
		for _, c := range s.codes {
			if strings.Contains(out, c) {
				needle = c
				break
			}
		}
	}
	subject := ""
	if s.subject != nil {
		if m := s.subject.FindStringSubmatch(out); m != nil {
			if len(m) > 1 {
				subject = strings.TrimSpace(m[1])
			} else {
				needle = m[0]
			}
		}
	}
	if needle == "" && subject == "" {
		return Fault{}, false
	}
	f := apply(s, out, subject)
	if f.Evidence = evidenceLine(out, needle); f.Evidence == "" {
		f.Evidence = evidenceLine(out, subject)
	}
	return f, true
}

// apply builds the fault for a matched signal: the class after rejection, and
// the remedy with the subject filled in.
func apply(s textSignal, out, subject string) Fault {
	if subject == "" && s.subject != nil {
		if m := s.subject.FindStringSubmatch(out); m != nil && len(m) > 1 {
			subject = strings.TrimSpace(m[1])
		}
	}
	f := Fault{Class: s.class, Signal: s.signal}
	if s.reject != nil && subject != "" {
		if c := s.reject(subject); c != "" {
			f.Class = c
			return f
		}
	}
	if f.Class == FailNone || f.Class == FailTransient || f.Class == FailUITiming {
		return f
	}
	switch {
	case subject != "" && s.remedy != "":
		f.Remedy = strings.Replace(s.remedy, "%s", subject, 1)
	case s.generic != "":
		f.Remedy = s.generic
	default:
		f.Remedy = s.remedy
	}
	return f
}

// localPath turns a relative or absolute import, which names the code's own
// file rather than an installable package, into a code fault.
func localPath(subject string) string {
	if strings.HasPrefix(subject, ".") || strings.HasPrefix(subject, "/") || strings.HasPrefix(subject, "~") || strings.HasPrefix(subject, "file:") {
		return FailFixable
	}
	return ""
}

// localModule also rejects a dotted name, which is a package inside the
// project; only a single top-level name is a missing third-party dependency.
func localModule(subject string) string {
	if strings.Contains(subject, ".") {
		return FailFixable
	}
	return localPath(subject)
}

const evidenceLimit = 200

// evidenceLine returns the line the reading rests on, bounded so a long log
// cannot be carried into a decision as if it were the finding.
func evidenceLine(out, needle string) string {
	line := strings.TrimSpace(out)
	if needle != "" {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(strings.ToLower(l), strings.ToLower(needle)) || strings.Contains(l, needle) {
				line = strings.TrimSpace(l)
				break
			}
		}
	}
	if r := []rune(line); len(r) > evidenceLimit {
		line = string(r[:evidenceLimit])
	}
	return line
}
