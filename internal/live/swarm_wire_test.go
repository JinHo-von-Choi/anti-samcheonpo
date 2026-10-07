package live

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
)

// A session a plugin declares as spawned by another joins its lineage tree;
// once the tree's shared budget is spent, every session in it is refused
// before its next execution, while a session with no lineage is untouched.
func TestSwarmTreeBudgetRefusesEverySessionInTheTree(t *testing.T) {
	h := startDaemon(t)
	_ = os.WriteFile(filepath.Join(h.proj, ".samcheonpo.yml"), []byte("notify: {desktop: false}\nexperiment: {enabled: false}\nswarm: {tree_krw: 1}\n"), 0o644)
	h.send("SessionStart", map[string]any{"source": "startup", "session_id": "parent"})
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 고쳐 줘", "session_id": "parent"})
	h.send("SessionStart", map[string]any{"source": "startup", "session_id": "child", "parent_session_id": "parent"})
	h.send("UserPromptSubmit", map[string]any{"prompt": "하위 작업", "session_id": "child"})
	h.send("SessionStart", map[string]any{"source": "startup", "session_id": "alone"})
	h.send("UserPromptSubmit", map[string]any{"prompt": "별개 작업", "session_id": "alone"})

	read := func(session, id string) map[string]any {
		in := map[string]any{"session_id": session, "tool_name": "Read", "tool_use_id": id, "tool_input": map[string]any{"file_path": h.proj + "/src/a.py"}}
		o := h.send("PreToolUse", in)
		if hs, ok := o["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			return o
		}
		in["tool_response"] = map[string]any{"type": "text", "file": map[string]any{"content": "x\n"}}
		h.send("PostToolUse", in)
		return o
	}
	deny := func(o map[string]any) string {
		if hs, ok := o["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			r, _ := hs["permissionDecisionReason"].(string)
			return r
		}
		return ""
	}
	if deny(read("child", "c0")) != "" {
		t.Fatal("nothing spent yet: the tree is not blocked")
	}
	// the child spends past the one-won tree budget
	h.send("Usage", map[string]any{"session_id": "child", "message_id": "m1", "model": "claude-sonnet-5-5",
		"usage": map[string]any{"input_tokens": 1000, "output_tokens": 2000, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}})
	time.Sleep(200 * time.Millisecond)
	r := deny(read("child", "c1"))
	if !strings.Contains(r, "예산") {
		t.Fatalf("the spending session is refused with the budget reason: %q", r)
	}
	if r := deny(read("parent", "p1")); !strings.Contains(r, "예산") {
		t.Fatalf("the parent shares the tree and is refused too: %q", r)
	}
	if r := deny(read("alone", "a1")); r != "" {
		t.Fatalf("a session with no lineage is never blocked by another tree: %q", r)
	}
	text, _, _ := hookclient.Query("Statusline", map[string]string{"session_id": "child"}, 2*time.Second)
	if !strings.Contains(text, "군집 지출") || !strings.Contains(text, "/1원") {
		t.Fatalf("the status line shows the tree spend against its limit: %q", text)
	}
}
