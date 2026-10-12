package eval

import (
	"reflect"
	"testing"
)

func TestCandidatesCannotLearnFromHoldoutOrActivatePolicy(t *testing.T) {
	s := Split{Version: "split/2", Purpose: "preregistered_holdout", Calibration: []string{"train"}, Holdout: []string{"test"}, Groups: map[string]SplitGroup{"train": {Repository: "r1", Family: "f1"}, "test": {Repository: "r2", Family: "f2"}}}
	ls := []Label{{Session: "train", Start: 1, End: 3, Symptom: "S2", Justified: "no"}, {Session: "test", Start: 2, End: 6, Symptom: "S3"}}
	a, err := BuildCandidates(ls, s)
	if err != nil {
		t.Fatal(err)
	}
	ls[1].Symptom = "S8"
	ls[1].Note = "holdout change"
	b, err := BuildCandidates(ls, s)
	if err != nil || !reflect.DeepEqual(a, b) || a.Applied || len(a.Candidates) != 1 || a.Candidates[0].Symptom != "S2" {
		t.Fatalf("%+v %+v %v", a, b, err)
	}
	s.Groups["test"] = SplitGroup{Repository: "r1", Family: "f2"}
	if _, err = BuildCandidates(ls, s); err == nil {
		t.Fatal("leaking split accepted")
	}
}
