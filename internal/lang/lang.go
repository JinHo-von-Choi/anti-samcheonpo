// Package lang decides which language a session's messages are written in.
package lang

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
)

// Languages with a full set of messages.
const (
	Korean  = "ko"
	English = "en"
)

const (
	// A line counts as Korean when this percentage of its letters is Hangul,
	// so a Korean word quoted in an English sentence does not switch it.
	koreanLinePercent = 30
	minKoreanLetters  = 2
	// Replies such as "ok" or "yes" say nothing about the user's language.
	minEnglishLetters = 8
	maxScanBytes      = 64 << 10
)

// Fixed returns the language a setting names, or "" when the setting leaves
// the choice to the session.
func Fixed(setting string) string {
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case Korean:
		return Korean
	case English:
		return English
	}
	return ""
}

// Detect returns the language of a user's message, or "" when the message
// is too short to tell. One Korean line is enough, because a Korean request
// often comes with a pasted English log. Languages without messages of their
// own read as English. Slash commands and host tags are not the user's words
// and are skipped.
func Detect(text string) string {
	if len(text) > maxScanBytes {
		text = text[:maxScanBytes]
	}
	total := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "/") || strings.HasPrefix(line, "<") {
			continue
		}
		hangul, letters := 0, 0
		for _, r := range line {
			if unicode.IsLetter(r) {
				letters++
				if unicode.Is(unicode.Hangul, r) {
					hangul++
				}
			}
		}
		if hangul >= minKoreanLetters && hangul*100 >= letters*koreanLinePercent {
			return Korean
		}
		total += letters
	}
	if total >= minEnglishLetters {
		return English
	}
	return ""
}

// System returns the language of the user's locale settings.
func System() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(name); v != "" {
			if strings.HasPrefix(strings.ToLower(v), Korean) {
				return Korean
			}
			return English
		}
	}
	return English
}

//go:embed en.json
var catalogJSON []byte

var catalog = sync.OnceValue(func() map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal(catalogJSON, &out)
	return out
})

var current atomic.Value

func init() { current.Store(initial()) }

// initial is the language of a process before any session has spoken: the
// environment override, then the user's settings, then the locale. Test
// binaries keep the source language so that fixtures do not depend on the
// machine they run on.
func initial() string {
	base := filepath.Base(os.Args[0])
	if strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".test.exe") {
		return Korean
	}
	if l := Fixed(os.Getenv("SAMCHEONPO_LANGUAGE")); l != "" {
		return l
	}
	if l := configured(); l != "" {
		return l
	}
	return System()
}

// configured reads the language line of the user's settings. The settings
// package depends on this one, so the file is scanned rather than parsed.
func configured() string {
	home := os.Getenv("SAMCHEONPO_HOME")
	if home == "" {
		dir, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(dir, ".samcheonpo")
	}
	b, err := os.ReadFile(filepath.Join(home, "config.yml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "language:"); ok {
			return Fixed(strings.Trim(strings.TrimSpace(strings.SplitN(v, "#", 2)[0]), `"'`))
		}
	}
	return ""
}

// Current returns the language of text that belongs to no single session:
// command output, receipts and goal cards.
func Current() string { return current.Load().(string) }

// SetCurrent changes the language returned by Current.
func SetCurrent(l string) { current.Store(l) }

// T returns a Korean source string in the current language.
func T(ko string) string {
	if Current() != English {
		return ko
	}
	if s, ok := catalog()[ko]; ok {
		return s
	}
	return ko
}
