package live

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A commit whose message reports how the work was done is told before it
// runs; pushing that commit unchanged is refused before it runs.
func TestCommitWithWorkReportIsAdvisedThenPushBlocked(t *testing.T) {
	h := startDaemon(t)
	rollout := filepath.Join(h.home, "rollout-meta.jsonl")
	_ = os.MkdirAll(h.home, 0o755)
	_ = os.WriteFile(rollout, []byte(`{"type":"turn_context","payload":{"model":"gpt-5.5"}}`+"\n"), 0o644)
	prep := func(name string) map[string]any {
		m := loadFixture(t, name)
		m["cwd"] = h.proj
		m["transcript_path"] = rollout
		m["session_id"] = "codex-meta"
		return m
	}
	h.sendAgent("codex", "SessionStart", prep("SessionStart"))
	h.sendAgent("codex", "UserPromptSubmit", prep("UserPromptSubmit"))
	n := 0
	call := func(cmd string) map[string]any {
		n++
		id := fmt.Sprintf("exec-meta-%d", n)
		pre := prep("PreToolUse-bash")
		pre["tool_use_id"], pre["tool_input"] = id, map[string]any{"command": cmd}
		o := h.sendAgent("codex", "PreToolUse", pre)
		if hs, ok := o["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			return o
		}
		f, _ := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0o644)
		fmt.Fprintf(f, `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"%s","exit_code":0}}}`+"\n", id)
		f.Close()
		post := prep("PostToolUse-bash")
		post["tool_use_id"], post["tool_input"], post["tool_response"] = id, map[string]any{"command": cmd}, ""
		h.sendAgent("codex", "PostToolUse", post)
		time.Sleep(150 * time.Millisecond)
		return o
	}
	o := call(`git commit -m "fix: 종료 알림 보존" -m "리뷰 의견을 반영해 정리했다."`)
	if !strings.Contains(ctxOf(o), "작업 경위") || !strings.Contains(ctxOf(o), "리뷰 의견을 반영해 정리했다.") {
		t.Fatalf("the commit is told about the sentence before it runs: %v", o)
	}
	o = call("git push origin main")
	hs, _ := o["hookSpecificOutput"].(map[string]any)
	if hs == nil || hs["permissionDecision"] != "deny" {
		t.Fatalf("pushing the same message after the advice is refused: %v", o)
	}
}
