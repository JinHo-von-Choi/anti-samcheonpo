package hookclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticsDistinguishesEmptySuccessFromTimeout(t *testing.T) {
	for _, status := range []string{"ok", "read_failed", "daemon_error", "invalid_response"} {
		t.Run(status, func(t *testing.T) {
			t.Setenv("SAMCHEONPO_HOME", t.TempDir())
			t.Setenv("SAMCHEONPO_NO_SPAWN", "1")
			if err := os.MkdirAll(filepath.Dir(socketPath()), 0700); err != nil {
				t.Fatal(err)
			}
			ln, err := net.Listen("unix", socketPath())
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				_, _ = bufio.NewReader(conn).ReadBytes('\n')
				switch status {
				case "ok":
					_, _ = io.WriteString(conn, "{\"v\":1}\n")
				case "daemon_error":
					_, _ = io.WriteString(conn, "{\"error\":\"secret response\"}\n")
				case "invalid_response":
					_, _ = io.WriteString(conn, "not json\n")
				case "read_failed":
					time.Sleep(50 * time.Millisecond)
				}
			}()
			var out, diagnostic bytes.Buffer
			if code := runAgent("PreToolUse", "claude", strings.NewReader(`{"prompt":"secret prompt"}`), &out, &diagnostic); code != 0 {
				t.Fatal(code)
			}
			<-done
			var result Outcome
			if err := json.Unmarshal(diagnostic.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != status || result.TimedOut != (status == "read_failed") {
				t.Fatalf("%+v", result)
			}
			if result.HandlingNS <= 0 {
				t.Fatal("handling interval was not measured")
			}
			if result.ResponseReceived != (status == "ok" || status == "daemon_error") {
				t.Fatalf("%+v", result)
			}
			if out.Len() != 0 || strings.Contains(diagnostic.String(), "secret") {
				t.Fatal("diagnostics changed hook output or leaked content")
			}
		})
	}
}
