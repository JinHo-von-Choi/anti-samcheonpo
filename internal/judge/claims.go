package judge

import (
	"regexp"
	"strings"
)

var claimWords = regexp.MustCompile(`(?i)(완료|끝났|구현했|구현할|승인|확인해|계속할|진행할|done|complete|finished|permission|approve|shall I|should I|may I)`)

func BoundedText(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		r = r[:limit]
	}
	return string(r)
}

// ClaimSummary extracts bounded prose claims; code fences and implementation
// text are excluded. These remain self-reports, never completion evidence.
func ClaimSummary(text string) string {
	var lines []string
	fence := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fence = !fence
			continue
		}
		if fence || !claimWords.MatchString(line) {
			continue
		}
		lines = append(lines, BoundedText(line, 200))
		if len(lines) == 3 {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "agent claim (unverified): " + strings.Join(lines, " ")
}
