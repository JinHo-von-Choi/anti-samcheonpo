//go:build windows

package pathnorm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsoluteUsesTheCaseTheFileSystemStores(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "MyProject", "Src"), 0o755); err != nil {
		t.Fatal(err)
	}
	want, err := Absolute(filepath.Join(base, "MyProject"))
	if err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{
		strings.ToLower(filepath.Join(base, "MyProject")),
		strings.ToUpper(filepath.Join(base, "MyProject")),
	} {
		got, err := Absolute(spelling)
		if err != nil || got != want {
			t.Errorf("Absolute(%q) = %q, want %q (%v)", spelling, got, want, err)
		}
	}
	// a path that does not exist yet keeps its last elements as written
	got, err := Absolute(strings.ToLower(filepath.Join(base, "MyProject", "Src", "New.py")))
	if err != nil || !strings.HasSuffix(got, filepath.Join("MyProject", "Src", "new.py")) {
		t.Errorf("a new file under a canonical directory: %q %v", got, err)
	}
}
