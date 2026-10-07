package bench

import (
	"fmt"
	"math"
	"reflect"
	"testing"
)

func clusterRows() []TrialResult {
	var rows []TrialResult
	monitor := int64(3)
	for task := 0; task < 16; task++ {
		for trial := 0; trial < 4; trial++ {
			for _, arm := range []string{"off", "on"} {
				amount := int64(1000 + task*200)
				passed := (task+trial)%7 != 0
				if arm == "on" {
					amount = int64(float64(amount) * (0.55 + float64(task%4)*0.03))
					passed = passed || task%3 == 0
				}
				rows = append(rows, TrialResult{ProtocolVersion: "2", RunID: "run", Task: fmt.Sprint(task), Trial: trial, Arm: arm, TaskDigest: fmt.Sprint(task), GraderDigest: "grader", IndependentGrader: true, Agent: "claude", AgentVersion: "version", Model: "model", PolicyVersion: arm, PriceVersion: "price", EvaluatorVersion: "eval", Verified: passed, CostKnown: true, TotalKRW: amount, MonitorKRW: &monitor})
			}
		}
	}
	return rows
}

func TestClusterComparisonPairingReproducibilityAndCosts(t *testing.T) {
	rows := clusterRows()
	c := compareClusters(rows, "off", "on", 1)
	if c.Tasks != 16 || c.Blocks != 64 || c.SuccessDifference == nil || c.CostReduction == nil || c.CostReduction.Lo < .2 {
		t.Fatalf("%+v", c)
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	if got := compareClusters(rows, "off", "on", 1); !reflect.DeepEqual(c, got) {
		t.Fatal("input order changes seeded interval")
	}
	rows = rows[1:]
	if got := compareClusters(rows, "off", "on", 1); got.CostReduction != nil || got.Reason == "" {
		t.Fatal("dropped unmatched block")
	}
}

func TestClusterWithholdsUnknownDegenerateAndCorrelatedSamples(t *testing.T) {
	for _, kind := range []string{"monitor", "agent-cost", "versions", "one-task", "constant"} {
		t.Run(kind, func(t *testing.T) {
			rows := clusterRows()
			switch kind {
			case "monitor":
				rows[0].MonitorKRW = nil
			case "agent-cost":
				rows[0].CostKnown = false
			case "versions":
				rows[0].Model = "other"
			case "one-task":
				for i := range rows {
					rows[i].Task = "same"
					rows[i].TaskDigest = "same"
					rows[i].Trial = i / 2
				}
			case "constant":
				for i := range rows {
					rows[i].Verified = true
					rows[i].TotalKRW = 100
				}
			}
			got := compareClusters(rows, "off", "on", 1)
			if got.CostReduction != nil || got.Reason == "" {
				t.Fatalf("invalid inference: %+v", got)
			}
		})
	}
}

func TestPlanningTaskPairs(t *testing.T) {
	n, err := RequiredTaskPairs(.2, .05, .05, .8, 1)
	if err != nil || n != 126 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	adjusted, err := RequiredTaskPairs(.2, .05, .05, .8, 2)
	if err != nil || adjusted <= n {
		t.Fatal("multiple comparison adjustment missing")
	}
	for _, sd := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if _, err := RequiredTaskPairs(sd, .05, .05, .8, 1); err == nil {
			t.Fatal("invalid pilot accepted")
		}
	}
}

func TestClusterCountsSourceEventGroupsNotVariants(t *testing.T) {
	rows := clusterRows()
	for i := range rows {
		// sixteen task variants derived from eight source events
		rows[i].Group = fmt.Sprintf("event-%s", string(rune('a'+(len(rows[i].Task)+int(rows[i].Task[len(rows[i].Task)-1]))%8)))
	}
	c := compareClusters(rows, "off", "on", 1)
	if c.Tasks != 8 || c.CostReduction != nil || c.Reason == "" {
		t.Fatalf("variants of one source event are one cluster, and eight are too few: %+v", c)
	}
	rows[0].Group = "other"
	if got := compareClusters(rows, "off", "on", 1); got.Reason == "" || got.Tasks != 0 {
		t.Fatalf("a task whose group changes between trials is rejected: %+v", got)
	}
}
