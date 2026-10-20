package rulepack

import (
	"os"
	"path/filepath"
	"testing"
)

// A missing pack file is a normal state, not a failure: a machine that has
// never learned a rule has no file. A present but broken file is a failure
// and must be distinguishable from absence.
func TestLoadWithStateSeparatesAbsentFromBroken(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "learned-rules.json")
	if got := LoadWithState(missing).State; got != LoadAbsent {
		t.Fatalf("missing pack state = %q, want %q", got, LoadAbsent)
	}

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := LoadWithState(broken).State; got != LoadUnreadable {
		t.Fatalf("unparsable pack state = %q, want %q", got, LoadUnreadable)
	}

	invalid := filepath.Join(dir, "invalid.json")
	raw := `{"version":"advisory-rulepack/1","definitions":[],"proposal_digest":"a","evaluation_digest":"b","effectiveness":"validated"}`
	if err := os.WriteFile(invalid, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if got := LoadWithState(invalid).State; got != LoadInvalid {
		t.Fatalf("invalid pack state = %q, want %q", got, LoadInvalid)
	}
}

// Load must keep its old contract: a missing file reports os.IsNotExist and
// a broken file reports a real error.
func TestLoadKeepsNotExistContract(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(filepath.Join(dir, "learned-rules.json"))
	if !os.IsNotExist(err) {
		t.Fatalf("missing pack error = %v, want a not-exist error", err)
	}
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(broken); err == nil || os.IsNotExist(err) {
		t.Fatalf("broken pack error = %v, want a real error", err)
	}
}
