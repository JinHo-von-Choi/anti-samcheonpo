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
