package hookclient

import (
	"errors"
	"strconv"
	"strings"
)

// AnchorHeaderTag marks the contract reminder that rides on hook responses so
// it can be found again after the conversation is compacted away.
const AnchorHeaderTag = "[SAMCHEONPO ANCHOR]"

// AnchorHeaderMaxBytes bounds one reminder line, tag included. Segments are
// rendered in priority order and a segment that no longer fits is dropped
// rather than growing the line, so hostile contract text cannot inflate it.
const AnchorHeaderMaxBytes = 150

const (
	anchorGoalSegmentMax   = 72
	anchorListSegmentMax   = 46
	anchorBudgetSegmentMax = 32
	anchorDoneSegmentMax   = 50
	anchorPatternSeparator = ";"
)

// ContractAnchorInfo is the contract state repeated to the model on every
// guarded hook response. Check counts travel as counters, not as check lists,
// to keep the reminder small.
type ContractAnchorInfo struct {
	Goal               string
	AllowPatterns      []string
	ProtectPatterns    []string
	BudgetRemainingKRW int64
	PassedChecks       int
	TotalChecks        int
}

// FormatAnchorHeader renders the contract state as a single bounded line.
// An empty Goal yields a header that ParseAnchorHeader rejects.
func FormatAnchorHeader(info ContractAnchorInfo) string {
	var b strings.Builder
	b.WriteString(AnchorHeaderTag)
	appendSegment := func(seg string) {
		if b.Len()+len(seg) <= AnchorHeaderMaxBytes {
			b.WriteString(seg)
		}
	}

	appendSegment(anchorSegment("goal", info.Goal, anchorGoalSegmentMax))
	if len(info.ProtectPatterns) > 0 {
		appendSegment(anchorSegment("protect",
			strings.Join(info.ProtectPatterns, anchorPatternSeparator), anchorListSegmentMax))
	}
	if len(info.AllowPatterns) > 0 {
		appendSegment(anchorSegment("allow",
			strings.Join(info.AllowPatterns, anchorPatternSeparator), anchorListSegmentMax))
	}

	passed, total := anchorCheckCounts(info.PassedChecks, info.TotalChecks)
	appendSegment(anchorSegment("budget",
		strconv.FormatInt(info.BudgetRemainingKRW, 10), anchorBudgetSegmentMax))
	appendSegment(anchorSegment("done",
		strconv.Itoa(passed)+"/"+strconv.Itoa(total), anchorDoneSegmentMax))
	return b.String()
}

// ParseAnchorHeader recovers the contract state from a hook response, whether
// the reminder is bare or wrapped in surrounding text. Key/value pairs after
// the anchor tag end the reminder at the first token that is not an anchor
// key, so trailing wrapper text is ignored.
func ParseAnchorHeader(s string) (ContractAnchorInfo, error) {
	var info ContractAnchorInfo

	at := strings.Index(s, AnchorHeaderTag)
	if at < 0 {
		return info, errors.New("anchor header: tag missing")
	}
	rest := s[at+len(AnchorHeaderTag):]

	for {
		rest = strings.TrimLeft(rest, " \t\r\n")
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			break
		}
		key := rest[:eq]
		if !isAnchorKey(key) {
			break
		}
		value, remainder, err := anchorValue(rest[eq+1:])
		if err != nil {
			return info, err
		}
		rest = remainder

		switch key {
		case "goal":
			info.Goal = value
		case "allow":
			info.AllowPatterns = anchorPatterns(value)
		case "protect":
			info.ProtectPatterns = anchorPatterns(value)
		case "budget":
			budget, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return info, errors.New("anchor header: budget is not an integer")
			}
			info.BudgetRemainingKRW = budget
		case "done":
			if err := anchorDone(value, &info); err != nil {
				return info, err
			}
		}
	}

	if info.Goal == "" {
		return info, errors.New("anchor header: goal missing")
	}
	return info, nil
}

// anchorSegment renders " key=\"value\"" and shortens value until the segment
// fits max bytes, keeping the reminder bounded for unbounded contract text.
func anchorSegment(key, value string, max int) string {
	if len(value) > max {
		value = strings.ToValidUTF8(value[:max], "")
	}
	for {
		seg := " " + key + "=" + strconv.Quote(value)
		if len(seg) <= max || value == "" {
			return seg
		}
		value = strings.ToValidUTF8(value[:len(value)-1], "")
	}
}

// anchorCheckCounts clamps the counters so a reminder never reports more
// passed checks than total checks, and never reports a negative count.
func anchorCheckCounts(passed, total int) (int, int) {
	if passed < 0 {
		passed = 0
	}
	if total < 0 {
		total = 0
	}
	if passed > total {
		passed = total
	}
	return passed, total
}

// anchorValue reads one quoted or bare value and returns the remaining text.
func anchorValue(s string) (string, string, error) {
	if s == "" {
		return "", "", errors.New("anchor header: empty value")
	}
	if s[0] != '"' {
		if i := strings.IndexByte(s, ' '); i >= 0 {
			return s[:i], s[i:], nil
		}
		return s, "", nil
	}
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			value, err := strconv.Unquote(s[:i+1])
			if err != nil {
				return "", "", errors.New("anchor header: malformed quoted value")
			}
			return value, s[i+1:], nil
		}
	}
	return "", "", errors.New("anchor header: unterminated quoted value")
}

// anchorPatterns splits a joined pattern list into entries.
func anchorPatterns(value string) []string {
	if value == "" {
		return nil
	}
	var patterns []string
	for _, pattern := range strings.Split(value, anchorPatternSeparator) {
		if pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	return patterns
}

// anchorDone parses the "passed/total" value into the check counters.
func anchorDone(value string, info *ContractAnchorInfo) error {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return errors.New("anchor header: done must be passed/total")
	}
	passed, err := strconv.Atoi(parts[0])
	if err != nil || passed < 0 {
		return errors.New("anchor header: passed is not a count")
	}
	total, err := strconv.Atoi(parts[1])
	if err != nil || total < 0 {
		return errors.New("anchor header: total is not a count")
	}
	if passed > total {
		return errors.New("anchor header: passed exceeds total")
	}
	info.PassedChecks = passed
	info.TotalChecks = total
	return nil
}

// isAnchorKey reports whether key belongs to the anchor header format.
func isAnchorKey(key string) bool {
	switch key {
	case "goal", "allow", "protect", "budget", "done":
		return true
	}
	return false
}
