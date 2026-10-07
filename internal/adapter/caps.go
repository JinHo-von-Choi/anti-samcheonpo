// Package adapter declares what each agent's hook interface can do and probes
// the installed agent version.
package adapter

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Caps is an adapter capability declaration.
type Caps struct {
	BlockPre     bool `json:"block_pre"`     // deny a tool call before it runs, with a reason
	InjectPre    bool `json:"inject_pre"`    // add context before a tool call
	InjectPost   bool `json:"inject_post"`   // add context next to a tool result
	BlockStop    bool `json:"block_stop"`    // refuse a stop and continue
	EndTurnPost  bool `json:"end_turn_post"` // end the turn from a post-tool hook, above other hooks
	PromptInject bool `json:"prompt_inject"` // add context to a user prompt
	StatusLine   bool `json:"status_line"`   // persistent status line
	UserMessage  bool `json:"user_message"`  // show a message to the user (systemMessage)
	PostFailure  bool `json:"post_failure"`  // separate hook for failed tools carrying the error
	ExitInHook   bool `json:"exit_in_hook"`  // shell exit code is visible to hooks
	// SessionEndBudget is the longest a SessionEnd hook may take.
	SessionEndBudget time.Duration `json:"session_end_budget"`
	// FailClosed means a hook that prints nothing blocks the call; the client
	// must then always print an explicit allow.
	FailClosed bool `json:"fail_closed"`
}

// Tested records the agent versions whose hook payloads were captured and
// asserted (testdata/hooks/<agent>/). Outside this range the adapter falls
// back to observation only.
type Tested struct {
	Min, Max string
}

// Profile is a capability declaration with its tested version range.
type Profile struct {
	Agent  string `json:"agent"`
	Caps   Caps   `json:"caps"`
	Tested Tested `json:"tested"`
}

// Profiles lists the agents with hook adapters.
var Profiles = map[string]Profile{
	"claude": {Agent: "claude", Tested: Tested{Min: "2.1.0", Max: "2.1.999"}, Caps: Caps{
		BlockPre: true, InjectPre: true, InjectPost: true, BlockStop: true, EndTurnPost: true, PromptInject: true,
		StatusLine: true, UserMessage: true, PostFailure: true, ExitInHook: true, SessionEndBudget: 30 * time.Second}},
	// Codex 0.160.0: payloads captured in testdata/hooks/codex. PostToolUse
	// carries the output as a plain string without the exit code; apply_patch
	// may have no PostToolUse; SessionEnd may take at most 3 seconds.
	"codex": {Agent: "codex", Tested: Tested{Min: "0.160.0", Max: "0.160.999"}, Caps: Caps{
		BlockPre: true, InjectPre: true, InjectPost: true, BlockStop: true, EndTurnPost: false, PromptInject: true,
		StatusLine: false, UserMessage: true, PostFailure: false, ExitInHook: false, SessionEndBudget: 3 * time.Second}},
	// opencode 1.18.34 through the forwarder plugin (plugins/opencode): a
	// thrown error blocks a call, tool output can be extended, a stop can
	// only be followed by one more prompt.
	"opencode": {Agent: "opencode", Tested: Tested{Min: "1.18.0", Max: "1.18.999"}, Caps: Caps{
		BlockPre: true, InjectPre: false, InjectPost: true, BlockStop: true, EndTurnPost: false, PromptInject: true,
		StatusLine: false, UserMessage: false, PostFailure: true, ExitInHook: true, SessionEndBudget: 3 * time.Second}},
	// Docs-based, not yet captured from a running agent: observation only
	// until fixtures are recorded (Tested is empty). Copilot uses the
	// PascalCase hook mode; Cursor and agy fail closed on missing decisions.
	"copilot": {Agent: "copilot", Caps: Caps{SessionEndBudget: 5 * time.Second}},
	"cursor":  {Agent: "cursor", Caps: Caps{FailClosed: true, SessionEndBudget: 5 * time.Second}},
	"agy":     {Agent: "agy", Caps: Caps{FailClosed: true, SessionEndBudget: 5 * time.Second}},
}

// ObserveOnly is used for unknown agents and untested versions.
var ObserveOnly = Caps{SessionEndBudget: time.Second}

var verRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion extracts a semantic version triple.
func ParseVersion(s string) ([3]int, bool) {
	m := verRe.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := 0; i < 3; i++ {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func cmpVer(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Resolve returns the capabilities for an agent at a version string. Unknown
// agents, unparsable or untested versions get observation only.
func Resolve(agent, version string) (Caps, bool) {
	p, ok := Profiles[agent]
	if !ok {
		return ObserveOnly, false
	}
	observe := ObserveOnly
	observe.FailClosed = p.Caps.FailClosed
	if p.Tested.Min == "" {
		return observe, false
	}
	v, ok := ParseVersion(version)
	if !ok {
		return observe, false
	}
	lo, _ := ParseVersion(p.Tested.Min)
	hi, _ := ParseVersion(p.Tested.Max)
	if cmpVer(v, lo) < 0 || cmpVer(v, hi) > 0 {
		return observe, false
	}
	return p.Caps, true
}

// ProbeVersion runs `<agent> --version` and returns the first line.
func ProbeVersion(agent string) string {
	if _, ok := Profiles[agent]; !ok {
		return ""
	}
	bin := agent
	if agent == "cursor" {
		bin = "cursor-agent"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.WaitDelay = 50 * time.Millisecond
	var out versionOutput
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(out.data), "\n", 2)[0])
}

type versionOutput struct{ data []byte }

func (w *versionOutput) Write(p []byte) (int, error) {
	n := len(p)
	if room := 4096 - len(w.data); room > 0 {
		w.data = append(w.data, p[:min(room, n)]...)
	}
	return n, nil
}
