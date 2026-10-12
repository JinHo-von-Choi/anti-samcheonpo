package bench

import "testing"

func TestConfigurableGatesRequireTotalCostEvidence(t *testing.T) {
	rows := clusterRows()
	a := SummarizeWithGates(rows, "off", .02, .15)[1]
	if a.NonInferior == nil || a.MeetsCostGoal == nil || !*a.NonInferior || !*a.MeetsCostGoal || a.TotalCostPerVerified == nil {
		t.Fatalf("%+v", a)
	}
	b := SummarizeWithGates(rows, "off", .02, .95)[1]
	if b.MeetsCostGoal == nil || *b.MeetsCostGoal {
		t.Fatal("stricter gate ignored")
	}
	rows[0].MonitorKRW = nil
	c := SummarizeWithGates(rows, "off", .02, .15)[1]
	if c.MeetsCostGoal != nil {
		t.Fatal("monitor cost missing but savings certified")
	}
}
