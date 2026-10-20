package learning

import (
	"errors"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"math"
	"sort"
)

type Gate struct {
	PrecisionLower float64 `json:"precision_lower_min"`
	FPRUpper       float64 `json:"false_positive_upper_max"`
}
type Score struct {
	ID             string `json:"candidate_id"`
	TP, FP, FN, TN int
	Unknown        int `json:"unknown_groups"`
	Precision, FPR eval.Interval
	Passed         bool `json:"passed"`
}
type Evaluation struct {
	Version                                   string `json:"version"`
	Digest                                    string `json:"digest"`
	ProposalDigest, SplitDigest, LabelsDigest string
	Sources                                   map[string]string
	Gate                                      Gate
	Scores                                    []Score
	// DerivedScores are the same counts computed against labels an
	// independent grader derived. They are a separate trust grade: they
	// never stand in for a person's judgement.
	DerivedScores []Score `json:"derived_scores,omitempty"`
	// Passed is the human-label gate. A candidate that passes only the
	// derived gate is recorded in EvidenceTier and never in Passed.
	Passed bool `json:"passed"`
	// DerivedPassed is the derived-label gate.
	DerivedPassed bool `json:"derived_passed"`
	// EvidenceTier says which grade of evidence carries the promotion.
	// It is empty when neither grade passed.
	EvidenceTier string `json:"evidence_tier,omitempty"`
	// Probe is what the candidate did to a live run. It is additional
	// evidence: it never replaces the holdout gate, and it can refuse a
	// promotion the holdout gate allowed.
	Probe         *eval.ProbeVerdict `json:"probe,omitempty"`
	Effectiveness string             `json:"effectiveness"`
	Unit          string             `json:"unit"`
}

func DefaultGate() Gate              { return Gate{.9, .05} }
func evalDigest(r Evaluation) string { r.Digest = ""; return rulepack.Digest(r) }

// Evaluate checks the frozen training inputs and independently labeled holdout.
// Sources sharing a repository OR family stay in one confidence-interval unit.
func Evaluate(p Proposal, split eval.Split, labels []eval.Label, data map[string][]*event.Event, gate Gate) (Evaluation, error) {
	return EvaluateWithProbe(p, split, labels, data, gate, nil)
}

// EvaluateWithProbe is the evaluation with the live evidence attached.
//
// The probe says what the candidate did to a real run. It is additional
// evidence: it never replaces the holdout gate, and it can refuse a
// promotion the holdout allowed.
func EvaluateWithProbe(p Proposal, split eval.Split, labels []eval.Label, data map[string][]*event.Event, gate Gate, probe *eval.ProbeVerdict) (Evaluation, error) {
	r := Evaluation{Version: "rule-evaluation/1", ProposalDigest: p.Digest, SplitDigest: rulepack.Digest(split), Sources: map[string]string{}, Gate: gate, Effectiveness: "unvalidated", Unit: "independent_repository_or_family_groups", Probe: probe}
	if err := p.Validate(); err != nil {
		return r, err
	}
	if err := split.Validate(); err != nil {
		return r, err
	}
	if p.SplitDigest != r.SplitDigest {
		return r, errors.New("split changed after proposal")
	}
	if math.IsNaN(gate.PrecisionLower) || math.IsNaN(gate.FPRUpper) || gate.PrecisionLower <= 0 || gate.PrecisionLower >= 1 || gate.FPRUpper <= 0 || gate.FPRUpper >= 1 {
		return r, errors.New("invalid learning gate")
	}
	if gate != p.Gate {
		return r, errors.New("evaluation gate changed after proposal")
	}
	rebuilt, err := ProposeWithGate(labels, split, data, p.Gate)
	if err != nil {
		return r, err
	}
	if rebuilt.Digest != p.Digest {
		return r, errors.New("training labels/sources changed after proposal")
	}
	hold := map[string]bool{}
	parent := map[string]string{}
	for _, id := range split.Holdout {
		hold[id] = true
		parent[id] = id
		if data[id] == nil {
			return r, errors.New("holdout source missing")
		}
		r.Sources[rulepack.Digest(id)] = SourceDigest(data[id])
	}
	var find func(string) string
	find = func(x string) string {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	for i, a := range split.Holdout {
		for _, b := range split.Holdout[i+1:] {
			ga, gb := split.Groups[a], split.Groups[b]
			if ga.Repository == gb.Repository || ga.Family == gb.Family {
				parent[find(b)] = find(a)
			}
		}
	}
	var heldLabels []eval.Label
	for _, l := range labels {
		if hold[l.Session] && l.Meta == nil {
			heldLabels = append(heldLabels, l)
		}
	}
	sort.Slice(heldLabels, func(i, j int) bool { return rulepack.Digest(heldLabels[i]) < rulepack.Digest(heldLabels[j]) })
	r.LabelsDigest = rulepack.Digest(heldLabels)
	// The two grades are kept apart from here on. A label a person made
	// and a label a grader derived answer the same question, but they do
	// not carry the same trust, so they are counted apart.
	var human, derived []eval.Label
	for _, l := range heldLabels {
		switch l.Tier() {
		case eval.LabelSourceHuman:
			human = append(human, l)
		case eval.LabelSourceDerived:
			derived = append(derived, l)
		}
	}
	r.Passed = split.Purpose == "preregistered_holdout"
	humanScores, err := scoreTier(p.Definitions, eval.LabelSourceHuman, split.Holdout, human, data, find, gate)
	if err != nil {
		return r, err
	}
	r.Scores = humanScores
	for _, sc := range r.Scores {
		r.Passed = r.Passed && sc.Passed
	}
	derivedScores, err := scoreTier(p.Definitions, eval.LabelSourceDerived, split.Holdout, derived, data, find, gate)
	if err != nil {
		return r, err
	}
	r.DerivedScores = derivedScores
	r.DerivedPassed = len(r.DerivedScores) == len(p.Definitions) && len(r.DerivedScores) > 0
	for _, sc := range r.DerivedScores {
		r.DerivedPassed = r.DerivedPassed && sc.Passed
	}
	switch {
	case r.Passed:
		r.EvidenceTier = rulepack.EvidenceHumanLabeled
	case r.DerivedPassed:
		r.EvidenceTier = rulepack.EvidenceDerivedExecution
	}
	if err := r.check(); err != nil {
		return r, err
	}
	r.Digest = evalDigest(r)
	return r, nil
}

// scoreTier counts one candidate's confusion against the holdout labels of
// one trust grade.
//
// The human grade needs agreement: one person may have misread a span, so a
// span counts only when at least two people agreed. The derived grade comes
// from an independent grader, and its independence is a property of where
// the grader lives rather than of how many agreed, so one derivation is
// enough. Two derivations that disagree still leave the span unknown.
//
// The counts are the same in both grades. What differs is the trust they
// carry, and that is recorded by the caller.
func scoreTier(defs []rulepack.Definition, tier string, holdout []string, held []eval.Label, data map[string][]*event.Event, find func(string) string, gate Gate) ([]Score, error) {
	// One person must not decide a span alone. A grader stands outside
	// the task, so its single verdict is not the same claim.
	minVotes := 2
	if tier == eval.LabelSourceDerived {
		minVotes = 1
	}
	var out []Score
	for _, d := range defs {
		score := Score{ID: d.ID}
		type group struct{ positive, negative, predicted, unknown bool }
		groups := map[string]*group{}
		for _, id := range holdout {
			gid := find(id)
			g := groups[gid]
			if g == nil {
				g = &group{}
				groups[gid] = g
			}
			type interval struct{ start, end int64 }
			votes := map[interval]map[string]string{}
			for _, l := range held {
				if l.Session != id {
					continue
				}
				if l.Start < 0 || l.End < l.Start {
					return nil, errors.New("invalid holdout interval")
				}
				vote := ""
				if l.Symptom == "none" {
					vote = "negative"
				} else if l.Symptom == d.Symptom && l.Justified == "yes" {
					vote = "positive"
				} else if l.Symptom == d.Symptom && l.Justified == "no" {
					vote = "negative"
				}
				if vote == "" || l.Labeler == "" {
					continue
				}
				key := interval{l.Start, l.End}
				if votes[key] == nil {
					votes[key] = map[string]string{}
				}
				if old := votes[key][l.Labeler]; old != "" && old != vote {
					g.unknown = true
				}
				votes[key][l.Labeler] = vote
			}
			if len(votes) == 0 {
				g.unknown = true
				continue
			}
			for span, byLabeler := range votes {
				if len(byLabeler) < minVotes {
					g.unknown = true
					continue
				}
				vote := ""
				for _, v := range byLabeler {
					if vote != "" && vote != v {
						g.unknown = true
					}
					vote = v
				}
				if vote == "positive" {
					g.positive = true
				} else {
					g.negative = true
				}
				matcher, _ := rulepack.New([]rulepack.Definition{d})
				matched := false
				startFound, endFound := false, false
				for _, e := range data[id] {
					if e.Seq < span.start || e.Seq > span.end {
						continue
					}
					startFound = startFound || e.Seq == span.start
					endFound = endFound || e.Seq == span.end
					scope := observationScope(id, e)
					if len(matcher.Observe(e, scope)) > 0 {
						matched = true
					}
				}
				if !startFound || !endFound {
					g.unknown = true
				}
				g.predicted = g.predicted || matched
			}
		}
		for _, g := range groups {
			switch {
			case g.unknown || g.positive && g.negative:
				score.Unknown++
			case g.positive && g.predicted:
				score.TP++
			case g.positive:
				score.FN++
			case g.negative && g.predicted:
				score.FP++
			case g.negative:
				score.TN++
			default:
				score.Unknown++
			}
		}
		score.Precision = eval.Wilson(score.TP, score.TP+score.FP)
		score.FPR = eval.Wilson(score.FP, score.FP+score.TN)
		score.Passed = score.Unknown == 0 && score.TP+score.FP > 0 && score.FP+score.TN > 0 && score.Precision.Lo >= gate.PrecisionLower && score.FPR.Hi <= gate.FPRUpper
		out = append(out, score)
	}
	return out, nil
}

// check verifies that the evaluation says what it counted.
func (r Evaluation) check() error {
	for _, sc := range append(append([]Score{}, r.Scores...), r.DerivedScores...) {
		if sc.TP < 0 || sc.FP < 0 || sc.FN < 0 || sc.TN < 0 || sc.Unknown < 0 {
			return errors.New("invalid candidate counts")
		}
		if sc.Precision != eval.Wilson(sc.TP, sc.TP+sc.FP) || sc.FPR != eval.Wilson(sc.FP, sc.FP+sc.TN) {
			return errors.New("candidate interval mismatch")
		}
	}
	if r.Passed && r.EvidenceTier != rulepack.EvidenceHumanLabeled {
		return errors.New("passed evaluation without the human evidence tier")
	}
	if r.DerivedPassed && !r.Passed && r.EvidenceTier != rulepack.EvidenceDerivedExecution {
		return errors.New("derived evaluation without the derived evidence tier")
	}
	if r.Probe != nil {
		if err := r.Probe.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func Promote(p Proposal, r Evaluation) (rulepack.Pack, error) {
	pack := rulepack.Pack{Version: "advisory-rulepack/1", ProposalDigest: p.Digest, EvaluationDigest: r.Digest, Effectiveness: "unvalidated"}
	if err := p.Validate(); err != nil {
		return pack, err
	}
	if r.Version != "rule-evaluation/1" || r.Digest != evalDigest(r) || r.ProposalDigest != p.Digest || r.SplitDigest != p.SplitDigest || r.Gate != p.Gate {
		return pack, errors.New("independent candidate gates not passed")
	}
	// The promotion runs on one grade of evidence. A person's labels and
	// a grader's verdicts are counted apart, and the pack says which one
	// carried it: a pack built on machine-derived evidence is a weaker
	// claim than one a person confirmed, and it must not read as the
	// stronger one.
	scores := r.Scores
	tier := rulepack.EvidenceHumanLabeled
	if !r.Passed {
		if !r.DerivedPassed {
			return pack, errors.New("independent candidate gates not passed")
		}
		scores = r.DerivedScores
		tier = rulepack.EvidenceDerivedExecution
	}
	if len(scores) != len(p.Definitions) {
		return pack, errors.New("independent candidate gates not passed")
	}
	// A live probe can refuse what the holdout allowed. A candidate that
	// leaves the agent with nothing is not a rule to adopt, and the
	// holdout alone may not see it.
	if r.Probe != nil {
		if err := r.Probe.Validate(); err != nil {
			return pack, err
		}
		if r.Probe.Veto {
			return pack, errors.New("생중 probe가 후보를 거부했다: 산출물 부재")
		}
		if r.Probe.Verdict == eval.VerdictHurt {
			return pack, errors.New("생중 probe에서 후보가 해를 끼쳤다")
		}
		if r.Probe.Verdict == eval.VerdictConfounded {
			return pack, errors.New("생중 probe 결과가 혼재되어 승격 근거가 아니다")
		}
	}
	pack.EvidenceTier = tier
	for i, d := range p.Definitions {
		sc := scores[i]
		if sc.TP < 0 || sc.FP < 0 || sc.FN < 0 || sc.TN < 0 || sc.Unknown != 0 || sc.Precision != eval.Wilson(sc.TP, sc.TP+sc.FP) || sc.FPR != eval.Wilson(sc.FP, sc.FP+sc.TN) || sc.Precision.Lo < p.Gate.PrecisionLower || sc.FPR.Hi > p.Gate.FPRUpper || scores[i].ID != d.ID || !scores[i].Passed {
			return pack, errors.New("candidate evaluation mismatch")
		}
		pack.Definitions = append(pack.Definitions, d)
	}
	return pack, nil
}

func observationScope(id string, e *event.Event) string {
	if e.Reliability != nil {
		return id + "/" + e.Reliability.TaskID + "/" + fmt.Sprint(e.Reliability.Revision)
	}
	return id
}

func (g Gate) Validate() error {
	if math.IsNaN(g.PrecisionLower) || math.IsNaN(g.FPRUpper) || g.PrecisionLower <= 0 || g.PrecisionLower >= 1 || g.FPRUpper <= 0 || g.FPRUpper >= 1 {
		return errors.New("invalid learning gate")
	}
	return nil
}
