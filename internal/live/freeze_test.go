package live

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
)

// A cause outside the code that repeats unchanged is advised once; the next
// source edit under it is refused with the external action, and the user's
// release lifts the freeze for that target.
func TestExternalCauseFreezesEditsUntilReleased(t *testing.T) {
	h := startDaemon(t)
	h.send("SessionStart", map[string]any{"source": "startup"})
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/app.cjs 시험 통과시켜 줘"})
	out := "Error: Cannot find module 'yaml'\nRequire stack:\n- /w/src/app.cjs"
	for i := 0; i < 2; i++ {
		h.shell(fmt.Sprintf("r%d", i), "node test_app.cjs", out, 1)
		time.Sleep(150 * time.Millisecond)
	}
	write := func(i int) map[string]any {
		p := filepath.Join(h.proj, "src", "app.cjs")
		content := fmt.Sprintf("module.exports = %d\n", i)
		w := map[string]any{"tool_name": "Write", "tool_use_id": fmt.Sprintf("w%d", i), "tool_input": map[string]any{"file_path": p, "content": content}}
		o := h.send("PreToolUse", w)
		if hs, ok := o["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			return o
		}
		_ = os.WriteFile(p, []byte(content), 0o644)
		w["tool_response"] = map[string]any{"type": "create"}
		h.send("PostToolUse", w)
		time.Sleep(150 * time.Millisecond)
		return o
	}
	var advised bool
	var denied map[string]any
	for i := 0; i < 4 && denied == nil; i++ {
		o := write(i)
		if hs, ok := o["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			denied = hs
			break
		}
		if c := ctxOf(o); strings.Contains(c, "코드 밖 원인") && strings.Contains(c, "npm install yaml") {
			advised = true
		}
	}
	if !advised {
		t.Fatal("the first edit under a standing external cause is advised with the remedy")
	}
	if denied == nil {
		t.Fatal("an edit repeated after the advice is refused")
	}
	reason, _ := denied["permissionDecisionReason"].(string)
	if !strings.Contains(reason, "npm install yaml") || !strings.Contains(reason, "src/app.cjs") {
		t.Fatalf("the refusal names the external action and the file: %q", reason)
	}
	text, errText, ok := hookclient.Query("Command", CommandInput{Name: "keep", Arg: "normal", Root: h.proj}, 5*time.Second)
	if !ok || errText != "" || !strings.Contains(text, "오탐") {
		t.Fatalf("keep normal releases the freeze: %q %q %v", text, errText, ok)
	}
	for i := 10; i < 12; i++ {
		o := write(i)
		if hs, ok := o["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			t.Fatalf("a released freeze must not refuse edits: %v", hs)
		}
		if strings.Contains(ctxOf(o), "수정을 시도했다") {
			t.Fatal("a released freeze no longer advises on edits")
		}
	}
}
