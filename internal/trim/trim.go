// Package trim checks that a pass meant only to remove text from a document
// added nothing: every content word of the result already appears in the
// original. Joining two sentences may change particles, endings and
// connectives, so those do not count as new.
package trim

import (
	"strings"
	"unicode"
)

// connectives may appear when two sentences are joined.
var connectives = map[string]bool{
	"그리고": true, "그래서": true, "그런데": true, "하지만": true, "그러나": true, "반면": true, "또한": true, "또": true, "및": true, "따라서": true, "즉": true,
	"and": true, "but": true, "so": true, "or": true, "the": true, "a": true, "an": true, "also": true, "then": true, "which": true, "that": true,
}

// endings are particles and verb endings stripped before comparing Korean
// words, longest first.
var endings = []string{
	"으로부터", "에서부터", "이라는", "에서는", "으로는", "이지만", "하지만", "합니다", "입니다", "습니다", "었는데", "았는데",
	"에서", "으로", "에게", "까지", "부터", "처럼", "보다", "이며", "이고", "지만", "는데", "으며", "면서", "니다", "어요", "아요", "해요",
	"을", "를", "이", "가", "은", "는", "에", "의", "와", "과", "도", "로", "만", "며", "고", "면", "다", "요", "서", "게", "지",
}

// NewWords returns the words of after whose stem appears nowhere in before,
// in the order they first appear.
func NewWords(before, after string) []string {
	known := map[string]bool{}
	var stems []string
	for _, w := range words(before) {
		s := stem(w)
		if !known[s] {
			known[s] = true
			stems = append(stems, s)
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, w := range words(after) {
		s := stem(w)
		if s == "" || known[s] || connectives[s] || seen[s] {
			continue
		}
		if !related(s, stems) {
			seen[s] = true
			out = append(out, w)
		}
	}
	return out
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.')
	})
}

func stem(w string) string {
	w = strings.Trim(w, "-.")
	if w == "" || isNumber(w) {
		return ""
	}
	if !hasHangul(w) {
		for _, suf := range []string{"ing", "ed", "es", "s"} {
			if len(w) > len(suf)+2 && strings.HasSuffix(w, suf) {
				return strings.TrimSuffix(w, suf)
			}
		}
		return w
	}
	for _, e := range endings {
		if r := []rune(w); len(r) > len([]rune(e)) && strings.HasSuffix(w, e) {
			return strings.TrimSuffix(w, e)
		}
	}
	return w
}

// related reports a stem that differs from a known one only in its last
// syllable's ending: 돌린(다) and 돌리(고) share every jamo but the final
// consonant.
// One-syllable stems and words in other scripts have to match exactly.
func related(s string, known []string) bool {
	if !hasHangul(s) {
		return false
	}
	js := jamo(s)
	for _, k := range known {
		jk := jamo(k)
		n := commonPrefix(js, jk)
		if short := min(len(js), len(jk)); short >= 4 && n >= short-1 {
			return true
		}
	}
	return false
}

func jamo(s string) []rune {
	var out []rune
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			i := r - 0xAC00
			out = append(out, 0x1100+i/588, 0x1161+(i%588)/28)
			if t := i % 28; t != 0 {
				out = append(out, 0x11A7+t)
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func commonPrefix(a, b []rune) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func hasHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}

func isNumber(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) && r != '.' {
			return false
		}
	}
	return true
}
