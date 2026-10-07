package live

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/patch"
)

// A confirmed pass records the agent's files durably; a later return to that
// point restores only what the agent wrote and still owns.
func TestGoldenPointIsRecordedAtPassAndRestoresOwnedFiles(t *testing.T) {
	h := startDaemon(t)
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 고쳐 줘"})
	write := func(id, rel, content string) {
		abs := filepath.Join(h.proj, rel)
		in := map[string]any{"tool_name": "Write", "tool_use_id": id, "tool_input": map[string]any{"file_path": abs, "content": content}}
		h.send("PreToolUse", in)
		_ = os.MkdirAll(filepath.Dir(abs), 0o755)
		_ = os.WriteFile(abs, []byte(content), 0o644)
		in["tool_response"] = map[string]any{"type": "update"}
		h.send("PostToolUse", in)
	}
	cmd := func(arg string) string {
		text, errText, ok := hookclient.Query("Command", CommandInput{Name: "rollback", Arg: arg, Root: h.proj}, 5*time.Second)
		if !ok || errText != "" {
			t.Fatalf("rollback %q: %q %v", arg, errText, ok)
		}
		return text
	}
	read := func(rel string) string {
		b, _ := os.ReadFile(filepath.Join(h.proj, rel))
		return string(b)
	}
	if out := cmd("golden"); !strings.Contains(out, "기록된 통과 시점이 없다") {
		t.Fatalf("no pass yet: %s", out)
	}
	write("w1", "src/a.py", "x = 2\n")
	write("w2", "src/b.py", "y = 1\n")
	h.shell("v1", "pytest -q", "2 passed", 0)
	time.Sleep(400 * time.Millisecond)
	g := patch.NewGoldenStateManager(h.proj)
	list := g.ListSnapshots()
	if len(list) != 1 || list[0].Reason != "pytest -q" || list[0].Session != "sess-1" || strings.Join(list[0].Files, ",") != "src/a.py,src/b.py" {
		t.Fatalf("the pass is recorded with the agent's files: %+v", list)
	}
	if !strings.HasPrefix(list[0].Tree, "git:") {
		t.Fatalf("the record carries the workspace fingerprint: %+v", list[0])
	}
	id := list[0].ID
	// the agent wanders, and the person edits b.py by hand afterwards
	write("w3", "src/a.py", "x = 3  # rewritten\n")
	write("w4", "src/b.py", "y = 2\n")
	_ = os.WriteFile(filepath.Join(h.proj, "src", "b.py"), []byte("y = 9  # mine\n"), 0o644)
	time.Sleep(200 * time.Millisecond)

	if out := cmd("golden"); !strings.Contains(out, id) || !strings.Contains(out, "pytest -q") {
		t.Fatalf("the list names the point: %s", out)
	}
	if out := cmd("golden nope"); !strings.Contains(out, "없거나 이미 정리") {
		t.Fatalf("an unknown point: %s", out)
	}
	preview := cmd("golden " + id)
	if !strings.Contains(preview, "기록된 통과 시점 "+id) || !strings.Contains(preview, "src/a.py: 이전 내용으로 복구") || !strings.Contains(preview, "src/b.py: 대상 아님") {
		t.Fatalf("preview: %s", preview)
	}
	if read("src/a.py") != "x = 3  # rewritten\n" {
		t.Fatal("a preview changes nothing")
	}
	m := regexp.MustCompile(`rollback golden ` + regexp.QuoteMeta(id) + ` apply ([0-9a-f]{8})`).FindStringSubmatch(preview)
	if m == nil {
		t.Fatalf("the preview shows the plan ID to apply: %s", preview)
	}
	if out := cmd("golden " + id + " apply deadbeef"); !strings.Contains(out, "적용하지 않았다") {
		t.Fatalf("a wrong plan ID writes nothing: %s", out)
	}
	applied := cmd("golden " + id + " apply " + m[1])
	if !strings.Contains(applied, "1개 파일을 되돌렸다") {
		t.Fatalf("apply: %s", applied)
	}
	if read("src/a.py") != "x = 2\n" || read("src/b.py") != "y = 9  # mine\n" {
		t.Fatalf("only the agent-owned file returns: %q %q", read("src/a.py"), read("src/b.py"))
	}
}
