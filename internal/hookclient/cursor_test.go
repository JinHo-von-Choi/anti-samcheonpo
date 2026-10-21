package hookclient

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCursorBudgetsAndUnavailable(t *testing.T) {
	for event, want := range map[string]time.Duration{"preToolUse": 15 * time.Millisecond, "beforeShellExecution": 15 * time.Millisecond, "beforeReadFile": 15 * time.Millisecond, "sessionEnd": 2500 * time.Millisecond, "stop": 2500 * time.Millisecond, "postToolUse": 50 * time.Millisecond} {
		if got := timeoutFor(event, "cursor"); got != want {
			t.Fatalf("%s: %s", event, got)
		}
	}
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("SAMCHEONPO_NO_SPAWN", "1")
	for _, event := range []string{"preToolUse", "beforeReadFile", "beforeShellExecution", "beforeMCPExecution", "subagentStart"} {
		for _, input := range []string{`{"conversation_id":"s"}`, `broken`} {
			var out bytes.Buffer
			MainAgent(event, "cursor", strings.NewReader(input), &out)
			if strings.TrimSpace(out.String()) != `{"permission":"allow"}` {
				t.Fatalf("%s: %q", event, out.String())
			}
		}
	}
}

func TestCursorMalformedResponseAndTimeout(t *testing.T) {
	for _, response := range []string{`{}`, `{"permission":"unexpected"}`, `{"permission":null}`, `{"permission":"deny","agent_message":"blocked"}`, "timeout"} {
		t.Run(response, func(t *testing.T) {
			t.Setenv("SAMCHEONPO_HOME", t.TempDir())
			t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
			t.Setenv("SAMCHEONPO_NO_SPAWN", "1")
			path := socketPath()
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			ln, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				c, err := ln.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(time.Second))
				rd := bufio.NewReader(c)
				if _, err := rd.ReadBytes('\n'); err != nil {
					return
				}
				if response == "timeout" {
					time.Sleep(40 * time.Millisecond)
					return
				}
				io.WriteString(c, `{"output":`+response+"}\n")
			}()
			var out bytes.Buffer
			MainAgent("preToolUse", "cursor", strings.NewReader(`{"conversation_id":"s"}`), &out)
			<-done
			want := `"permission":"allow"`
			if strings.Contains(response, `"deny"`) {
				want = `"permission":"deny"`
			}
			if !strings.Contains(out.String(), want) {
				t.Fatalf("response %q: %q", response, out.String())
			}
		})
	}
}
