package classify

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"strings"
)

// CheckSelection removes only known launchers and cosmetic output switches.
// Test selectors, unknown options, ordering and stop limits remain significant.
// This is an advisory hash, never execution identity or authority to skip.
func CheckSelection(cmd string) string {
	n, _ := fp.NormalizeCmd(cmd)
	n = bareProgram(n)
	for i := 0; i < 3; i++ {
		prefix := runnerPrefixRe.FindString(n)
		if prefix == "" {
			break
		}
		n = strings.TrimSpace(n[len(prefix):])
	}
	var fields []string
	for _, word := range strings.Fields(n) {
		switch word {
		case "-q", "-qq", "-v", "-vv", "-vvv", "--quiet", "--verbose":
			continue
		}
		fields = append(fields, word)
	}
	return fp.Hash(strings.Join(fields, " "))
}
