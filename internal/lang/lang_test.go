package lang

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := map[string]string{
		"이 에러 고쳐 줘": Korean,
		"이 에러 고쳐\npanic: runtime error at main.go:12\ngoroutine 1 [running]": Korean,
		"Please fix the failing test":          English,
		"translate '저장하기' into English please": English,
		"3":                      "",
		"ok":                     "",
		"/samcheonpo:keep wrong": "",
	}
	for text, want := range cases {
		if got := Detect(text); got != want {
			t.Errorf("Detect(%q) = %q, want %q", text, got, want)
		}
	}
}

// A translation with different format verbs prints %!d(MISSING) to the user.
func TestCatalogKeepsFormatVerbs(t *testing.T) {
	verb := regexp.MustCompile(`%(\[\d+\])?[-+#0]*\d*(\.\d+)?[a-zA-Z]`)
	signature := func(s string) string {
		var out []string
		position := 0
		for _, m := range verb.FindAllStringSubmatch(s, -1) {
			if m[1] != "" {
				position, _ = strconv.Atoi(strings.Trim(m[1], "[]"))
			} else {
				position++
			}
			out = append(out, fmt.Sprint(position, m[0][len(m[0])-1:]))
		}
		sort.Strings(out)
		return strings.Join(out, " ")
	}
	for ko, en := range catalog() {
		if signature(ko) != signature(en) {
			t.Errorf("%q and %q use different verbs", ko, en)
		}
	}
}
