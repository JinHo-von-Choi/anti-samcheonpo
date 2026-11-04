package verification

import (
	"encoding/json"
	"regexp"
	"strings"
)

var emptyTestOutput = regexp.MustCompile(`(?im)(?:^|\n).*\b(?:no tests ran|no tests found|collected 0 items|0 passing)\b`)

// EmptyTestRun recognizes explicit zero-test reports. Unknown counts remain
// unknown; packages with no tests do not invalidate other packages that ran.
func EmptyTestRun(output string) bool {
	if emptyTestOutput.MatchString(output) {
		return true
	}
	empty, ran := false, false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		var entry struct {
			Action string
			Test   string
			Output string
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Action != "" {
			if entry.Test != "" && (entry.Action == "run" || entry.Action == "pass") {
				ran = true
			}
			line = strings.TrimSpace(entry.Output)
		}
		if strings.Contains(line, "[no tests to run]") || strings.Contains(line, "[no test files]") {
			empty = true
			continue
		}
		if strings.HasPrefix(line, "ok ") || strings.HasPrefix(line, "ok\t") {
			ran = true
		}
	}
	return empty && !ran
}
