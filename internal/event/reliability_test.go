package event

import "testing"

func TestExplicitWaitUsesExecutedCommand(t *testing.T) {
	for _, cmd := range []string{"sleep 2", "/bin/sleep 0.5", "sleep 1m", "sleep 1; echo ok", "echo sleep 1", "python -c 'sleep(1)'", "date", "sleep $(cat seconds)", "timeout 10 sleep 1", "sleep 1 &", "sleep 1\necho ok"} {
		exit := 0
		e := &Event{Kind: KindTool, Tool: ToolShell, Cmd: cmd, ExitCode: &exit}
		want := cmd == "sleep 2" || cmd == "/bin/sleep 0.5" || cmd == "sleep 1m"
		if got := ExplicitWait(e) == "explicit"; got != want {
			t.Errorf("%q: %v", cmd, got)
		}
		e.Cmd = ""
		e.CmdNorm = cmd
		if ExplicitWait(e) != "" {
			t.Error("normalized legacy command inferred")
		}
	}
}
