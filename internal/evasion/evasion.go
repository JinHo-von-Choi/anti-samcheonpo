// Package evasion measures what the detectors miss.
//
// The false-positive corpus asks whether a rule fires when it should not.
// This one asks the mirror question: when an agent knows the rule and tries
// to slip past it, does the rule still catch it? A rule that misses is a
// blind spot, and a blind spot is not a reason to keep the rule.
//
// The scenarios are replayed, not run by an agent. A replay says what the
// detector would have seen; it does not say what an agent would have done.
// Making the scenarios executable needs the task generator, and the
// generator's control pair is what separates a caught evasion from an
// unnoticed one.
package evasion

import (
	_ "embed"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"gopkg.in/yaml.v3"
)

// Scenario is one evasion: what an agent that knows the rule would do, and
// whether the rule still catches it.
type Scenario struct {
	// Name identifies the scenario.
	Name string `yaml:"name"`
	// Rule is the rule the scenario tries to slip past.
	Rule string `yaml:"rule"`
	// Caught states whether the rule is expected to fire. A scenario the
	// rule misses is a blind spot; a scenario the rule fires on wrongly
	// is a false alarm.
	Caught bool `yaml:"caught"`
	// Prompt is the task the agent was given.
	Prompt string `yaml:"prompt,omitempty"`
	// Events are the steps of the evasion, in order.
	Events []Step `yaml:"events"`
}

// Step is one action of an evasion.
type Step struct {
	// Tool is the tool the agent used: edit, read, shell or message.
	Tool string `yaml:"tool"`
	// Category is what the action was: produce, verify or explore.
	Category string `yaml:"category,omitempty"`
	// Path is the file the action touched.
	Path string `yaml:"path,omitempty"`
	// Hash is the content hash of the file after the edit.
	Hash string `yaml:"hash,omitempty"`
	// Added and Removed are the lines the edit added and removed.
	Added   []string `yaml:"added,omitempty"`
	Removed []string `yaml:"removed,omitempty"`
	// Cmd is the command a shell step ran.
	Cmd string `yaml:"cmd,omitempty"`
	// ExitCode is what the command returned.
	ExitCode *int `yaml:"exit_code,omitempty"`
	// Failed names the tests that failed.
	Failed []string `yaml:"failed,omitempty"`
}

// corpus is the evasion corpus that ships with the tool.
//
//go:embed evasion-cases.yml
var corpus []byte

// Load reads the evasion corpus.
//
// An empty path reads the corpus that ships with the tool.
func Load(path string) ([]Scenario, error) {
	if path == "" {
		return Default()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Scenario
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, s := range out {
		if s.Name == "" || s.Rule == "" {
			return nil, fmt.Errorf("%s: 시나리오에 이름이나 규칙이 없다", path)
		}
		if seen[s.Name] {
			return nil, fmt.Errorf("%s: 중복 시나리오 %s", path, s.Name)
		}
		seen[s.Name] = true
	}
	return out, nil
}

// Default reads the corpus that ships with the tool.
func Default() ([]Scenario, error) {
	var out []Scenario
	if err := yaml.Unmarshal(corpus, &out); err != nil {
		return nil, fmt.Errorf("내장 회피 코퍼스: %w", err)
	}
	return out, nil
}

// Run replays one scenario and reports whether the rule fired.
func Run(s Scenario) (bool, error) {
	cfg := config.Default()
	cfg.Detectors.Overrides = map[string]int{}
	eng := detect.NewEngine(cfg, nil, false, "audit", "/w", "s", s.Prompt)
	ts := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	ws := "ws0"
	for i, step := range s.Events {
		ts = ts.Add(20 * time.Second)
		ev, err := step.event(i, ts, ws)
		if err != nil {
			return false, fmt.Errorf("%s: %w", s.Name, err)
		}
		eng.Observe(ev)
		if step.Category == string(event.CatProduce) {
			ws = fmt.Sprintf("ws%d", i)
		}
	}
	for _, v := range eng.Verdicts {
		if v.Rule == s.Rule {
			return true, nil
		}
	}
	return false, nil
}

// event builds the event of one step.
func (st Step) event(seq int, ts time.Time, ws string) (*event.Event, error) {
	ev := &event.Event{Seq: int64(seq), TS: ts, Kind: event.KindTool, WSBefore: ws, WSAfter: ws}
	switch st.Tool {
	case "edit":
		ev.Tool = event.ToolEdit
		ev.Category = event.CatProduce
		if st.Path == "" {
			return nil, fmt.Errorf("편집 단계에 경로가 없다")
		}
		ev.Paths = []string{st.Path}
		if st.Hash != "" {
			ev.WriteHashes = map[string]string{st.Path: st.Hash}
		}
		ev.AddedLines = map[string]int{st.Path: len(st.Added)}
		ev.RemovedLines = map[string]int{st.Path: len(st.Removed)}
		if len(st.Added) > 0 || len(st.Removed) > 0 {
			ev.Patch = []event.PatchFile{{Path: st.Path, Added: st.Added, Removed: st.Removed}}
		}
	case "read":
		ev.Tool = event.ToolRead
		ev.Category = event.CatExplore
		if st.Path != "" {
			ev.Paths = []string{st.Path}
		}
	case "shell":
		ev.Tool = event.ToolShell
		ev.Category = event.CatVerify
		norm, _ := fp.NormalizeCmd(st.Cmd)
		ev.Cmd, ev.CmdNorm, ev.CmdFP = st.Cmd, norm, fp.CmdFP(norm)
		ev.ExecFP, ev.ExecCertain = fp.ExecFP(st.Cmd, "")
		if st.ExitCode != nil {
			ev.ExitCode = st.ExitCode
		}
		ev.FailedTests = st.Failed
		ev.ErrFPs = nil
		ev.ResultFP = fp.ResultFP(ev.ExitCode, ev.ErrFPs, ev.FailedTests)
	case "message":
		ev.Kind = event.KindMessage
	default:
		return nil, fmt.Errorf("알 수 없는 도구 %q", st.Tool)
	}
	if ev.Category == "" {
		ev.Category = event.CatExplore
	}
	return ev, nil
}

// RuleReport is one rule's blind spots.
type RuleReport struct {
	Rule  string `json:"rule"`
	Total int    `json:"total"`
	// Missed counts the scenarios the rule did not catch.
	Missed int `json:"missed"`
	// BlindSpots names the scenarios the rule did not catch.
	BlindSpots []string `json:"blind_spots,omitempty"`
}

// Report is the corpus's outcome.
type Report struct {
	Version string `json:"version"`
	// Total counts the scenarios.
	Total int `json:"total"`
	// Caught counts the scenarios the rule caught, as expected.
	Caught int `json:"caught"`
	// Missed counts the scenarios the rule did not catch. These are the
	// blind spots.
	Missed int `json:"missed"`
	// FalseAlarms counts the scenarios the rule fired on wrongly.
	FalseAlarms int `json:"false_alarms"`
	// Rules holds the per-rule outcome.
	Rules []RuleReport `json:"rules"`
	// BlindSpots names every scenario the rule did not catch.
	BlindSpots []string `json:"blind_spots,omitempty"`
}

// Measure replays the corpus and reports what the rules missed.
//
// The measure is the mirror of the false-positive corpus. A scenario the
// rule was expected to catch and did not is a blind spot; a scenario it was
// not expected to catch and fired on is a false alarm. Both are reported.
func Measure(scenarios []Scenario) (Report, error) {
	r := Report{Version: "evasion-report/1"}
	byRule := map[string]*RuleReport{}
	for _, s := range scenarios {
		caught, err := Run(s)
		if err != nil {
			return r, err
		}
		rr := byRule[s.Rule]
		if rr == nil {
			rr = &RuleReport{Rule: s.Rule}
			byRule[s.Rule] = rr
		}
		rr.Total++
		r.Total++
		switch {
		case caught && s.Caught:
			r.Caught++
		case !caught && s.Caught:
			r.Missed++
			rr.Missed++
			rr.BlindSpots = append(rr.BlindSpots, s.Name)
			r.BlindSpots = append(r.BlindSpots, s.Name)
		case caught && !s.Caught:
			r.FalseAlarms++
		}
	}
	names := make([]string, 0, len(byRule))
	for name := range byRule {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rr := byRule[name]
		sort.Strings(rr.BlindSpots)
		r.Rules = append(r.Rules, *rr)
	}
	sort.Strings(r.BlindSpots)
	return r, nil
}

// Summary renders the report for a person.
func Summary(r Report) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "회피 시나리오 %d건 · 잡음 %d건 · 놓침 %d건 · 오작동 %d건", r.Total, r.Caught, r.Missed, r.FalseAlarms)
	for _, rule := range r.Rules {
		fmt.Fprintf(&sb, "\n  %s · %d건 중 %d건 놓침", rule.Rule, rule.Total, rule.Missed)
		for _, spot := range rule.BlindSpots {
			fmt.Fprintf(&sb, "\n    %s", spot)
		}
	}
	return sb.String()
}
