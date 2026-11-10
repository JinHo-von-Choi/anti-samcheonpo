package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RestraintCase holds a request, the least that answers it and what was
// produced. Units are paragraphs and list items of Answer, or Elements for a
// screen. Length plays no part: a long answer that was asked for has no
// excess.
type RestraintCase struct {
	Name     string   `json:"name"`
	Request  string   `json:"request"`
	Answer   string   `json:"answer,omitempty"`
	Elements []string `json:"elements,omitempty"`
	// Minimal lists what a sufficient answer has to contain.
	Minimal []string `json:"minimal"`
	// WantExcess is the number of units beyond the minimal answer.
	WantExcess int `json:"want_excess"`
}

// RestraintResult is one case's count.
type RestraintResult struct {
	Name    string   `json:"name"`
	Excess  []string `json:"excess"`
	Missing []string `json:"missing"`
	Want    int      `json:"want_excess"`
}

// LoadRestraint reads a case file.
func LoadRestraint(path string) ([]RestraintCase, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []RestraintCase
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

func restraintUnits(c RestraintCase) []string {
	if len(c.Elements) > 0 {
		return c.Elements
	}
	var out []string
	for _, para := range strings.Split(c.Answer, "\n\n") {
		for _, line := range strings.Split(para, "\n") {
			line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*"))
			if line != "" {
				out = append(out, line)
			}
		}
	}
	return out
}

// ScoreRestraint counts the units that carry nothing from the minimal answer
// and the required items no unit carries.
func ScoreRestraint(c RestraintCase) RestraintResult {
	r := RestraintResult{Name: c.Name, Want: c.WantExcess}
	units := restraintUnits(c)
	for _, u := range units {
		needed := false
		for _, m := range c.Minimal {
			needed = needed || strings.Contains(strings.ToLower(u), strings.ToLower(m))
		}
		if !needed {
			r.Excess = append(r.Excess, u)
		}
	}
	joined := strings.ToLower(strings.Join(units, "\n"))
	for _, m := range c.Minimal {
		if !strings.Contains(joined, strings.ToLower(m)) {
			r.Missing = append(r.Missing, m)
		}
	}
	return r
}
