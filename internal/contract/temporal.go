package contract

// TemporalRequirement is accepted task intent, not a budget extension or
// execution grant. Absence means unknown, including legacy contracts.
type TemporalRequirement struct {
	Kind    string `yaml:"kind" json:"kind"`
	Seconds int64  `yaml:"seconds,omitempty" json:"seconds,omitempty"`
	CheckID string `yaml:"check_id,omitempty" json:"check_id,omitempty"`
}

// WaitingObservationSatisfied requires the state declared by this contract;
// a minimum-duration or observe-until task can never be treated as "none".
func (c *Contract) WaitingObservationSatisfied(state string) bool {
	if c == nil || c.TemporalRequirement == nil {
		return false
	}
	switch c.TemporalRequirement.Kind {
	case "completion":
		return state == "none"
	case "minimum_duration", "observe_until":
		return state == "fulfilled"
	}
	return false
}

func (c *Contract) TemporalConflict(ceilingSeconds float64) bool {
	if c == nil || c.TemporalRequirement == nil || c.TemporalRequirement.Kind != "minimum_duration" {
		return false
	}
	limit := ceilingSeconds
	if c.Budget.Minutes > 0 && (limit <= 0 || float64(c.Budget.Minutes)*60 < limit) {
		limit = float64(c.Budget.Minutes) * 60
	}
	return limit > 0 && float64(c.TemporalRequirement.Seconds) > limit
}
