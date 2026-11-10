package cli

import "testing"

func TestCritiqueCommandFollowsTheWorkingAgent(t *testing.T) {
	for agent, want := range map[string]string{"codex": "codex", "mcode": "mcode", "agy": "agy", "copilot": "copilot", "cursor": "cursor-agent", "claude": "claude", "hermes": "claude", "": "claude"} {
		if got := critiqueCommand(nil, agent)[0]; got != want {
			t.Errorf("%q: %s", agent, got)
		}
	}
	if got := critiqueCommand([]string{"my-review"}, "codex")[0]; got != "my-review" {
		t.Errorf("the configured command wins: %s", got)
	}
}
