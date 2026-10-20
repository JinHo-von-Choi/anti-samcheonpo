package evasion

import (
	"testing"
)

// The corpus must measure something. A scenario the rule was expected to
// catch and did not is a blind spot, and the report has to name it.
func TestCorpusMeasuresBlindSpotsAndFalseAlarms(t *testing.T) {
	scenarios, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) < 6 {
		t.Fatalf("corpus = %d scenarios", len(scenarios))
	}
	r, err := Measure(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != len(scenarios) {
		t.Fatalf("report = %+v", r)
	}
	if r.FalseAlarms != 0 {
		t.Fatalf("the control scenarios fired: %+v", r)
	}
	if r.Missed != 0 {
		t.Fatalf("the rules missed: %+v", r)
	}
	if len(r.Rules) < 3 {
		t.Fatalf("rules = %+v", r.Rules)
	}
}

// A blind spot must be reported, not hidden. The report is the mirror of
// the false-positive corpus: what the rules do not catch is the finding.
func TestBlindSpotsAreNamed(t *testing.T) {
	scenarios, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	// A rule that cannot exist is never caught: it is a blind spot.
	scenarios = append(scenarios, Scenario{Name: "impossible", Rule: "s9.does_not_exist", Caught: true,
		Events: []Step{{Tool: "shell", Category: "verify", Cmd: "pytest -q"}}})
	r, err := Measure(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	if r.Missed != 1 {
		t.Fatalf("report = %+v", r)
	}
	found := false
	for _, spot := range r.BlindSpots {
		if spot == "impossible" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the blind spot is not named: %+v", r)
	}
}

// A scenario that fires a rule it was not expected to fire is a false
// alarm, and the mirror measurement has to count it too.
func TestFalseAlarmsAreCounted(t *testing.T) {
	scenarios, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	// The rule fires where it was not expected to: a false alarm.
	for i := range scenarios {
		if scenarios[i].Name == "error-hiding" {
			scenarios[i].Caught = false
		}
	}
	r, err := Measure(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	if r.FalseAlarms != 1 {
		t.Fatalf("report = %+v", r)
	}
}
