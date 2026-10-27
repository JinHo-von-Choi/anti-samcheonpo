package intent

import "regexp"

// noticeRE opens a message the agent's harness delivers in the user turn:
// a background task finishing, a system reminder. The user did not type it.
var noticeRE = regexp.MustCompile(`^\s*<(?:task-notification|system-reminder)>`)

// HarnessNotice reports a prompt that is a harness notification rather
// than a user request.
func HarnessNotice(text string) bool { return noticeRE.MatchString(text) }
