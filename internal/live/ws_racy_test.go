package live

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A same-size edit in the same second as the index must still change the
// fingerprint: otherwise a rerun right after an edit looks unchanged.
func TestFingerprintSeesSameSizeEditInIndexSecond(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	run("init", "-q")
	file := filepath.Join(root, "a.py")
	if err := os.WriteFile(file, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(file)
	run("add", "a.py")
	ws := NewWorkspace(root)
	defer ws.Close()
	before, err := ws.Compute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(file, st.ModTime(), st.ModTime())
	time.Sleep(1100 * time.Millisecond) // the fingerprint runs in a later second
	after, err := ws.Compute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("a same-size edit with an unchanged mtime must change the fingerprint")
	}
}
