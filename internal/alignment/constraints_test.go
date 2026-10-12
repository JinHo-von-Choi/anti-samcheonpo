package alignment

import "testing"

func TestRealIntegrationRequiresExplicitNonNegatedRequest(t *testing.T) {
	for _, tc := range []struct {
		goal string
		want bool
	}{
		{"실제 API 연동을 유지해. 목으로 대체하지 마.", true},
		{"Use the real API. Do not use mocks.", true},
		{"Never use mock responses; preserve real API integration.", true},
		{"Use mocks for this prototype.", false},
		{"실제 API 연동하지 마. 로컬 기능만 구현해.", false},
		{"Do not use a real API integration in this exercise.", false},
	} {
		t.Run(tc.goal, func(t *testing.T) {
			found := false
			for _, c := range Constraints(tc.goal) {
				found = found || c.Kind == "real_integration"
			}
			if found != tc.want {
				t.Fatalf("real constraint=%v want=%v", found, tc.want)
			}
		})
	}
}
