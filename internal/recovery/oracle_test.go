package recovery

import (
	"strings"
	"sync"
	"testing"
)

// A process failure whose cause lives outside the source tree must be reported
// as external and must freeze source writes. The remedy names the concrete
// command an operator runs; the oracle itself runs nothing.
func TestExternalSignalsAreClassifiedWithARemedy(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		exit   int
		signal string
		action string
	}{
		{"node missing module", "Error: Cannot find module 'yaml'", 1, "node_module_missing", "npm install yaml"},
		{"node missing module code", "Error [ERR_MODULE_NOT_FOUND]: Cannot find module 'yaml'", 1, "node_module_missing", "npm install yaml"},
		{"python missing module", "ModuleNotFoundError: No module named 'requests'", 1, "python_module_missing", "python3 -m pip install requests"},
		{"go missing package", "main.go:3:2: no required module provides package github.com/lib/pq; to add it:\n\tgo get github.com/lib/pq", 1, "go_package_missing", "go get github.com/lib/pq"},
		{"port in use", "Error: listen EADDRINUSE: address already in use 0.0.0.0:8080", 1, "port_in_use", "lsof -nP -i :8080"},
		{"port in use ipv6", "listen tcp6 :::3000: bind: address already in use :::3000", 1, "port_in_use", "lsof -nP -i :3000"},
		{"connection refused", "connect ECONNREFUSED 127.0.0.1:5432", 1, "service_refused", "ss -ltnp"},
		{"dns unresolved", "getaddrinfo ENOTFOUND api.example.com", 1, "dns_unresolved", "getent hosts"},
		{"dns unresolved glibc", "curl: (6) Could not resolve host: registry.example.com", 1, "dns_unresolved", "getent hosts"},
		{"dns unresolved python", "URLError: <urlopen error [Errno -2] Name or service not known>", 1, "dns_unresolved", "getent hosts"},
		{"unauthorized", "HTTP/1.1 401 Unauthorized", 1, "auth_rejected", "curl"},
		{"forbidden", "HTTP/1.1 403 Forbidden", 1, "auth_rejected", "curl"},
		{"permission denied", "open .env: permission denied", 2, "permission_denied", "id"},
	}
	o := NewFaultOracle()
	for _, c := range cases {
		got := o.Diagnose(c.exit, c.stderr)
		if got.Category != CategoryExternalEnvironment {
			t.Errorf("%s: category = %q, want %q", c.name, got.Category, CategoryExternalEnvironment)
			continue
		}
		if !got.RequiresCodeFreeze {
			t.Errorf("%s: an external fault must freeze source writes", c.name)
		}
		if got.Signal != c.signal {
			t.Errorf("%s: signal = %q, want %q", c.name, got.Signal, c.signal)
		}
		if !strings.Contains(got.PrescribedAction, c.action) {
			t.Errorf("%s: remedy %q does not name %q", c.name, got.PrescribedAction, c.action)
		}
		if got.Evidence == "" {
			t.Errorf("%s: a diagnosis without evidence is not a diagnosis", c.name)
		}
		if got.Cause() != Environment {
			t.Errorf("%s: an external fault must not prescribe code changes", c.name)
		}
	}
}

// A missing local import only looks environmental. Rewriting the source is the
// only possible remedy, so it stays a code fault.
func TestLocalImportIsNotAnExternalFault(t *testing.T) {
	for _, stderr := range []string{
		"Error: Cannot find module './util'",
		"Error: Cannot find module '/srv/app/lib/parser'",
		"ModuleNotFoundError: No module named 'app.util'",
		"main.go:3:2: no required module provides package ./internal/util",
	} {
		got := NewFaultOracle().Diagnose(1, stderr)
		if got.Category != CategoryCode {
			t.Errorf("%q: category = %q, want %q", stderr, got.Category, CategoryCode)
		}
		if got.RequiresCodeFreeze {
			t.Errorf("%q: a code fault must not freeze source writes", stderr)
		}
		if got.PrescribedAction != "" {
			t.Errorf("%q: a code fault must prescribe no external command, got %q", stderr, got.PrescribedAction)
		}
	}
}

// Syntax errors, logic failures and assertion failures are code facts.
func TestCodeSignalsAreClassifiedAsCode(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		exit   int
	}{
		{"go syntax", "./main.go:4:15: expected ';', found '}'", 2},
		{"go undefined", "./main.go:9:2: undefined: parseConfig", 2},
		{"go assert", "--- FAIL: TestParse (0.00s)\n    main_test.go:12: want 3, got 4", 1},
		{"python syntax", "  File \"app.py\", line 3\n    return (\n           ^\nSyntaxError: '(' was never closed", 1},
		{"python assert", "Traceback (most recent call last):\nAssertionError: 3 != 4", 1},
		{"node assert", "AssertionError [ERR_ASSERTION]: 3 === 4", 1},
		{"node syntax", "SyntaxError: Unexpected token '}'", 1},
		{"panic", "panic: runtime error: index out of range [3] with length 3", 2},
		{"failed build", "FAIL\tgithub.com/JinHo-von-Choi/anti-samcheonpo/internal/fp [build failed]", 2},
	}
	o := NewFaultOracle()
	for _, c := range cases {
		got := o.Diagnose(c.exit, c.stderr)
		if got.Category != CategoryCode {
			t.Errorf("%s: category = %q, want %q", c.name, got.Category, CategoryCode)
		}
		if got.RequiresCodeFreeze || got.PrescribedAction != "" {
			t.Errorf("%s: a code fault must not prescribe an external command", c.name)
		}
	}
}

// A successful process is not a fault. Prescribing anything for it would be an
// invention.
func TestSuccessfulExecutionPrescribesNothing(t *testing.T) {
	got := NewFaultOracle().Diagnose(0, "")
	if got.Category != CategoryNoFault {
		t.Errorf("category = %q, want %q", got.Category, CategoryNoFault)
	}
	if got.RequiresCodeFreeze || got.PrescribedAction != "" || got.Evidence != "" {
		t.Errorf("a successful run produced a diagnosis: %+v", got)
	}
	if got.Cause() != Unknown {
		t.Errorf("cause = %q, want %q", got.Cause(), Unknown)
	}
}

// Output with no recognizable cause stays a code failure: unknown evidence must
// not authorize an external remedy.
func TestUnrecognizedFailureIsACodeFault(t *testing.T) {
	got := NewFaultOracle().Diagnose(3, "something went sideways\n")
	if got.Category != CategoryCode || got.RequiresCodeFreeze || got.PrescribedAction != "" {
		t.Fatalf("%+v", got)
	}
}

// A failed execution is evidence; prose without one is not.
func TestDiagnosisWithoutAFailureHasNoFault(t *testing.T) {
	if got := NewFaultOracle().Diagnose(0, "Cannot find module 'yaml'"); got.Category != CategoryNoFault {
		t.Fatalf("%+v", got)
	}
}

// Exit status alone can carry an external cause when the output names no better
// one.
func TestExitStatusSignals(t *testing.T) {
	cases := []struct {
		name   string
		exit   int
		stderr string
		signal string
	}{
		{"command not found", 127, "sh: 1: pulumi: not found", "command_missing"},
		{"not executable", 126, "sh: 1: ./run.sh: Permission denied", "not_executable"},
		{"killed", 137, "", "process_killed"},
		{"timed out", 124, "", "process_timeout"},
	}
	o := NewFaultOracle()
	for _, c := range cases {
		got := o.Diagnose(c.exit, c.stderr)
		if got.Category != CategoryExternalEnvironment || got.Signal != c.signal {
			t.Errorf("%s: %+v", c.name, got)
		}
		if !got.RequiresCodeFreeze {
			t.Errorf("%s: an external fault must freeze source writes", c.name)
		}
	}
}

// The root cause outranks the exit status and a code marker in the same output:
// an external fault cannot be repaired by editing the source, so it wins.
func TestExternalEvidenceOutranksCodeMarkers(t *testing.T) {
	got := NewFaultOracle().Diagnose(1, "--- FAIL: TestBoot\n    boot_test.go:20: connect ECONNREFUSED 127.0.0.1:5432")
	if got.Category != CategoryExternalEnvironment || got.Signal != "service_refused" {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(got.Evidence, "ECONNREFUSED") {
		t.Errorf("evidence %q does not carry the matched output", got.Evidence)
	}
}

// The remedy must name the specific subject when the output names one, so an
// operator does not have to guess which dependency is missing.
func TestRemedyNamesTheObservedSubject(t *testing.T) {
	if got := NewFaultOracle().Diagnose(1, "Error: Cannot find module 'lodash'"); !strings.Contains(got.PrescribedAction, "lodash") {
		t.Errorf("remedy %q does not name the missing module", got.PrescribedAction)
	}
	if got := NewFaultOracle().Diagnose(1, "python3: can't open file 'run.py': [Errno 2] No such file or directory"); got.Category != CategoryCode {
		t.Errorf("a missing project file is a code fact: %+v", got)
	}
}

// One oracle is shared across concurrent diagnosis; a shared mutable matcher
// would report a fault from another process.
func TestOracleIsSafeForConcurrentUse(t *testing.T) {
	o := NewFaultOracle()
	stderrs := []string{
		"Error: Cannot find module 'yaml'",
		"listen tcp: address already in use :8080",
		"connect ECONNREFUSED 127.0.0.1:5432",
		"HTTP/1.1 401 Unauthorized",
		"./main.go:4:15: expected ';', found '}'",
		"",
	}
	want := make([]string, len(stderrs))
	for i, s := range stderrs {
		want[i] = string(o.Diagnose(1, s).Category)
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if got := string(o.Diagnose(1, stderrs[i%len(stderrs)]).Category); got != want[i%len(stderrs)] {
				t.Errorf("concurrent diagnosis returned %q, want %q", got, want[i%len(stderrs)])
			}
		}(i)
	}
	wg.Wait()
}

// The oracle feeds the bounded recovery model: one category, one cause, one
// prescription.
func TestOracleBridgesIntoDiagnoseAndPrescribe(t *testing.T) {
	res, cause := DiagnoseExecution("s2.stuck_error", 1, "connect ECONNREFUSED 127.0.0.1:5432")
	if res.Category != CategoryExternalEnvironment || cause != Environment {
		t.Fatalf("%+v cause=%q", res, cause)
	}
	p := Prescribe(cause, res)
	if !p.Handoff || p.MaxAttempts != 1 || p.StopCondition == "" {
		t.Fatalf("%+v", p)
	}
	if !strings.Contains(p.Shell, "ss -ltnp") {
		t.Fatalf("the prescription dropped the remedy: %+v", p)
	}

	res, cause = DiagnoseExecution("s2.stuck_error", 1, "./main.go:9:2: undefined: parseConfig")
	if res.Category != CategoryCode || cause != Code {
		t.Fatalf("%+v cause=%q", res, cause)
	}
	if p := Prescribe(cause, res); p.Handoff || p.Shell != "" {
		t.Fatalf("a code fault handed off or prescribed a shell remedy instead of a bounded repair: %+v", p)
	}

	// The oracle keeps an observed external fault from being re-read as a
	// code repair even when the rule alone would say so.
	res, _ = DiagnoseExecution("s1.verification", 1, "open .env: permission denied")
	if p := Prescribe(Code, res); !p.Handoff || p.Cause != Environment {
		t.Fatalf("%+v", p)
	}
}
