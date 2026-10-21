package bench

import (
	"encoding/json"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// RunConditions records controls outside the policy being compared. Hashes
// identify reviewed manifests; secrets and raw configuration are never stored.
// Empty fields mean unmeasured, not default or disabled.
type RunConditions struct {
	ReasoningEffort    string `json:"reasoning_effort" yaml:"reasoning_effort"`
	InstructionsSHA256 string `json:"instructions_sha256" yaml:"instructions_sha256"`
	SkillsSHA256       string `json:"skills_sha256" yaml:"skills_sha256"`
	Sandbox            string `json:"sandbox" yaml:"sandbox"`
	Network            string `json:"network" yaml:"network"`
	MCPSHA256          string `json:"mcp_sha256" yaml:"mcp_sha256"`
	CachePolicy        string `json:"cache_policy" yaml:"cache_policy"`
	TimeoutSec         int    `json:"timeout_sec" yaml:"timeout_sec"`
}

func (c *RunConditions) complete() bool {
	return c != nil && c.ReasoningEffort != "" && c.InstructionsSHA256 != "" && c.SkillsSHA256 != "" && c.Sandbox != "" && c.Network != "" && c.MCPSHA256 != "" && c.CachePolicy != "" && c.TimeoutSec > 0
}

func (c *RunConditions) fingerprint() string {
	if c == nil {
		return ""
	}
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return fp.Hash(string(b))
}

// A task may have its own declared timeout. Compare it inside the matched
// task pair, while keeping the other controls fixed across the experiment.
func (c *RunConditions) commonFingerprint() string {
	if c == nil {
		return ""
	}
	common := *c
	common.TimeoutSec = 0
	return common.fingerprint()
}
