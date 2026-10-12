package cost

import (
	"math"
	"testing"
)

func TestPositiveRatioNeverDisplaysZero(t *testing.T) {
	for _, tc := range []struct {
		n, d int64
		want string
	}{{1, 1000000, "<0.1%"}, {4000, 1000000, "0.4%"}, {0, 1, "0%"}, {1, 0, "미확인"}, {1, 2, "50%"}} {
		if got := RatioText(tc.n, tc.d); got != tc.want {
			t.Fatalf("%d/%d: %s", tc.n, tc.d, got)
		}
	}
	if PercentageText(math.NaN()) != "미확인" {
		t.Fatal("invalid percentage")
	}
}
