package judge

import (
	"encoding/json"
	"slices"
	"strings"
)

const Unknown = "unknown"

type Issue struct {
	Requirements   []string `json:"requirements,omitempty"`
	Claims         []int64  `json:"claims,omitempty"`
	Results        []int64  `json:"results,omitempty"`
	Authorizations []int64  `json:"authorizations,omitempty"`
	Kind           string   `json:"kind"`
	Evidence       []int64  `json:"evidence"`
}

// ParseVerdict accepts only enumerated issues supported by supplied event IDs.
// Free prose and model-provided authority, commands and explanations are dropped.
func ParseVerdict(text string, in Input) Verdict {
	t := strings.TrimSpace(text)
	if t == OnTrack || t == SideQuest || t == Drift || t == Unknown {
		return Verdict{Label: t}
	}
	var wire struct {
		Label  string  `json:"label"`
		Issues []Issue `json:"issues"`
	}
	if json.Unmarshal([]byte(t), &wire) != nil {
		return Verdict{Label: Unknown}
	}
	switch wire.Label {
	case OnTrack, SideQuest, Drift, Unknown:
	default:
		return Verdict{Label: Unknown}
	}
	allowed := map[int64]bool{}
	for _, id := range in.EvidenceIDs {
		allowed[id] = true
	}
	v := Verdict{Label: wire.Label}
	if wire.Label == Unknown {
		return v
	}
	for _, issue := range wire.Issues {
		if len(v.Issues) >= 4 {
			break
		}
		switch issue.Kind {
		case "requirement_substitution", "partial_completion", "repeated_permission":
		default:
			continue
		}
		if len(issue.Evidence) == 0 || len(issue.Evidence) > 8 {
			continue
		}
		valid := true
		for _, id := range issue.Evidence {
			if !allowed[id] {
				valid = false
			}
		}
		if valid && in.Graph != nil {
			valid = in.Graph.ValidIssue(issue.Kind, issue.Requirements, issue.Claims, issue.Results, issue.Authorizations)
			for _, id := range append(append(append([]int64(nil), issue.Claims...), issue.Results...), issue.Authorizations...) {
				if !allowed[id] || !slices.Contains(issue.Evidence, id) {
					valid = false
				}
			}
		}
		if valid {
			v.Issues = append(v.Issues, issue)
		}
	}
	return v
}

// Validate also protects non-provider Judge implementations at the live boundary.
// Usage remains billable even when every semantic claim is rejected.
func Validate(v Verdict, in Input) Verdict {
	b, err := json.Marshal(struct {
		Label  string  `json:"label"`
		Issues []Issue `json:"issues"`
	}{v.Label, v.Issues})
	if err != nil {
		v.Label = Unknown
		v.Issues = nil
		return v
	}
	normalized := ParseVerdict(string(b), in)
	v.Label, v.Issues = normalized.Label, normalized.Issues
	return v
}
