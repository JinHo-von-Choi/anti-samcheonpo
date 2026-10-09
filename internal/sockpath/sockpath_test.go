package sockpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShortPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	if p := Path(); !strings.HasPrefix(p, dir) && len(dir) < 80 {
		t.Errorf("short runtime dir is used: %s", p)
	}
	long := dir + "/" + strings.Repeat("x", 120)
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("SAMCHEONPO_HOME", long)
	p := Path()
	if len(p) > maxLen || !strings.HasPrefix(p, filepath.Join(os.TempDir(), "samcheonpo-")) {
		t.Errorf("long paths fall back to the temp dir: %s", p)
	}
	if Path() != p {
		t.Error("fallback is stable")
	}
}

func TestExplicitHomeHasOwnSocket(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	homeA, homeB := filepath.Join(os.TempDir(), "sc-a"), filepath.Join(os.TempDir(), "sc-b")
	t.Setenv("SAMCHEONPO_HOME", homeA)
	a := Path()
	t.Setenv("SAMCHEONPO_HOME", homeB)
	if b := Path(); a == b || a != filepath.Join(homeA, "run", "samcheonpo.sock") {
		t.Errorf("homes share a socket: %s %s", a, b)
	}
}

func TestFallbackShortensItsNameWhenTheTempDirIsLong(t *testing.T) {
	base := os.TempDir()
	if len(base) > 70 {
		t.Skip("the temp directory is already too long to leave room for a socket name")
	}
	// long enough that "samcheonpo-<user>-<hash>.sock" overflows, short enough
	// that "sc-<hash>.sock" still fits
	longTmp := filepath.Join(base, strings.Repeat("t", 75-len(base)-1))
	for _, env := range []string{"TMPDIR", "TEMP", "TMP"} {
		t.Setenv(env, longTmp)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(os.TempDir(), strings.Repeat("h", 120)))
	p := Path()
	if !strings.HasPrefix(p, longTmp) {
		t.Skipf("this system does not take its temp directory from the environment: %s", p)
	}
	if len(p) > maxLen {
		t.Fatalf("socket path of %d bytes exceeds %d: %s", len(p), maxLen, p)
	}
}
