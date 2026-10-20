package hostlimit

import "testing"

func TestWatchRatioAndRemaining(t *testing.T) {
	cases := []struct {
		name      string
		w         Watch
		ratio     float64
		remaining int
		saturated bool
	}{
		{"unknown budget is not a full one", Watch{}, 0, 0, false},
		{"headroom", Watch{Used: 10, Limit: 100, Known: true}, 0.1, 90, false},
		{"nearly full", Watch{Used: 85, Limit: 100, Known: true}, 0.85, 15, false},
		{"saturated", Watch{Used: 100, Limit: 100, Known: true}, 1, 0, true},
		{"over the limit", Watch{Used: 140, Limit: 100, Known: true}, 1.4, -40, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.w.Ratio(); got != c.ratio {
				t.Fatalf("ratio = %v, want %v", got, c.ratio)
			}
			if got := c.w.Remaining(); got != c.remaining {
				t.Fatalf("remaining = %d, want %d", got, c.remaining)
			}
			if got := c.w.Saturated(); got != c.saturated {
				t.Fatalf("saturated = %v, want %v", got, c.saturated)
			}
		})
	}
}

func TestUnknownLimitIsNeverSaturated(t *testing.T) {
	// A host that does not expose a limit must not be reported as full:
	// that would make every session claim it cannot observe anything.
	if (Watch{Used: 5000}).Saturated() {
		t.Fatal("unknown limit reported as saturated")
	}
	if (Watch{Used: 5000, Limit: 0, Known: true}).Saturated() {
		t.Fatal("zero limit reported as saturated")
	}
}
