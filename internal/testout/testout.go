// Package testout extracts failed test names from test runner output.
package testout

import (
	"encoding/xml"
	"os"
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rules"
)

var (
	ansiRe     = lazyre.New(`\x1b\[[0-9;]*[A-Za-z]`)
	pytestRe   = lazyre.New(`^(?:FAILED|ERROR) (\S+::\S+)`)
	jestFileRe = lazyre.New(`^\s*FAIL\s+(\S+)`)
	jestXRe    = lazyre.New(`^\s*(?:✕|×|✗)\s+(.+?)(?:\s+\(\d+\s*m?s\))?$`)
	jestDotRe  = lazyre.New(`^\s*●\s+(.+)$`)
	goFailRe   = lazyre.New(`^\s*--- FAIL: (\S+)`)
	cargoRe    = lazyre.New(`^test (\S+) \.\.\. FAILED`)
	gradleRe   = lazyre.New(`^(\S+) > (.+) FAILED$`)
	mavenRe    = lazyre.New(`^\[ERROR\]\s+(\S+\.\S+):\d+`)
	junitRe    = lazyre.New(`(\S+\.xml)`)
)

// FailedTests returns the sorted unique failed test names found in out.
func FailedTests(out string) []string {
	if out == "" {
		return nil
	}
	out = ansiRe.ReplaceAllString(out, "")
	set := map[string]bool{}
	extra := rules.Current().FailedTests
	for _, raw := range strings.Split(out, "\n") {
		ln := strings.TrimRight(raw, "\r")
		for _, name := range extra.Captures(ln) {
			set[name] = true
		}
		if m := pytestRe.FindStringSubmatch(ln); m != nil {
			set[strings.TrimSuffix(m[1], " -")] = true
			continue
		}
		if m := goFailRe.FindStringSubmatch(ln); m != nil {
			set[m[1]] = true
			continue
		}
		if m := cargoRe.FindStringSubmatch(ln); m != nil {
			set[m[1]] = true
			continue
		}
		if m := gradleRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			set[m[1]+" > "+m[2]] = true
			continue
		}
		if m := jestXRe.FindStringSubmatch(ln); m != nil {
			set[strings.TrimSpace(m[1])] = true
			continue
		}
		if m := jestDotRe.FindStringSubmatch(ln); m != nil {
			name := strings.TrimSpace(m[1])
			if !strings.HasPrefix(name, "Console") {
				set[name] = true
			}
			continue
		}
		if m := mavenRe.FindStringSubmatch(ln); m != nil && strings.Contains(ln, "<<<") {
			set[m[1]] = true
		}
	}
	res := make([]string, 0, len(set))
	for k := range set {
		res = append(res, k)
	}
	sort.Strings(res)
	return res
}

// FromJUnitFiles reads JUnit XML report files referenced in out (when they
// exist locally) and returns failed test names. Used only in live mode.
func FromJUnitFiles(out string) []string {
	set := map[string]bool{}
	for _, p := range junitRe.FindAllString(out, -1) {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, n := range parseJUnit(b) {
			set[n] = true
		}
	}
	res := make([]string, 0, len(set))
	for k := range set {
		res = append(res, k)
	}
	sort.Strings(res)
	return res
}

type junitCase struct {
	Name      string    `xml:"name,attr"`
	ClassName string    `xml:"classname,attr"`
	Failure   *struct{} `xml:"failure"`
	Error     *struct{} `xml:"error"`
}
type junitSuite struct {
	Cases  []junitCase  `xml:"testcase"`
	Suites []junitSuite `xml:"testsuite"`
}

func parseJUnit(b []byte) []string {
	var s junitSuite
	if err := xml.Unmarshal(b, &s); err != nil {
		return nil
	}
	var out []string
	var walk func(junitSuite)
	walk = func(x junitSuite) {
		for _, c := range x.Cases {
			if c.Failure != nil || c.Error != nil {
				out = append(out, c.ClassName+"."+c.Name)
			}
		}
		for _, sub := range x.Suites {
			walk(sub)
		}
	}
	walk(s)
	return out
}

// Summary holds pass/fail counts when the runner prints them.
type Summary struct {
	Failed, Passed int
	Found          bool
}

var (
	pySumRe   = lazyre.New(`(?:(\d+) failed)?(?:, )?(?:(\d+) passed)`)
	jestSumRe = lazyre.New(`Tests:\s+(?:(\d+) failed, )?(?:\d+ skipped, )?(\d+) passed`)
)

// ParseSummary extracts failed/passed counts (pytest, jest/vitest).
func ParseSummary(out string) Summary {
	out = ansiRe.ReplaceAllString(out, "")
	if m := jestSumRe.FindStringSubmatch(out); m != nil {
		return Summary{Failed: atoi(m[1]), Passed: atoi(m[2]), Found: true}
	}
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-15; i-- {
		if strings.Contains(lines[i], " passed") && strings.Contains(lines[i], "==") {
			if m := pySumRe.FindStringSubmatch(lines[i]); m != nil {
				return Summary{Failed: atoi(m[1]), Passed: atoi(m[2]), Found: true}
			}
		}
	}
	return Summary{}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
