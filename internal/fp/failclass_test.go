package fp

import "testing"

func TestClassifyFailure(t *testing.T) {
	for _, tc := range []struct{ out, want string }{
		{"urllib.error.URLError: <urlopen error [Errno -2] Name or service not known>", FailUnresolved},
		{"Error: getaddrinfo ENOTFOUND log-sample.invalid", FailUnresolved},
		{"curl: (6) Could not resolve host: example.invalid", FailUnresolved},
		{"dial tcp 127.0.0.1:5432: connect: connection refused", FailServiceDown},
		{"Error: connect ECONNREFUSED 127.0.0.1:8765", FailServiceDown},
		{"Error: Cannot find module 'yaml'\nRequire stack:", FailDependency},
		{"Error: Cannot find module './lib/util'", FailFixable},
		{"ModuleNotFoundError: No module named 'requests'", FailDependency},
		{"ModuleNotFoundError: No module named 'app.sub'", FailFixable},
		{"main.go:3:8: no required module provides package github.com/x/y", FailDependency},
		{"error: no matching package named `serde_yamll` found", FailDependency},
		{"npm ERR! code EACCES", FailPermission},
		{"socket.gaierror: [Errno -3] Temporary failure in name resolution", FailTransient},
		{"Error: read ECONNRESET", FailTransient},
		{"AssertionError: assert 1 == 2", FailNone},
		{"", FailNone},
	} {
		if got := ClassifyFailure(tc.out); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.out, got, tc.want)
		}
	}
	if !External(FailDependency) || External(FailTransient) || External(FailFixable) || External(FailNone) {
		t.Error("only classes that need an outside action are external")
	}
}

func TestDiagnoseFailureNamesTheRemedy(t *testing.T) {
	for _, tc := range []struct {
		exit         int
		out          string
		class, rem   string
		wantExternal bool
	}{
		{1, "Error: listen EADDRINUSE: address already in use 0.0.0.0:8080", FailPortInUse, "lsof -nP -i :8080", true},
		{1, "HTTP/1.1 401 Unauthorized", FailAuth, "curl -sS -D- -o /dev/null <요청-url>", true},
		{127, "sh: 1: pulumi: not found", FailCommandMissing, "command -v pulumi", true},
		{126, "sh: 1: ./run.sh: Permission denied", FailNotExecutable, "ls -l <실행-경로>", true},
		{1, "write /tmp/x: no space left on device", FailDiskFull, "df -h .", true},
		{1, "bash: foo: command not found", FailCommandMissing, "command -v <실행-파일>", true},
		{1, "Error: Cannot find module 'yaml'", FailDependency, "npm install yaml", true},
		{1, "Error: Cannot find module './util'", FailFixable, "", false},
		{1, "socket.gaierror: [Errno -3] Temporary failure in name resolution", FailTransient, "", false},
		{1, "AssertionError: assert 1 == 2", FailNone, "", false},
		{124, "", FailNone, "", false},
		{0, "Cannot find module 'yaml'", FailNone, "", false},
	} {
		f := DiagnoseFailure(tc.exit, tc.out)
		if f.Class != tc.class || f.Remedy != tc.rem || f.External() != tc.wantExternal {
			t.Errorf("%d %q: got %+v", tc.exit, tc.out, f)
		}
	}
	if f := DiagnoseFailure(1, "AssertionError: assert 1 == 2"); f.Signal != "assertion_failure" || f.Evidence == "" {
		t.Errorf("a code failure still names its signal and evidence: %+v", f)
	}
}
