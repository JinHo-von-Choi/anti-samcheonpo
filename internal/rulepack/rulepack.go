// Package rulepack describes fixed, advisory rules learned from labeled traces.
package rulepack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"io"
	"os"
	"regexp"
)

type Definition struct {
	ID       string         `json:"id"`
	Symptom  string         `json:"symptom"`
	Tool     string         `json:"tool"`
	Category event.Category `json:"category"`
	Minimum  int            `json:"minimum"`
	Window   int            `json:"window"`
}
type Pack struct {
	Version          string       `json:"version"`
	Definitions      []Definition `json:"definitions"`
	ProposalDigest   string       `json:"proposal_digest"`
	EvaluationDigest string       `json:"evaluation_digest"`
	Effectiveness    string       `json:"effectiveness"`
}

func Digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (d Definition) RuleID() string {
	return string(d.Symptom[0]+32) + d.Symptom[1:] + ".learned_repeat_" + d.ID[:12]
}

var idPattern = regexp.MustCompile(`^s[1-8]\.learned_repeat_[a-f0-9]{12}$`)

func IsLearned(id string) bool { return idPattern.MatchString(id) }
func (d Definition) Validate() error {
	if len(d.Symptom) != 2 || d.Symptom[0] != 'S' || d.Symptom[1] < '1' || d.Symptom[1] > '8' || d.Minimum < 3 || d.Minimum > 12 || d.Window < d.Minimum || d.Window > 64 {
		return errors.New("invalid candidate definition")
	}
	switch d.Tool {
	case event.ToolShell, event.ToolRead, event.ToolSearch, event.ToolWeb, event.ToolTask:
	default:
		return errors.New("unsupported candidate tool")
	}
	if d.Category != event.CatVerify && d.Category != event.CatExplore {
		return errors.New("candidate category must be observational")
	}
	copy := d
	copy.ID = ""
	if d.ID != Digest(copy) {
		return errors.New("candidate definition digest mismatch")
	}
	return nil
}
func Load(path string) (Pack, error) {
	var p Pack
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() > 1<<20 {
		return p, errors.New("invalid rule pack size")
	}
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err = dec.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return p, errors.New("trailing rule pack data")
	}
	if p.Version != "advisory-rulepack/1" || len(p.Definitions) > 32 || p.ProposalDigest == "" || p.EvaluationDigest == "" || p.Effectiveness != "unvalidated" {
		return p, errors.New("invalid advisory pack")
	}
	seen := map[string]bool{}
	for _, d := range p.Definitions {
		if err = d.Validate(); err != nil {
			return p, err
		}
		if seen[d.ID] {
			return p, errors.New("duplicate candidate")
		}
		seen[d.ID] = true
	}
	return p, nil
}

type Match struct {
	Definition Definition
	Evidence   []int64
	Target     string
}
type point struct {
	seq int64
	key string
}
type Matcher struct {
	defs   []Definition
	points map[string][]point
	scope  string
}

func New(defs []Definition) (*Matcher, error) {
	if len(defs) > 32 {
		return nil, errors.New("candidate cap exceeded")
	}
	m := &Matcher{points: map[string][]point{}}
	for _, d := range defs {
		if err := d.Validate(); err != nil {
			return nil, err
		}
		m.defs = append(m.defs, d)
	}
	return m, nil
}
func Signature(ev *event.Event) string {
	if ev == nil {
		return ""
	}
	if ev.Kind != event.KindTool || ev.Category == event.CatWatch || ev.Unknown || ev.Mutating && ev.Category != event.CatProduce || ev.Background || ev.IsError && ev.Category != event.CatVerify {
		return ""
	}
	if ev.Reliability != nil && (ev.Reliability.Flaky || !ev.Reliability.Complete || ev.Reliability.SourceMissing) {
		return ""
	}
	if ev.Category == event.CatVerify && (ev.ExitCode == nil || *ev.ExitCode < 0) {
		return ""
	}
	if ev.BehaviorTarget == "" || ev.BehaviorResult == "" {
		return ""
	}
	input, env := "", ""
	if ev.Reliability != nil {
		input = ev.Reliability.InputHash
		env = ev.Reliability.EnvironmentHash
	}
	selection := ev.BehaviorSelection
	if selection == "" && ev.Category == event.CatVerify {
		selection = ev.CmdFP
	}
	return fp.Hash(selection, ev.Tool, string(ev.Category), ev.BehaviorTarget, ev.BehaviorResult, ev.BehaviorContext, input, env)
}
func (m *Matcher) Observe(ev *event.Event, scope string) []Match {
	if m.scope != scope || ev.Kind == event.KindPrompt {
		m.points = map[string][]point{}
		m.scope = scope
	}
	if ev.Kind != event.KindTool || ev.Category == event.CatWatch {
		return nil
	}
	if ev.Category == event.CatProduce || ev.Unknown || ev.Mutating {
		m.points = map[string][]point{}
		return nil
	}
	if ev.Category == event.CatVerify && ev.ExitCode != nil && *ev.ExitCode == 0 && !ev.IsError && len(ev.FailedTests) == 0 && len(ev.ErrFPs) == 0 {
		m.points = map[string][]point{}
		return nil
	}
	signature := Signature(ev)
	if signature == "" {
		return nil
	}
	var out []Match
	for _, d := range m.defs {
		if d.Tool != ev.Tool || d.Category != ev.Category {
			continue
		}
		ps := append(m.points[d.ID], point{ev.Seq, signature})
		if len(ps) > d.Window {
			ps = ps[len(ps)-d.Window:]
		}
		m.points[d.ID] = ps
		var evidence []int64
		for _, p := range ps {
			if p.key == signature {
				evidence = append(evidence, p.seq)
			}
		}
		if len(evidence) >= d.Minimum {
			out = append(out, Match{Definition: d, Evidence: evidence, Target: ev.BehaviorTarget})
		}
	}
	return out
}
