package policy

import (
	"path/filepath"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/judge"
)

// RiskGrade is how much a behavior deserves judgment budget.
//
// The grade is about what the behavior can change, not about how often it
// happens. Reading a file cannot leak a secret or move a commit; a shell
// command can. The semantic judge is the most expensive thing the monitor
// does, so it is spent where the damage is, and a session that only reads
// spends none of it.
type RiskGrade int

const (
	// RiskLow is behavior that only observes.
	RiskLow RiskGrade = iota
	// RiskMedium is behavior that changes files and nothing else.
	RiskMedium
	// RiskHigh is behavior that runs commands, reaches credentials, moves
	// commits or claims completion.
	RiskHigh
)

// String names the grade.
func (g RiskGrade) String() string {
	switch g {
	case RiskLow:
		return "low"
	case RiskMedium:
		return "medium"
	case RiskHigh:
		return "high"
	}
	return "unknown"
}

// Risk is one graded event.
type Risk struct {
	Grade RiskGrade `json:"grade"`
	// Reason says what made the event what it is. It names the shape of
	// the behavior and never quotes it.
	Reason string `json:"reason"`
}

// GradeEvent grades one event.
//
// The mapping is fixed and written down so that a reader can predict where
// the budget goes:
//
//   - read, search, web, todo, task: low
//   - write, edit: medium
//   - shell, credential path, git commit or push: high
//   - a message that claims completion: high
//   - a prompt from the user: low
func GradeEvent(ev *event.Event) Risk {
	if ev == nil {
		return Risk{Grade: RiskLow, Reason: "empty"}
	}
	switch ev.Kind {
	case event.KindPrompt:
		return Risk{Grade: RiskLow, Reason: "prompt"}
	case event.KindMessage:
		if judge.ClaimSummary(ev.Text) != "" {
			return Risk{Grade: RiskHigh, Reason: "claim"}
		}
		return Risk{Grade: RiskLow, Reason: "message"}
	case event.KindTool:
		return gradeTool(ev)
	}
	return Risk{Grade: RiskLow, Reason: "other"}
}

// gradeTool grades a tool event.
func gradeTool(ev *event.Event) Risk {
	switch ev.Tool {
	case event.ToolShell:
		if reason := shellRiskReason(ev); reason != "" {
			return Risk{Grade: RiskHigh, Reason: reason}
		}
		return Risk{Grade: RiskHigh, Reason: "shell"}
	case event.ToolWrite, event.ToolEdit:
		if reason := pathRiskReason(ev.Paths); reason != "" {
			return Risk{Grade: RiskHigh, Reason: reason}
		}
		return Risk{Grade: RiskMedium, Reason: "write"}
	case event.ToolRead:
		if reason := pathRiskReason(ev.Paths); reason != "" {
			return Risk{Grade: RiskHigh, Reason: reason}
		}
		return Risk{Grade: RiskLow, Reason: "read"}
	case event.ToolSearch, event.ToolWeb, event.ToolTodo, event.ToolTask:
		return Risk{Grade: RiskLow, Reason: ev.Tool}
	}
	return Risk{Grade: RiskLow, Reason: "other"}
}

// shellRiskReason names the high-risk part of a shell command, if any.
//
// The grade of a shell command is already high. The reason separates the
// commands that move work outside the workspace from the ones that only
// look around, because the report has to say which is which.
func shellRiskReason(ev *event.Event) string {
	norm := strings.ToLower(ev.CmdNorm)
	if norm == "" {
		return ""
	}
	if strings.Contains(norm, "git commit") || strings.Contains(norm, "git push") ||
		strings.Contains(norm, "git merge") || strings.Contains(norm, "git rebase") {
		return "commit"
	}
	for _, p := range ev.Paths {
		if credentialPath(p) {
			return "credential"
		}
	}
	if credentialPath(norm) {
		return "credential"
	}
	return ""
}

// pathRiskReason names the credential a path list reaches.
func pathRiskReason(paths []string) string {
	for _, p := range paths {
		if credentialPath(p) {
			return "credential"
		}
	}
	return ""
}

// credentialPath reports whether a path holds a secret.
//
// The list is deliberately narrow. A secret file is a file whose contents
// are credentials, not a file whose name sounds sensitive.
func credentialPath(p string) bool {
	if p == "" {
		return false
	}
	base := filepath.Base(p)
	lower := strings.ToLower(base)
	switch {
	case lower == ".env" || strings.HasPrefix(lower, ".env."):
		return true
	case lower == ".netrc" || lower == ".npmrc" || lower == ".pypirc" || lower == ".git-credentials" || lower == ".dockercfg":
		return true
	case lower == "credentials" && strings.Contains(strings.ToLower(p), ".aws"):
		return true
	case lower == "id_rsa" || lower == "id_dsa" || lower == "id_ecdsa" || lower == "id_ed25519":
		return true
	case strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".p12") || strings.HasSuffix(lower, ".pfx"):
		return true
	case lower == "service-account.json" || lower == "serviceaccount.json":
		return true
	}
	if strings.Contains(strings.ToLower(p), "/.ssh/") {
		return true
	}
	return false
}

// WorthJudgment reports whether an event deserves the semantic judge's
// budget.
//
// The answer is the grade and nothing else: a low-risk behavior is judged
// by the deterministic detectors, which cost nothing.
func WorthJudgment(ev *event.Event) bool {
	return GradeEvent(ev).Grade >= RiskHigh
}
