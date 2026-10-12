package eval

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLabelLockAcrossProcesses(t *testing.T) {
	if root := os.Getenv("SAM_TEST_LABEL_LOCK_ROOT"); root != "" {
		_, err := LockLabels(root)
		if os.Getenv("SAM_TEST_LABEL_LOCK_BUSY") == "yes" {
			if !errors.Is(err, ErrLabelsBusy) {
				t.Fatalf("expected busy lock, got %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		// Intentionally leave an acquired handle open: process exit must
		// release ownership without deleting the common lock inode.
		return
	}
	root := t.TempDir()
	unlock, err := LockLabels(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
	child := func(busy bool) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestLabelLockAcrossProcesses$")
		mode := "no"
		if busy {
			mode = "yes"
		}
		cmd.Env = append(os.Environ(), "SAM_TEST_LABEL_LOCK_ROOT="+root, "SAM_TEST_LABEL_LOCK_BUSY="+mode)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %v %s", err, out)
		}
	}
	child(true)
	unlock()
	child(false)
	child(false)
	st, err := os.Stat(filepath.Join(root, ".label.lock"))
	if err != nil || runtime.GOOS != "windows" && st.Mode().Perm() != 0600 {
		t.Fatalf("lock privacy: %v %v", st, err)
	}
}
