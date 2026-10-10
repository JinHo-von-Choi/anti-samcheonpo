package contract

import "testing"

func TestTemporalParsingAndAuthority(t *testing.T) {
	base := "goal: test\ndone: [{id: ready, check: 'true'}]\n"
	for _, tc := range []struct {
		yaml  string
		valid bool
	}{
		{"kind: completion", true}, {"kind: minimum_duration, seconds: 60", true}, {"kind: observe_until, check_id: ready", true}, {"kind: minimum_duration, seconds: 0", false}, {"kind: observe_until, check_id: missing", false}, {"kind: completion, seconds: 1", false}, {"kind: other", false},
	} {
		c, errs := Parse([]byte(base + "temporal_requirement: {" + tc.yaml + "}\n"))
		if (len(errs) == 0) != tc.valid {
			t.Errorf("%s: %v", tc.yaml, errs)
		}
		if tc.valid {
			orig, _ := Parse([]byte(base))
			if AuthorityDigest(c) == AuthorityDigest(orig) {
				t.Fatal("temporal field omitted from authority")
			}
		}
	}
	c, _ := Parse([]byte(base + "temporal_requirement: {kind: minimum_duration, seconds: 120}\nbudget: {minutes: 1}\n"))
	if !c.TemporalConflict(0) {
		t.Fatal("budget conflict absent")
	}
	c.Budget.Minutes = 0
	if !c.TemporalConflict(60) || c.TemporalConflict(200) {
		t.Fatal("ceiling comparison")
	}
}

func TestWaitingObligationMatchesDeclaredTemporalRequirement(t *testing.T) {
	for _, kind := range []string{"completion", "minimum_duration", "observe_until"} {
		c := &Contract{TemporalRequirement: &TemporalRequirement{Kind: kind}}
		for _, state := range []string{"none", "pending", "fulfilled", "unknown"} {
			want := (kind == "completion" && state == "none") || (kind != "completion" && state == "fulfilled")
			if c.WaitingObservationSatisfied(state) != want {
				t.Fatal(kind, state)
			}
		}
	}
	if (&Contract{}).WaitingObservationSatisfied("none") {
		t.Fatal("legacy contract implied completion")
	}
}
