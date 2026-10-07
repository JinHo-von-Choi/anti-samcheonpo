package hookclient

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func canonicalAnchorInfo() ContractAnchorInfo {
	return ContractAnchorInfo{
		Goal:               "Fix session expiration issue",
		AllowPatterns:      []string{"src/auth/**"},
		ProtectPatterns:    []string{"migrations/**"},
		BudgetRemainingKRW: 4200,
		PassedChecks:       1,
		TotalChecks:        3,
	}
}

func TestAnchor_FormatSystemReminder(t *testing.T) {
	info := canonicalAnchorInfo()
	msg := FormatAnchorHeader(info)

	if !strings.Contains(msg, AnchorHeaderTag) {
		t.Fatalf("reminder must carry the anchor tag, got %q", msg)
	}
	if !strings.Contains(msg, "migrations/**") {
		t.Fatalf("reminder must protect migrations, got %q", msg)
	}
}

func TestAnchor_HeaderStaysWithinSizeBudget(t *testing.T) {
	msg := FormatAnchorHeader(canonicalAnchorInfo())

	if len(msg) > AnchorHeaderMaxBytes {
		t.Fatalf("canonical reminder is %d bytes, limit is %d: %q", len(msg), AnchorHeaderMaxBytes, msg)
	}
	for _, key := range []string{"goal=", "allow=", "protect=", "budget=", "done="} {
		if !strings.Contains(msg, key) {
			t.Fatalf("canonical reminder must keep %s, got %q", key, msg)
		}
	}
}

func TestAnchor_HeaderBoundedForLargeContract(t *testing.T) {
	patterns := func(prefix string, count int) []string {
		list := make([]string, 0, count)
		for i := 0; i < count; i++ {
			list = append(list, prefix+"/deeply/nested/path_"+string(rune('a'+i%26))+"**")
		}
		return list
	}
	info := ContractAnchorInfo{
		Goal:               strings.Repeat("keep the goal in view ", 20),
		AllowPatterns:      patterns("src/generated", 24),
		ProtectPatterns:    patterns("migrations", 24),
		BudgetRemainingKRW: math.MaxInt64,
		PassedChecks:       999,
		TotalChecks:        1000,
	}

	msg := FormatAnchorHeader(info)

	if len(msg) > AnchorHeaderMaxBytes {
		t.Fatalf("hostile reminder is %d bytes, limit is %d: %q", len(msg), AnchorHeaderMaxBytes, msg)
	}
	if !strings.HasPrefix(msg, AnchorHeaderTag) {
		t.Fatalf("hostile reminder must start with the anchor tag, got %q", msg)
	}
	if !strings.Contains(msg, "protect=") {
		t.Fatalf("protect patterns must survive size pressure, got %q", msg)
	}
	if strings.Contains(msg, "\n") {
		t.Fatalf("reminder must stay on one line, got %q", msg)
	}

	parsed, err := ParseAnchorHeader(msg)
	if err != nil {
		t.Fatalf("bounded reminder must stay parseable: %v", err)
	}
	if parsed.Goal == "" {
		t.Fatalf("bounded reminder must keep a goal, got %q", msg)
	}
}

func TestAnchor_ParseRoundTrip(t *testing.T) {
	info := canonicalAnchorInfo()

	parsed, err := ParseAnchorHeader(FormatAnchorHeader(info))
	if err != nil {
		t.Fatalf("canonical reminder must parse: %v", err)
	}
	if !reflect.DeepEqual(info, parsed) {
		t.Fatalf("round trip changed the anchor: want %+v, got %+v", info, parsed)
	}
}

func TestAnchor_ParseExtractsAnchorFromWrappedReminder(t *testing.T) {
	msg := "<system-reminder>\n" + FormatAnchorHeader(canonicalAnchorInfo()) + "\n</system-reminder>"

	parsed, err := ParseAnchorHeader(msg)
	if err != nil {
		t.Fatalf("wrapped reminder must parse: %v", err)
	}
	if parsed.Goal != "Fix session expiration issue" {
		t.Fatalf("wrapped reminder lost the goal, got %q", parsed.Goal)
	}
	if !reflect.DeepEqual(parsed.ProtectPatterns, []string{"migrations/**"}) {
		t.Fatalf("wrapped reminder lost protect patterns, got %+v", parsed.ProtectPatterns)
	}
	if parsed.BudgetRemainingKRW != 4200 || parsed.PassedChecks != 1 || parsed.TotalChecks != 3 {
		t.Fatalf("wrapped reminder lost counts, got %+v", parsed)
	}
}

func TestAnchor_ParseRejectsInvalidHeaders(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"empty input", ""},
		{"missing tag", "goal=\"Fix session expiration issue\" protect=\"migrations/**\""},
		{"tag without goal", AnchorHeaderTag},
		{"empty goal", AnchorHeaderTag + " goal=\"\""},
		{"malformed budget", AnchorHeaderTag + " goal=\"g\" budget=\"abc\""},
		{"passed above total", AnchorHeaderTag + " goal=\"g\" done=\"5/2\""},
		{"non numeric checks", AnchorHeaderTag + " goal=\"g\" done=\"x/2\""},
		{"unterminated quote", AnchorHeaderTag + " goal=\"unterminated"},
		{"missing value", AnchorHeaderTag + " goal="},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseAnchorHeader(tc.msg); err == nil {
				t.Fatalf("expected rejection for %q", tc.msg)
			}
		})
	}
}

func TestAnchor_FormatNormalizesCheckCounts(t *testing.T) {
	negative := FormatAnchorHeader(ContractAnchorInfo{Goal: "g", PassedChecks: -3, TotalChecks: -1})
	if !strings.Contains(negative, "done=\"0/0\"") {
		t.Fatalf("negative counts must clamp to zero, got %q", negative)
	}

	overstated := FormatAnchorHeader(ContractAnchorInfo{Goal: "g", PassedChecks: 5, TotalChecks: 2})
	if !strings.Contains(overstated, "done=\"2/2\"") {
		t.Fatalf("passed must never exceed total, got %q", overstated)
	}
}
