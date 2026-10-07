package policy

import (
	"strings"
	"testing"
)

func TestRolloutDoesNotPromoteWithoutRuleEvidence(t *testing.T) {
	for _, rule := range []string{"s3.protected_path", "s8.budget"} {
		if RolloutRoute("shadow", rule, nil) != "block" {
			t.Fatal("explicit guardrail disabled")
		}
	}
	evidence := map[string]string{"tested": strings.Repeat("a", 64), "broken": "not-a-hash"}
	for _, tc := range []struct{ mode, rule, want string }{{"shadow", "tested", "observe"}, {"recommend", "tested", "advice"}, {"validated", "tested", "block"}, {"validated", "missing", "advice"}, {"validated", "broken", "advice"}, {"", "tested", "advice"}} {
		if got := RolloutRoute(tc.mode, tc.rule, evidence); got != tc.want {
			t.Fatalf("%+v got %s", tc, got)
		}
	}
}

func TestEscalateOnlyRepeatedAdviceWithinCap(t *testing.T) {
	for _, tc := range []struct {
		route          string
		advised, freed bool
		used, max      int
		want           string
	}{
		{"advice", false, false, 0, 3, "advice"},
		{"advice", true, false, 0, 3, "block"},
		{"advice", true, false, 3, 3, "advice"},
		{"advice", true, true, 0, 3, "advice"},
		{"advice", true, false, 0, 0, "advice"},
		{"observe", true, false, 0, 3, "observe"},
		{"block", false, false, 9, 3, "block"},
	} {
		if got := Escalate(tc.route, tc.advised, tc.freed, tc.used, tc.max); got != tc.want {
			t.Fatalf("%+v got %s", tc, got)
		}
	}
}

func TestEscalationKeyAndAuthority(t *testing.T) {
	base := EscalationKey(1, "s5.error_hiding", "iron_laws:IL-301", "a.py")
	for _, other := range []string{
		EscalationKey(2, "s5.error_hiding", "iron_laws:IL-301", "a.py"),
		EscalationKey(1, "s5.error_hiding", "iron_laws:IL-304", "a.py"),
		EscalationKey(1, "s5.error_hiding", "iron_laws:IL-301", "b.py"),
		EscalationKey(1, "s2.stuck_error", "iron_laws:IL-301", "a.py"),
	} {
		if other == base {
			t.Fatal("revision, pattern, target and rule each scope ignored advice")
		}
	}
	for _, tc := range []struct {
		rule, kind string
		contract   bool
		want       bool
	}{
		{"s1.identical_rerun", "", false, true},
		{"s2.stuck_error", "", false, true},
		{"s5.error_hiding", "or_true", false, true},
		{"s5.test_weakening", "literal_replaced", true, false},
		{"s3.out_of_scope", "", false, false},
		{"s3.out_of_scope", "", true, true},
		{"s1.verify_ratio", "", false, false},
		{"s3.drift", "", true, false},
		{"s5.answer_copy", "", true, false},
	} {
		if got := Escalable(tc.rule, tc.kind, tc.contract); got != tc.want {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
}
