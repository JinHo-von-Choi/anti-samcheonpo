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

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestDeliveryReceiptOnlyAfterSuccessfulOutput(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "printed", true: "failed-output"}[broken], func(t *testing.T) {
			t.Setenv("SAMCHEONPO_HOME", t.TempDir())
			t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
			t.Setenv("SAMCHEONPO_NO_SPAWN", "1")
			path := socketPath()
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			ln, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			acks := make(chan bool, 1)
			go func() {
				c, err := ln.Accept()
				if err != nil {
					acks <- false
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(time.Second))
				rd := bufio.NewReader(c)
				if _, err = rd.ReadBytes('\n'); err != nil {
					acks <- false
					return
				}
				io.WriteString(c, "{\"output\":{\"systemMessage\":\"bounded advice\"},\"delivery_token\":\"receipt-token\"}\n")
				line, err := rd.ReadBytes('\n')
				if err != nil {
					acks <- false
					return
				}
				var ack struct {
					DeliveryToken string `json:"delivery_token"`
					Printed       bool   `json:"printed"`
				}
				acks <- json.Unmarshal(line, &ack) == nil && ack.Printed && ack.DeliveryToken == "receipt-token"
			}()
			var output bytes.Buffer
			var writer io.Writer = &output
			if broken {
				writer = brokenOutput{}
			}
			if code := MainAgent("Stop", "claude", strings.NewReader(`{"session_id":"s"}`), writer); code != 0 {
				t.Fatal(code)
			}
			select {
			case ack := <-acks:
				if ack == broken {
					t.Fatal("incorrect delivery receipt", ack)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("missing server completion")
			}
			if !broken && !strings.Contains(output.String(), "bounded advice") {
				t.Fatal("no hook output")
			}
		})
	}
}

func TestRequestCarriesClientDeadline(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("SAMCHEONPO_NO_SPAWN", "1")
	path := socketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan int64, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- 0
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadBytes('\n')
		var req struct {
			Deadline int64 `json:"deadline_unix_ms"`
		}
		_ = json.Unmarshal(line, &req)
		got <- req.Deadline
		io.WriteString(c, "{}\n")
	}()
	start := time.Now()
	MainAgent("PreToolUse", "claude", strings.NewReader(`{"session_id":"s"}`), io.Discard)
	d := time.UnixMilli(<-got)
	if d.Before(start) || d.After(start.Add(timeoutFor("PreToolUse", "claude")+50*time.Millisecond)) {
		t.Fatalf("deadline %v not within the PreToolUse wait from %v", d, start)
	}
}

func TestSessionEndWaitMatchesHostBudget(t *testing.T) {
	if timeoutFor("SessionEnd", "claude") < 25*time.Second {
		t.Fatal("Claude Code allows the receipt to be written; the client must wait for it")
	}
	for _, agent := range []string{"codex", "opencode", "cursor", "agy"} {
		if timeoutFor("SessionEnd", agent) >= 3*time.Second {
			t.Fatalf("%s stops hooks after about 3 seconds", agent)
		}
	}
}
