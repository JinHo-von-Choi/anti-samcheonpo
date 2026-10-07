package recovery

import (
	"regexp"
	"strings"
)

// FaultCategory separates a fault inside the source tree from a fault outside
// it. Only the second kind can be repaired without touching the source, and it
// is the only kind that authorizes prescribing a command here.
type FaultCategory string

const (
	CategoryNoFault             FaultCategory = "no_fault"
	CategoryCode                FaultCategory = "code"
	CategoryExternalEnvironment FaultCategory = "external_environment"
)

// DiagnosticResult reports what failed and, for an external fault, which
// command an operator runs to observe or correct it. The oracle prescribes the
// remedy and never runs it: executing a remedy here would be the code change the
// diagnosis exists to question.
type DiagnosticResult struct {
	Category FaultCategory `json:"category"`
	// Signal is the stable machine key of the matched cause.
	Signal string `json:"signal"`
	// Evidence is the output the verdict rests on, bounded in length.
	Evidence string `json:"evidence"`
	// PrescribedAction is a shell command to run outside this process. It is
	// empty unless the category is external.
	PrescribedAction string `json:"prescribed_action"`
	// RequiresCodeFreeze freezes source writes: editing the source cannot
	// correct a fault outside it, so a change made now is an unevidenced one.
	RequiresCodeFreeze bool `json:"requires_code_freeze"`
}

// Cause maps the category onto the bounded recovery model.
func (r DiagnosticResult) Cause() Cause {
	switch r.Category {
	case CategoryExternalEnvironment:
		return Environment
	case CategoryCode:
		return Code
	default:
		return Unknown
	}
}

type faultSignal struct {
	signal string
	// phrases are lowercased markers of the cause and match case-insensitively.
	phrases []string
	// codes are markers whose case carries meaning, so they match exactly: a
	// lowercased "ModuleNotFoundError" contains "enotfound" and would
	// otherwise be read as a DNS failure.
	codes []string
	// subject names the specific thing the remedy must mention. It is empty
	// when the output cannot be reduced to one safe identifier. A subject that
	// matches on its own is enough to identify the fault, so a signal with
	// only a subject still reports it.
	subject *regexp.Regexp
	// reject rejects a subject that only looks external. A missing import of
	// the code's own file is a mistake in the source, not an absent
	// dependency, so it must not authorize an install.
	reject  func(subject string) bool
	remedy  string
	generic string
}

type exitSignal struct {
	code   int
	signal string
	remedy string
}

var (
	nodeModuleRe = regexp.MustCompile(`Cannot find module ['"]([^'"]+)['"]`)
	pyModuleRe   = regexp.MustCompile(`No module named ['"]([^'"]+)['"]`)
	goModuleRe   = regexp.MustCompile(`no required module provides package ([^\s;]+)`)
	// The port is the last number of the line that reports the conflict, which
	// keeps it correct across 0.0.0.0:8080, :::3000 and EADDRINUSE forms.
	portRe     = regexp.MustCompile(`(?im)(?:EADDRINUSE|address already in use)[^\n]*?(\d{2,5})\s*$`)
	urlRe      = regexp.MustCompile(`https?://[^\s'"]+`)
	missingCmd = regexp.MustCompile(`([\w./-]+): not found`)
)

func externalSignals() []faultSignal {
	return []faultSignal{
		{
			signal:  "node_module_missing",
			subject: nodeModuleRe,
			reject:  isLocalPath,
			remedy:  "npm install %s",
			generic: "npm install <누락-모듈>",
		},
		{
			signal:  "python_module_missing",
			subject: pyModuleRe,
			reject:  isLocalModule,
			remedy:  "python3 -m pip install %s",
			generic: "python3 -m pip install <누락-모듈>",
		},
		{
			signal:  "go_package_missing",
			subject: goModuleRe,
			reject:  isLocalPath,
			remedy:  "go get %s",
			generic: "go mod tidy",
		},
		{
			signal:  "port_in_use",
			subject: portRe,
			remedy:  "lsof -nP -i :%s",
			generic: "lsof -nP -i | grep LISTEN",
		},
		{
			signal: "service_refused",
			// Nothing listens on the address; observe the listeners before
			// starting or stopping anything.
			phrases: []string{"connection refused"},
			codes:   []string{"ECONNREFUSED"},
			remedy:  "ss -ltnp",
		},
		{
			signal: "dns_unresolved",
			// Resolution happens outside the process, so the remedy observes
			// it with the name the resolver itself uses.
			phrases: []string{"could not resolve host", "name or service not known", "nodename nor servname", "no such host", "temporary failure in name resolution"},
			codes:   []string{"ENOTFOUND", "EAI_AGAIN"},
			remedy:  "getent hosts",
		},
		{
			signal:  "auth_rejected",
			subject: urlRe,
			phrases: []string{"401 unauthorized", "403 forbidden"},
			remedy:  "curl -sS -D- -o /dev/null %s",
			generic: "curl -sS -D- -o /dev/null <요청-url>",
		},
		{
			signal:  "permission_denied",
			phrases: []string{"permission denied", "operation not permitted"},
			codes:   []string{"EACCES"},
			remedy:  "id",
		},
	}
}

func externalExitSignals() []exitSignal {
	return []exitSignal{
		{code: 126, signal: "not_executable", remedy: "ls -l <실행-경로>"},
		{code: 127, signal: "command_missing", remedy: "command -v <실행-파일>"},
		{code: 124, signal: "process_timeout", remedy: "ps -eo pid,etime,cmd"},
		// 137 is a kill, usually the out-of-memory killer; the source is not
		// what stopped the process.
		{code: 137, signal: "process_killed", remedy: "dmesg --ctime | tail -n 20"},
	}
}

// codeSignals are failures whose remedy is a change to the source. They carry
// no shell command: prescribing one would move the repair outside the evidence
// that the repair needs.
func codeSignals() []faultSignal {
	return []faultSignal{
		{
			signal: "assertion_failure",
			phrases: []string{
				"assertionerror", "assertion failed", "--- fail:", "fail\t",
				"test result: fail", "expected:", "panic:", "fatal error:",
				"traceback (most recent call last)",
			},
		},
		{
			signal: "syntax_error",
			phrases: []string{
				"syntaxerror", "syntax error", "unexpected token", "unexpected eof",
				"unexpected identifier", "indentationerror", "expected ';'", "expected expression",
				"unclosed", "mismatched types", "cannot use", "undefined:",
				"undeclared identifier", "declared and not used", "could not compile",
				"build failed", "compile error", "error: expected",
			},
		},
	}
}

// FaultOracle classifies a failed process. It holds no mutable state, so one
// oracle may serve every diagnosis of a session.
type FaultOracle struct {
	signals []faultSignal
	exits   []exitSignal
}

// NewFaultOracle returns an oracle that classifies a process failure by exit
// status and by output. Nothing is executed while building it.
func NewFaultOracle() *FaultOracle {
	return &FaultOracle{signals: externalSignals(), exits: externalExitSignals()}
}

// Diagnose reads a failed execution. The output is evidence; it is never read as
// an instruction. A zero exit status is not a fault, so it produces no remedy,
// and output with no recognizable cause stays a code failure, so an unknown
// failure never authorizes a command outside the source tree.
func (o *FaultOracle) Diagnose(exitCode int, stderr string) DiagnosticResult {
	if exitCode == 0 {
		return DiagnosticResult{Category: CategoryNoFault}
	}
	// The exit status is the most reliable statement about why the process
	// stopped, so it outranks the text of a run that never got to run.
	for _, s := range o.exits {
		if exitCode == s.code {
			remedy := s.remedy
			if s.signal == "command_missing" {
				if m := missingCmd.FindStringSubmatch(stderr); m != nil {
					remedy = "command -v " + m[1]
				}
			}
			return DiagnosticResult{
				Category:           CategoryExternalEnvironment,
				Signal:             s.signal,
				Evidence:           evidence(stderr, ""),
				PrescribedAction:   remedy,
				RequiresCodeFreeze: true,
			}
		}
	}
	// An external cause outranks a code marker in the same output: a test that
	// failed because nothing listened on its port was not repaired by a source
	// edit, and prescribing one would hide the real cause.
	for _, s := range o.signals {
		if res, ok := o.match(s, stderr); ok {
			return res
		}
	}
	for _, s := range codeSignals() {
		if res, ok := o.match(s, stderr); ok {
			return DiagnosticResult{Category: CategoryCode, Signal: s.signal, Evidence: res.Evidence}
		}
	}
	return DiagnosticResult{Category: CategoryCode, Signal: "unclassified_failure", Evidence: evidence(stderr, "")}
}

func (o *FaultOracle) match(s faultSignal, stderr string) (DiagnosticResult, bool) {
	needle := ""
	lower := strings.ToLower(stderr)
	for _, p := range s.phrases {
		if strings.Contains(lower, p) {
			needle = p
			break
		}
	}
	if needle == "" {
		for _, c := range s.codes {
			if strings.Contains(stderr, c) {
				needle = c
				break
			}
		}
	}
	subject := ""
	if s.subject != nil {
		if m := s.subject.FindStringSubmatch(stderr); m != nil {
			subject = strings.TrimSpace(m[1])
		}
	}
	if needle == "" && subject == "" {
		return DiagnosticResult{}, false
	}
	if s.reject != nil && s.reject(subject) {
		return DiagnosticResult{}, false
	}
	remedy := s.generic
	if s.remedy != "" {
		switch {
		case subject != "":
			remedy = strings.Replace(s.remedy, "%s", subject, 1)
		case remedy == "":
			remedy = s.remedy
		}
	}
	line := evidence(stderr, needle)
	if line == "" {
		line = evidence(stderr, subject)
	}
	return DiagnosticResult{
		Category:           CategoryExternalEnvironment,
		Signal:             s.signal,
		Evidence:           line,
		PrescribedAction:   remedy,
		RequiresCodeFreeze: true,
	}, true
}

// isLocalPath rejects a relative or absolute import, which names the code's own
// file rather than a package that could be installed.
func isLocalPath(subject string) bool {
	return strings.HasPrefix(subject, ".") || strings.HasPrefix(subject, "/") ||
		strings.HasPrefix(subject, "~") || strings.HasPrefix(subject, "file:")
}

// isLocalModule rejects a dotted name, which is a package inside the project.
func isLocalModule(subject string) bool {
	return strings.Contains(subject, ".") || isLocalPath(subject)
}

const evidenceLimit = 200

// evidence returns the line the verdict rests on, bounded so a long log cannot
// be carried into a decision as if it were the finding.
func evidence(stderr, needle string) string {
	line := strings.TrimSpace(stderr)
	if needle != "" {
		for _, l := range strings.Split(stderr, "\n") {
			if strings.Contains(strings.ToLower(l), strings.ToLower(needle)) || strings.Contains(l, needle) {
				line = strings.TrimSpace(l)
				break
			}
		}
	}
	if line == "" {
		return ""
	}
	r := []rune(line)
	if len(r) > evidenceLimit {
		line = string(r[:evidenceLimit])
	}
	return line
}
