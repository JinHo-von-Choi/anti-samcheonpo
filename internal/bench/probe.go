package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
)

// Probe compares a base arm with a candidate arm over paired trials.
//
// The comparison is the rule's effect only when the two arms differ in the
// monitor fingerprint and in nothing else. When they differ in more, the
// difference cannot be read as the rule's effect, and the verdict says so
// instead of guessing.
//
// A verdict is never written without the evidence it rests on. Too few
// paired trials is a refusal, not a result.
func Probe(rows []TrialResult, base, arm, candidateID string) eval.ProbeVerdict {
	v := eval.ProbeVerdict{Version: "rule-probe/1", CandidateID: candidateID, Verdict: eval.VerdictNoEntry}
	by := map[string]map[string]TrialResult{}
	conditions := map[string]string{}
	fingerprints := map[string]string{}
	for _, r := range rows {
		if r.Arm != base && r.Arm != arm {
			continue
		}
		if r.RunID == "" || r.Task == "" || r.TaskDigest == "" || r.GraderDigest == "" || !r.IndependentGrader || r.ProtocolVersion != "2" {
			v.Verdict = eval.VerdictConfounded
			v.Reason = "실험 식별자·독립 채점 근거 누락"
			return v
		}
		condition := r.Agent + "/" + r.AgentVersion + "/" + r.Model + "/" + r.ObservedModel + "/" + r.PriceVersion + "/" + r.EvaluatorVersion
		if old, ok := conditions[r.Arm]; ok && old != condition {
			v.Verdict = eval.VerdictConfounded
			v.Reason = "실행 방식 안에서 에이전트·모델·가격 조건이 바뀜"
			return v
		}
		conditions[r.Arm] = condition
		if r.MonitorFingerprint != "" {
			if old, ok := fingerprints[r.Arm]; ok && old != r.MonitorFingerprint {
				v.Verdict = eval.VerdictConfounded
				v.Reason = "실행 방식 안에서 모니터 구성이 바뀜"
				return v
			}
			fingerprints[r.Arm] = r.MonitorFingerprint
		}
		key := fmt.Sprintf("%q/%q/%d", r.RunID, r.Task, r.Trial)
		if by[key] == nil {
			by[key] = map[string]TrialResult{}
		}
		by[key][r.Arm] = r
	}
	if conditions[base] != conditions[arm] {
		v.Verdict = eval.VerdictConfounded
		v.Reason = "기준군과 후보군의 에이전트·모델이 달라 규칙의 효과를 분리할 수 없음"
		return v
	}
	if fingerprints[base] != "" && fingerprints[base] == fingerprints[arm] {
		v.Verdict = eval.VerdictConfounded
		v.Reason = "두 팔의 모니터 구성이 같아 규칙의 차이를 측정하지 못했다"
		return v
	}
	type pair = probePair
	var pairs []pair
	clusters := map[string]bool{}
	for _, block := range by {
		a, aok := block[base]
		b, bok := block[arm]
		if !aok || !bok {
			v.Verdict = eval.VerdictConfounded
			v.Reason = "짝이 없는 시행 블록; 누락 시행을 버리지 않음"
			return v
		}
		if a.Seed != b.Seed {
			v.Verdict = eval.VerdictConfounded
			v.Reason = "짝 시행의 seed 불일치"
			return v
		}
		if a.TaskDigest != b.TaskDigest || a.GraderDigest != b.GraderDigest {
			v.Verdict = eval.VerdictConfounded
			v.Reason = "짝 시행의 과제·채점기가 다르다"
			return v
		}
		pairs = append(pairs, pair{a, b})
		clusters[clusterOf(a)] = true
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].base.RunID != pairs[j].base.RunID {
			return pairs[i].base.RunID < pairs[j].base.RunID
		}
		if pairs[i].base.Task != pairs[j].base.Task {
			return pairs[i].base.Task < pairs[j].base.Task
		}
		return pairs[i].base.Trial < pairs[j].base.Trial
	})
	v.Tasks = len(clusters)
	if len(pairs) < MinTrials {
		v.Verdict = eval.VerdictNoEntry
		v.Reason = fmt.Sprintf("짝 시행 %d개로 판정 근거가 부족하다", len(pairs))
		return v
	}
	for _, p := range pairs {
		v.BaseTrials++
		v.CandidateTrials++
		if p.base.Verified {
			v.BaseVerified++
		}
		if p.other.Verified {
			v.CandidateVerified++
		}
		if p.base.ProducedArtifact {
			v.BaseArtifacts++
		}
		if p.other.ProducedArtifact {
			v.CandidateArtifacts++
		}
	}
	// How early the monitor spoke. A rule that only turns up in the last
	// third of the trajectory has already spent the money it was meant to
	// save, and the pass rate alone would not show that.
	v.BaseDetectionTrials, v.BaseDetectionPosition, v.BaseLastThird = detectionTiming(pairs, func(p pair) TrialResult { return p.base })
	v.CandidateDetectionTrials, v.CandidateDetectionPosition, v.CandidateLastThird = detectionTiming(pairs, func(p pair) TrialResult { return p.other })
	// The veto. A candidate rule that leaves the agent with nothing is
	// not a rule to adopt, and the grader alone may not see it: a task
	// the agent never attempted stays ambiguous.
	if v.BaseArtifacts > 0 && v.CandidateArtifacts == 0 {
		v.Veto = true
	}
	switch {
	case v.Veto:
		v.Verdict = eval.VerdictHurt
		v.Reason = "후보 팔에서 에이전트가 아무것도 내지 못했다"
	case v.CandidateVerified > v.BaseVerified:
		v.Verdict = eval.VerdictHelped
		v.Reason = "후보 팔의 채점기 통과가 늘었다"
	case v.CandidateVerified < v.BaseVerified:
		v.Verdict = eval.VerdictHurt
		v.Reason = "후보 팔의 채점기 통과가 줄었다"
	default:
		v.Verdict = eval.VerdictNoObservedEffect
		v.Reason = "두 팔의 채점기 통과가 같았다"
	}
	v.BaseFingerprint, v.CandidateFingerprint = fingerprints[base], fingerprints[arm]
	v.Evidence = probeEvidence(rows, base, arm)
	return v
}

// probePair is one matched pair of trials: the same run, task and seed
// under the base arm and under the candidate arm.
type probePair struct{ base, other TrialResult }

// detectionTiming summarizes how early one arm's monitor spoke.
//
// It returns the number of trials that produced a judgment at all, the
// median position of the first judgment inside the trial trajectory, and
// how many of those judgments arrived in the last third of the
// trajectory.
//
// A trial with no judgment contributes to the count of trials with a
// judgment not at all: an arm whose monitor never spoke has no position
// to report, and counting it as a late judgment would invent lateness.
func detectionTiming(pairs []probePair, pick func(probePair) TrialResult) (trials int, median float64, lastThird int) {
	var positions []float64
	for _, p := range pairs {
		d := pick(p).MonitorDetection
		if d == nil || d.Verdicts == 0 {
			continue
		}
		trials++
		if d.Position > 0 {
			positions = append(positions, d.Position)
		}
		if d.InLastThird {
			lastThird++
		}
	}
	if len(positions) == 0 {
		return trials, 0, lastThird
	}
	sort.Float64s(positions)
	m := len(positions)
	if m%2 == 1 {
		median = positions[m/2]
	} else {
		median = (positions[m/2-1] + positions[m/2]) / 2
	}
	return trials, median, lastThird
}

// probeEvidence returns the digest of the rows the verdict rests on.
//
// The digest covers the rows the verdict was made from, so a reader can
// check that the verdict and the evidence belong together.
func probeEvidence(rows []TrialResult, base, arm string) string {
	var picked []TrialResult
	for _, r := range rows {
		if r.Arm == base || r.Arm == arm {
			picked = append(picked, r)
		}
	}
	sort.Slice(picked, func(i, j int) bool {
		if picked[i].RunID != picked[j].RunID {
			return picked[i].RunID < picked[j].RunID
		}
		if picked[i].Task != picked[j].Task {
			return picked[i].Task < picked[j].Task
		}
		if picked[i].Arm != picked[j].Arm {
			return picked[i].Arm < picked[j].Arm
		}
		return picked[i].Trial < picked[j].Trial
	})
	rows2 := make([][]any, 0, len(picked))
	for _, r := range picked {
		rows2 = append(rows2, []any{r.RunID, r.Task, r.Arm, r.Trial, r.Seed, r.TaskDigest, r.GraderDigest,
			r.Verified, r.ProducedArtifact, r.MonitorFingerprint, r.Agent, r.AgentVersion, r.Model, r.PolicyVersion,
			r.PriceVersion, r.EvaluatorVersion, r.MonitorBinarySHA256, r.MonitorConfigSHA256})
	}
	b, _ := json.Marshal(rows2)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// positionText renders a detection position for a person.
//
// A position of zero means the arm produced no judgment at all, and the
// text says so rather than showing a number that reads like a perfect
// score.
func positionText(p float64) string {
	if p <= 0 {
		return "판정 없음"
	}
	return fmt.Sprintf("%.1f%%", 100*p)
}

// ProbeSummary renders a verdict for a person.
//
// The counts and the reason travel with the verdict: a verdict without them
// cannot be checked.
func ProbeSummary(v eval.ProbeVerdict) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s · %s", v.CandidateID, v.Verdict)
	if v.Reason != "" {
		fmt.Fprintf(&sb, " · %s", v.Reason)
	}
	fmt.Fprintf(&sb, "\n  짝 시행 %d · 과제군 %d · 채점기 통과 기준군 %d 후보군 %d · 산출물 기준군 %d 후보군 %d",
		v.BaseTrials, v.Tasks, v.BaseVerified, v.CandidateVerified, v.BaseArtifacts, v.CandidateArtifacts)
	if v.BaseDetectionTrials > 0 || v.CandidateDetectionTrials > 0 {
		fmt.Fprintf(&sb, "\n  최초 판정 위치 기준군 %s (판정 %d건 · 마지막 1/3 %d건) 후보군 %s (판정 %d건 · 마지막 1/3 %d건)",
			positionText(v.BaseDetectionPosition), v.BaseDetectionTrials, v.BaseLastThird,
			positionText(v.CandidateDetectionPosition), v.CandidateDetectionTrials, v.CandidateLastThird)
	}
	if v.Veto {
		sb.WriteString("\n  승격 거부: 후보 팔이 산출물을 내지 못했다")
	}
	return sb.String()
}
