package live

import (
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
)

// After a compaction the next readable response carries the contract anchor
// once; before one, and after that one response, nothing is injected.
func TestAnchorRidesOnceAfterCompaction(t *testing.T) {
	h := startDaemon(t)
	h.send("SessionStart", map[string]any{"source": "startup"})
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 만료 처리 고쳐 줘"})
	read := func(id string) map[string]any {
		in := map[string]any{"tool_name": "Read", "tool_use_id": id, "tool_input": map[string]any{"file_path": h.proj + "/src/a.py"}}
		o := h.send("PreToolUse", in)
		in["tool_response"] = map[string]any{"type": "text", "file": map[string]any{"content": "x = 1\n"}}
		h.send("PostToolUse", in)
		time.Sleep(50 * time.Millisecond)
		return o
	}
	if c := ctxOf(read("r0")); strings.Contains(c, hookclient.AnchorHeaderTag) {
		t.Fatalf("a normal turn injects no anchor: %q", c)
	}
	h.send("PreCompact", map[string]any{"trigger": "auto"})
	first := ctxOf(read("r1"))
	if !strings.Contains(first, hookclient.AnchorHeaderTag) {
		t.Fatalf("the first response after compaction carries the anchor: %q", first)
	}
	info, err := hookclient.ParseAnchorHeader(first)
	if err != nil || !strings.Contains(info.Goal, "만료 처리") {
		t.Fatalf("the anchor names the goal: %+v %v", info, err)
	}
	if c := ctxOf(read("r2")); strings.Contains(c, hookclient.AnchorHeaderTag) {
		t.Fatalf("the anchor is repeated only once per compaction: %q", c)
	}
	// a resumed session that starts from a compaction owes an anchor too
	h.send("SessionStart", map[string]any{"source": "compact"})
	if c := ctxOf(read("r3")); !strings.Contains(c, hookclient.AnchorHeaderTag) {
		t.Fatalf("a compact session start carries the anchor: %q", c)
	}
}
