package intent

import (
	"regexp"
	"strings"
)

// ConversationOnly recognizes complete conversational messages. A phrase
// inside a larger request must not discard the rest of that request.
var conversationOnly = regexp.MustCompile(`(?i)^(?:context|status|상태|진행\s*상황|어디까지\s*(?:됐어|했어)|뭐\s*(?:해|하니|하고\s*있어)|잘\s*되고\s*있어|응|네|예|알겠어|좋아|고마워|감사합니다|ok|okay|thanks)[.!?？\s]*$`)

func ConversationOnly(text string) bool {
	return conversationOnly.MatchString(strings.TrimSpace(text))
}

type ChangeKind string

const (
	NewRequest ChangeKind = "new_task"
	Followup   ChangeKind = "followup"
	Redirect   ChangeKind = "redirect"
	Ignored    ChangeKind = "ignored"
	Confirmed  ChangeKind = "confirmed_contract"
	Cancelled  ChangeKind = "cancelled"
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
	if origin != User || text == "" || ConversationOnly(text) {
		return Change{Kind: Ignored}
	}
	if hasTask && IsCancellation(text) {
		return Change{Kind: Cancelled, Goal: text}
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

func IsCancellation(text string) bool {
	switch strings.Trim(strings.ToLower(strings.TrimSpace(text)), ".!? ") {
	case "작업 취소", "작업 취소해", "이 작업 취소해", "요청 취소", "cancel task", "cancel this task":
		return true
	}
	return false
}
