package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

type LearningCandidate struct {
	Symptom        string   `json:"symptom"`
	Sessions       int      `json:"independent_sessions"`
	Occurrences    int      `json:"labeled_intervals"`
	Unjustified    int      `json:"unjustified_interventions"`
	Unresolved     int      `json:"unresolved_followups"`
	SessionHashes  []string `json:"session_hashes"`
	Recommendation string   `json:"recommendation"`
}
type CandidatePack struct {
	Version            string              `json:"version"`
	SplitSHA256        string              `json:"split_sha256"`
	LabelsSHA256       string              `json:"labels_sha256"`
	TrainingSessions   int                 `json:"training_sessions"`
	Applied            bool                `json:"applied"`
	Candidates         []LearningCandidate `json:"candidates"`
	RequiredValidation string              `json:"required_validation"`
}

func candidateDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// BuildCandidates uses calibration only. It never promotes policies or learns
// from holdout labels; isolated labels remain review requests, not rules.
func BuildCandidates(labels []Label, split Split) (CandidatePack, error) {
	p := CandidatePack{Version: "candidate-pack/1", Candidates: []LearningCandidate{}, RequiredValidation: "독립 holdout 재생·오탐·완료율·감시 포함 총비용 검증 후 별도 정책 검토"}
	if err := split.Validate(); err != nil {
		return p, err
	}
	if split.Version != "split/2" {
		return p, fmt.Errorf("출처 그룹을 격리한 split/2 필요")
	}
	raw, _ := json.Marshal(split)
	p.SplitSHA256 = candidateDigest(raw)
	train := map[string]bool{}
	for _, id := range split.Calibration {
		train[id] = true
	}
	groups := map[string]*LearningCandidate{}
	sources := map[string]map[string]bool{}
	var selected []Label
	for _, l := range labels {
		if !train[l.Session] || l.Meta != nil {
			continue
		}
		if l.End < l.Start || l.Start < 0 {
			return p, fmt.Errorf("invalid calibration label interval")
		}
		if l.Symptom == "none" {
			selected = append(selected, l)
			continue
		}
		if len(l.Symptom) != 2 || l.Symptom[0] != 'S' || l.Symptom[1] < '1' || l.Symptom[1] > '8' {
			return p, fmt.Errorf("unknown calibration symptom")
		}
		selected = append(selected, l)
		g := groups[l.Symptom]
		if g == nil {
			g = &LearningCandidate{Symptom: l.Symptom}
			groups[l.Symptom] = g
			sources[l.Symptom] = map[string]bool{}
		}
		g.Occurrences++
		if l.Justified == "no" {
			g.Unjustified++
		}
		if l.Outcome == "unresolved" {
			g.Unresolved++
		}
		sources[l.Symptom][l.Session] = true
	}
	sort.Slice(selected, func(i, j int) bool {
		a, _ := json.Marshal(selected[i])
		b, _ := json.Marshal(selected[j])
		return string(a) < string(b)
	})
	raw, _ = json.Marshal(selected)
	p.LabelsSHA256 = candidateDigest(raw)
	sessions := map[string]bool{}
	for _, l := range selected {
		sessions[l.Session] = true
	}
	p.TrainingSessions = len(sessions)
	for symptom, g := range groups {
		for id := range sources[symptom] {
			g.SessionHashes = append(g.SessionHashes, candidateDigest([]byte(id)))
		}
		sort.Strings(g.SessionHashes)
		g.Sessions = len(g.SessionHashes)
		if g.Sessions < 3 {
			g.Recommendation = "표본 추가; 규칙 변경 근거 부족"
		} else if g.Unjustified > 0 {
			g.Recommendation = "오탐 예외와 증거 적격성 검토"
		} else {
			g.Recommendation = "놓친 구간을 재생해 규칙 후보 설계; 효과 미검증"
		}
		p.Candidates = append(p.Candidates, *g)
	}
	sort.Slice(p.Candidates, func(i, j int) bool { return p.Candidates[i].Symptom < p.Candidates[j].Symptom })
	return p, nil
}
