package eval

import "testing"

func TestRestraintCases(t *testing.T) {
	cases, err := LoadRestraint("../../testdata/restraint/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if r := ScoreRestraint(c); len(r.Excess) != c.WantExcess {
			t.Errorf("%s: excess %q, want %d", c.Name, r.Excess, c.WantExcess)
		}
	}
	if r := ScoreRestraint(cases[len(cases)-1]); len(r.Missing) != 1 || r.Missing[0] != "호스트" {
		t.Errorf("a shorter answer that drops a required item is not better: %v", r.Missing)
	}
}
