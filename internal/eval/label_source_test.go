package eval

import (
	"os"
	"path/filepath"
	"testing"
)

// A label says where it came from, and the source decides the grade it
// counts in.
func TestLabelSourceDecidesTheGrade(t *testing.T) {
	human := Label{Session: "s", Start: 0, End: 1, Symptom: "S2"}
	if human.Tier() != LabelSourceHuman {
		t.Fatalf("default tier = %q", human.Tier())
	}
	if err := human.ValidateSource(); err != nil {
		t.Fatal(err)
	}
	derived := Label{Session: "s", Start: 0, End: 1, Symptom: "S2", Source: LabelSourceDerived,
		Derivation: &Derivation{Method: "execution_oracle", Oracle: "grader"}}
	if derived.Tier() != LabelSourceDerived {
		t.Fatalf("derived tier = %q", derived.Tier())
	}
	if err := derived.ValidateSource(); err != nil {
		t.Fatal(err)
	}
	unknown := Label{Session: "s", Source: "guess"}
	if err := unknown.ValidateSource(); err == nil {
		t.Fatal("unknown label source accepted")
	}
}

// A machine label without a derivation is an anonymous claim, and an
// anonymous claim is not evidence.
func TestDerivedLabelNeedsItsDerivation(t *testing.T) {
	anonymous := Label{Session: "s", Source: LabelSourceDerived}
	if err := anonymous.ValidateSource(); err == nil {
		t.Fatal("derived label without a derivation accepted")
	}
	// A derivation on a human label would claim a machine judged it.
	claimed := Label{Session: "s", Source: LabelSourceHuman, Derivation: &Derivation{Method: "execution_oracle"}}
	if err := claimed.ValidateSource(); err == nil {
		t.Fatal("human label carrying a derivation accepted")
	}
}

// The labels a machine derived are read from the same place as the labels
// a person made, and the reader refuses a derived label that does not say
// how it was derived.
func TestLoadLabelsChecksTheSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "grader"), 0o755); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "grader", "s.jsonl")
	if err := os.WriteFile(good, []byte(`{"session":"s","start":1,"end":3,"symptom":"S2","source":"derived","derivation":{"method":"execution_oracle","oracle":"grader"}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ls, err := LoadLabels(dir)
	if err != nil || len(ls) != 1 {
		t.Fatalf("labels %+v %v", ls, err)
	}
	if ls[0].Tier() != LabelSourceDerived || ls[0].Labeler != "grader" {
		t.Fatalf("label %+v", ls[0])
	}
	bad := filepath.Join(dir, "grader", "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"session":"s","start":1,"end":3,"symptom":"S2","source":"derived"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLabels(dir); err == nil {
		t.Fatal("derived label without a derivation accepted")
	}
}
