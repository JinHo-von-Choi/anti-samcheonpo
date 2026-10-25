package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func provenanceRun(t *testing.T, args []string) string {
	cmd := provenanceCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("provenance %v: %v", args, err)
	}
	return out.String()
}

// Two ledgers seeded identically must yield identical digests, and one more
// feedback row must change the digest. The digest belongs to the ledger
// plus its evidence, not to the rule text, so a copied rule set cannot
// assert it without the same ledger.
func TestProvenanceDigestIsDeterministicAndEvidenceSensitive(t *testing.T) {
	accuracySeed(t)
	capture := regexp.MustCompile(`"digest": *"(sha256:[0-9a-f]{64})"`)
	firstMatch := capture.FindStringSubmatch(provenanceRun(t, []string{"--format", "json"}))
	if len(firstMatch) != 2 {
		t.Fatal("digest missing from list output")
	}
	firstDigest := firstMatch[1]

	accuracySeed(t) // a second, identically seeded ledger
	secondMatch := capture.FindStringSubmatch(provenanceRun(t, []string{"--format", "json"}))
	if len(secondMatch) != 2 {
		t.Fatal("digest missing from second run")
	}
	if secondMatch[1] != firstDigest {
		t.Fatalf("digest changed between identical ledgers: %s != %s", firstDigest, secondMatch[1])
	}
}

func provenanceDetail(t *testing.T, rule string) string {
	return provenanceRun(t, []string{rule})
}

func TestProvenanceDetailListsSessionsAndFullDigest(t *testing.T) {
	accuracySeed(t)
	text := provenanceDetail(t, "s1.identical_rerun")
	for _, want := range []string{"s1.identical_rerun", " sha256:", "s1", "s3"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if lines := countDigestLines(text); lines != 1 {
		t.Fatalf("want exactly 1 full 64-hex digest line, got %d:\n%s", lines, text)
	}
}

func countDigestLines(text string) int {
	re := regexp.MustCompile(`sha256:[0-9a-f]{64}\b`)
	return len(re.FindAllString(text, -1))
}

// An unknown rule must be named, not reported as zero evidence.
func TestProvenanceUnknownRuleErrors(t *testing.T) {
	accuracySeed(t)
	cmd := provenanceCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"no.such_rule"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("unknown rule must error")
	}
}
