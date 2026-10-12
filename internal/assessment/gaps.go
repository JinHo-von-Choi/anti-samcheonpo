package assessment

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"sort"
	"time"
)

// Gap spans affected observations, not the exact onset of an unseen failure.
type Gap struct {
	First         int64      `json:"first_affected_seq"`
	Last          int64      `json:"last_affected_seq"`
	FirstAt       time.Time  `json:"first_affected_at,omitempty"`
	LastAt        time.Time  `json:"last_affected_at,omitempty"`
	ResumedAt     *time.Time `json:"resumed_at,omitempty"`
	Open          bool       `json:"open"`
	TimeUnknown   bool       `json:"time_unknown"`
	Reasons       []string   `json:"reasons"`
	AffectedRules []string   `json:"affected_rules"`
}
type GapTracker struct{ Gaps []Gap }

func (t *GapTracker) Observe(e *event.Event) {
	if e == nil || e.Kind != event.KindTool {
		return
	}
	reasons := map[string]bool{}
	rules := map[string]bool{}
	for _, d := range e.Decisions {
		switch d.Code {
		case "watch_unavailable", "queue_saturated", "storage_error", "tool_schema_unknown":
			reasons[d.Code] = true
		}
		if d.Rule != "observation.collection" && d.Rule != "" {
			rules[d.Rule] = true
		}
	}
	if len(reasons) == 0 && e.Reliability != nil && !e.Reliability.Complete {
		reasons["incomplete_observation"] = true
	}
	if len(reasons) == 0 {
		if e.Reliability != nil && e.Reliability.Complete && len(t.Gaps) > 0 && t.Gaps[len(t.Gaps)-1].Open {
			last := &t.Gaps[len(t.Gaps)-1]
			last.Open = false
			if !e.TS.IsZero() {
				at := e.TS
				last.ResumedAt = &at
			} else {
				last.TimeUnknown = true
			}
		}
		return
	}
	if len(t.Gaps) == 0 || !t.Gaps[len(t.Gaps)-1].Open {
		t.Gaps = append(t.Gaps, Gap{First: e.Seq, FirstAt: e.TS, Open: true})
	}
	g := &t.Gaps[len(t.Gaps)-1]
	g.Last = e.Seq
	g.LastAt = e.TS
	g.TimeUnknown = g.TimeUnknown || e.TS.IsZero()
	merge := func(old []string, values map[string]bool) []string {
		for _, v := range old {
			values[v] = true
		}
		out := []string{}
		for v := range values {
			out = append(out, v)
		}
		sort.Strings(out)
		return out
	}
	g.Reasons = merge(g.Reasons, reasons)
	g.AffectedRules = merge(g.AffectedRules, rules)
	if len(t.Gaps) > 32 {
		t.Gaps = t.Gaps[len(t.Gaps)-32:]
	}
}
func (t *GapTracker) Snapshot() []Gap {
	out := append([]Gap(nil), t.Gaps...)
	for i := range out {
		out[i].Reasons = append([]string(nil), out[i].Reasons...)
		out[i].AffectedRules = append([]string(nil), out[i].AffectedRules...)
		if out[i].ResumedAt != nil {
			at := *out[i].ResumedAt
			out[i].ResumedAt = &at
		}
	}
	return out
}
