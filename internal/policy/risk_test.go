package policy

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// The grade is about what a behavior can change. Reading is low, writing
// is medium, and running a command, reaching a credential or claiming a
// result is high.
func TestGradeEventSeparatesReadingFromRunning(t *testing.T) {
	cases := []struct {
		name  string
		ev    *event.Event
		grade RiskGrade
	}{
		{"read", &event.Event{Kind: event.KindTool, Tool: event.ToolRead, Paths: []string{"src/main.go"}}, RiskLow},
		{"search", &event.Event{Kind: event.KindTool, Tool: event.ToolSearch}, RiskLow},
		{"prompt", &event.Event{Kind: event.KindPrompt, Text: "해줘"}, RiskLow},
		{"write", &event.Event{Kind: event.KindTool, Tool: event.ToolWrite, Paths: []string{"src/main.go"}}, RiskMedium},
		{"edit", &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Paths: []string{"src/main.go"}}, RiskMedium},
		{"shell", &event.Event{Kind: event.KindTool, Tool: event.ToolShell, CmdNorm: "go test ./..."}, RiskHigh},
		{"commit", &event.Event{Kind: event.KindTool, Tool: event.ToolShell, CmdNorm: "git commit -m wip"}, RiskHigh},
		{"credential read", &event.Event{Kind: event.KindTool, Tool: event.ToolRead, Paths: []string{".env"}}, RiskHigh},
		{"credential write", &event.Event{Kind: event.KindTool, Tool: event.ToolWrite, Paths: []string{"home/.ssh/id_rsa"}}, RiskHigh},
		{"plain message", &event.Event{Kind: event.KindMessage, Text: "확인했습니다."}, RiskLow},
		{"claim", &event.Event{Kind: event.KindMessage, Text: "구현을 완료했습니다."}, RiskHigh},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := GradeEvent(c.ev).Grade; got != c.grade {
				t.Fatalf("grade %v, want %v", got, c.grade)
			}
		})
	}
}

// The semantic judge's budget goes to the behavior that can do damage. A
// session that only reads spends none of it.
func TestWorthJudgmentOnlyForHighRisk(t *testing.T) {
	read := &event.Event{Kind: event.KindTool, Tool: event.ToolRead, Paths: []string{"src/main.go"}}
	if WorthJudgment(read) {
		t.Fatal("a read was given judgment budget")
	}
	shell := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, CmdNorm: "go test ./..."}
	if !WorthJudgment(shell) {
		t.Fatal("a shell command was refused judgment budget")
	}
}

// The credential list is narrow on purpose: a file whose name sounds
// sensitive is not a file whose contents are credentials.
func TestCredentialPathIsNarrow(t *testing.T) {
	yes := []string{".env", ".env.local", "home/.ssh/id_rsa", "home/.aws/credentials", "home/.npmrc", "keys/server.pem", "sa/service-account.json"}
	for _, p := range yes {
		if !credentialPath(p) {
			t.Fatalf("%q not seen as a credential", p)
		}
	}
	no := []string{"src/main.go", "README.md", "env.go", "keychain.go", "docs/environment.md", ""}
	for _, p := range no {
		if credentialPath(p) {
			t.Fatalf("%q seen as a credential", p)
		}
	}
}
