package receipt

import (
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
)

// A receipt must say what the observation could not cover. It never changes
// a number; it says which numbers cannot be read as complete.
func TestReceiptReportsObservationLimits(t *testing.T) {
	cases := []struct {
		name   string
		obs    analyze.Observation
		word   string
		absent bool
	}{
		{"lost watch", analyze.Observation{WatchState: "unavailable"}, "파일 감시를 잃어", false},
		{"partial watch", analyze.Observation{WatchState: "degraded"}, "일부 디렉터리만 감시해", false},
		{"unreadable learned rules", analyze.Observation{CandidatePack: "unreadable"}, "학습 규칙 파일을 읽지 못해", false},
		{"invalid learned rules", analyze.Observation{CandidatePack: "invalid"}, "학습 규칙 파일 형식이 올바르지 않아", false},
		{"healthy observation", analyze.Observation{WatchState: "ok", CandidatePack: "absent"}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Build(Input{Title: "t", Totals: totals(), Grade: analyze.GradeVerified, Observation: c.obs})
			if c.absent {
				for _, notice := range r.Notices {
					if strings.Contains(notice, "감시") || strings.Contains(notice, "학습 규칙 파일") {
						t.Fatalf("healthy observation produced a limit notice: %q", notice)
					}
				}
				return
			}
			found := false
			for _, notice := range r.Notices {
				if strings.Contains(notice, c.word) {
					found = true
				}
			}
			if !found {
				t.Fatalf("receipt did not report %q; notices: %v", c.word, r.Notices)
			}
		})
	}
}
