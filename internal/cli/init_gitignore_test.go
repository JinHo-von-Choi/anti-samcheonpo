package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIgnoreStateDirAddsOnceAndOnlyInsideARepository(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if added, err := ignoreStateDir(".gitignore"); err != nil || added {
		t.Fatalf("without a repository nothing is written: %v %v", added, err)
	}
	if err := os.Mkdir(".git", 0o755); err != nil {
		t.Fatal(err)
	}
	if added, err := ignoreStateDir(".gitignore"); err != nil || !added {
		t.Fatalf("a repository without the entry gets it: %v %v", added, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(b) != ".samcheonpo/\n" {
		t.Fatalf("content: %q", b)
	}
	if added, err := ignoreStateDir(".gitignore"); err != nil || added {
		t.Fatalf("an existing entry is not repeated: %v %v", added, err)
	}
	_ = os.WriteFile(".gitignore", []byte("node_modules"), 0o644)
	if added, _ := ignoreStateDir(".gitignore"); !added {
		t.Fatal("appended after a file without a trailing newline")
	}
	b, _ = os.ReadFile(".gitignore")
	if string(b) != "node_modules\n.samcheonpo/\n" {
		t.Fatalf("content: %q", b)
	}
}
