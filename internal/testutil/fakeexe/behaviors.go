package fakeexe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ironLaws stands in for the iron-laws auditor: `iron-laws audit FILE --format
// json -o OUT` reports one IL-301 violation for every line of FILE that
// contains SWALLOW.
func ironLaws(_ Spec, args []string) int {
	if len(args) < 2 {
		return 2
	}
	file, out := args[1], ""
	for i, a := range args {
		if a == "-o" && i+1 < len(args) {
			out = args[i+1]
		}
	}
	if out == "" {
		return 2
	}
	n := 0
	if b, err := os.ReadFile(file); err == nil {
		for _, line := range bytes.Split(b, []byte("\n")) {
			if bytes.Contains(line, []byte("SWALLOW")) {
				n++
			}
		}
	}
	items := make([]string, n)
	for i := range items {
		items[i] = `{"rule_id":"IL-301","iron_law":3,"line_number":1,"confidence":"CONFIRMED"}`
	}
	if err := os.WriteFile(out, []byte(`{"violations":[`+strings.Join(items, ",")+`]}`), 0o644); err != nil {
		return 1
	}
	return 0
}

// benchAgent stands in for an agent CLI in live A/B trials. With FIX=1 it
// repairs add.txt in the working directory. It copies the transcript named by
// TR into CD/proj/sid-FIX.jsonl with the session id and project path rewritten,
// requires SAMCHEONPO_HOME, and prints the session id as JSON.
func benchAgent(_ Spec, _ []string) int {
	fix := os.Getenv("FIX")
	cwd, err := os.Getwd()
	if err != nil {
		return 1
	}
	if fix == "1" {
		if err := os.WriteFile("add.txt", []byte("fixed\n"), 0o644); err != nil {
			return 1
		}
	}
	b, err := os.ReadFile(os.Getenv("TR"))
	if err != nil {
		return 1
	}
	// the replacement lands inside JSON strings, so backslashes in a Windows
	// path have to be escaped
	quoted, err := json.Marshal(cwd)
	if err != nil {
		return 1
	}
	s := strings.ReplaceAll(string(b), "s1", "sid-"+fix)
	s = strings.ReplaceAll(s, "/w/proj", string(quoted[1:len(quoted)-1]))
	dst := filepath.Join(os.Getenv("CD"), "proj", "sid-"+fix+".jsonl")
	if err := os.WriteFile(dst, []byte(s), 0o644); err != nil {
		return 1
	}
	if os.Getenv("SAMCHEONPO_HOME") == "" {
		return 3
	}
	fmt.Printf("{\"type\":\"result\",\"session_id\":\"sid-%s\"}\n", fix)
	return 0
}

// hookStub stands in for `samcheonpo hook EVENT AGENT`: it logs every call as a
// JSON line to $SAMCHEONPO_STUB_LOG and answers like the daemon. Option
// "hermes" holds a stop only when stop_hook_active is false and also gives
// advice after a failed tool call; option "openclaw" holds every stop.
func hookStub(spec Spec, args []string) int {
	if len(args) < 3 {
		return 2
	}
	ev, agent := args[1], args[2]
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 1
	}
	var p map[string]any
	_ = json.Unmarshal(payload, &p)
	line, err := json.Marshal(map[string]any{"ev": ev, "agent": agent, "p": json.RawMessage(payload)})
	if err != nil {
		return 1
	}
	f, err := os.OpenFile(os.Getenv("SAMCHEONPO_STUB_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 1
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return 1
	}
	if err := f.Close(); err != nil {
		return 1
	}
	advice := func(name string) string {
		return `{"hookSpecificOutput":{"hookEventName":"` + name + `","additionalContext":"[advice]"}}`
	}
	deny := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"repeat"}}`
	hold := `{"decision":"block","reason":"run the check"}`
	isBash := bytes.Contains(payload, []byte(`"Bash"`))
	switch spec.Option {
	case "hermes":
		active, _ := p["stop_hook_active"].(bool)
		switch {
		case ev == "PreToolUse" && p["tool_name"] == "Bash":
			fmt.Println(deny)
		case ev == "UserPromptSubmit" || ev == "PostToolUseFailure":
			fmt.Println(advice(ev))
		case ev == "Stop" && !active:
			fmt.Println(hold)
		}
	case "openclaw":
		switch {
		case ev == "UserPromptSubmit":
			fmt.Println(advice(ev))
		case ev == "PreToolUse" && isBash:
			fmt.Println(deny)
		case ev == "Stop":
			fmt.Println(hold)
		}
	default:
		return 2
	}
	return 0
}

// argsEcho prints its arguments joined by "|" on one line.
func argsEcho(_ Spec, args []string) int {
	fmt.Println(strings.Join(args, "|"))
	return 0
}

// spawner starts a child from its own file that sleeps for 60 seconds with the
// same stdout and stderr. Option "orphan" exits at once and leaves the child
// holding the pipes; any other option waits for the child.
func spawner(spec Spec, _ []string) int {
	self, err := os.Executable()
	if err != nil {
		return 1
	}
	child := exec.Command(self)
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	raw, err := json.Marshal(Spec{SleepMS: 60000})
	if err != nil {
		return 1
	}
	child.Env = append(os.Environ(), specEnv+"="+string(raw))
	if err := child.Start(); err != nil {
		return 1
	}
	if spec.Option == "orphan" {
		return 0
	}
	_ = child.Wait()
	return 0
}

// dial connects to the unix socket named by its first argument, sends a line
// and reports what came back: exit 0 when the server answered "ok", 3 when the
// server closed the connection without answering, 4 when it could not connect.
func dial(_ Spec, args []string) int {
	if len(args) < 1 {
		return 2
	}
	c, err := net.Dial("unix", args[0])
	if err != nil {
		fmt.Println(err)
		return 4
	}
	defer c.Close()
	if _, err := c.Write([]byte("ping\n")); err != nil {
		return 3
	}
	reply, _ := io.ReadAll(c)
	if strings.TrimSpace(string(reply)) == "ok" {
		return 0
	}
	return 3
}

// sleepPID writes its own process id to the file named by its first argument
// and then sleeps for 30 seconds. The id is the operating system's, which is
// not what a shell's $$ gives on Windows.
func sleepPID(_ Spec, args []string) int {
	if len(args) < 1 {
		return 2
	}
	if err := os.WriteFile(args[0], []byte(fmt.Sprint(os.Getpid())), 0o644); err != nil {
		return 1
	}
	time.Sleep(30 * time.Second)
	return 0
}
