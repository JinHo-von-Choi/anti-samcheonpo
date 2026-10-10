package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"testing"
)

func TestIronLawsRequiresChangedLinesAndStableSnapshot(t *testing.T) {
	s, _ := reliabilitySession(t)
	findings := []IronLawsFinding{{RuleID: "IL-301", Line: 1, Confidence: "CONFIRMED"}}
	ev := &event.Event{Seq: 1}
	for _, tc := range []struct {
		name   string
		patch  []event.PatchFile
		stable bool
		want   bool
	}{
		{"unknown patch", nil, true, false},
		{"existing violation", []event.PatchFile{{Path: "io.py", Added: []string{"new code"}}}, true, false},
		{"moved existing violation", []event.PatchFile{{Path: "io.py", Added: []string{"SWALLOW"}, Removed: []string{"SWALLOW"}}}, true, false},
		{"new violation", []event.PatchFile{{Path: "io.py", Added: []string{"SWALLOW"}}}, true, true},
		{"changed during audit", []event.PatchFile{{Path: "io.py", Added: []string{"SWALLOW"}}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.ironSeen = nil
			ev.Patch = tc.patch
			got := s.ironLawsSignals(ev, "io.py", findings, []string{"SWALLOW"}, tc.stable)
			active := false
			for _, v := range got {
				active = active || v.Level > detect.L0
			}
			if active != tc.want {
				t.Fatalf("unexpected attribution: %+v", got)
			}
		})
	}
}

func TestIronLawsFindingKeySurvivesLineMovementAndRestart(t *testing.T) {
	s, _ := reliabilitySession(t)
	ev := &event.Event{Seq: 1, Patch: []event.PatchFile{{Path: "io.py", Added: []string{"SWALLOW"}}}}
	findings := []IronLawsFinding{{RuleID: "IL-301", Line: 1}}
	vs := s.ironLawsSignals(ev, "io.py", findings, []string{"SWALLOW"}, true)
	if len(vs) != 1 || vs[0].Level == detect.L0 {
		t.Fatal(vs)
	}
	s.ironSeen = nil
	s.restoreIronLaws(vs)
	findings[0].Line = 2
	ev.Seq = 2
	if got := s.ironLawsSignals(ev, "io.py", findings, []string{"new line", "SWALLOW"}, true); len(got) != 0 {
		t.Fatalf("same finding repeated after restart: %+v", got)
	}
}
