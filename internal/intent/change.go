package intent

import "strings"

type ChangeKind string

const (
	NewRequest ChangeKind = "new_task"
	Followup   ChangeKind = "followup"
	Redirect   ChangeKind = "redirect"
	Ignored    ChangeKind = "ignored"
	Confirmed  ChangeKind = "confirmed_contract"
)

type Change struct {
	Kind ChangeKind
	Goal string
}

// ClassifyChange deliberately recognizes only explicit direction markers.
// Ordinary follow-up prose is not permission to replace the accepted goal.
// Origin is supplied by ingress; text claiming to be a user is not a source.
func ClassifyChange(origin Origin, text string, hasTask bool) Change {
	text = strings.TrimSpace(text)
	if origin != User || text == "" {
		return Change{Kind: Ignored}
	}
	for _, prefix := range []string{"목표 변경:", "방향 변경:", "Change goal:", "/samcheonpo:edit "} {
		if strings.HasPrefix(text, prefix) {
			goal := strings.TrimSpace(strings.TrimPrefix(text, prefix))
			if goal != "" {
				return Change{Kind: Redirect, Goal: goal}
			}
		}
	}
	if strings.HasPrefix(text, "/") {
		return Change{Kind: Ignored}
	}
	if !hasTask {
		return Change{Kind: NewRequest, Goal: text}
	}
	return Change{Kind: Followup, Goal: text}
}
