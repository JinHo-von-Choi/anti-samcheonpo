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
	Passed                                    bool   `json:"passed"`
	Effectiveness                             string `json:"effectiveness"`
	Unit                                      string `json:"unit"`
}

func DefaultGate() Gate              { return Gate{.9, .05} }
func evalDigest(r Evaluation) string { r.Digest = ""; return rulepack.Digest(r) }

// Evaluate checks the frozen training inputs and independently labeled holdout.
// Sources sharing a repository OR family stay in one confidence-interval unit.
func Evaluate(p Proposal, split eval.Split, labels []eval.Label, data map[string][]*event.Event, gate Gate) (Evaluation, error) {
	r := Evaluation{Version: "rule-evaluation/1", ProposalDigest: p.Digest, SplitDigest: rulepack.Digest(split), Sources: map[string]string{}, Gate: gate, Effectiveness: "unvalidated", Unit: "independent_repository_or_family_groups"}
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
	r.Passed = split.Purpose == "preregistered_holdout"
	for _, d := range p.Definitions {
		score := Score{ID: d.ID}
		type group struct{ positive, negative, predicted, unknown bool }
		groups := map[string]*group{}
		for _, id := range split.Holdout {
			gid := find(id)
			g := groups[gid]
			if g == nil {
				g = &group{}
				groups[gid] = g
			}
			type interval struct{ start, end int64 }
			votes := map[interval]map[string]string{}
			for _, l := range heldLabels {
				if l.Session != id {
					continue
				}
				if l.Start < 0 || l.End < l.Start {
					return r, errors.New("invalid holdout interval")
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
				if len(byLabeler) < 2 {
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
		r.Passed = r.Passed && score.Passed
		r.Scores = append(r.Scores, score)
	}
	r.Digest = evalDigest(r)
	return r, nil
}
func Promote(p Proposal, r Evaluation) (rulepack.Pack, error) {
	pack := rulepack.Pack{Version: "advisory-rulepack/1", ProposalDigest: p.Digest, EvaluationDigest: r.Digest, Effectiveness: "unvalidated"}
	if err := p.Validate(); err != nil {
		return pack, err
	}
	if r.Version != "rule-evaluation/1" || r.Digest != evalDigest(r) || r.ProposalDigest != p.Digest || r.SplitDigest != p.SplitDigest || r.Gate != p.Gate || !r.Passed || len(r.Scores) != len(p.Definitions) {
		return pack, errors.New("independent candidate gates not passed")
	}
	for i, d := range p.Definitions {
		sc := r.Scores[i]
		if sc.TP < 0 || sc.FP < 0 || sc.FN < 0 || sc.TN < 0 || sc.Unknown != 0 || sc.Precision != eval.Wilson(sc.TP, sc.TP+sc.FP) || sc.FPR != eval.Wilson(sc.FP, sc.FP+sc.TN) || sc.Precision.Lo < p.Gate.PrecisionLower || sc.FPR.Hi > p.Gate.FPRUpper || r.Scores[i].ID != d.ID || !r.Scores[i].Passed {
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
