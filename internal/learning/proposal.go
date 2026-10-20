package learning

import (
	"errors"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"sort"
)

type Proposal struct {
	Gate                 Gate                  `json:"gate"`
	Version              string                `json:"version"`
	Digest               string                `json:"digest"`
	SplitDigest          string                `json:"split_digest"`
	TrainingLabelsDigest string                `json:"training_labels_digest"`
	Sources              map[string]string     `json:"training_sources"`
	Definitions          []rulepack.Definition `json:"definitions"`
	// EvidenceTier says what kind of evidence the candidates were built
	// from. It is empty when the proposal has no training labels.
	EvidenceTier string `json:"evidence_tier,omitempty"`
}

// evidenceTier maps a label source to the pack tier it belongs to.
//
// A label a person made and a label a grader derived are both labels, but
// they are not the same grade of evidence, and a pack that held both could
// not say what it was holding.
func evidenceTier(source string) string {
	if source == eval.LabelSourceDerived {
		return rulepack.EvidenceDerivedExecution
	}
	return rulepack.EvidenceHumanLabeled
}

func proposalDigest(p Proposal) string { p.Digest = ""; return rulepack.Digest(p) }
func SourceDigest(events []*event.Event) string {
	var rows []any
	for _, e := range events {
		if e == nil {
			rows = append(rows, nil)
			continue
		}
		rows = append(rows, struct {
			Seq                                    int64
			Tool                                   string
			Kind                                   event.Kind
			Category                               event.Category
			CmdFP, Target, Result, Context         string
			Exit                                   *int
			Reliability                            *event.Reliability
			Selection                              string
			Unknown, Mutating, Background, IsError bool
			FailedTests, ErrFPs                    []string
		}{e.Seq, e.Tool, e.Kind, e.Category, e.CmdFP, e.BehaviorTarget, e.BehaviorResult, e.BehaviorContext, e.ExitCode, e.Reliability, e.BehaviorSelection, e.Unknown, e.Mutating, e.Background, e.IsError, e.FailedTests, e.ErrFPs})
	}
	return rulepack.Digest(rows)
}
func Propose(labels []eval.Label, split eval.Split, data map[string][]*event.Event) (Proposal, error) {
	return ProposeWithGate(labels, split, data, DefaultGate())
}
func ProposeWithGate(labels []eval.Label, split eval.Split, data map[string][]*event.Event, gate Gate) (Proposal, error) {
	p := Proposal{Gate: gate, Version: "rule-proposal/1", Sources: map[string]string{}, Definitions: []rulepack.Definition{}}
	if err := gate.Validate(); err != nil {
		return p, err
	}
	if err := split.Validate(); err != nil {
		return p, err
	}
	if split.Version != "split/2" {
		return p, errors.New("isolated split/2 required")
	}
	p.SplitDigest = rulepack.Digest(split)
	train := map[string]bool{}
	for _, id := range split.Calibration {
		train[id] = true
		if data[id] == nil {
			return p, errors.New("training source missing")
		}
		p.Sources[rulepack.Digest(id)] = SourceDigest(data[id])
	}
	var selected []eval.Label
	defs := map[string]rulepack.Definition{}
	for _, l := range labels {
		if !train[l.Session] || l.Meta != nil {
			continue
		}
		selected = append(selected, l)
		if l.Start < 0 || l.End < l.Start {
			return p, errors.New("invalid training interval")
		}
		if l.Symptom == "none" || l.Justified != "yes" {
			continue
		}
		matched := map[string][]*event.Event{}
		for _, e := range data[l.Session] {
			if e.Seq < l.Start || e.Seq > l.End {
				continue
			}
			sig := rulepack.Signature(e)
			if sig != "" {
				matched[sig] = append(matched[sig], e)
			}
		}
		for _, es := range matched {
			if len(es) < 3 {
				continue
			}
			d := rulepack.Definition{Symptom: l.Symptom, Tool: es[0].Tool, Category: es[0].Category, Minimum: min(12, len(es)), Window: min(64, max(len(es), int(l.End-l.Start+1)))}
			d.ID = rulepack.Digest(d)
			if err := d.Validate(); err != nil {
				return p, err
			}
			matcher, _ := rulepack.New([]rulepack.Definition{d})
			witnessed := false
			for _, e := range data[l.Session] {
				if e.Seq < l.Start || e.Seq > l.End {
					continue
				}
				if len(matcher.Observe(e, observationScope(l.Session, e))) > 0 {
					witnessed = true
				}
			}
			if witnessed {
				defs[d.ID] = d
			}
		}
	}
	sort.Slice(selected, func(i, j int) bool { return rulepack.Digest(selected[i]) < rulepack.Digest(selected[j]) })
	p.TrainingLabelsDigest = rulepack.Digest(selected)
	// The proposal is built from one grade of evidence. A rule learned
	// from a person's label and a rule derived from a grader's verdict
	// are not the same claim, and a proposal that held both could not say
	// what it was holding.
	tier := ""
	for _, l := range selected {
		if err := l.ValidateSource(); err != nil {
			return p, err
		}
		t := l.Tier()
		if tier == "" {
			tier = t
			continue
		}
		if t != tier {
			return p, errors.New("학습 구간의 라벨 출처가 섞여 있다")
		}
	}
	if tier != "" {
		p.EvidenceTier = evidenceTier(tier)
	}
	for _, d := range defs {
		p.Definitions = append(p.Definitions, d)
	}
	sort.Slice(p.Definitions, func(i, j int) bool { return p.Definitions[i].ID < p.Definitions[j].ID })
	if len(p.Definitions) > 32 {
		return p, errors.New("candidate cap exceeded")
	}
	p.Digest = proposalDigest(p)
	return p, nil
}
func (p Proposal) Validate() error {
	if p.Version != "rule-proposal/1" || p.Digest != proposalDigest(p) || len(p.Definitions) == 0 || len(p.Definitions) > 32 {
		return errors.New("invalid frozen proposal")
	}
	switch p.EvidenceTier {
	case "", rulepack.EvidenceHumanLabeled, rulepack.EvidenceDerivedExecution:
	default:
		return errors.New("invalid proposal evidence tier")
	}
	if err := p.Gate.Validate(); err != nil {
		return err
	}
	for _, d := range p.Definitions {
		if err := d.Validate(); err != nil {
			return err
		}
	}
	return nil
}
