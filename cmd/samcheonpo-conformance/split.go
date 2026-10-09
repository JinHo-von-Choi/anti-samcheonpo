package main

import "strings"

// splitCommand splits an implementation command line at blanks outside single
// or double quotes and removes the quotes, so a program path that contains a
// space (C:\Program Files\...) can be given as one word. Backslashes are kept
// literally, as Windows paths need.
func splitCommand(s string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	started := false
	flush := func() {
		if started {
			out = append(out, b.String())
			b.Reset()
			started = false
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, started = r, true
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			b.WriteRune(r)
			started = true
		}
	}
	flush()
	return out
}
