package bench

import (
	"fmt"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

// probeRows builds paired trial rows for the probe.
//
// The base arm and the candidate arm differ only in the monitor
// fingerprint, unless the test says otherwise.
func probeRows(tasks, trials int, baseFingerprint, armFingerprint string, baseVerified, armVerified, baseArtifacts, armArtifacts bool) []TrialResult {
	var rows []TrialResult
	for task := 0; task < tasks; task++ {
		for trial := 0; trial < trials; trial++ {
			key := fmt.Sprintf("%q/%q/%d", "run", fmt.Sprintf("task-%02d", task), trial)
			for _, arm := range []struct {
				name        string
				fingerprint string
				verified    bool
				artifact    bool
			}{
				{"off", baseFingerprint, baseVerified, baseArtifacts},
				{"on", armFingerprint, armVerified, armArtifacts},
			} {
				rows = append(rows, TrialResult{
					ProtocolVersion: "2", RunID: "run", Seed: 1,
					Task: fmt.Sprintf("task-%02d", task), Group: fmt.Sprintf("group-%02d", task), Trial: trial,
					TaskDigest: fmt.Sprintf("task-%02d", task), GraderDigest: "grader", IndependentGrader: true,
					Arm: arm.name, Agent: "claude", AgentVersion: "2.1.0", Model: "model",
					PriceVersion: "price", EvaluatorVersion: "eval", PolicyVersion: "policy",
					MonitorFingerprint: arm.fingerprint, Verified: arm.verified, ProducedArtifact: arm.artifact,
				})
			}
			_ = key
		}
	}
	return rows
}

// The probe reads the rule's effect only when the two arms differ in the
// monitor configuration and in nothing else.
func TestProbeRefusesWhenTheArmsDifferByMore(t *testing.T) {
	rows := probeRows(20, 2, "fp-a", "fp-b", true, true, true, true)
	v := Probe(rows, "off", "on", "candidate")
	if v.Verdict != eval.VerdictNoObservedEffect {
		t.Fatalf("verdict = %+v", v)
	}
	if v.Tasks != 20 {
		t.Fatalf("tasks = %d", v.Tasks)
	}
	// The same monitor configuration on both arms measures nothing.
	same := probeRows(20, 2, "fp-a", "fp-a", true, true, true, true)
	v = Probe(same, "off", "on", "candidate")
	if v.Verdict != eval.VerdictConfounded {
		t.Fatalf("verdict = %+v", v)
	}
	// A different agent cannot be read as the rule's effect.
	mixed := probeRows(20, 2, "fp-a", "fp-b", true, true, true, true)
	for i := range mixed {
		if mixed[i].Arm == "on" {
			mixed[i].Model = "another"
		}
	}
	v = Probe(mixed, "off", "on", "candidate")
	if v.Verdict != eval.VerdictConfounded {
		t.Fatalf("verdict = %+v", v)
	}
}

// A candidate rule that leaves the agent with nothing is not a rule to
// adopt. The grader may stay ambiguous about a task the agent never
// attempted, so the artifact's absence has to carry the veto on its own.
func TestProbeVetoesWhenTheCandidateArmProducesNothing(t *testing.T) {
	rows := probeRows(20, 2, "fp-a", "fp-b", true, false, true, false)
	v := Probe(rows, "off", "on", "candidate")
	if !v.Veto || v.Verdict != eval.VerdictHurt {
		t.Fatalf("verdict = %+v", v)
	}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	// A candidate that produced something but failed the grader is not a
	// veto: the grader saw it and refused it.
	rows = probeRows(20, 2, "fp-a", "fp-b", true, false, true, true)
	v = Probe(rows, "off", "on", "candidate")
	if v.Veto {
		t.Fatalf("verdict = %+v", v)
	}
	if v.Verdict != eval.VerdictHurt {
		t.Fatalf("verdict = %+v", v)
	}
}

// Too few paired trials is a refusal, not a result.
func TestProbeRefusesWithoutEnoughTrials(t *testing.T) {
	rows := probeRows(2, 2, "fp-a", "fp-b", true, true, true, true)
	v := Probe(rows, "off", "on", "candidate")
	if v.Verdict != eval.VerdictNoEntry || v.Reason == "" {
		t.Fatalf("verdict = %+v", v)
	}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
}

// The probe reports what it counted, and the digest ties the verdict to the
// rows it came from.
func TestProbeReportsItsEvidence(t *testing.T) {
	rows := probeRows(20, 2, "fp-a", "fp-b", true, true, true, true)
	v := Probe(rows, "off", "on", "candidate")
	if v.BaseTrials != 40 || v.CandidateTrials != 40 || v.BaseVerified != 40 || v.CandidateVerified != 40 {
		t.Fatalf("verdict = %+v", v)
	}
	if v.Evidence == "" || v.BaseFingerprint != "fp-a" || v.CandidateFingerprint != "fp-b" {
		t.Fatalf("verdict = %+v", v)
	}
	// The same rows give the same digest; a different arm does not.
	if Probe(rows, "off", "on", "candidate").Evidence != v.Evidence {
		t.Fatal("the evidence digest is not stable")
	}
	other := probeRows(20, 2, "fp-a", "fp-b", true, false, true, true)
	if Probe(other, "off", "on", "candidate").Evidence == v.Evidence {
		t.Fatal("the evidence digest does not cover the rows")
	}
}

// The monitor fingerprint covers the rule pack. A comparison that changes
// the pack changes the fingerprint, and nothing else does.
func TestMonitorFingerprintCoversTheRulePack(t *testing.T) {
	base := MonitorFingerprint("bin", "cfg", "pack-a")
	if base != MonitorFingerprint("bin", "cfg", "pack-a") {
		t.Fatal("the fingerprint is not stable")
	}
	if base == MonitorFingerprint("bin", "cfg", "pack-b") {
		t.Fatal("the fingerprint ignores the rule pack")
	}
	if base == MonitorFingerprint("bin", "other", "pack-a") {
		t.Fatal("the fingerprint ignores the configuration")
	}
	if base == MonitorFingerprint("other", "cfg", "pack-a") {
		t.Fatal("the fingerprint ignores the binary")
	}
}

// The probe says how early each arm's monitor spoke. A candidate rule that
// only turns up late is a rule that arrived after the money was spent.
func TestProbeReportsDetectionTiming(t *testing.T) {
	rows := probeRows(20, 2, "fp-a", "fp-b", true, true, true, true)
	for i := range rows {
		if rows[i].Arm == "off" {
			rows[i].MonitorDetection = &ledger.DetectionTiming{Verdicts: 1, Position: 0.8, InLastThird: true}
		} else {
			rows[i].MonitorDetection = &ledger.DetectionTiming{Verdicts: 1, Position: 0.2}
		}
	}
	v := Probe(rows, "off", "on", "candidate")
	if v.BaseDetectionTrials != 40 || v.CandidateDetectionTrials != 40 {
		t.Fatalf("detection trials %d/%d", v.BaseDetectionTrials, v.CandidateDetectionTrials)
	}
	if v.BaseDetectionPosition < 0.79 || v.BaseDetectionPosition > 0.81 {
		t.Fatalf("base position %v", v.BaseDetectionPosition)
	}
	if v.CandidateDetectionPosition < 0.19 || v.CandidateDetectionPosition > 0.21 {
		t.Fatalf("candidate position %v", v.CandidateDetectionPosition)
	}
	if v.BaseLastThird != 40 || v.CandidateLastThird != 0 {
		t.Fatalf("last third %d/%d", v.BaseLastThird, v.CandidateLastThird)
	}
	// A trial whose monitor never judged anything has no position to
	// report, and is not counted as a late judgment.
	for i := range rows {
		rows[i].MonitorDetection = nil
	}
	v = Probe(rows, "off", "on", "candidate")
	if v.BaseDetectionTrials != 0 || v.CandidateDetectionTrials != 0 {
		t.Fatalf("detection trials %d/%d", v.BaseDetectionTrials, v.CandidateDetectionTrials)
	}
	if v.BaseDetectionPosition != 0 || v.BaseLastThird != 0 {
		t.Fatalf("verdict = %+v", v)
	}
}
