package main

import (
	"reflect"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	for in, want := range map[string][]string{
		"samcheonpo spec run":                           {"samcheonpo", "spec", "run"},
		`"C:\Program Files\sc\samcheonpo.exe" spec run`: {`C:\Program Files\sc\samcheonpo.exe`, "spec", "run"},
		"'/tmp/a b/samcheonpo' spec task":               {"/tmp/a b/samcheonpo", "spec", "task"},
		"  a   b ":                                      {"a", "b"},
		`a "" b`:                                        {"a", "", "b"},
		"":                                              nil,
	} {
		if got := splitCommand(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitCommand(%q) = %q, want %q", in, got, want)
		}
	}
}
