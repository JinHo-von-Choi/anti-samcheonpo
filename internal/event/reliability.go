package event

import (
	"regexp"
	"strings"
)

// Reliability is safe replay input. No raw prompt, output or environment is
// stored here. Empty identities and incomplete observations are not evidence.
type Reliability struct {
	WaitKind          string `json:"wait_kind,omitempty"` // sleep | tool; separate from relation/eligibility
	SourceMissing     bool   `json:"source_missing,omitempty"`
	TaskID            string `json:"task_id,omitempty"`
	Revision          uint64 `json:"revision,omitempty"`
	AuthorityHash     string `json:"authority_hash,omitempty"`
	CheckID           string `json:"check_id,omitempty"`
	CommandHash       string `json:"command_hash,omitempty"`
	InputHash         string `json:"input_hash,omitempty"`
	EnvironmentHash   string `json:"environment_hash,omitempty"`
	Complete          bool   `json:"complete,omitempty"`
	Flaky             bool   `json:"flaky,omitempty"`
	FailureComparable bool   `json:"failure_comparable,omitempty"`
	CriteriaMet       bool   `json:"criteria_met,omitempty"`
	Observation       string `json:"observation,omitempty"` // none | pending | fulfilled | unknown
	Wait              string `json:"wait,omitempty"`        // explicit | linked | unknown
	WaitTarget        string `json:"wait_target,omitempty"` // independently observed target hash
	Phase             string `json:"phase,omitempty"`       // input | result | checkpoint
	DurationSource    string `json:"duration_source,omitempty"`
}

var standaloneSleep = regexp.MustCompile(`^(?:/bin/|/usr/bin/)?sleep\s+(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)[smhd]?$`)

// ExplicitWait recognizes only a completed standalone sleep. Compound shell
// text, clock reads and tool output cannot establish executed waiting.
func ExplicitWait(e *Event) string {
	if e.Kind != KindTool || e.Tool != ToolShell || e.ExitCode == nil || *e.ExitCode != 0 || e.Background || e.IsError {
		return ""
	}
	cmd := strings.TrimSpace(e.Cmd)
	if cmd == "" {
		return ""
	} // legacy CmdNorm may have discarded shell effects
	if standaloneSleep.MatchString(cmd) {
		return "explicit"
	}
	return ""
}
