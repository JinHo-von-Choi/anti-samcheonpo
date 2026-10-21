package bench

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"testing"
)

func TestProbeRejectsDuplicateCorrelatedAndContaminatedTrials(t *testing.T) {
	rows := probeRows(20, 2, "a", "b", false, true, true, true)
	if v := Probe(append(rows, rows[0]), "off", "on", "c"); v.Verdict != eval.VerdictConfounded {
		t.Fatal("duplicate accepted")
	}
	for i := range rows {
		rows[i].Group = "single-source"
	}
	if v := Probe(rows, "off", "on", "c"); v.Verdict != eval.VerdictNoEntry {
		t.Fatal("repeats counted as independent evidence")
	}
	rows = probeRows(20, 2, "a", "b", false, true, true, true)
	rows[0].Contamination = &Contamination{Verdict: ContaminationTouched}
	if v := Probe(rows, "off", "on", "c"); v.Verdict != eval.VerdictConfounded {
		t.Fatal("contaminated trial accepted")
	}
}

func TestProbeEvidenceIncludesCostTimingAndControls(t *testing.T) {
	for _, change := range []func(*TrialResult){
		func(r *TrialResult) { r.TotalKRW = 1 },
		func(r *TrialResult) { r.Group = "other" },
		func(r *TrialResult) { r.Contamination = &Contamination{Verdict: ContaminationClean} },
		func(r *TrialResult) { r.MonitorDetection = &ledger.DetectionTiming{Verdicts: 1, Position: .8} },
		func(r *TrialResult) { r.Conditions = &RunConditions{Sandbox: "workspace-write"} },
	} {
		rows := probeRows(20, 2, "a", "b", true, true, true, true)
		before := probeEvidence(rows, "off", "on")
		change(&rows[0])
		if before == probeEvidence(rows, "off", "on") {
			t.Fatal("evidence change omitted from digest")
		}
	}
}

func TestProbeWithholdsBenefitWithoutCostsAndControls(t *testing.T) {
	v := Probe(probeRows(20, 2, "a", "b", false, true, true, true), "off", "on", "c")
	if v.Verdict == eval.VerdictHelped || v.EvidenceSufficient || v.Outcome != eval.VerdictHelped {
		t.Fatalf("descriptive improvement became efficiency proof: %+v", v)
	}
}

func TestVerifiedNoChangeTaskIsNotArtifactVeto(t *testing.T) {
	rows := probeRows(20, 2, "a", "b", true, true, true, false)
	v := Probe(rows, "off", "on", "c")
	if v.CandidateVerified == 0 || v.Veto {
		t.Fatalf("verified no-change task incorrectly vetoed: %+v", v)
	}
}

func TestProbeUsesClusterCostIntervals(t *testing.T) {
	rows := clusterRows()
	conditions := &RunConditions{ReasoningEffort: "high", InstructionsSHA256: "instructions", SkillsSHA256: "skills", Sandbox: "workspace-write", Network: "off", MCPSHA256: "mcp", CachePolicy: "natural", TimeoutSec: 900}
	for i := range rows {
		rows[i].Conditions = conditions
		rows[i].ObservedModel = "model"
		rows[i].MonitorFingerprint = rows[i].Arm
		rows[i].ProducedArtifact = true
		rows[i].Contamination = &Contamination{Verdict: ContaminationClean}
	}
	v := Probe(rows, "off", "on", "c")
	if !v.EvidenceSufficient || v.CostReduction == nil || v.SuccessDifference == nil || v.Efficiency != "improved" || v.Verdict != eval.VerdictHelped {
		t.Fatalf("cluster inference missing: %+v", v)
	}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		perTask := *conditions
		perTask.TimeoutSec += len(rows[i].Task) * 100
		rows[i].Conditions = &perTask
	}
	if v := Probe(rows, "off", "on", "c"); !v.EvidenceSufficient {
		t.Fatal("declared per-task timeouts treated as confounding")
	}
	rows[0].Conditions.TimeoutSec++
	if v := Probe(rows, "off", "on", "c"); v.Verdict != eval.VerdictConfounded {
		t.Fatal("unmatched timeout accepted")
	}
	changed := *conditions
	changed.ReasoningEffort = "low"
	rows[0].Conditions = &changed
	if v := Probe(rows, "off", "on", "c"); v.Verdict != eval.VerdictConfounded {
		t.Fatal("uncontrolled reasoning effort accepted")
	}
}
