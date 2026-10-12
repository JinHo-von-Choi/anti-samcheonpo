package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeSourceDigestRejectsDirectoryAndTracksContent(t *testing.T) {
	root := t.TempDir()
	if _, err := NativeSourceDigest(root); err == nil {
		t.Fatal("directory accepted")
	}
	path := filepath.Join(root, "source.jsonl")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := NativeSourceDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := NativeSourceDigest(path)
	if err != nil || a == b {
		t.Fatal("changed bytes retain digest", err)
	}
}
