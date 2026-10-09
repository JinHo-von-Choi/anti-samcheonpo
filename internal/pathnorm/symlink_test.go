package pathnorm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAbsoluteResolvesSymlinkedSpellings(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symbolic links unavailable:", err)
	}
	a, err := Absolute(filepath.Join(link, "proj"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Absolute(filepath.Join(real, "proj"))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("one directory, two spellings: %q vs %q", a, b)
	}
	spellings, err := Spellings(filepath.Join(link, "proj"))
	if err != nil || len(spellings) != 2 || spellings[0] != b || spellings[1] != filepath.Join(link, "proj") {
		t.Fatalf("spellings: %q %v", spellings, err)
	}
	// a path that does not exist yet keeps its tail
	n, err := Absolute(filepath.Join(link, "proj", "new", "x.py"))
	if err != nil || n != filepath.Join(b, "new", "x.py") {
		t.Fatalf("new path: %q %v", n, err)
	}
}
