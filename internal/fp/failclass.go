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
	FailNone        = ""
	FailUnresolved  = "unresolvable_host" // the name does not resolve at all
	FailServiceDown = "service_down"      // nothing listens at the address
	FailDependency  = "dependency_missing"
	FailPermission  = "permission"
	FailTransient   = "transient"
	FailFixable     = "fixable_import"
)

var (
	nodeModuleRe = regexp.MustCompile(`Cannot find module '([^']+)'`)
	pyModuleRe   = regexp.MustCompile(`No module named '([^']+)'`)
	goPkgRe      = regexp.MustCompile(`no required module provides package |cannot find package "`)
)

// External reports whether a class needs an action outside the code.
func External(class string) bool {
	switch class {
	case FailUnresolved, FailServiceDown, FailDependency, FailPermission:
		return true
	}
	return false
}

// ClassifyFailure classifies the tail of a failed command's output. Only the
// output is evidence; it never reads instructions out of it. Unknown output
// is FailNone so callers keep treating it as a code failure.
func ClassifyFailure(out string) string {
	lower := strings.ToLower(out)
	has := func(phrases ...string) bool {
		for _, p := range phrases {
			if strings.Contains(lower, p) {
				return true
			}
		}
		return false
	}
	// Node error codes are matched case-sensitively: "ModuleNotFoundError"
	// lowercases to a string containing "enotfound".
	code := func(codes ...string) bool {
		for _, c := range codes {
			if strings.Contains(out, c) {
				return true
			}
		}
		return false
	}
	switch {
	case has("temporary failure in name resolution", "connection timed out", "read timed out",
		"connection reset", "tls handshake timeout", "503 service unavailable", "502 bad gateway") || code("EAI_AGAIN", "ETIMEDOUT", "ECONNRESET"):
		return FailTransient
	case has("name or service not known", "could not resolve host", "nodename nor servname", "no such host") || code("ENOTFOUND"):
		return FailUnresolved
	case has("connection refused") || code("ECONNREFUSED"):
		return FailServiceDown
	case has("permission denied", "operation not permitted") || code("EACCES"):
		return FailPermission
	}
	if m := nodeModuleRe.FindStringSubmatch(out); m != nil {
		return moduleClass(m[1])
	}
	if m := pyModuleRe.FindStringSubmatch(out); m != nil {
		// a dotted name can be a local package; only a single top-level name
		// is treated as a missing third-party dependency
		if strings.Contains(m[1], ".") {
			return FailFixable
		}
		return FailDependency
	}
	if goPkgRe.MatchString(out) || has("could not find `", "failed to select a version for", "error: no matching package named") {
		return FailDependency
	}
	return FailNone
}

// moduleClass separates a relative or absolute module path (a code mistake)
// from a bare package name (a dependency that is not installed).
func moduleClass(name string) string {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "/") {
		return FailFixable
	}
	return FailDependency
}
