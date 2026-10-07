package plugins

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenclawForwarder replays hook events captured from OpenClaw 2026.7.1
// through the forwarder plugin against a stub hook client.
func TestOpenclawForwarder(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.jsonl")
	stub := filepath.Join(dir, "samcheonpo")
	_ = os.WriteFile(stub, []byte(`#!/bin/sh
ev="$2"
payload=$(cat)
printf '{"ev":"%s","agent":"%s","p":%s}\n' "$ev" "$3" "$payload" >> "`+log+`"
case "$ev" in
  UserPromptSubmit) echo '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"[advice]"}}' ;;
  PreToolUse) case "$payload" in *'"Bash"'*) echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"repeat"}}' ;; esac ;;
  Stop) echo '{"decision":"block","reason":"run the check"}' ;;
esac
`), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "index.mjs"), OpenclawIndex, 0o644)
	fixture, _ := filepath.Abs(filepath.Join("..", "testdata", "hooks", "openclaw", "events.jsonl"))
	driver := `
import { readFileSync } from "node:fs"
const { default: plugin } = await import(process.argv[1])
const hooks = {}
plugin.register({ on: (name, fn) => { hooks[name] = fn } })
const lines = readFileSync(process.argv[2], "utf8").trim().split("\n").map((l) => JSON.parse(l))
// the captured run blocks nothing; replay the shell call as allowed first, then denied
const out = []
for (const r of lines) {
  const fn = hooks[r.ev]
  if (fn) out.push({ ev: r.ev, tool: r.event.toolName ?? "", ret: (await fn(r.event, r.ctx)) ?? null })
}
console.log(JSON.stringify(out))
`
	cmd := exec.Command(node, "--input-type=module", "-e", driver, filepath.Join(dir, "index.mjs"), fixture)
	cmd.Env = append(os.Environ(), "SAMCHEONPO_BIN="+stub)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var rets []struct {
		Ev, Tool string
		Ret      map[string]any
	}
	if err := json.Unmarshal(out, &rets); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := map[string]map[string]any{}
	for _, r := range rets {
		got[r.Ev+"/"+r.Tool] = r.Ret
	}
	if got["before_prompt_build/"]["prependContext"] != "[advice]" {
		t.Errorf("prompt advice becomes prependContext: %v", got["before_prompt_build/"])
	}
	if r := got["before_tool_call/exec"]; r["block"] != true || r["blockReason"] != "repeat" {
		t.Errorf("deny becomes a block: %v", r)
	}
	if got["before_tool_call/write"] != nil {
		t.Errorf("a pass returns nothing: %v", got["before_tool_call/write"])
	}
	if r := got["before_agent_finalize/"]; r["action"] != "revise" || r["reason"] != "run the check" {
		t.Errorf("stop hold becomes a revise: %v", r)
	}
	calls, _ := os.ReadFile(log)
	var seen []string
	for _, l := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		var c struct {
			Ev, Agent string
			P         map[string]any
		}
		if err := json.Unmarshal([]byte(l), &c); err != nil {
			t.Fatalf("%v: %s", err, l)
		}
		if c.Agent != "openclaw" || c.P["session_id"] != "cap4" || c.P["cwd"] != "/work/proj" {
			t.Errorf("calls carry the dialect, session and workspace: %s", l)
		}
		seen = append(seen, c.Ev)
		if c.Ev == "PostToolUse" {
			in, _ := c.P["tool_input"].(map[string]any)
			if c.P["tool_name"] != "Write" || in["file_path"] != "/work/proj/b.txt" {
				t.Errorf("write result with an absolute path: %v", c.P)
			}
		}
	}
	// the blocked shell call reports no result; usage was zero in the capture
	want := "UserPromptSubmit PreToolUse PreToolUse PostToolUse Stop"
	if strings.Join(seen, " ") != want {
		t.Errorf("event order:\n got %s\nwant %s", strings.Join(seen, " "), want)
	}
}
