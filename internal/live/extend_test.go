package live

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestParseExtend(t *testing.T) {
	for arg, want := range map[string]struct {
		hours  float64
		tokens int64
	}{
		"2h":       {2, 0},
		"30m":      {0.5, 0},
		"90분":      {1.5, 0},
		"20M":      {0, 20_000_000},
		"500K 1h":  {1, 500_000},
		"1.5시간 1G": {1.5, 1_000_000_000},
	} {
		h, tok, err := parseExtend(arg)
		if err != nil || h != want.hours || tok != want.tokens {
			t.Errorf("parseExtend(%q) = %v %v %v, want %v %v", arg, h, tok, err, want.hours, want.tokens)
		}
	}
	for _, bad := range []string{"", "2", "two hours", "-1h", "20mb"} {
		if _, _, err := parseExtend(bad); err == nil {
			t.Errorf("parseExtend(%q) accepted", bad)
		}
	}
}

func TestAddedLinesFromPatch(t *testing.T) {
	ev := &event.Event{Patch: []event.PatchFile{{Path: "a.py", Added: []string{"  try:", "", "    pass  "}}}}
	got := addedLines(ev, "a.py")
	if !got["try:"] || !got["pass"] || got[""] {
		t.Fatalf("trimmed non-empty added lines: %v", got)
	}
	if addedLines(ev, "b.py") != nil {
		t.Fatal("a file without a patch has unknown added lines")
	}
}
