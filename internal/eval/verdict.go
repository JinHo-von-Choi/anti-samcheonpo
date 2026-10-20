package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Rule verdicts.
//
// A verdict says what a candidate rule did to a real run. It is not a
// measure of the rule's quality in general, and it is never written
// without the evidence it rests on.
const (
	// VerdictHelped means the candidate arm did better than the base.
	VerdictHelped = "helped"
	// VerdictHurt means the candidate arm did worse.
	VerdictHurt = "hurt"
	// VerdictNoObservedEffect means the two arms did not differ.
	VerdictNoObservedEffect = "no observed effect"
	// VerdictConfounded means the two arms differed by more than the one
	// thing the comparison was meant to change, so the difference cannot
	// be read as the rule's effect.
	VerdictConfounded = "confounded"
	// VerdictNoEntry means there was nothing to compare. It is a
	// refusal, not a result.
	VerdictNoEntry = "NO_ENTRY"
)

// ProbeVerdict is what a candidate rule did to a live run.
//
// The evidence travels with the verdict: a reader must be able to check the
// counts, the trial set and the configuration digests that produced it.
type ProbeVerdict struct {
	Version     string `json:"version"`
	CandidateID string `json:"candidate_id"`
	Verdict     string `json:"verdict"`
	Reason      string `json:"reason"`
	// Veto is true when the candidate arm produced nothing where the base
	// arm produced something. A rule that stops the agent from delivering
	// anything is not a rule to adopt.
	Veto bool `json:"veto"`
	// BaseTrials and CandidateTrials count the paired trials.
	BaseTrials      int `json:"base_trials"`
	CandidateTrials int `json:"candidate_trials"`
	// BaseVerified and CandidateVerified count the trials the grader
	// accepted.
	BaseVerified      int `json:"base_verified"`
	CandidateVerified int `json:"candidate_verified"`
	// BaseArtifacts and CandidateArtifacts count the trials in which the
	// agent left something behind. A trial that changed nothing produced
	// nothing, whatever the grader said about it.
	BaseArtifacts      int `json:"base_artifacts"`
	CandidateArtifacts int `json:"candidate_artifacts"`
	// Tasks counts the independent task clusters the trials covered.
	Tasks int `json:"tasks"`
	// BaseDetectionTrials and CandidateDetectionTrials count the trials in
	// which the arm's monitor judged something at all. A trial with no
	// judgment has no detection position.
	BaseDetectionTrials      int `json:"base_detection_trials"`
	CandidateDetectionTrials int `json:"candidate_detection_trials"`
	// BaseDetectionPosition and CandidateDetectionPosition are the median
	// positions of the first judgment inside the trial trajectory. A lower
	// number means the monitor spoke earlier in the run.
	BaseDetectionPosition      float64 `json:"base_detection_position,omitempty"`
	CandidateDetectionPosition float64 `json:"candidate_detection_position,omitempty"`
	// BaseLastThird and CandidateLastThird count the trials whose first
	// judgment arrived in the last third of the trajectory, where the
	// monitor spoke after most of the cost was already spent.
	BaseLastThird      int `json:"base_last_third,omitempty"`
	CandidateLastThird int `json:"candidate_last_third,omitempty"`
	// BaseFingerprint and CandidateFingerprint are the monitor
	// configuration digests of the two arms.
	BaseFingerprint      string `json:"base_fingerprint,omitempty"`
	CandidateFingerprint string `json:"candidate_fingerprint,omitempty"`
	// Evidence is the digest of the rows the verdict rests on.
	Evidence string `json:"evidence_digest"`
}

// Digest returns the content address of the verdict.
//
// The digest covers the counts and the evidence, not the wording of the
// reason: the same facts worded differently are the same verdict.
func (v ProbeVerdict) Digest() string {
	rows := []any{v.Version, v.CandidateID, v.Verdict, v.Veto, v.BaseTrials, v.CandidateTrials,
		v.BaseVerified, v.CandidateVerified, v.Tasks, v.BaseDetectionTrials, v.CandidateDetectionTrials,
		v.BaseDetectionPosition, v.CandidateDetectionPosition, v.BaseLastThird, v.CandidateLastThird,
		v.BaseFingerprint, v.CandidateFingerprint, v.Evidence}
	b, _ := json.Marshal(rows)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Validate checks that the verdict says what it counted.
func (v ProbeVerdict) Validate() error {
	switch v.Verdict {
	case VerdictHelped, VerdictHurt, VerdictNoObservedEffect, VerdictConfounded, VerdictNoEntry:
	default:
		return fmt.Errorf("알 수 없는 판정 %q", v.Verdict)
	}
	if v.Verdict == VerdictNoEntry && v.Reason == "" {
		return errors.New("판정 거절에는 이유가 필요하다")
	}
	if v.Veto && v.Verdict != VerdictHurt {
		return errors.New("차단 거절은 hurt 판정과 함께만 기록된다")
	}
	return nil
}
