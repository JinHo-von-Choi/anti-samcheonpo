package plugins

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHermesForwarder replays hook kwargs captured from Hermes 0.21.5
// through the forwarder plugin against a stub hook client.
func TestHermesForwarder(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.jsonl")
	stub := filepath.Join(dir, "samcheonpo")
	// the stub answers like the daemon: deny, advice, a stop hold
	_ = os.WriteFile(stub, []byte(`#!/usr/bin/env python3
import json, sys
ev = sys.argv[2]
p = json.load(sys.stdin)
open(`+"`"+`LOG`+"`"+`, "a").write(json.dumps({"ev": ev, "agent": sys.argv[3], "p": p}) + "\n")
if ev == "PreToolUse" and p["tool_name"] == "Bash":
    print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "repeat"}}))
elif ev in ("UserPromptSubmit", "PostToolUseFailure"):
    print(json.dumps({"hookSpecificOutput": {"hookEventName": ev, "additionalContext": "[advice]"}}))
elif ev == "Stop" and not p["stop_hook_active"]:
    print(json.dumps({"decision": "block", "reason": "run the check"}))
`), 0o755)
	b, _ := os.ReadFile(stub)
	_ = os.WriteFile(stub, []byte(strings.Replace(string(b), "`LOG`", `"`+log+`"`, 1)), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "__init__.py"), HermesInit, 0o644)
	driver := `
import importlib.util, json, sys
spec = importlib.util.spec_from_file_location("fwd", sys.argv[1])
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
hooks = {}
class Ctx:
    def register_hook(self, name, fn): hooks[name] = fn
m.register(Ctx())
for line in open(sys.argv[2]):
    r = json.loads(line)
    fn = hooks.get(r["ev"])
    if fn:
        print(json.dumps({"ev": r["ev"], "tool": r["kw"].get("tool_name"), "ret": fn(**r["kw"])}))
`
	fixture, _ := filepath.Abs(filepath.Join("..", "testdata", "hooks", "hermes", "kwargs.jsonl"))
	cmd := exec.Command(py, "-I", "-c", driver, filepath.Join(dir, "__init__.py"), fixture)
	cmd.Env = append(os.Environ(), "SAMCHEONPO_BIN="+stub)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	rets := map[string]any{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var r struct {
			Ev, Tool string
			Ret      any
		}
		if json.Unmarshal([]byte(l), &r) == nil {
			rets[r.Ev+"/"+r.Tool] = r.Ret
		}
	}
	if c, _ := rets["pre_llm_call/"].(map[string]any); c["context"] != "[advice]" {
		t.Errorf("prompt advice becomes pre_llm_call context: %v", rets["pre_llm_call/"])
	}
	if c, _ := rets["pre_tool_call/terminal"].(map[string]any); c["action"] != "block" || c["message"] != "repeat" {
		t.Errorf("deny becomes a block: %v", rets["pre_tool_call/terminal"])
	}
	if rets["pre_tool_call/write_file"] != nil {
		t.Errorf("a pass returns nothing: %v", rets["pre_tool_call/write_file"])
	}
	if s, _ := rets["transform_tool_result/terminal"].(string); !strings.HasSuffix(s, "\n\n[advice]") || !strings.HasPrefix(s, `{"output"`) {
		t.Errorf("advice extends the tool result: %q", s)
	}
	if c, _ := rets["pre_verify/"].(map[string]any); c["action"] != "continue" || c["message"] != "run the check" {
		t.Errorf("stop hold becomes a verify continue: %v", rets["pre_verify/"])
	}
	calls, _ := os.ReadFile(log)
	var seen []string
	for _, l := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		var c struct {
			Ev, Agent string
			P         map[string]any
		}
		_ = json.Unmarshal([]byte(l), &c)
		if c.Agent != "hermes" {
			t.Fatalf("calls name the hermes dialect: %s", l)
		}
		seen = append(seen, c.Ev)
		switch c.Ev {
		case "PostToolUseFailure":
			if !strings.HasPrefix(c.P["error"].(string), "Exit code 1\n") || c.P["tool_name"] != "Bash" {
				t.Errorf("terminal exit code reaches the daemon: %v", c.P)
			}
		case "PostToolUse":
			if in, _ := c.P["tool_input"].(map[string]any); !filepath.IsAbs(in["file_path"].(string)) {
				t.Errorf("file paths are absolute: %v", c.P)
			}
		case "Usage":
			u := c.P["usage"].(map[string]any)
			if c.P["message_id"] == "" || u["input_tokens"] == nil || u["cache_read_input_tokens"] == nil {
				t.Errorf("usage per API request: %v", c.P)
			}
		}
	}
	// pre_verify answered this turn, so post_llm_call sends no second Stop
	want := "SessionStart UserPromptSubmit Usage PreToolUse PostToolUseFailure PreToolUse PostToolUse Usage Stop SessionEnd"
	if got := strings.Join(seen, " "); got != want {
		t.Errorf("event order:\n got %s\nwant %s", got, want)
	}
}
