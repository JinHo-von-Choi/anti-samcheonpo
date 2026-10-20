package taskgen

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
)

// Split builds the split file for the generated bundles.
//
// The two copies of a bundle are the same task, so they belong to the same
// side of the split. A family is one independent unit: two bundles of one
// family share the same function and the same bug, so they belong together
// too. Splitting them apart would count one task as two independent ones
// and make the confidence interval too narrow.
//
// The split is written before any run, which is what lets the holdout side
// be a preregistered holdout rather than a set of tasks that happened to
// be looked at.
func Split(bundles []Bundle) eval.Split {
	s := eval.Split{Version: "split/2", Purpose: "preregistered_holdout", Groups: map[string]eval.SplitGroup{}}
	var order []string
	seen := map[string]bool{}
	for _, b := range bundles {
		if !seen[b.Family] {
			seen[b.Family] = true
			order = append(order, b.Family)
		}
	}
	if len(order) == 0 {
		return s
	}
	// Every family belongs to one side. The first families calibrate and
	// the rest are held out.
	half := len(order) / 2
	if half == 0 {
		half = 1
	}
	side := map[string]bool{}
	for i, f := range order {
		side[f] = i < half
	}
	for _, b := range bundles {
		group := eval.SplitGroup{Repository: "generated-" + b.Family, Family: b.Family}
		for _, name := range []string{b.Sniper, b.Blind} {
			s.Groups[name] = group
			if side[b.Family] {
				s.Calibration = append(s.Calibration, name)
			} else {
				s.Holdout = append(s.Holdout, name)
			}
		}
	}
	return s
}
