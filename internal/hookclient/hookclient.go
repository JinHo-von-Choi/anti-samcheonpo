// Package hookclient is the thin hook process: read stdin, ask the daemon
// once, print its decision, exit 0. Every failure path prints nothing and
// exits 0, which Claude Code treats as "proceed".
package hookclient

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sockpath"
)

// Timeouts per hook event. Stop runs the contract checkpoint and SessionEnd
// writes the receipt, so they wait longer than per-tool hooks. Claude Code
// gives SessionEnd hooks 35 seconds (plugin setting) and the daemon budgets
// 30; Codex and the opencode forwarder stop a hook after about 3 seconds.
func timeoutFor(event, agent string) time.Duration {
	switch event {
	case "PreToolUse":
		return 15 * time.Millisecond
	case "Stop":
		return 150 * time.Second
	case "SessionEnd":
		if agent == "claude" {
			return 30 * time.Second
		}
		return 2500 * time.Millisecond
	}
	return 50 * time.Millisecond
}

// ConnectTimeout bounds the socket connect.
const ConnectTimeout = 5 * time.Millisecond

// Only a missing socket uses this bootstrap allowance. Warm hooks keep their
// existing short deadlines. Never retry after writing a request.
const ColdStartTimeout = 500 * time.Millisecond

func connectHook(sock string, spawn func()) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", sock, ConnectTimeout)
	if err == nil {
		return conn, nil
	}
	spawn()
	deadline := time.Now().Add(ColdStartTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		conn, err = net.DialTimeout("unix", sock, min(ConnectTimeout, remaining))
		if err == nil {
			return conn, nil
		}
	}
	return nil, err
}

// Main runs the hook client for Claude Code and returns the exit code (always 0).
func Main(event string, stdin io.Reader, stdout io.Writer) int {
	return MainAgent(event, "claude", stdin, stdout)
}

// failClosedAllow is the explicit "proceed" for agents that block when a
// hook prints nothing (agy pre-tool, Cursor permission hooks).
func failClosedAllow(agent, event string) string {
	switch {
	case agent == "agy" && event == "PreToolUse":
		return `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`
	case agent == "cursor" && strings.HasPrefix(event, "before") && event != "beforeSubmitPrompt":
		return `{"permission":"allow"}`
	case agent == "cursor" && event == "beforeSubmitPrompt":
		return `{"continue":true}`
	}
	return ""
}

// MainAgent runs the hook client for an agent dialect.
func MainAgent(event, agent string, stdin io.Reader, stdout io.Writer) int {
	var diagnostics io.Writer
	if os.Getenv("SAMCHEONPO_HOOK_DIAGNOSTICS") == "1" {
		diagnostics = os.Stderr
	}
	return runAgent(event, agent, stdin, stdout, diagnostics)
}

// Outcome reports transport handling, not whether the agent read or obeyed advice.
// No request text, tool arguments, or response content is included.
type Outcome struct {
	Status           string `json:"status"`
	TimedOut         bool   `json:"timed_out"`
	ResponseReceived bool   `json:"response_received"`
	HandlingNS       int64  `json:"handling_ns,omitempty"`
}

func runAgent(event, agent string, stdin io.Reader, stdout, diagnostics io.Writer) int {
	result := Outcome{Status: "invalid_input"}
	var started time.Time
	if diagnostics != nil {
		started = time.Now()
	}
	defer func() {
		if diagnostics != nil {
			result.HandlingNS = time.Since(started).Nanoseconds()
			_ = json.NewEncoder(diagnostics).Encode(result)
		}
	}()
	fail := func(status string, err error) {
		result.Status = status
		if e, ok := err.(net.Error); ok {
			result.TimedOut = e.Timeout()
		}
	}
	allow := failClosedAllow(agent, event)
	pass := func() int {
		if allow != "" {
			_, _ = io.WriteString(stdout, allow+"\n")
		}
		return 0
	}
	payload, err := io.ReadAll(io.LimitReader(stdin, 32<<20))
	if err != nil || len(payload) == 0 || !json.Valid(payload) {
		return pass()
	}
	sock := socketPath()
	var conn net.Conn
	if os.Getenv("SAMCHEONPO_NO_SPAWN") == "1" {
		conn, err = net.DialTimeout("unix", sock, ConnectTimeout)
	} else {
		conn, err = connectHook(sock, spawnDaemon)
	}
	if err != nil {
		fail("connect_failed", err)
		return pass()
	}
	defer conn.Close()
	deadline := time.Now().Add(timeoutFor(event, agent))
	_ = conn.SetDeadline(deadline)
	// The daemon bounds its own work by this deadline: a decision computed
	// after the client gave up is never printed.
	req, _ := json.Marshal(map[string]any{"v": 1, "agent": agent, "event": event, "deadline_unix_ms": deadline.UnixMilli(), "payload": json.RawMessage(payload)})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		fail("write_failed", err)
		return pass()
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		fail("read_failed", err)
		return pass()
	}
	var resp struct {
		Output        json.RawMessage `json:"output"`
		Error         string          `json:"error"`
		DeliveryToken string          `json:"delivery_token"`
	}
	if json.Unmarshal(line, &resp) != nil {
		result.Status = "invalid_response"
		return pass()
	}
	result.ResponseReceived = true
	if resp.Error != "" {
		result.Status = "daemon_error"
		return pass()
	}
	result.Status = "ok"
	if len(resp.Output) == 0 || string(resp.Output) == "null" {
		return pass()
	}
	output := append(resp.Output, '\n')
	n, err := stdout.Write(output)
	if err != nil || n != len(output) {
		result.Status = "output_failed"
	} else if resp.DeliveryToken != "" {
		ack, _ := json.Marshal(map[string]any{"delivery_token": resp.DeliveryToken, "printed": true})
		_, _ = conn.Write(append(ack, '\n'))
	}
	return 0
}

func socketPath() string { return sockpath.Path() }

// spawnDaemon starts the daemon detached; duplicates exit on the flock.
func spawnDaemon() {
	if os.Getenv("SAMCHEONPO_NO_SPAWN") == "1" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer devnull.Close()
	cmd := exec.Command(daemonExecutable(exe), "daemon")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

// Query sends a non-hook request (statusline, command) and returns the text.
func Query(event string, payload any, timeout time.Duration) (string, string, bool) {
	conn, err := net.DialTimeout("unix", socketPath(), ConnectTimeout*4)
	if err != nil {
		return "", "", false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	p, _ := json.Marshal(payload)
	req, _ := json.Marshal(map[string]any{"v": 1, "event": event, "payload": json.RawMessage(p)})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return "", "", false
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return "", "", false
	}
	var resp struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if json.Unmarshal(line, &resp) != nil {
		return "", "", false
	}
	return resp.Text, resp.Error, true
}
