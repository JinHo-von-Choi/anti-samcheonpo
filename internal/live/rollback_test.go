package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testutil/fsx"
)

func rollbackSession(t *testing.T) *Session {
	t.Helper()
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Session{Root: root, ID: "rb"}
}

func write(t *testing.T, s *Session, call, rel, content string) {
	t.Helper()
	in := HookInput{ToolName: "Write", ToolInput: json.RawMessage(`{"content":` + strconvQuote(content) + `}`)}
	s.trackBefore(in, call, []string{rel})
	abs := filepath.Join(s.Root, rel)
	_ = os.MkdirAll(filepath.Dir(abs), 0o755)
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s.trackAfter(call, []string{rel})
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func applyPlan(t *testing.T, s *Session) string {
	t.Helper()
	preview, _ := s.rollback("")
	m := regexp.MustCompile(`rollback apply ([0-9a-f]{8})`).FindStringSubmatch(preview)
	if m == nil {
		return preview
	}
	out, _ := s.rollback("apply " + m[1])
	return preview + out
}

func TestRollbackNeverLeavesTheProjectThroughLinks(t *testing.T) {
	s := rollbackSession(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// the agent writes inside the project, then src is swapped for a link
	write(t, s, "w1", "src/keep.txt", "agent\n")
	_ = os.RemoveAll(filepath.Join(s.Root, "src"))
	fsx.Symlink(t, outside, filepath.Join(s.Root, "src"))
	out := applyPlan(t, s)
	b, _ := os.ReadFile(filepath.Join(outside, "keep.txt"))
	if string(b) != "outside\n" {
		t.Fatalf("a rollback changed a file outside the project: %q\n%s", b, out)
	}
	if !strings.Contains(out, "src/keep.txt: 대상 아님") {
		t.Fatalf("the unreachable file is listed as not covered: %s", out)
	}
}

func TestRollbackSkipsMixedEditsAndListsUntracked(t *testing.T) {
	s := rollbackSession(t)
	write(t, s, "w1", "a.txt", "one\n")
	// a person adds a line, then the agent edits again
	_ = os.WriteFile(filepath.Join(s.Root, "a.txt"), []byte("one\nmine\n"), 0o644)
	write(t, s, "w2", "a.txt", "one\nmine\ntwo\n")
	// a file whose actual result differs from what the tool input implied
	in := HookInput{ToolName: "Write", ToolInput: json.RawMessage(`{"content":"planned\n"}`)}
	s.trackBefore(in, "w3", []string{"b.txt"})
	_ = os.WriteFile(filepath.Join(s.Root, "b.txt"), []byte("something else\n"), 0o644)
	s.trackAfter("w3", []string{"b.txt"})
	// a file too large to keep
	big := strings.Repeat("x", maxTrackedFile+1)
	_ = os.WriteFile(filepath.Join(s.Root, "big.txt"), []byte(big), 0o644)
	s.trackBefore(HookInput{ToolName: "Write", ToolInput: json.RawMessage(`{"content":"small"}`)}, "w4", []string{"big.txt"})

	out := applyPlan(t, s)
	if got, _ := os.ReadFile(filepath.Join(s.Root, "a.txt")); string(got) != "one\nmine\ntwo\n" {
		t.Fatalf("a file with a person's edit mixed in is never rewritten: %q\n%s", got, out)
	}
	for _, want := range []string{"a.txt: 대상 아님 (AI가 쓴 뒤 다른 편집이 섞여", "b.txt: 대상 아님 (도구 결과와 실제 파일이 달라", "big.txt: 대상 아님 (파일이"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
}

func TestRollbackRefusesHardLinksAndKeepsBackup(t *testing.T) {
	s := rollbackSession(t)
	write(t, s, "w1", "c.txt", "agent\n")
	if err := os.Link(filepath.Join(s.Root, "c.txt"), filepath.Join(t.TempDir(), "other")); err != nil {
		t.Skip("hard links unsupported here")
	}
	if out := applyPlan(t, s); !strings.Contains(out, "하드링크") {
		t.Fatalf("a hard-linked file is not rewritten: %s", out)
	}
	s2 := rollbackSession(t)
	_ = os.WriteFile(filepath.Join(s2.Root, "d.txt"), []byte("before\n"), 0o644)
	write(t, s2, "w1", "d.txt", "after\n")
	out := applyPlan(t, s2)
	if got, _ := os.ReadFile(filepath.Join(s2.Root, "d.txt")); string(got) != "before\n" || !strings.Contains(out, "백업했다") {
		t.Fatalf("restore with backup: %q\n%s", got, out)
	}
}
