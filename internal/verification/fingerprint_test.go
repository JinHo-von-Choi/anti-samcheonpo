package verification

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFingerprintContentMissingLinksAndCancellation(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "input")
	os.WriteFile(p, []byte("aaa"), 0o600)
	a, err := Fingerprint(context.Background(), root, []string{"input"})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	os.WriteFile(p, []byte("bbb"), 0o600)
	os.Chtimes(p, st.ModTime(), st.ModTime())
	b, err := Fingerprint(context.Background(), root, []string{"input"})
	if err != nil || a == b {
		t.Fatal("same-size/same-mtime change missed")
	}
	if _, err := Fingerprint(context.Background(), root, []string{"missing"}); err == nil {
		t.Fatal("missing input accepted")
	}
	os.Symlink(p, filepath.Join(root, "link"))
	if _, err := Fingerprint(context.Background(), root, []string{"link"}); err == nil {
		t.Fatal("symlink accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Fingerprint(ctx, root, []string{"input"}); err == nil {
		t.Fatal("canceled scan succeeded")
	}
}
