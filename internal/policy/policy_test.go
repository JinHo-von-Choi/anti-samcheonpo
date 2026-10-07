package policy

import "testing"

func TestCapabilityDowngradeNeverClaimsBlock(t *testing.T) {
	for _, tc := range []struct {
		caps  Capabilities
		route string
		block bool
	}{
		{Capabilities{}, "observe", false},
		{Capabilities{Inject: true}, "advice", false},
		{Capabilities{UserMessage: true}, "user", false},
		{Capabilities{Block: true}, "block", true},
	} {
		d := Decide("block", tc.caps)
		if d.Route != tc.route || d.CanBlock != tc.block {
			t.Fatalf("%+v", d)
		}
	}
}
