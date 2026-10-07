package codex

import (
	"encoding/json"
	"math"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
)

// Missing fields are not zero usage. A malformed new observation invalidates
// the previous quota rather than silently presenting it as current.
func parseQuota(raw json.RawMessage) *cost.Quota {
	var limits struct {
		Primary *struct {
			Used   *float64 `json:"used_percent"`
			Window *int     `json:"window_minutes"`
		} `json:"primary"`
	}
	if json.Unmarshal(raw, &limits) != nil || limits.Primary == nil {
		return nil
	}
	p := limits.Primary
	if p.Used == nil || p.Window == nil || *p.Window <= 0 || *p.Used < 0 || *p.Used > 100 || math.IsNaN(*p.Used) || math.IsInf(*p.Used, 0) {
		return nil
	}
	return &cost.Quota{UsedPct: *p.Used, WindowMinutes: *p.Window, Source: "agent_reported"}
}
