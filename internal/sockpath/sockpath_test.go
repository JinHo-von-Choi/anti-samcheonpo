package sockpath

import (
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
	if len(p) > maxLen || !strings.HasPrefix(p, "/tmp/samcheonpo-") {
		t.Errorf("long paths fall back to /tmp: %s", p)
	}
	if Path() != p {
		t.Error("fallback is stable")
	}
}

func TestExplicitHomeHasOwnSocket(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("SAMCHEONPO_HOME", "/h/a")
	a := Path()
	t.Setenv("SAMCHEONPO_HOME", "/h/b")
	if b := Path(); a == b || a != "/h/a/run/samcheonpo.sock" {
		t.Errorf("homes share a socket: %s %s", a, b)
	}
}
