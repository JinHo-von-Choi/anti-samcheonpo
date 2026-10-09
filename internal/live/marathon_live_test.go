package live

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A Codex session that reads a failed CI run and ships again without a local
// reproduction is told before the push; doing it again is refused before it
// runs, whatever the new tag is.
func TestCodexReleaseAfterFailedCIIsAdvisedThenBlocked(t *testing.T) {
	h := startDaemon(t)
	rollout := filepath.Join(h.home, "rollout-rel.jsonl")
	_ = os.MkdirAll(h.home, 0o755)
	_ = os.WriteFile(rollout, []byte(`{"type":"turn_context","payload":{"model":"gpt-5.5"}}`+"\n"), 0o644)
	prep := func(name string) map[string]any {
		m := loadFixture(t, name)
		m["cwd"] = h.proj
		m["transcript_path"] = rollout
		m["session_id"] = "codex-rel"
		return m
	}
	h.sendAgent("codex", "SessionStart", prep("SessionStart"))
	h.sendAgent("codex", "UserPromptSubmit", prep("UserPromptSubmit"))
	n := 0
	call := func(cmd, out string, exit int) map[string]any {
		n++
		id := fmt.Sprintf("exec-rel-%d", n)
		pre := prep("PreToolUse-bash")
		pre["tool_use_id"], pre["tool_input"] = id, map[string]any{"command": cmd}
		o := h.sendAgent("codex", "PreToolUse", pre)
		if hs, ok := o["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			return o
		}
		f, _ := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0o644)
		fmt.Fprintf(f, `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"%s","exit_code":%d}}}`+"\n", id, exit)
		f.Close()
		post := prep("PostToolUse-bash")
		post["tool_use_id"], post["tool_input"], post["tool_response"] = id, map[string]any{"command": cmd}, out
		h.sendAgent("codex", "PostToolUse", post)
		time.Sleep(150 * time.Millisecond)
		return o
	}
	call("gh run watch 37 --exit-status", "X gates failed", 1)
	o := call("git push origin main v1.0.1", "", 0)
	if !strings.Contains(ctxOf(o), "CI") {
		t.Fatalf("the first release after a failed CI run is told before it runs: %v", o)
	}
	o = call("git push origin main v1.0.2", "", 0)
	hs, _ := o["hookSpecificOutput"].(map[string]any)
	if hs == nil || hs["permissionDecision"] != "deny" || !strings.Contains(fmt.Sprint(hs["permissionDecisionReason"]), "막았다") {
		t.Fatalf("shipping again without reproducing the failure is refused: %v", o)
	}
}
