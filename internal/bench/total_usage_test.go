package bench

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"testing"
)

func TestCompletedTaskTokensAndTimeIncludeFailuresAndMonitor(t *testing.T) {
	monitor := event.Usage{In: 5, Out: 5}
	rows := []TrialResult{{Arm: "on", Verified: true, UsageKnown: true, Usage: event.Usage{In: 100, Out: 20}, MonitorUsage: &monitor, TotalDurationMS: 2000}, {Arm: "on", UsageKnown: true, Usage: event.Usage{In: 50, Out: 10}, MonitorUsage: &monitor, TotalDurationMS: 1000}}
	s := Summarize(rows, "on")[0]
	if s.TotalTokensPerVerified == nil || *s.TotalTokensPerVerified != 200 || s.TotalMSPerVerified == nil || *s.TotalMSPerVerified != 3000 {
		t.Fatalf("%+v", s)
	}
	rows[1].MonitorUsage = nil
	if Summarize(rows, "on")[0].TotalTokensPerVerified != nil {
		t.Fatal("missing monitoring tokens silently treated as zero")
	}
	rows[1].TotalDurationMS = 0
	if Summarize(rows, "on")[0].TotalMSPerVerified != nil {
		t.Fatal("legacy partial duration presented as total time")
	}
}
